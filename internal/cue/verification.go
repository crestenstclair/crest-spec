package cue

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var environmentNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

var validationScopes = map[string]bool{
	"resource": true, "dependency_contract": true, "integration_wave": true,
	"goal": true, "project": true, "regression": true, "behavioral": true,
}

var validationKinds = map[string]bool{
	"compiles": true, "test": true, "custom": true, "integration": true,
}

var observationTypes = map[string]bool{
	"number": true, "string": true, "bool": true, "array": true,
	"object": true, "any": true,
}

var predicateOps = map[string]bool{
	"eq": true, "differ": true, "gt": true, "gte": true, "lt": true,
	"lte": true, "range": true, "count": true, "distinct_count": true,
	"monotonic": true, "set_contains": true, "equals": true,
}

func normalizeAndValidateVerificationDefinitions(p *Project, errs *[]string) {
	validationIDs := make(map[string]bool, len(p.Validations))
	hasNamedValidations := false
	for index := range p.Validations {
		definition := &p.Validations[index]
		path := fmt.Sprintf("project.validations[%d]", index)
		if definition.ID == "" {
			definition.ID = legacyValidationID("project."+p.Name, index, *definition)
			definition.Named = false
		}
		if definition.Scope == "" {
			definition.Scope = "project"
		}
		hasNamedValidations = hasNamedValidations || definition.Named
		if validationIDs[definition.ID] {
			*errs = append(*errs, fmt.Sprintf("%s duplicates validation id %q", path, definition.ID))
		}
		validationIDs[definition.ID] = true
		validateValidationDefinition(errs, path, *definition, nil)
	}

	witnessIDs := make(map[string]bool, len(p.Witnesses))
	witnessNames := sortedMapKeys(p.Witnesses)
	for _, name := range witnessNames {
		witness := p.Witnesses[name]
		path := "project.witnesses." + name
		canonicalID := "witness." + name
		if witness.ID != "" && witness.ID != canonicalID {
			*errs = append(*errs, fmt.Sprintf("%s.id must be %q", path, canonicalID))
		}
		witness.ID = canonicalID
		p.Witnesses[name] = witness
		witnessIDs[canonicalID] = true
	}

	goalIDs := prefixedIDs("goal", p.Goals)
	capabilityIDs := prefixedIDs("capability", p.Capabilities)
	evidenceIDs := prefixedIDs("evidence", p.Evidence)
	for index, definition := range p.Validations {
		path := fmt.Sprintf("project.validations[%d]", index)
		validateRefs(errs, path+".goals", definition.Goals, goalIDs)
		validateRefs(errs, path+".capabilities", definition.Capabilities, capabilityIDs)
	}
	for _, name := range witnessNames {
		validateWitnessDefinition(errs, "project.witnesses."+name, p.Witnesses[name], goalIDs, capabilityIDs, evidenceIDs)
	}

	for _, name := range sortedMapKeys(p.Evidence) {
		evidence := p.Evidence[name]
		path := "project.evidence." + name
		validateRefs(errs, path+".validations", evidence.Validations, validationIDs)
		validateRefs(errs, path+".witnesses", evidence.Witnesses, witnessIDs)
	}
	for _, checkID := range p.Completion.ProjectChecks {
		if !validationIDs[checkID] {
			// Before named validations existed, projectChecks were opaque host
			// check identifiers. Preserve those specifications as a read-only
			// compatibility path. Once a specification opts into named
			// validations, every completion check is required to resolve.
			if hasNamedValidations {
				*errs = append(*errs, fmt.Sprintf("project.completion.projectChecks references unknown validation %q", checkID))
			}
			continue
		}
		for _, definition := range p.Validations {
			if definition.ID == checkID && !definition.Named {
				*errs = append(*errs, fmt.Sprintf("project.completion.projectChecks cannot reference legacy anonymous validation %q", checkID))
			}
		}
	}
}

func validateValidationDefinition(errs *[]string, path string, definition Validation, validResources map[string]bool) {
	if !strings.HasPrefix(definition.ID, "validation.") {
		*errs = append(*errs, path+".id must use the validation.<name> form")
	}
	if !validationScopes[definition.Scope] {
		*errs = append(*errs, fmt.Sprintf("%s.scope has unsupported value %q", path, definition.Scope))
	}
	if !validationKinds[definition.Kind] {
		*errs = append(*errs, fmt.Sprintf("%s.kind has unsupported value %q", path, definition.Kind))
	}
	validateCommand(errs, path+".command", definition.Command)
	validateExecutionPolicy(errs, path, definition.WorkingDirectory, definition.Timeout, definition.Environment, nil)
	if validResources != nil {
		validateRefs(errs, path+".resources", definition.Resources, validResources)
	}
	switch definition.Scope {
	case "dependency_contract":
		if len(definition.Resources) < 2 {
			*errs = append(*errs, path+".resources must contain provider and consumer resources")
		}
	case "goal", "regression":
		if len(definition.Goals) == 0 {
			*errs = append(*errs, path+".goals must contain at least one goal")
		}
	}
}

func validateWitnessDefinition(errs *[]string, path string, witness Witness, goals, capabilities, evidence map[string]bool) {
	if witness.Scope != "resource" && witness.Scope != "goal" && witness.Scope != "project" {
		*errs = append(*errs, fmt.Sprintf("%s.scope must be resource, goal, or project", path))
	}
	if witness.Scope == "goal" && witness.Goal == "" {
		*errs = append(*errs, path+".goal is required for goal scope")
	}
	if witness.Scope == "resource" && len(witness.Resources) == 0 {
		*errs = append(*errs, path+".resources must contain at least one resource for resource scope")
	}
	if witness.Goal != "" {
		validateRefs(errs, path+".goal", []string{witness.Goal}, goals)
	}
	if witness.Capability != "" {
		validateRefs(errs, path+".capability", []string{witness.Capability}, capabilities)
	}
	validateRefs(errs, path+".evidence", witness.Evidence, evidence)
	validateCommand(errs, path+".command", witness.Command)
	validateCommand(errs, path+".negativeCommand", witness.NegativeCommand)
	if equalStringSlices(witness.Command, witness.NegativeCommand) {
		*errs = append(*errs, path+".negativeCommand must differ from command")
	}
	validateExecutionPolicy(errs, path, witness.WorkingDirectory, witness.Timeout, witness.Environment, witness.Artifacts)
	if witness.Observation.Kind != "json_stdout" {
		*errs = append(*errs, path+`.observation.kind must be "json_stdout"`)
	}
	if strings.TrimSpace(witness.Observation.Marker) == "" {
		*errs = append(*errs, path+".observation.marker is required")
	}
	if len(witness.Observation.Schema) == 0 {
		*errs = append(*errs, path+".observation.schema must contain at least one field")
	}
	for field, kind := range witness.Observation.Schema {
		if strings.TrimSpace(field) == "" || !observationTypes[kind] {
			*errs = append(*errs, fmt.Sprintf("%s.observation.schema[%q] has unsupported type %q", path, field, kind))
		}
	}
	if len(witness.Predicates) == 0 {
		*errs = append(*errs, path+".predicates must contain at least one predicate")
	}
	for index, predicate := range witness.Predicates {
		predicatePath := fmt.Sprintf("%s.predicates[%d]", path, index)
		if _, ok := witness.Observation.Schema[predicate.Field]; !ok {
			*errs = append(*errs, fmt.Sprintf("%s.field %q is absent from the observation schema", predicatePath, predicate.Field))
		}
		if !predicateOps[predicate.Op] {
			*errs = append(*errs, fmt.Sprintf("%s.op has unsupported value %q", predicatePath, predicate.Op))
			continue
		}
		switch predicate.Op {
		case "eq", "gt", "gte", "lt", "lte", "count", "distinct_count":
			if predicate.Value == nil && predicate.Member == nil {
				*errs = append(*errs, predicatePath+" requires value or member")
			}
		case "range":
			if predicate.Min == nil || predicate.Max == nil || (predicate.Min != nil && predicate.Max != nil && *predicate.Min > *predicate.Max) {
				*errs = append(*errs, predicatePath+" requires ordered min and max bounds")
			}
		case "set_contains", "equals":
			if predicate.Member == nil {
				*errs = append(*errs, predicatePath+" requires member")
			}
		}
	}
}

func validateCommand(errs *[]string, path string, command []string) {
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		*errs = append(*errs, path+" must contain an executable")
		return
	}
	for index, argument := range command {
		if strings.ContainsRune(argument, '\x00') {
			*errs = append(*errs, fmt.Sprintf("%s[%d] contains a NUL byte", path, index))
		}
	}
}

func validateExecutionPolicy(errs *[]string, path, cwd, timeout string, environment, artifacts []string) {
	if cwd != "" && !safeRelativePath(cwd) {
		*errs = append(*errs, path+".workingDirectory must remain inside the project root")
	}
	if timeout != "" {
		parsed, err := time.ParseDuration(timeout)
		if err != nil || parsed <= 0 {
			*errs = append(*errs, path+".timeout must be a positive duration")
		}
	}
	seenEnvironment := make(map[string]bool)
	for _, name := range environment {
		if !environmentNamePattern.MatchString(name) {
			*errs = append(*errs, fmt.Sprintf("%s.environment contains invalid variable name %q", path, name))
		}
		if seenEnvironment[name] {
			*errs = append(*errs, fmt.Sprintf("%s.environment contains duplicate variable %q", path, name))
		}
		seenEnvironment[name] = true
	}
	for _, artifact := range artifacts {
		if !safeRelativePath(artifact) {
			*errs = append(*errs, fmt.Sprintf("%s.artifacts contains unsafe path %q", path, artifact))
		}
	}
}

func safeRelativePath(path string) bool {
	if path == "" || filepath.IsAbs(path) {
		return false
	}
	clean := filepath.Clean(path)
	return clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func legacyValidationID(owner string, index int, definition Validation) string {
	copy := definition
	copy.ID = ""
	copy.Named = false
	encoded, _ := json.Marshal(struct {
		Owner      string
		Ordinal    int
		Definition Validation
	}{owner, index, copy})
	sum := sha256.Sum256(encoded)
	return "validation.legacy." + hex.EncodeToString(sum[:6])
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sortedValidationIDs(definitions []Validation) []string {
	ids := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		ids = append(ids, definition.ID)
	}
	sort.Strings(ids)
	return ids
}
