package spec

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"time"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/store"
	validationpkg "github.com/crestenstclair/crest-spec/internal/validation"
)

type resourceValidationAlias struct {
	definition cuepkg.Validation
	resourceID string
}

type resourceValidationGroup struct {
	definition cuepkg.Validation
	aliases    []resourceValidationAlias
	owners     []string
}

// resourceValidationGroups deduplicates operationally identical commands
// while retaining every canonical definition ID for evidence projection.
func resourceValidationGroups(registry *cuepkg.Registry, resources []store.SessionResource) []resourceValidationGroup {
	ordered := append([]store.SessionResource(nil), resources...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].WaveIndex != ordered[j].WaveIndex {
			return ordered[i].WaveIndex < ordered[j].WaveIndex
		}
		return ordered[i].ResourceID < ordered[j].ResourceID
	})
	groups := make([]resourceValidationGroup, 0)
	groupIndex := make(map[string]int)
	for _, sessionResource := range ordered {
		if ResourceState(sessionResource.State) != StateCommitted {
			continue
		}
		resource, ok := registry.Resources[sessionResource.ResourceID]
		if !ok {
			continue
		}
		for _, definition := range resourceValidations(resource) {
			key := operationalValidationKey(definition)
			index, exists := groupIndex[key]
			if !exists {
				index = len(groups)
				groupIndex[key] = index
				groups = append(groups, resourceValidationGroup{definition: definition})
			}
			groups[index].aliases = append(groups[index].aliases, resourceValidationAlias{
				definition: definition, resourceID: sessionResource.ResourceID,
			})
			groups[index].owners = append(groups[index].owners, sessionResource.ResourceID)
		}
	}
	for index := range groups {
		sort.Strings(groups[index].owners)
		groups[index].owners = uniqueSortedValidationOwners(groups[index].owners)
	}
	return groups
}

func uniqueSortedValidationOwners(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func operationalValidationKey(definition cuepkg.Validation) string {
	workingDirectory := definition.WorkingDirectory
	if workingDirectory == "" {
		workingDirectory = "."
	}
	workingDirectory = filepath.ToSlash(filepath.Clean(workingDirectory))

	timeout := validationpkg.DefaultTimeout
	if parsed, err := time.ParseDuration(definition.Timeout); err == nil && parsed > 0 {
		timeout = parsed
	}

	environment := append([]string(nil), definition.Environment...)
	sort.Strings(environment)
	normalizedEnvironment := environment[:0]
	for _, name := range environment {
		if name == "" || len(normalizedEnvironment) > 0 && name == normalizedEnvironment[len(normalizedEnvironment)-1] {
			continue
		}
		normalizedEnvironment = append(normalizedEnvironment, name)
	}

	operational := struct {
		Kind             string
		Command          []string
		WorkingDirectory string
		Timeout          int64
		Environment      []string
		Assertions       []cuepkg.Assertion
	}{
		Kind: definition.Kind, Command: definition.Command,
		WorkingDirectory: workingDirectory, Timeout: int64(timeout),
		Environment: normalizedEnvironment, Assertions: definition.Assertions,
	}
	encoded, _ := json.Marshal(operational)
	return string(encoded)
}
