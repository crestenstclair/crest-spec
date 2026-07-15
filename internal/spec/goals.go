package spec

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// projectIntentSnapshot maps the strict CUE intent layer into stable SQLite
// identities. DDD and asset declarations remain separate architectural
// resources; this projection contains only project intent and acceptance.
func projectIntentSnapshot(project *cuepkg.Project) (store.ProjectIntentSnapshot, error) {
	if err := cuepkg.ValidateProjectIntent(project); err != nil {
		return store.ProjectIntentSnapshot{}, err
	}

	snapshot := store.ProjectIntentSnapshot{
		ProjectName:   project.Name,
		Mission:       project.Mission,
		RequiredGoals: append([]string(nil), project.Completion.RequiredGoals...),
	}

	for _, name := range sortedIntentKeys(project.Actors) {
		actor := project.Actors[name]
		snapshot.Actors = append(snapshot.Actors, store.IntentActor{ID: "actor." + name, Description: actor.Description})
	}
	for _, name := range sortedIntentKeys(project.Goals) {
		goal := project.Goals[name]
		snapshot.Goals = append(snapshot.Goals, store.IntentGoal{
			ID: "goal." + name, Description: goal.Description, Priority: goal.Priority,
			Actors: append([]string(nil), goal.Actors...), DependsOn: append([]string(nil), goal.DependsOn...),
		})
	}
	for _, name := range sortedIntentKeys(project.Capabilities) {
		capability := project.Capabilities[name]
		capabilityID := "capability." + name
		snapshot.Capabilities = append(snapshot.Capabilities, store.IntentCapability{
			ID: capabilityID, Description: capability.Description, Goals: append([]string(nil), capability.Goals...),
		})
		for ordinal, scenarioName := range sortedIntentKeys(capability.Acceptance) {
			scenario := capability.Acceptance[scenarioName]
			acceptance := store.IntentAcceptance{
				ID: "acceptance." + name + "." + scenarioName, CapabilityID: capabilityID,
				ActorID: scenario.Actor, Description: scenario.Description, Ordinal: ordinal,
				Evidence: append([]string(nil), scenario.Evidence...),
			}
			for _, step := range scenario.Steps {
				acceptance.Steps = append(acceptance.Steps, store.IntentAcceptanceStep{Action: step.Action, Observes: step.Observes})
			}
			snapshot.Acceptance = append(snapshot.Acceptance, acceptance)
		}
	}
	for _, name := range sortedIntentKeys(project.Requirements) {
		requirement := project.Requirements[name]
		snapshot.Requirements = append(snapshot.Requirements, store.IntentRequirement{
			ID: "requirement." + name, Kind: requirement.Kind, Description: requirement.Description,
			Goals: append([]string(nil), requirement.Goals...), Capabilities: append([]string(nil), requirement.Capabilities...),
		})
	}
	for _, name := range sortedIntentKeys(project.Evidence) {
		evidence := project.Evidence[name]
		snapshot.Evidence = append(snapshot.Evidence, store.IntentEvidence{ID: "evidence." + name, Kind: evidence.Kind, Description: evidence.Description})
	}
	for _, name := range sortedIntentKeys(project.NonGoals) {
		snapshot.NonGoals = append(snapshot.NonGoals, store.IntentNonGoal{ID: "non_goal." + name, Description: project.NonGoals[name]})
	}

	canonical, err := json.Marshal(snapshot)
	if err != nil {
		return store.ProjectIntentSnapshot{}, fmt.Errorf("marshal project intent: %w", err)
	}
	snapshot.SpecHash = fmt.Sprintf("%x", sha256.Sum256(canonical))
	return snapshot, nil
}

func verificationDefinitionSnapshot(registry *cuepkg.Registry) ([]store.IntentVerificationDefinition, error) {
	project := registry.Project
	evidenceByDefinition := make(map[string][]string)
	for _, name := range sortedIntentKeys(project.Evidence) {
		evidence := project.Evidence[name]
		evidenceID := "evidence." + name
		for _, definitionID := range evidence.Validations {
			evidenceByDefinition[definitionID] = append(evidenceByDefinition[definitionID], evidenceID)
		}
		for _, definitionID := range evidence.Witnesses {
			evidenceByDefinition[definitionID] = append(evidenceByDefinition[definitionID], evidenceID)
		}
	}
	completionChecks := make(map[string]bool, len(project.Completion.ProjectChecks))
	for _, definitionID := range project.Completion.ProjectChecks {
		completionChecks[definitionID] = true
	}

	var definitions []store.IntentVerificationDefinition
	appendDefinition := func(id, definitionType, scope, kind string, declaration any, targets []store.IntentVerificationTarget) error {
		canonical, err := json.Marshal(declaration)
		if err != nil {
			return err
		}
		definitionHash := fmt.Sprintf("%x", sha256.Sum256(canonical))
		snapshotSeed := project.Name + "\x00" + id + "\x00" + definitionHash
		snapshotHash := fmt.Sprintf("%x", sha256.Sum256([]byte(snapshotSeed)))
		seen := make(map[string]bool)
		uniqueTargets := targets[:0]
		for _, target := range targets {
			key := target.Kind + "\x00" + target.ID
			if target.ID == "" || seen[key] {
				continue
			}
			seen[key] = true
			uniqueTargets = append(uniqueTargets, target)
		}
		definitions = append(definitions, store.IntentVerificationDefinition{
			SnapshotID: "verification." + snapshotHash[:24], DefinitionID: id,
			DefinitionType: definitionType, Scope: scope, Kind: kind,
			DefinitionHash: definitionHash, DefinitionJSON: string(canonical), Targets: uniqueTargets,
		})
		return nil
	}
	validationTargets := func(validation cuepkg.Validation, owner string) []store.IntentVerificationTarget {
		var targets []store.IntentVerificationTarget
		if owner != "" {
			targets = append(targets, store.IntentVerificationTarget{Kind: "resource", ID: owner})
		}
		for _, id := range validation.Resources {
			targets = append(targets, store.IntentVerificationTarget{Kind: "resource", ID: id})
		}
		for _, id := range validation.Capabilities {
			targets = append(targets, store.IntentVerificationTarget{Kind: "capability", ID: id})
		}
		for _, id := range validation.Goals {
			targets = append(targets, store.IntentVerificationTarget{Kind: "goal", ID: id})
		}
		for _, id := range evidenceByDefinition[validation.ID] {
			targets = append(targets, store.IntentVerificationTarget{Kind: "evidence", ID: id})
		}
		if completionChecks[validation.ID] {
			targets = append(targets, store.IntentVerificationTarget{Kind: "completion_check", ID: validation.ID})
		}
		return targets
	}
	for _, validation := range project.Validations {
		if err := appendDefinition(validation.ID, "validation", validation.Scope, validation.Kind, validation, validationTargets(validation, "")); err != nil {
			return nil, fmt.Errorf("snapshot validation %s: %w", validation.ID, err)
		}
	}
	for _, resourceID := range sortedIntentKeys(registry.Resources) {
		resource := registry.Resources[resourceID]
		for _, validation := range resource.Validations {
			if err := appendDefinition(validation.ID, "validation", validation.Scope, validation.Kind, validation, validationTargets(validation, resourceID)); err != nil {
				return nil, fmt.Errorf("snapshot validation %s: %w", validation.ID, err)
			}
		}
	}
	for _, name := range sortedIntentKeys(project.Witnesses) {
		witness := project.Witnesses[name]
		var targets []store.IntentVerificationTarget
		for _, id := range witness.Resources {
			targets = append(targets, store.IntentVerificationTarget{Kind: "resource", ID: id})
		}
		if witness.Capability != "" {
			targets = append(targets, store.IntentVerificationTarget{Kind: "capability", ID: witness.Capability})
		}
		if witness.Goal != "" {
			targets = append(targets, store.IntentVerificationTarget{Kind: "goal", ID: witness.Goal})
		}
		for _, id := range append(append([]string(nil), witness.Evidence...), evidenceByDefinition[witness.ID]...) {
			targets = append(targets, store.IntentVerificationTarget{Kind: "evidence", ID: id})
		}
		if err := appendDefinition(witness.ID, "witness", witness.Scope, "behavioral", witness, targets); err != nil {
			return nil, fmt.Errorf("snapshot witness %s: %w", witness.ID, err)
		}
	}
	return definitions, nil
}

func sortedIntentKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func resourceTraceSnapshot(registry *cuepkg.Registry) []store.IntentResourceTrace {
	var traces []store.IntentResourceTrace
	for _, resourceID := range sortedIntentKeys(registry.Resources) {
		resource := registry.Resources[resourceID]
		trace := store.IntentResourceTrace{ResourceID: resource.ID, ResourceKind: resource.Kind}
		for _, contribution := range resource.Contributions {
			trace.Contributions = append(trace.Contributions, store.IntentContribution{CapabilityID: contribution.Capability, Description: contribution.Contribution})
		}
		switch declaration := resource.Declaration.(type) {
		case cuepkg.Port:
			if declaration.Direction != "" {
				trace.Boundary = &store.IntentBoundaryProfile{Direction: declaration.Direction}
			}
		case cuepkg.Adapter:
			if declaration.Profile.Kind != "" {
				profile := declaration.Profile
				trace.Boundary = &store.IntentBoundaryProfile{Kind: profile.Kind, Method: profile.Method, Path: profile.Path, Protocol: profile.Protocol, Topology: profile.Topology, Device: profile.Device, Medium: profile.Medium, System: profile.System, Topic: profile.Topic, Trigger: profile.Trigger, Surfaces: append([]string(nil), profile.Surfaces...), Accessibility: append([]string(nil), profile.Accessibility...)}
			}
		case cuepkg.Asset:
			if declaration.Profile.Kind != "" {
				profile := declaration.Profile
				trace.Asset = &store.IntentAssetProfile{Kind: profile.Kind, Ecosystem: profile.Ecosystem, Witness: profile.Witness, Source: profile.Source, SecretPolicy: profile.SecretPolicy, FailurePolicy: profile.FailurePolicy, Constraint: profile.Constraint, Audience: profile.Audience, Predecessor: profile.Predecessor, Compatibility: profile.Compatibility, Rollback: profile.Rollback, Signals: append([]string(nil), profile.Signals...), RequiredExamples: append([]string(nil), profile.RequiredExamples...)}
			}
		}
		if len(trace.Contributions) > 0 || trace.Boundary != nil || trace.Asset != nil {
			traces = append(traces, trace)
		}
	}
	return traces
}
