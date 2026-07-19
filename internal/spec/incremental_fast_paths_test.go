package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	graphpkg "github.com/crestenstclair/crest-spec/internal/graph"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"github.com/crestenstclair/crest-spec/internal/store"
	"github.com/stretchr/testify/require"
)

func TestReusableCandidateRequiresExactOwnedWorkspaceBytes(t *testing.T) {
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	path := filepath.Join(root, "synth.go")
	content := "package synth\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	hash := contextmanifest.Hash(content)
	require.NoError(t, st.SetResource(store.Resource{ID: "resource.synth", Kind: "asset"}))
	require.NoError(t, st.SetGeneratedFile(store.GeneratedFile{
		Path: "synth.go", ResourceID: "resource.synth", ContentHash: hash,
	}))
	s := New(st, OSFileSystem{}, &config.Config{SpecDir: specDir})
	candidate := &store.CandidateSet{
		ID: "candidate-1", Status: "accepted", CandidateHash: "candidate-hash",
		GenerationInputHash: "generation", DependencyContractHash: "dependencies",
		Files: []store.CandidateFile{{Path: "synth.go", Content: content, ContentHash: hash}},
	}

	matches, err := s.candidateMatchesOwnedWorkspace("resource.synth", candidate)
	require.NoError(t, err)
	require.True(t, matches)

	require.NoError(t, os.WriteFile(path, []byte("package user_edit\n"), 0o644))
	matches, err = s.candidateMatchesOwnedWorkspace("resource.synth", candidate)
	require.NoError(t, err)
	require.False(t, matches, "matching fingerprints must never overwrite divergent user edits")
}

func TestGenerationFingerprintV1SettlesOnlyAfterCoveredClosureValidation(t *testing.T) {
	const specCUE = `package crestsynth

project: name: "rt"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: Tone: {
	from: "f64"
	contributesTo: [{capability: "capability.exercise_resource", contribution: "represents the valid result under test"}]
	validations: [{kind: "custom", command: ["true"]}]
}
project: invariants: [{text: "no global state"}]
`
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(specDir, "spec.cue"), []byte(withTestIntent(specCUE)), 0o644,
	))
	s := New(st, OSFileSystem{}, &config.Config{
		SpecDir: specDir, GenerateModel: "test-model", MaxRetries: 3,
	})

	initial, err := s.Plan(context.Background())
	require.NoError(t, err)
	const resourceID = "valueObject.Audio.Tone"
	resource := initial.Registry.Resources[resourceID]
	legacyV1 := graphpkg.ComputeLegacyGenerationInputHashesV1(initial.Registry.Resources)[resourceID]
	declarationJSON, err := json.Marshal(resource.Declaration)
	require.NoError(t, err)
	declarationHash := fmt.Sprintf("%x", sha256.Sum256(declarationJSON))
	generatedPath := "tone.go"
	require.NoError(t, os.WriteFile(filepath.Join(root, generatedPath), []byte("package audio\n"), 0o644))
	require.NoError(t, st.SetResource(store.Resource{
		ID: resourceID, Kind: resource.Kind,
		DeclarationHash: declarationHash, EffectiveHash: "settled-v1",
		GenerationInputHash:    legacyV1,
		ExportedContractHash:   initial.ExportedContractHashes[resourceID],
		DependencyContractHash: initial.DependencyContractHashes[resourceID],
		Model:                  "old-model",
	}))
	require.NoError(t, st.SetGeneratedFile(store.GeneratedFile{
		Path: generatedPath, ResourceID: resourceID,
	}))

	planned, err := s.Plan(context.Background())
	require.NoError(t, err)
	require.Len(t, planned.Actions, 1)
	require.Equal(t, planpkg.ExecutionStrategyValidateExisting, planned.Actions[0].ExecutionStrategy)
	persisted, err := st.GetResource(resourceID)
	require.NoError(t, err)
	require.Equal(t, legacyV1, persisted.GenerationInputHash,
		"planning must leave the v1 fingerprint unsettled")

	begin, err := s.Begin(context.Background(), BeginOpts{})
	require.NoError(t, err)
	sessionResource, err := st.GetSessionResource(begin.SessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, "committed", sessionResource.State,
		"covered v1 migration should avoid a model dispatch")
	verified := s.VerifyWave(context.Background(), begin.SessionID, sessionResource.WaveIndex)
	require.True(t, verified.Passed)

	settled, err := st.GetResource(resourceID)
	require.NoError(t, err)
	require.Equal(t, planned.GenerationInputHashes[resourceID], settled.GenerationInputHash)
	require.Equal(t, planned.CodeDeclarationHashes[resourceID], settled.CodeDeclarationHash)
	require.Equal(t, planned.VerificationContractHashes[resourceID], settled.VerificationContractHash)
	require.NotEqual(t, legacyV1, settled.GenerationInputHash)
}

func TestFastPathsDowngradeWithoutExecutableClosureValidator(t *testing.T) {
	s := &Spec{cfg: &config.Config{}}
	plan := &PlanResult{
		Registry: &cuepkg.Registry{
			Project: &cuepkg.Project{Name: "test"},
			Resources: map[string]cuepkg.Resource{
				"consumer": {ID: "consumer"},
				"cached":   {ID: "cached"},
			},
		},
		Actions: []planpkg.PlannedAction{
			{ResourceID: "consumer", ExecutionStrategy: planpkg.ExecutionStrategyValidateExisting},
			{ResourceID: "cached", ExecutionStrategy: planpkg.ExecutionStrategyReuseCandidate, SourceCandidateID: "candidate-1"},
		},
	}

	s.requireClosureValidationForFastPaths(plan)
	for _, action := range plan.Actions {
		require.Equal(t, planpkg.ExecutionStrategyGenerate, action.ExecutionStrategy)
		require.Empty(t, action.SourceCandidateID)
	}
}

func TestFastPathsRemainWhenAffectedClosureHasExecutableValidator(t *testing.T) {
	s := &Spec{cfg: &config.Config{}}
	plan := &PlanResult{
		Registry: &cuepkg.Registry{
			Project: &cuepkg.Project{Name: "test"},
			Resources: map[string]cuepkg.Resource{
				"consumer": {
					ID:          "consumer",
					Validations: []cuepkg.Validation{{Kind: "compiles", Command: []string{"true"}}},
				},
			},
		},
		Actions: []planpkg.PlannedAction{{
			ResourceID: "consumer", ExecutionStrategy: planpkg.ExecutionStrategyValidateExisting,
		}},
	}

	s.requireClosureValidationForFastPaths(plan)
	require.Equal(t, planpkg.ExecutionStrategyValidateExisting, plan.Actions[0].ExecutionStrategy)
}

func TestRepairWitnessKeepsVerificationFastPathWithoutModelDispatch(t *testing.T) {
	const resourceID = "asset.Audio.Renderer"
	registry := &cuepkg.Registry{
		Project: &cuepkg.Project{Name: "test", Witnesses: map[string]cuepkg.Witness{
			"render": {
				ID: "witness.render", Resources: []string{resourceID}, RepairResources: []string{resourceID},
				Command: []string{"observe"}, NegativeCommand: []string{"counterexample"},
			},
		},
		},
		Resources: map[string]cuepkg.Resource{resourceID: {ID: resourceID}},
	}
	resource := registry.Resources[resourceID]
	resource.VerificationContractHash = registry.VerificationContractForResource(resourceID).Hash()
	registry.Resources[resourceID] = resource
	plan := &PlanResult{
		Registry: registry,
		Actions: []planpkg.PlannedAction{{
			ResourceID: resourceID, ExecutionStrategy: planpkg.ExecutionStrategyValidateExisting,
			VerificationContractHash: resource.VerificationContractHash,
		}},
	}

	require.True(t, (&Spec{cfg: &config.Config{}}).closureValidationAvailable(plan, []string{resourceID}))
	(&Spec{cfg: &config.Config{}}).requireClosureValidationForFastPaths(plan)
	require.Equal(t, planpkg.ExecutionStrategyValidateExisting, plan.Actions[0].ExecutionStrategy)
}

func TestOperationInputsMatchCurrentIncludesVerificationContract(t *testing.T) {
	const resourceID = "asset.Audio.Renderer"
	action := planpkg.PlannedAction{
		ResourceID: resourceID, GenerationInputHash: "generation", DependencyContractHash: "dependency",
		VerificationContractHash: "repair-v1",
	}
	current := &PlanResult{
		GenerationInputHashes:      map[string]string{resourceID: "generation"},
		DependencyContractHashes:   map[string]string{resourceID: "dependency"},
		VerificationContractHashes: map[string]string{resourceID: "repair-v2"},
	}
	require.False(t, operationInputsMatchCurrent(action, current),
		"a context bound before repair witness/validation edits must be rejected")
	current.VerificationContractHashes[resourceID] = "repair-v1"
	require.True(t, operationInputsMatchCurrent(action, current))
}

func TestLegacyCommittedOperationDefersMissingVerificationBindingToCurrentClosure(t *testing.T) {
	const resourceID = "asset.Audio.Renderer"
	action := planpkg.PlannedAction{
		ResourceID: resourceID, Kind: planpkg.ActionCreate, Reason: "new resource",
		GenerationInputHash: "generation", DependencyContractHash: "dependency",
		ExecutionStrategy: planpkg.ExecutionStrategyGenerate,
	}
	legacyIdentity := fmt.Sprintf(
		"%s\x00%s\x00%s\x00%s\x00%s",
		action.ResourceID, action.Kind, action.Reason,
		action.GenerationInputHash, action.DependencyContractHash,
	)
	action.OperationID = fmt.Sprintf("op.%x", sha256.Sum256([]byte(legacyIdentity)))
	encodedPlan, err := json.Marshal([]planpkg.PlannedAction{action})
	require.NoError(t, err)

	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	require.NoError(t, st.CreateSession(store.Session{
		ID: "legacy-session", ApplyID: "legacy-apply", PlanJSON: string(encodedPlan),
		WavesJSON: `[["asset.Audio.Renderer"]]`, HashesJSON: `{}`,
	}))
	require.NoError(t, st.UpsertSessionResource(store.SessionResource{
		SessionID: "legacy-session", ResourceID: resourceID, State: string(StateCommitted),
		ExecutionStrategy: planpkg.ExecutionStrategyGenerate,
	}))
	require.NoError(t, st.SetResource(store.Resource{
		ID: resourceID, Kind: "asset", DeclarationHash: "current-declaration",
		GenerationInputHash: "generation", DependencyContractHash: "dependency",
	}))

	current := &PlanResult{
		DeclarationHashes:                map[string]string{resourceID: "current-declaration"},
		GenerationInputHashes:            map[string]string{resourceID: "current-generation"},
		LegacyGenerationInputHashesV2:    map[string]string{resourceID: "generation"},
		DependencyContractHashes:         map[string]string{resourceID: "current-dependency"},
		LegacyDependencyContractHashesV2: map[string]string{resourceID: "dependency"},
		VerificationContractHashes:       map[string]string{resourceID: "current-verification"},
	}
	s := &Spec{store: st}
	resources, err := st.ListSessionResources("legacy-session")
	require.NoError(t, err)
	require.Empty(t, s.sessionOperationFingerprintErrors("legacy-session", current, resources),
		"an accepted pre-migration operation must run the current closure instead of reopening every resource")

	resources[0].State = string(StatePending)
	require.NotEmpty(t, s.sessionOperationFingerprintErrors("legacy-session", current, resources),
		"the bridge is restricted to terminal accepted resources")

	resources[0].State = string(StateCommitted)
	persisted, err := st.GetResource(resourceID)
	require.NoError(t, err)
	require.True(t, operationInputsMatchCurrent(action, current),
		"the intermediate identity may cross only an exact legacy projection")
	originalIdentity := action
	originalValue := fmt.Sprintf("%s\x00%s\x00%s", action.ResourceID, action.Kind, action.Reason)
	originalIdentity.OperationID = fmt.Sprintf("op.%x", sha256.Sum256([]byte(originalValue)))
	require.False(t, operationInputsMatchCurrent(originalIdentity, current),
		"the original identity did not bind generation/dependency and cannot authorize injected hashes")
	tampered := action
	tampered.OperationID = "op.tampered"
	require.False(t, legacyCommittedOperationCanValidate(tampered, current, resources[0], persisted),
		"a missing field is not enough to claim legacy provenance")
	intermediate := action
	intermediateIdentity := fmt.Sprintf(
		"%s\x00%s\x00%s\x00%s\x00%s",
		intermediate.ResourceID, intermediate.Kind, intermediate.Reason,
		intermediate.GenerationInputHash, intermediate.DependencyContractHash,
	)
	intermediate.OperationID = fmt.Sprintf("op.%x", sha256.Sum256([]byte(intermediateIdentity)))
	require.True(t, legacyCommittedOperationCanValidate(intermediate, current, resources[0], persisted),
		"the intermediate generation+dependency identity is also pre-verification provenance")
	require.True(t, operationInputsMatchCurrent(intermediate, current),
		"exact current legacy generation and dependency projections are the same semantic operation")
	currentIdentity := action
	planpkg.AssignOperationIdentity(
		&currentIdentity, currentIdentity.GenerationInputHash, currentIdentity.DependencyContractHash, "",
	)
	require.False(t, legacyCommittedOperationCanValidate(currentIdentity, current, resources[0], persisted),
		"a current six-field operation with an omitted contract is not a legacy operation")
	require.False(t, operationInputsMatchCurrent(currentIdentity, current))
	nonemptyMismatch := action
	nonemptyMismatch.VerificationContractHash = "different-verification"
	require.False(t, legacyCommittedOperationCanValidate(nonemptyMismatch, current, resources[0], persisted),
		"a known verification mismatch must remain strict")
	require.False(t, operationInputsMatchCurrent(nonemptyMismatch, current))
	forged := intermediate
	forged.OperationID = "op.forged"
	require.False(t, operationInputsMatchCurrent(forged, current))
	wrongLegacyGeneration := *current
	wrongLegacyGeneration.LegacyGenerationInputHashesV2 = map[string]string{resourceID: "other-generation"}
	require.False(t, operationInputsMatchCurrent(intermediate, &wrongLegacyGeneration))
	wrongLegacyDependency := *current
	wrongLegacyDependency.LegacyDependencyContractHashesV2 = map[string]string{resourceID: "other-dependency"}
	require.False(t, operationInputsMatchCurrent(intermediate, &wrongLegacyDependency))
	differentDeclaration := *current
	differentDeclaration.DeclarationHashes = map[string]string{resourceID: "different-declaration"}
	require.False(t, legacyCommittedOperationCanValidate(action, &differentDeclaration, resources[0], persisted),
		"the bridge cannot cover implementation drift")
}

func TestReinstatementCandidateRequiresExactAuthenticatedOperationProvenance(t *testing.T) {
	const resourceID = "applicationService.Shell.KeyboardInputTranslator"
	action := planpkg.PlannedAction{
		ResourceID: resourceID, Kind: planpkg.ActionCreate, Reason: "new resource",
		GenerationInputHash: "legacy-generation", DependencyContractHash: "legacy-dependency",
		ExecutionStrategy: planpkg.ExecutionStrategyGenerate,
	}
	identity := fmt.Sprintf(
		"%s\x00%s\x00%s\x00%s\x00%s",
		action.ResourceID, action.Kind, action.Reason,
		action.GenerationInputHash, action.DependencyContractHash,
	)
	action.OperationID = fmt.Sprintf("op.%x", sha256.Sum256([]byte(identity)))
	current := &PlanResult{
		GenerationInputHashes:            map[string]string{resourceID: "current-generation"},
		LegacyGenerationInputHashesV2:    map[string]string{resourceID: "legacy-generation"},
		DependencyContractHashes:         map[string]string{resourceID: "current-dependency"},
		LegacyDependencyContractHashesV2: map[string]string{resourceID: "legacy-dependency"},
		VerificationContractHashes:       map[string]string{resourceID: "current-verification"},
	}
	candidate := &store.CandidateSet{
		Status: "accepted", GenerationInputHash: "legacy-generation",
		DependencyContractHash: "legacy-dependency",
	}
	require.True(t, reinstatementCandidateMatchesOperation(candidate, &action, current),
		"an exact accepted V2 candidate remains the same semantic operation after projection migration")

	wrongCandidateGeneration := *candidate
	wrongCandidateGeneration.GenerationInputHash = "other-generation"
	require.False(t, reinstatementCandidateMatchesOperation(&wrongCandidateGeneration, &action, current))
	wrongCandidateDependency := *candidate
	wrongCandidateDependency.DependencyContractHash = "other-dependency"
	require.False(t, reinstatementCandidateMatchesOperation(&wrongCandidateDependency, &action, current))
	rejectedCandidate := *candidate
	rejectedCandidate.Status = "rejected"
	require.False(t, reinstatementCandidateMatchesOperation(&rejectedCandidate, &action, current))

	forgedAction := action
	forgedAction.OperationID = "op.forged"
	require.False(t, reinstatementCandidateMatchesOperation(candidate, &forgedAction, current))
	knownMismatch := action
	knownMismatch.VerificationContractHash = "different-verification"
	require.False(t, reinstatementCandidateMatchesOperation(candidate, &knownMismatch, current))
	wrongLegacyProjection := *current
	wrongLegacyProjection.LegacyGenerationInputHashesV2 = map[string]string{resourceID: "different-generation"}
	require.False(t, reinstatementCandidateMatchesOperation(candidate, &action, &wrongLegacyProjection))
}

func TestLegacyVerificationBindingSettlesOnlyAfterPassingCurrentClosure(t *testing.T) {
	const (
		validatedOwner = "asset.Audio.ValidatedRenderer"
		witnessOwner   = "asset.Audio.WitnessedRenderer"
	)
	registry := &cuepkg.Registry{
		Project: &cuepkg.Project{Witnesses: map[string]cuepkg.Witness{
			"audio": {
				ID: "witness.audio", Resources: []string{witnessOwner}, RepairResources: []string{witnessOwner},
				Command: []string{"observe"}, NegativeCommand: []string{"counterexample"},
			},
		}},
		Resources: map[string]cuepkg.Resource{
			validatedOwner: {
				ID: validatedOwner, Kind: "asset",
				Validations: []cuepkg.Validation{{ID: "validation.renderer", Kind: "custom", Command: []string{"true"}}},
			},
			witnessOwner: {ID: witnessOwner, Kind: "asset"},
		},
	}
	verificationHashes := map[string]string{
		validatedOwner: registry.VerificationContractForResource(validatedOwner).Hash(),
		witnessOwner:   registry.VerificationContractForResource(witnessOwner).Hash(),
	}
	plan := &PlanResult{
		Registry:                         registry,
		Hashes:                           map[string]string{validatedOwner: "effective-a", witnessOwner: "effective-b"},
		DeclarationHashes:                map[string]string{validatedOwner: "declaration-a", witnessOwner: "declaration-b"},
		CodeDeclarationHashes:            map[string]string{validatedOwner: "code-a", witnessOwner: "code-b"},
		GenerationInputHashes:            map[string]string{validatedOwner: "current-generation-a", witnessOwner: "current-generation-b"},
		LegacyGenerationInputHashesV2:    map[string]string{validatedOwner: "legacy-generation-a", witnessOwner: "legacy-generation-b"},
		ExportedContractHashes:           map[string]string{validatedOwner: "export-a", witnessOwner: "export-b"},
		DependencyContractHashes:         map[string]string{validatedOwner: "current-dependency-a", witnessOwner: "current-dependency-b"},
		LegacyDependencyContractHashesV2: map[string]string{validatedOwner: "legacy-dependency-a", witnessOwner: "legacy-dependency-b"},
		VerificationContractHashes:       verificationHashes,
	}
	actions := []planpkg.PlannedAction{
		{ResourceID: validatedOwner, Kind: planpkg.ActionCreate, Reason: "new resource", GenerationInputHash: "legacy-generation-a", DependencyContractHash: "legacy-dependency-a", ExecutionStrategy: planpkg.ExecutionStrategyGenerate},
		{ResourceID: witnessOwner, Kind: planpkg.ActionCreate, Reason: "new resource", GenerationInputHash: "legacy-generation-b", DependencyContractHash: "legacy-dependency-b", ExecutionStrategy: planpkg.ExecutionStrategyGenerate},
	}
	for index := range actions {
		identity := fmt.Sprintf(
			"%s\x00%s\x00%s\x00%s\x00%s",
			actions[index].ResourceID, actions[index].Kind, actions[index].Reason,
			actions[index].GenerationInputHash, actions[index].DependencyContractHash,
		)
		actions[index].OperationID = fmt.Sprintf("op.%x", sha256.Sum256([]byte(identity)))
	}
	encodedPlan, err := json.Marshal(actions)
	require.NoError(t, err)

	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	require.NoError(t, st.CreateSession(store.Session{
		ID: "legacy-settlement", ApplyID: "legacy-apply", PlanJSON: string(encodedPlan),
		WavesJSON: `[["asset.Audio.ValidatedRenderer","asset.Audio.WitnessedRenderer"]]`, HashesJSON: `{}`,
	}))
	resources := []store.SessionResource{
		{SessionID: "legacy-settlement", ResourceID: validatedOwner, State: string(StateCommitted), ExecutionStrategy: planpkg.ExecutionStrategyGenerate},
		{SessionID: "legacy-settlement", ResourceID: witnessOwner, State: string(StateCommitted), ExecutionStrategy: planpkg.ExecutionStrategyGenerate},
	}
	actionByResource := map[string]planpkg.PlannedAction{
		validatedOwner: actions[0],
		witnessOwner:   actions[1],
	}
	for _, resource := range resources {
		require.NoError(t, st.UpsertSessionResource(resource))
		action := actionByResource[resource.ResourceID]
		require.NoError(t, st.SetResource(store.Resource{
			ID: resource.ResourceID, Kind: "asset", DeclarationHash: plan.DeclarationHashes[resource.ResourceID],
			GenerationInputHash:    action.GenerationInputHash,
			DependencyContractHash: action.DependencyContractHash,
		}))
	}

	s := &Spec{store: st}
	failed := &WaveVerifyResult{Passed: false, IntegrationValidated: true}
	s.settleLegacyVerificationContracts("legacy-settlement", plan, resources, failed)
	for _, resourceID := range []string{validatedOwner, witnessOwner} {
		persisted, getErr := st.GetResource(resourceID)
		require.NoError(t, getErr)
		require.Empty(t, persisted.VerificationContractHash,
			"a failed current closure must leave legacy verification provenance unknown")
	}

	passed := &WaveVerifyResult{Passed: true, IntegrationValidated: true}
	s.settleLegacyVerificationContracts("legacy-settlement", plan, resources, passed)
	require.True(t, passed.Passed)
	validated, err := st.GetResource(validatedOwner)
	require.NoError(t, err)
	require.Equal(t, verificationHashes[validatedOwner], validated.VerificationContractHash)
	require.Equal(t, plan.DeclarationHashes[validatedOwner], validated.DeclarationHash)
	require.Equal(t, plan.GenerationInputHashes[validatedOwner], validated.GenerationInputHash)
	require.Equal(t, plan.DependencyContractHashes[validatedOwner], validated.DependencyContractHash)

	witnessed, err := st.GetResource(witnessOwner)
	require.NoError(t, err)
	require.Empty(t, witnessed.VerificationContractHash,
		"repair witness ownership must remain unstamped until current witness evidence passes")
	require.Equal(t, plan.GenerationInputHashes[witnessOwner], witnessed.GenerationInputHash,
		"passing mechanical closure may migrate implementation bookkeeping before witness evidence")
	require.Equal(t, plan.DependencyContractHashes[witnessOwner], witnessed.DependencyContractHash)
}

func TestUnrelatedResourceValidatorDoesNotCertifyFastPathClosure(t *testing.T) {
	s := &Spec{cfg: &config.Config{}}
	plan := &PlanResult{
		Registry: &cuepkg.Registry{
			Project: &cuepkg.Project{Name: "test"},
			Resources: map[string]cuepkg.Resource{
				"fast-a": {ID: "fast-a"},
				"fast-b": {ID: "fast-b"},
				"unrelated": {
					ID:          "unrelated",
					Validations: []cuepkg.Validation{{Kind: "custom", Command: []string{"true"}}},
				},
			},
		},
		Actions: []planpkg.PlannedAction{
			{ResourceID: "fast-a", ExecutionStrategy: planpkg.ExecutionStrategyValidateExisting},
			{ResourceID: "fast-b", ExecutionStrategy: planpkg.ExecutionStrategyReuseCandidate},
			{ResourceID: "unrelated", ExecutionStrategy: planpkg.ExecutionStrategyGenerate},
		},
	}

	require.False(t, s.closureValidationAvailable(plan, []string{"fast-a", "fast-b"}))
	s.requireClosureValidationForFastPaths(plan)
	require.Equal(t, planpkg.ExecutionStrategyGenerate, plan.Actions[0].ExecutionStrategy)
	require.Equal(t, planpkg.ExecutionStrategyGenerate, plan.Actions[1].ExecutionStrategy)
}

func TestProjectValidationCoverageMustIncludeEveryFastPathResource(t *testing.T) {
	s := &Spec{cfg: &config.Config{}}
	plan := &PlanResult{Registry: &cuepkg.Registry{
		Project: &cuepkg.Project{Name: "test", Validations: cuepkg.FlexValidations{{
			Kind: "custom", Command: []string{"true"}, Resources: []string{"fast-a"},
		}}},
		Resources: map[string]cuepkg.Resource{
			"fast-a": {ID: "fast-a"},
			"fast-b": {ID: "fast-b"},
		},
	}}

	require.False(t, s.closureValidationAvailable(plan, []string{"fast-a", "fast-b"}))
	plan.Registry.Project.Validations[0].Resources = []string{"fast-a", "fast-b"}
	require.True(t, s.closureValidationAvailable(plan, []string{"fast-a", "fast-b"}))
	plan.Registry.Project.Validations[0].Resources = nil
	require.True(t, s.closureValidationAvailable(plan, []string{"fast-a", "fast-b"}),
		"an unrestricted project command covers the whole closure")
}

func TestSkippedResourceValidatorCannotAuthorizeCommittedFastPath(t *testing.T) {
	s := &Spec{cfg: &config.Config{}}
	plan := &PlanResult{Registry: &cuepkg.Registry{
		Project: &cuepkg.Project{Name: "test"},
		Resources: map[string]cuepkg.Resource{
			"fast": {ID: "fast"},
			"skipped": {
				ID: "skipped", Validations: []cuepkg.Validation{{Kind: "custom", Command: []string{"true"}}},
			},
		},
	}}
	resources := []store.SessionResource{
		{ResourceID: "fast", State: "committed", ExecutionStrategy: planpkg.ExecutionStrategyValidateExisting},
		{ResourceID: "skipped", State: "skipped", ExecutionStrategy: planpkg.ExecutionStrategyGenerate},
	}

	committedFastPaths := committedFastPathResourceIDs(resources)
	require.Equal(t, []string{"fast"}, committedFastPaths)
	require.False(t, s.closureValidationAvailable(plan, committedFastPaths))
}

func TestVerifyWaveRejectsFingerprintDriftBeforeValidationOrSettlement(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	s.cfg.TypeCheckCommand = "printf ran > fingerprint-validation.log"
	commitValidationResource(t, s, state.sessionID, state.resourceID, "stale-operation.go")

	before, err := s.store.GetResource(state.resourceID)
	require.NoError(t, err)
	require.NotEmpty(t, before.GenerationInputHash)
	specPath := filepath.Join(s.cfg.SpecDir, "spec.cue")
	contents, err := os.ReadFile(specPath)
	require.NoError(t, err)
	changed := strings.Replace(
		string(contents),
		"Generate and verify the resource under test",
		"Generate and verify the changed resource contract",
		1,
	)
	require.NotEqual(t, string(contents), changed)
	require.NoError(t, os.WriteFile(specPath, []byte(changed), 0o644))

	resource, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	result := s.VerifyWave(context.Background(), state.sessionID, resource.WaveIndex)
	require.False(t, result.Passed)
	require.False(t, result.IntegrationValidated)
	require.Equal(t, []string{state.resourceID}, result.ReopenedResources)
	require.NotEmpty(t, result.Errors)
	require.Equal(t, "operation_fingerprint_drift", result.Errors[0].Kind)
	_, err = os.Stat(filepath.Join(filepath.Dir(s.cfg.SpecDir), "fingerprint-validation.log"))
	require.ErrorIs(t, err, os.ErrNotExist, "validation must not run for a stale operation")

	after, err := s.store.GetResource(state.resourceID)
	require.NoError(t, err)
	require.Equal(t, before.GenerationInputHash, after.GenerationInputHash,
		"failed verification must not stamp the current generation fingerprint")
	reopened, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, "pending", reopened.State)
	require.Equal(t, planpkg.ExecutionStrategyGenerate, reopened.ExecutionStrategy)
}

func TestVerifyWaveReopensSkippedActionWhenItsFingerprintDrifts(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	s.cfg.TypeCheckCommand = "true"
	require.NoError(t, s.Skip(context.Background(), state.sessionID, state.resourceID, "waived for the original operation"))

	specPath := filepath.Join(s.cfg.SpecDir, "spec.cue")
	contents, err := os.ReadFile(specPath)
	require.NoError(t, err)
	changed := strings.Replace(
		string(contents),
		"Generate and verify the resource under test",
		"Generate and verify a newly constrained resource",
		1,
	)
	require.NotEqual(t, string(contents), changed)
	require.NoError(t, os.WriteFile(specPath, []byte(changed), 0o644))

	resource, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, "skipped", resource.State)
	result := s.VerifyWave(context.Background(), state.sessionID, resource.WaveIndex)
	require.False(t, result.Passed)
	require.Equal(t, []string{state.resourceID}, result.ReopenedResources)

	reopened, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, "pending", reopened.State,
		"a waiver for an old immutable operation must not carry into a changed operation")
	require.Equal(t, planpkg.ExecutionStrategyGenerate, reopened.ExecutionStrategy)
}

func TestVerifyWaveReopensPersistedFastPathWhenClosureValidatorIsUnavailable(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	resource, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	resource.State = "committed"
	resource.Phase = "integration_pending"
	resource.ExecutionStrategy = planpkg.ExecutionStrategyValidateExisting
	require.NoError(t, s.store.UpsertSessionResource(*resource))

	verified := s.VerifyWave(context.Background(), state.sessionID, resource.WaveIndex)
	require.False(t, verified.Passed)
	require.Equal(t, []string{state.resourceID}, verified.ReopenedResources)
	require.NotEmpty(t, verified.Errors)
	require.Equal(t, "closure_validation_unavailable", verified.Errors[0].Kind)
	reopened, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, "pending", reopened.State)
	require.Equal(t, planpkg.ExecutionStrategyGenerate, reopened.ExecutionStrategy)
}
