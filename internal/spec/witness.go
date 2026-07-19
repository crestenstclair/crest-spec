package spec

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/check"
	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/store"
	validationpkg "github.com/crestenstclair/crest-spec/internal/validation"
)

// VerifyWitness executes the specification's real and negative commands,
// parses both through one declared observation contract, applies the
// falsification gate, and stores the complete provenance event in SQLite.
func (s *Spec) VerifyWitness(ctx context.Context, witnessID, checkID, sessionID string) (VerifyResult, error) {
	if witnessID == "" {
		return VerifyResult{}, fmt.Errorf("witness_id is required")
	}
	planResult, err := s.Plan(ctx)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("verify witness: plan: %w", err)
	}
	project := planResult.Registry.Project
	witness, ok := witnessByID(project.Witnesses, witnessID)
	if !ok {
		return VerifyResult{}, fmt.Errorf("verify witness: unknown witness %q", witnessID)
	}
	snapshot, err := projectIntentSnapshot(project)
	if err != nil {
		return VerifyResult{}, err
	}
	snapshot.ResourceTrace = resourceTraceSnapshot(planResult.Registry)
	snapshot.Verification, err = verificationDefinitionSnapshot(planResult.Registry)
	if err != nil {
		return VerifyResult{}, err
	}
	if err := s.store.ReconcileProjectIntent(ctx, snapshot); err != nil {
		return VerifyResult{}, fmt.Errorf("verify witness: reconcile definitions: %w", err)
	}

	if checkID != "" {
		behavioralCheck, err := s.store.GetCheck(checkID)
		if err != nil {
			return VerifyResult{}, fmt.Errorf("verify witness: check %q: %w", checkID, err)
		}
		if !containsString(witness.Resources, behavioralCheck.ResourceID) {
			return VerifyResult{}, fmt.Errorf("verify witness: witness %s does not cover check resource %s", witnessID, behavioralCheck.ResourceID)
		}
	}

	root := s.projectRoot()
	timeout := time.Duration(0)
	if witness.Timeout != "" {
		timeout, _ = time.ParseDuration(witness.Timeout)
	}
	realExecution := validationpkg.Execute(ctx, validationpkg.Options{
		ProjectRoot: root, Command: witness.Command, WorkingDirectory: witness.WorkingDirectory,
		Timeout: timeout, Environment: witness.Environment, Artifacts: witness.Artifacts,
	})
	negativeExecution := validationpkg.Execute(ctx, validationpkg.Options{
		ProjectRoot: root, Command: witness.NegativeCommand, WorkingDirectory: witness.WorkingDirectory,
		Timeout: timeout, Environment: witness.Environment,
	})
	finalSourceTreeHash, finalSourceTreeHashErr := validationpkg.HashSourceTree(root)
	realExecutionFailed := witnessExecutionFailed(realExecution, 0)
	negativeExecutionFailed := witnessExecutionFailed(negativeExecution, witness.NegativeExpectedExitCode)

	classification, reason := "", ""
	var realObservation, negativeObservation check.Observation
	var realParseError, negativeParseError string
	if realExecutionFailed || negativeExecutionFailed {
		classification = "failed"
		reason = witnessExecutionFailure(realExecution, negativeExecution, witness.NegativeExpectedExitCode)
	} else if finalSourceTreeHashErr != nil {
		classification = "failed"
		reason = fmt.Sprintf("hash source tree after witness execution: %v", finalSourceTreeHashErr)
	} else if realExecution.SourceTreeHash != negativeExecution.SourceTreeHash || realExecution.SourceTreeHash != finalSourceTreeHash {
		classification = "malformed"
		reason = "source tree changed during real or negative witness execution"
	} else {
		realObservation, realParseError = parseWitnessObservation(witness.Observation, realExecution.Stdout)
		negativeObservation, negativeParseError = parseWitnessObservation(witness.Observation, negativeExecution.Stdout)
		if realParseError != "" || negativeParseError != "" {
			classification = "malformed"
			reason = strings.TrimSpace(strings.Join([]string{realParseError, negativeParseError}, "; "))
		}
	}

	behavioralCheck, predicateErr := witnessCheck(witness)
	var verdict check.Verdict
	verdictEvaluated := false
	if classification == "" && predicateErr != nil {
		classification, reason = "malformed", predicateErr.Error()
	}
	if classification == "" {
		if malformed, malformedReason := stubMalformed(behavioralCheck, realObservation, negativeObservation); malformed {
			classification, reason = "malformed", malformedReason
		} else {
			verdict = check.Verify(behavioralCheck, realObservation, negativeObservation)
			verdictEvaluated = true
			switch {
			case verdict.Structural:
				classification = "malformed"
			case verdict.Theater:
				classification = "theater"
			case verdict.Passed:
				classification = "passed"
			default:
				classification = "failed"
			}
			reason = verdict.Reason
		}
	}

	realExecutionID, negativeExecutionID := uuid.NewString(), uuid.NewString()
	realStored := witnessStoreExecution(realExecutionID, "real", realExecution)
	negativeStored := witnessStoreExecution(negativeExecutionID, "negative", negativeExecution)
	if realObservation != nil || realParseError != "" {
		realStored.Observation = witnessStoredObservation(witness.Observation, realObservation, realParseError)
	}
	if negativeObservation != nil || negativeParseError != "" {
		negativeStored.Observation = witnessStoredObservation(witness.Observation, negativeObservation, negativeParseError)
	}
	run := store.ValidationRun{
		DefinitionID: witnessID, SessionID: sessionID, SourceTreeHash: realExecution.SourceTreeHash,
		Classification: classification, Reason: reason, StartedAt: realExecution.StartedAt,
		CompletedAt: negativeExecution.CompletedAt,
		DurationMS:  negativeExecution.CompletedAt.Sub(realExecution.StartedAt).Milliseconds(),
		Executions:  []store.ValidationExecution{realStored, negativeStored},
	}
	if run.SourceTreeHash == "" {
		run.SourceTreeHash = negativeExecution.SourceTreeHash
	}
	if run.SourceTreeHash == "" {
		run.SourceTreeHash = contextmanifest.Hash("unavailable:" + witnessID)
	}
	if run.DurationMS < 0 {
		run.DurationMS = 0
	}
	if predicateErr == nil {
		run.PredicateResults = append(run.PredicateResults,
			witnessPredicateResults(realExecutionID, witness.Predicates, behavioralCheck.Predicates, realObservation)...)
		run.PredicateResults = append(run.PredicateResults,
			witnessPredicateResults(negativeExecutionID, witness.Predicates, behavioralCheck.Predicates, negativeObservation)...)
	}
	persisted, err := s.store.RecordValidationRun(ctx, store.ValidationRunWrite{ProjectName: project.Name, Run: run})
	if err != nil {
		return VerifyResult{}, fmt.Errorf("verify witness: persist run: %w", err)
	}
	if checkID != "" {
		state := classification
		if err := s.store.SetCheckState(checkID, state); err != nil {
			return VerifyResult{}, fmt.Errorf("verify witness: update check: %w", err)
		}
	}
	if err := s.reprojectCompletion(ctx, planResult, store.TransitionSource{
		Type: "validation_run", ID: persisted.ID, SessionID: sessionID,
	}); err != nil {
		return VerifyResult{}, fmt.Errorf("verify witness: project completion: %w", err)
	}
	if classification == "passed" {
		if err := s.settlePassedWitnessRepairContracts(ctx, planResult, witness, sessionID, persisted.SourceTreeHash); err != nil {
			return VerifyResult{}, err
		}
	}
	repairResources := witnessRepairResourcesForOutcome(
		witness, classification, realExecutionFailed, negativeExecutionFailed,
		finalSourceTreeHashErr != nil, verdictEvaluated, verdict,
		behavioralCheck, realObservation,
	)
	repairRouting, err := s.reopenFailedWitnessRepairResources(
		ctx, sessionID, witness, classification, persisted.ID, reason, repairResources,
	)
	if err != nil {
		return VerifyResult{}, err
	}
	if repairRouting == nil {
		repairRouting = &store.BehavioralWitnessRepairRoutingResult{}
	}
	return VerifyResult{
		WitnessID: witnessID, ValidationRunID: persisted.ID, Classification: classification,
		ReopenedResources:  repairRouting.ReopenedResources,
		ExhaustedResources: repairRouting.ExhaustedResources,
		RepairEpochs:       repairRouting.Epochs,
		Passed:             classification == "passed", Theater: classification == "theater",
		Malformed: classification == "malformed", Reason: reason,
	}, nil
}

// reopenFailedWitnessRepairResources turns a failed accepted-tree observation
// into narrowly declared repair work. Positive predicate failures route only
// their opted-in owners (or the legacy witness-wide owners when no predicate
// opts in). A controlled negative that fails to falsify may route only the
// explicit counterexample owners. Malformed results never reopen source. A
// missing session keeps spec/verify useful as evidence-only verification.
func (s *Spec) reopenFailedWitnessRepairResources(
	ctx context.Context,
	sessionID string,
	witness cuepkg.Witness,
	classification string,
	validationRunID string,
	reason string,
	repairResourceIDs []string,
) (*store.BehavioralWitnessRepairRoutingResult, error) {
	if (classification != "failed" && classification != "theater") ||
		sessionID == "" || len(repairResourceIDs) == 0 {
		return nil, nil
	}
	session, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("verify witness: inspect repair session: %w", err)
	}
	if session.Status != "active" {
		return nil, nil
	}

	eligible := make([]string, 0, len(repairResourceIDs))
	for _, resourceID := range repairResourceIDs {
		resource, getErr := s.store.GetSessionResource(sessionID, resourceID)
		if getErr != nil {
			// A repair owner outside the changed/verification frontier is not
			// authorization to enlarge this immutable session after Begin.
			continue
		}
		if ResourceState(resource.State) == StateCommitted {
			eligible = append(eligible, resourceID)
		}
	}
	if len(eligible) == 0 {
		return nil, nil
	}
	sort.Strings(eligible)
	repairReason := fmt.Sprintf(
		"behavioral witness %s failed in validation run %s: %s",
		witness.ID, validationRunID, reason,
	)
	maxRepairRetries := 0
	if s.cfg != nil {
		maxRepairRetries = s.cfg.WaveMaxRetries
	}
	routed, err := s.store.RouteBehavioralWitnessFailure(ctx, store.BehavioralWitnessRepairRoutingInput{
		SessionID: sessionID, ValidationRunID: validationRunID,
		ResourceIDs: eligible, Reason: repairReason,
		MaxRepairRetries: maxRepairRetries,
	})
	if err != nil {
		return nil, fmt.Errorf("verify witness: route declared repair resources: %w", err)
	}
	return routed, nil
}

// witnessRepairResourcesForOutcome keeps positive production failures and
// controlled-negative harness failures on separate ownership paths. Process-
// level real failures retain the witness-wide fallback because no predicate
// observation exists. Once any predicate opts into exact routing, only owners
// of predicates that actually failed are eligible. The negative side is
// always opt-in: theater or an unexpected negative exit can only repair the
// declared counterexample harness owners.
func witnessRepairResourcesForOutcome(
	witness cuepkg.Witness,
	classification string,
	realExecutionFailed, negativeExecutionFailed, sourceHashFailed bool,
	verdictEvaluated bool,
	verdict check.Verdict,
	behavioralCheck check.Check,
	realObservation check.Observation,
) []string {
	if classification != "failed" && classification != "theater" {
		return nil
	}
	owners := make(map[string]bool)
	add := func(resourceIDs []string) {
		for _, resourceID := range resourceIDs {
			owners[resourceID] = true
		}
	}

	if realExecutionFailed || sourceHashFailed {
		add(witness.RepairResources)
	} else if verdictEvaluated && !verdict.RealResult.Passed {
		predicateRoutingOptIn := false
		for _, predicate := range witness.Predicates {
			predicateRoutingOptIn = predicateRoutingOptIn || len(predicate.RepairResources) > 0
		}
		if !predicateRoutingOptIn {
			add(witness.RepairResources)
		} else {
			for index, predicate := range behavioralCheck.Predicates {
				evaluation := check.Evaluate(check.Check{Predicates: []check.Predicate{predicate}}, realObservation)
				if !evaluation.Passed {
					add(witness.Predicates[index].RepairResources)
				}
			}
		}
	}

	if negativeExecutionFailed || (verdictEvaluated && verdict.StubResult.Passed) {
		add(witness.CounterexampleRepairResources)
	}
	result := make([]string, 0, len(owners))
	for resourceID := range owners {
		result = append(result, resourceID)
	}
	sort.Strings(result)
	return result
}

func (s *Spec) projectRoot() string {
	return filepath.Dir(filepath.Clean(s.cfg.SpecDir))
}

func witnessByID(witnesses map[string]cuepkg.Witness, id string) (cuepkg.Witness, bool) {
	for _, witness := range witnesses {
		if witness.ID == id {
			return witness, true
		}
	}
	return cuepkg.Witness{}, false
}

func witnessExecutionFailed(execution validationpkg.Execution, expectedExitCode int) bool {
	return execution.LaunchError != "" || execution.TimedOut || execution.ExitCode != expectedExitCode
}

func witnessExecutionFailure(real, negative validationpkg.Execution, negativeExpectedExitCode int) string {
	var failures []string
	for _, item := range []struct {
		name             string
		run              validationpkg.Execution
		expectedExitCode int
	}{{"real", real, 0}, {"negative", negative, negativeExpectedExitCode}} {
		switch {
		case item.run.TimedOut:
			failures = append(failures, item.name+" command timed out")
		case item.run.LaunchError != "":
			failures = append(failures, item.name+" command could not run: "+item.run.LaunchError)
		case item.run.ExitCode != item.expectedExitCode:
			failure := fmt.Sprintf("%s command exited %d", item.name, item.run.ExitCode)
			if item.expectedExitCode != 0 {
				failure += fmt.Sprintf(", expected %d", item.expectedExitCode)
			}
			failures = append(failures, failure)
		}
	}
	return strings.Join(failures, "; ")
}

func parseWitnessObservation(declaration cuepkg.ObservationDeclaration, stdout string) (check.Observation, string) {
	var payloads []string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, declaration.Marker) {
			payloads = append(payloads, strings.TrimSpace(strings.TrimPrefix(line, declaration.Marker)))
		}
	}
	if len(payloads) != 1 {
		return nil, fmt.Sprintf("expected exactly one %q observation marker, found %d", declaration.Marker, len(payloads))
	}
	var observation check.Observation
	if err := json.Unmarshal([]byte(payloads[0]), &observation); err != nil {
		return nil, fmt.Sprintf("parse observation JSON: %v", err)
	}
	for _, field := range sortedStringMapKeys(declaration.Schema) {
		value, exists := observation[field]
		if !exists {
			return nil, fmt.Sprintf("observation is missing schema field %q", field)
		}
		if !witnessValueMatchesType(value, declaration.Schema[field]) {
			return nil, fmt.Sprintf("observation field %q does not match type %s", field, declaration.Schema[field])
		}
	}
	return observation, ""
}

func witnessValueMatchesType(value any, kind string) bool {
	switch kind {
	case "any":
		return true
	case "number":
		_, ok := value.(float64)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "bool":
		_, ok := value.(bool)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	default:
		return false
	}
}

func witnessCheck(witness cuepkg.Witness) (check.Check, error) {
	result := check.Check{Behavior: witness.ID}
	for _, predicate := range witness.Predicates {
		converted := check.Predicate{Field: predicate.Field, Op: check.Op(predicate.Op), Min: predicate.Min, Max: predicate.Max, Member: predicate.Member}
		if predicate.Value != nil {
			if number, ok := numericValue(predicate.Value); ok {
				converted.Value = check.F(number)
			} else if predicate.Op == "eq" || predicate.Op == "equals" {
				converted.Member = predicate.Value
			} else {
				return check.Check{}, fmt.Errorf("predicate %s.%s requires a numeric value", predicate.Field, predicate.Op)
			}
		}
		result.Predicates = append(result.Predicates, converted)
	}
	return result, nil
}

func numericValue(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	default:
		return 0, false
	}
}

func witnessStoreExecution(id, role string, execution validationpkg.Execution) store.ValidationExecution {
	stored := store.ValidationExecution{
		ID: id, Role: role, CommandJSON: execution.CommandJSON, CommandHash: execution.CommandHash,
		WorkingDirectory: execution.WorkingDirectory, EnvironmentJSON: execution.EnvironmentJSON,
		EnvironmentHash: execution.EnvironmentHash, SourceTreeHash: execution.SourceTreeHash,
		ExecutableHash: execution.ExecutableHash, Stdout: execution.Stdout, StdoutHash: execution.StdoutHash,
		StdoutBytes: execution.StdoutBytes, StdoutTruncated: execution.StdoutTruncated,
		Stderr: execution.Stderr, StderrHash: execution.StderrHash,
		StderrBytes: execution.StderrBytes, StderrTruncated: execution.StderrTruncated,
		ExitCode: execution.ExitCode, TimedOut: execution.TimedOut, LaunchError: execution.LaunchError,
		StartedAt: execution.StartedAt, CompletedAt: execution.CompletedAt, DurationMS: execution.DurationMS,
	}
	for _, artifact := range execution.Artifacts {
		stored.Artifacts = append(stored.Artifacts, store.ValidationArtifact{
			Path: artifact.Path, Content: artifact.Content, ContentHash: artifact.ContentHash,
			ByteSize: int(artifact.ByteSize), CapturedBytes: int(artifact.CapturedBytes),
			Truncated: artifact.Truncated, Missing: artifact.Missing,
		})
	}
	return stored
}

func witnessStoredObservation(declaration cuepkg.ObservationDeclaration, observation check.Observation, parseError string) *store.ParsedObservation {
	schemaJSON, _ := json.Marshal(declaration.Schema)
	observationJSON, _ := json.Marshal(observation)
	return &store.ParsedObservation{
		SchemaJSON: string(schemaJSON), SchemaHash: contextmanifest.Hash(string(schemaJSON)),
		ObservationJSON: string(observationJSON), ObservationHash: contextmanifest.Hash(string(observationJSON)),
		ParseError: parseError,
	}
}

func witnessPredicateResults(executionID string, declarations []cuepkg.WitnessPredicate, predicates []check.Predicate, observation check.Observation) []store.ValidationPredicateResult {
	results := make([]store.ValidationPredicateResult, 0, len(predicates))
	for index, predicate := range predicates {
		evaluation := check.Evaluate(check.Check{Predicates: []check.Predicate{predicate}}, observation)
		expectedJSON, _ := json.Marshal(declarations[index])
		observedJSON, _ := json.Marshal(observation[predicate.Field])
		reason := strings.Join(evaluation.Failures, "; ")
		results = append(results, store.ValidationPredicateResult{
			ExecutionID: executionID, Ordinal: index, Field: predicate.Field, Operator: string(predicate.Op),
			ExpectedJSON: string(expectedJSON), ObservedJSON: string(observedJSON), Passed: evaluation.Passed, Reason: reason,
		})
	}
	return results
}

func sortedStringMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
