package spec

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/store"
)

type waveValidationSource string

const (
	waveValidationResource  waveValidationSource = "resource"
	waveValidationTypeCheck waveValidationSource = "type_check"
	waveValidationTest      waveValidationSource = "test"
	waveValidationProject   waveValidationSource = "project"
)

type waveValidationAlias struct {
	definition           cuepkg.Validation
	source               waveValidationSource
	owners               []string
	repairOwnersDeclared bool
	recordEvidence       bool
}

type waveValidationGroup struct {
	definition cuepkg.Validation
	aliases    []waveValidationAlias
}

type waveValidationSchedule struct {
	groups                      []waveValidationGroup
	aliasCount                  int
	resourceDefinitionCount     int
	resourcePhysicalCommandKeys map[string]bool
}

type waveValidationAliasResult struct {
	alias  waveValidationAlias
	result ValidationResult
}

type waveValidationExecution struct {
	physical ValidationResult
	aliases  []waveValidationAliasResult
}

// newWaveValidationSchedule creates one execution plan for every validator
// that observes the affected closure. IDs, scopes, declared owners, and
// evidence targets remain aliases of the physical command; they never force a
// duplicate process launch when the execution contract is identical.
func newWaveValidationSchedule(
	registry *cuepkg.Registry,
	resources []store.SessionResource,
	typeCheckCommand, testCommand string,
) waveValidationSchedule {
	schedule := waveValidationSchedule{resourcePhysicalCommandKeys: make(map[string]bool)}
	groupIndex := make(map[string]int)
	add := func(alias waveValidationAlias) {
		key := operationalValidationKey(alias.definition)
		index, exists := groupIndex[key]
		if !exists {
			index = len(schedule.groups)
			groupIndex[key] = index
			schedule.groups = append(schedule.groups, waveValidationGroup{definition: alias.definition})
		}
		schedule.groups[index].aliases = append(schedule.groups[index].aliases, alias)
		schedule.aliasCount++
		if alias.source == waveValidationResource {
			schedule.resourceDefinitionCount++
			schedule.resourcePhysicalCommandKeys[key] = true
		}
	}

	// Resource order remains dependency-wave then resource ID, matching the
	// pre-batch scheduler and keeping side effects deterministic for diagnostics.
	for _, resourceGroup := range resourceValidationGroups(registry, resources) {
		for _, alias := range resourceGroup.aliases {
			add(waveValidationAlias{
				definition:           alias.definition,
				source:               waveValidationResource,
				owners:               []string{alias.resourceID},
				repairOwnersDeclared: true,
				recordEvidence:       true,
			})
		}
	}

	closureOwners := committedResourceIDs(resources)
	if typeCheckCommand != "" {
		add(waveValidationAlias{
			definition: cuepkg.Validation{
				ID: "configuration.type_check", Scope: "project", Kind: "compiles",
				Command: []string{"sh", "-c", typeCheckCommand},
			},
			source: waveValidationTypeCheck, owners: closureOwners,
		})
	}
	if testCommand != "" {
		add(waveValidationAlias{
			definition: cuepkg.Validation{
				ID: "configuration.test", Scope: "project", Kind: "test",
				Command: []string{"sh", "-c", testCommand},
			},
			source: waveValidationTest, owners: closureOwners,
		})
	}

	for _, definition := range registry.Project.Validations {
		owners := declaredValidationOwners(definition.Resources, resources)
		if len(owners) == 0 {
			owners = closureOwners
		}
		add(waveValidationAlias{
			definition: definition, source: waveValidationProject,
			owners: owners, repairOwnersDeclared: len(definition.Resources) > 0,
			recordEvidence: true,
		})
	}
	return schedule
}

func (s *Spec) executeWaveValidationSchedule(
	ctx context.Context,
	projectName, sessionID string,
	schedule waveValidationSchedule,
) []waveValidationExecution {
	executions := make([]waveValidationExecution, 0, len(schedule.groups))
	root := filepath.Dir(s.cfg.SpecDir)
	for _, group := range schedule.groups {
		results := ExecuteValidations(ctx, []cuepkg.Validation{group.definition}, root)
		if len(results) == 0 {
			continue
		}
		execution := waveValidationExecution{physical: results[0]}
		for _, alias := range group.aliases {
			aliasResult := results[0]
			aliasResult.ID = alias.definition.ID
			aliasResult.Scope = alias.definition.Scope
			aliasResult.Kind = alias.definition.Kind
			aliasResult.RunID = ""
			if alias.source == waveValidationTypeCheck || alias.source == waveValidationTest {
				preserveConfiguredGateInfrastructureClassification(&aliasResult, alias.source)
			}
			if alias.recordEvidence {
				s.recordValidationResult(
					ctx, projectName, alias.definition, &aliasResult,
					validationProvenance{SessionID: sessionID},
				)
			}
			execution.aliases = append(execution.aliases, waveValidationAliasResult{
				alias: alias, result: aliasResult,
			})
		}
		executions = append(executions, execution)
	}
	return executions
}

// The legacy configured gates treated shell exit 126/127 as an unavailable
// validator even when the shell emitted no recognizable nested-tool message.
// Keep that classification on the configured alias while CUE definitions
// retain their own declared validation semantics.
func preserveConfiguredGateInfrastructureClassification(result *ValidationResult, source waveValidationSource) {
	if result == nil || result.Execution == nil || result.Classification == "error" {
		return
	}
	if result.Execution.ExitCode != 126 && result.Execution.ExitCode != 127 {
		return
	}
	result.Passed = false
	result.Classification = "error"
	result.Message = fmt.Sprintf(
		"validation could not run: %s command exited %d: %s",
		source, result.Execution.ExitCode, truncateOutput(result.Execution.Stderr),
	)
}

func (s *Spec) runAffectedClosureValidationBatch(
	ctx context.Context,
	registry *cuepkg.Registry,
	resources []store.SessionResource,
	sessionID string,
	result *WaveVerifyResult,
) {
	schedule := newWaveValidationSchedule(
		registry, resources, s.cfg.TypeCheckCommand, s.cfg.TestCommand,
	)
	result.ResourceValidationDefinitions = schedule.resourceDefinitionCount
	result.ResourceValidationCommands = len(schedule.resourcePhysicalCommandKeys)
	result.ValidationAliases = schedule.aliasCount
	result.ValidationCommands = len(schedule.groups)

	executions := s.executeWaveValidationSchedule(
		ctx, registry.Project.Name, sessionID, schedule,
	)
	for _, execution := range executions {
		s.applyWaveValidationExecution(resources, execution, result)
	}

	// Completion depends on named project verification definitions. Refresh it
	// once after the whole batch rather than once per alias or physical command.
	if len(registry.Project.Validations) > 0 {
		if err := s.refreshCompletion(ctx, store.TransitionSource{
			Type: "validation_wave", ID: sessionID, SessionID: sessionID,
		}); err != nil {
			result.Passed = false
			result.Errors = append(result.Errors, WaveError{
				Kind: "completion_refresh", Message: fmt.Sprintf("refresh project completion: %v", err),
			})
		}
	}
}

func (s *Spec) applyWaveValidationExecution(
	resources []store.SessionResource,
	execution waveValidationExecution,
	result *WaveVerifyResult,
) {
	var infrastructure []waveValidationAliasResult
	for _, alias := range execution.aliases {
		if validationInfrastructureFailure(alias.result) {
			infrastructure = append(infrastructure, alias)
		}
	}
	if len(infrastructure) > 0 {
		result.Passed = false
		waveErr := validationInfrastructureWaveError(infrastructure[0].result)
		waveErr.ValidationIDs, waveErr.ValidationKinds = validationAliasIdentity(infrastructure)
		result.Errors = append(result.Errors, waveErr)
		return
	}

	bySource := make(map[waveValidationSource][]waveValidationAliasResult)
	for _, alias := range execution.aliases {
		if !alias.result.Passed {
			bySource[alias.alias.source] = append(bySource[alias.alias.source], alias)
		}
	}
	if len(bySource) == 0 {
		return
	}

	diagnosticOwners := s.attributeErrorToResources(
		validationDiagnosticOutput(execution.physical), resources,
	)
	for _, source := range []waveValidationSource{
		waveValidationResource, waveValidationTypeCheck, waveValidationTest, waveValidationProject,
	} {
		aliases := bySource[source]
		if len(aliases) == 0 {
			continue
		}
		owners := validationRepairOwners(diagnosticOwners, aliases)
		result.Passed = false
		waveErr := waveErrorForResources(
			owners, waveValidationErrorKind(source),
			fmt.Sprintf("%s: %s", aliases[0].result.Kind, aliases[0].result.Message),
		)
		waveErr.ValidationIDs, waveErr.ValidationKinds = validationAliasIdentity(aliases)
		result.Errors = append(result.Errors, waveErr)
	}
}

// validationRepairOwners combines two independent, structured ownership
// signals. Compiler/test diagnostics identify accepted files that participated
// in the failure, while validation aliases carry the definition's explicit
// repair contract. Neither signal may erase the other: assertion-only failures
// commonly have no diagnostic span, and diagnostics can point at a consumer
// while the declaring test/harness owner also needs repair. Unowned configured
// or project-wide gates contribute no aliases here, so an unattributed failure
// still stops for triage instead of reopening the whole closure.
func validationRepairOwners(
	diagnosticOwners []string,
	aliases []waveValidationAliasResult,
) []string {
	owners := append([]string(nil), diagnosticOwners...)
	for _, alias := range aliases {
		if alias.alias.repairOwnersDeclared {
			owners = append(owners, alias.alias.owners...)
		}
	}
	sort.Strings(owners)
	return uniqueSortedValidationOwners(owners)
}

func waveValidationErrorKind(source waveValidationSource) string {
	switch source {
	case waveValidationResource:
		return "resource_validation"
	case waveValidationTypeCheck:
		return "type_check"
	case waveValidationTest:
		return "test"
	case waveValidationProject:
		return "project_validation"
	default:
		return "validation"
	}
}

func validationAliasIdentity(aliases []waveValidationAliasResult) (ids, kinds []string) {
	for _, alias := range aliases {
		if alias.result.ID != "" {
			ids = append(ids, alias.result.ID)
		}
		if alias.result.Kind != "" {
			kinds = append(kinds, alias.result.Kind)
		}
	}
	sort.Strings(ids)
	sort.Strings(kinds)
	return uniqueSortedValidationOwners(ids), uniqueSortedValidationOwners(kinds)
}
