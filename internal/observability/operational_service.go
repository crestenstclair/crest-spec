package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	cserrors "github.com/crestenstclair/crest-spec/internal/errors"
	impactpkg "github.com/crestenstclair/crest-spec/internal/impact"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"github.com/crestenstclair/crest-spec/internal/store"
)

func (s *Service) Plan(ctx context.Context, projectName, specHash string, actions []planpkg.PlannedAction, waves [][]string, slices []planpkg.CapabilitySlice) (*PlanView, error) {
	result := &PlanView{Version: APIVersion, ProjectName: projectName, SpecHash: specHash, Links: []Link{
		link("self", "plan", "current", "/api/v1/plan"), link("project", "project", projectName, "/api/v1/project"),
	}}
	waveByResource := make(map[string]int)
	for index, resources := range waves {
		for _, resourceID := range resources {
			waveByResource[resourceID] = index
		}
	}
	session, _ := s.store.GetActiveSession()
	states := make(map[string]store.SessionResource)
	if session != nil {
		result.ActiveSessionID = session.ID
		rows, err := s.store.ListSessionResources(session.ID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			states[row.ResourceID] = row
		}
		var persisted []planpkg.PlannedAction
		if err := json.Unmarshal([]byte(session.PlanJSON), &persisted); err == nil && !sameOperationIDs(actions, persisted) {
			result.State.Stale = true
			result.State.Reason = "active session was created from a different plan revision"
		}
	}
	for _, action := range actions {
		state := states[action.ResourceID]
		executionStatus := state.State
		if executionStatus == "" {
			executionStatus = "planned"
		}
		result.Operations = append(result.Operations, PlanOperation{
			ID: action.OperationID, ResourceID: action.ResourceID, Kind: string(action.Kind), Category: action.Category,
			Reason: action.Reason, CascadedFrom: action.CascadedFrom, WaveIndex: waveByResource[action.ResourceID],
			RecommendedRole: action.RecommendedRole, Goals: append([]string(nil), action.Goals...),
			Capabilities: append([]string(nil), action.Capabilities...), Contributions: action.Contributions,
			ExpectedBehavior: append([]string(nil), action.ExpectedBehavior...), ExpectedEvidence: append([]string(nil), action.ExpectedEvidence...),
			Shared: action.SharedInfrastructure, ExecutionStatus: executionStatus, ExecutionAttempts: state.Attempts, LastError: state.LastError,
			Links: []Link{
				link("self", "plan_operation", action.OperationID, "/api/v1/plan/operations/"+action.OperationID),
				link("resource", "resource", action.ResourceID, "/api/v1/resources/"+action.ResourceID),
				link("contexts", "context", "", "/api/v1/contexts?resource_id="+action.ResourceID),
			},
		})
	}
	for _, slice := range slices {
		status := "planned"
		if allOperationsInState(slice.OperationIDs, result.Operations, "committed") {
			status = "implemented"
		} else if anyOperationStarted(slice.OperationIDs, result.Operations) {
			status = "in_progress"
		}
		result.Slices = append(result.Slices, PlanSlice{
			Capability: slice.Capability, Goals: append([]string(nil), slice.Goals...), Required: slice.Required,
			CurrentGap: slice.CurrentGap, ExpectedBehavior: append([]string(nil), slice.ExpectedBehavior...),
			ExpectedEvidence: append([]string(nil), slice.ExpectedEvidence...), OperationIDs: append([]string(nil), slice.OperationIDs...),
			Status: status, Links: []Link{link("capability", "capability", slice.Capability, "/api/v1/capabilities/"+slice.Capability)},
		})
	}
	operationsByResource := make(map[string]string)
	for _, operation := range result.Operations {
		operationsByResource[operation.ResourceID] = operation.ID
	}
	for index, resources := range waves {
		wave := PlanWave{Index: index, Resources: append([]string(nil), resources...), State: "pending", Counts: make(map[string]int)}
		for _, resourceID := range resources {
			if operationID := operationsByResource[resourceID]; operationID != "" {
				wave.Operations = append(wave.Operations, operationID)
			}
			state := states[resourceID].State
			if state == "" {
				state = "pending"
			}
			wave.Counts[state]++
		}
		if len(resources) > 0 && wave.Counts["committed"] == len(resources) {
			wave.State = "complete"
		} else if session != nil && index == session.CurrentWave {
			wave.State = "active"
		} else if session != nil && index < session.CurrentWave {
			wave.State = "blocked"
		}
		if session != nil {
			wave.Links = []Link{link("session_wave", "wave", fmt.Sprint(index), "/api/session-resources/"+session.ID+"/wave/"+fmt.Sprint(index))}
		}
		result.Waves = append(result.Waves, wave)
	}
	result.Status = explainPlanStatus(result)
	return result, nil
}

func PlanOperationByID(plan *PlanView, operationID string) (*PlanOperation, error) {
	for index := range plan.Operations {
		if plan.Operations[index].ID == operationID {
			return &plan.Operations[index], nil
		}
	}
	return nil, cserrors.ErrNotFound
}

func Impact(result impactpkg.Result) ImpactView {
	view := ImpactView{
		Version: APIVersion, SourceResource: result.SourceResource,
		AffectedResources: result.AffectedResources, Capabilities: result.Capabilities, Goals: result.Goals,
		Evidence: result.Evidence, RegressedGoals: result.RegressedGoals, Explanation: result.Explanation,
		Links: []Link{link("source", "resource", result.SourceResource, "/api/v1/resources/"+result.SourceResource)},
	}
	for _, goalID := range result.Goals {
		view.Links = append(view.Links, link("affected_goal", "goal", goalID, "/api/v1/goals/"+goalID))
	}
	return view
}

func (s *Service) Failures(ctx context.Context, request PageRequest) (*Page[FailureSummary], error) {
	request, err := NormalizePageRequest(request)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListFailureClassifications(ctx, 10000)
	if err != nil {
		return nil, err
	}
	views := make([]FailureSummary, 0, len(rows))
	for _, row := range rows {
		resolved := row.Resolution != ""
		if request.Kind != "" && row.Category != request.Kind {
			continue
		}
		if request.Status == "resolved" && !resolved || request.Status == "unresolved" && resolved {
			continue
		}
		if request.GoalID != "" && !contains(row.GoalIDs, request.GoalID) {
			continue
		}
		if request.Query != "" && !containsFold(row.ID+" "+row.ResourceID+" "+row.Category+" "+row.CorrectiveAction, request.Query) {
			continue
		}
		views = append(views, failureSummary(row))
	}
	if request.Cursor != "" {
		decoded, _ := decodeCursor(request.Cursor)
		filtered := views[:0]
		for _, view := range views {
			if afterCursor(view.CreatedAt, view.ID, decoded) {
				filtered = append(filtered, view)
			}
		}
		views = filtered
	}
	page, more := take(views, request.Limit)
	result := &Page[FailureSummary]{Version: APIVersion, Items: page, Page: PageInfo{Limit: request.Limit, HasMore: more}}
	if more && len(page) > 0 {
		last := page[len(page)-1]
		result.Page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	return result, nil
}

func (s *Service) Failure(ctx context.Context, id string) (*FailureDetail, error) {
	row, err := s.store.GetFailureClassification(ctx, id)
	if err != nil {
		return nil, err
	}
	result := &FailureDetail{Version: APIVersion, Failure: failureSummary(*row), Evidence: row.Evidence}
	if row.Resolution == "" {
		result.Status = StatusExplanation{State: "unresolved", Reason: row.CorrectiveAction, Blockers: []string{row.Category}, RecommendedNext: []string{row.CorrectiveAction}}
	} else {
		result.Status = StatusExplanation{State: "resolved", Reason: row.Resolution}
	}
	result.Links = result.Failure.Links
	return result, nil
}

func (s *Service) Attempt(ctx context.Context, attemptID string) (*AttemptDetail, error) {
	manifest, err := s.store.GetContextManifestByAttempt(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	attempt := manifest.Attempt
	result := &AttemptDetail{Version: APIVersion, Attempt: AttemptSummary{
		ID: attempt.ID, SessionID: attempt.SessionID, ResourceID: attempt.ResourceID, PlanOperationID: attempt.PlanOperationID,
		ParentAttemptID: attempt.ParentAttemptID, RetryNumber: attempt.RetryNumber, Role: attempt.Role, Status: attempt.Status,
		CreatedAt: attempt.CreatedAt, Links: []Link{link("context", "context", manifest.ID, "/api/v1/contexts/"+manifest.ID)},
	}}
	result.Context = &AttemptContext{
		ID: manifest.ID, ContextHash: manifest.ContextHash, SelectorVersion: manifest.SelectorVersion,
		EstimatorVersion: manifest.EstimatorVersion, SelectionStrategy: manifest.SelectionStrategy,
		BudgetTokens: manifest.BudgetTokens, EstimatedTokens: manifest.EstimatedTokens,
		TemplateHashes: manifest.TemplateHashes, Blocked: manifest.Blocked, BlockedReason: manifest.BlockedReason,
		Links: []Link{link("self", "context", manifest.ID, "/api/v1/contexts/"+manifest.ID)},
	}
	if execution, getErr := s.store.GetExecutionManifestByAttempt(ctx, attemptID); getErr == nil {
		result.Execution = &AttemptExecution{
			ID: execution.ID, HostName: execution.HostName, HostVersion: execution.HostVersion, Provider: execution.Provider,
			Model: execution.Model, Role: execution.Role, ContextPolicy: execution.ContextPolicy,
			InferenceHash: execution.InferenceConfigHash, AgentConfigHash: execution.AgentConfigHash,
			ToolHash: execution.ToolPermissionsHash, TemplateHash: execution.TemplateHashesHash,
			InstructionsHash: execution.SystemInstructionsHash, HostCommitRef: execution.HostCommitRef,
			Status: execution.Status, DispositionReason: execution.DispositionReason, DurationMS: execution.DurationMS,
			InputTokens: execution.InputTokens, OutputTokens: execution.OutputTokens, CostUSD: execution.CostUSD,
			GoalProgress: execution.GoalProgress, Links: []Link{link("self", "execution", execution.ID, "/api/v1/executions/"+execution.ID)},
		}
	}
	if candidate, getErr := s.store.GetCandidateSetByAttempt(ctx, attemptID); getErr == nil {
		view := &AttemptCandidate{ID: candidate.ID, CandidateHash: candidate.CandidateHash, Status: candidate.Status, DispositionReason: candidate.DispositionReason}
		for _, file := range candidate.Files {
			view.Files = append(view.Files, CandidateFileRef{Path: file.Path, ContentHash: file.ContentHash, ByteSize: file.ByteSize, WriteIntent: file.WriteIntent})
		}
		result.Candidate = view
	}
	runs, err := s.store.ListValidationRuns(ctx, 10000)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		if run.AttemptID != attemptID {
			continue
		}
		result.Validations = append(result.Validations, ValidationRef{
			ID: run.ID, DefinitionID: run.DefinitionID, Classification: run.Classification,
			SourceTreeHash: run.SourceTreeHash, CreatedAt: run.CreatedAt,
			Links: []Link{link("validation", "validation", run.ID, "/api/v1/verifications/"+run.ID)},
		})
	}
	failures, err := s.store.ListFailureClassificationsByAttempt(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	for _, failure := range failures {
		result.Failures = append(result.Failures, failureSummary(failure))
	}
	result.Status = explainAttemptStatus(result)
	result.Links = []Link{
		link("self", "attempt", attemptID, "/api/v1/attempts/"+attemptID),
		link("resource", "resource", attempt.ResourceID, "/api/v1/resources/"+attempt.ResourceID),
		link("plan_operation", "plan_operation", attempt.PlanOperationID, "/api/v1/plan/operations/"+attempt.PlanOperationID),
	}
	return result, nil
}

func (s *Service) CompareAttempts(ctx context.Context, leftID, rightID string) (*AttemptComparison, error) {
	if leftID == "" || rightID == "" || leftID == rightID {
		return nil, fmt.Errorf("left and right must identify two different attempts")
	}
	left, err := s.Attempt(ctx, leftID)
	if err != nil {
		return nil, fmt.Errorf("left attempt: %w", err)
	}
	right, err := s.Attempt(ctx, rightID)
	if err != nil {
		return nil, fmt.Errorf("right attempt: %w", err)
	}
	comparison := &AttemptComparison{Version: APIVersion, Left: *left, Right: *right}
	comparison.Changes = []AttemptComparisonChange{
		comparisonChange("task", left.Attempt.PlanOperationID+"/"+left.Attempt.Role, right.Attempt.PlanOperationID+"/"+right.Attempt.Role),
		comparisonChange("context", contextIdentity(left.Context), contextIdentity(right.Context)),
		comparisonChange("execution", executionIdentity(left.Execution), executionIdentity(right.Execution)),
		comparisonChange("candidate", candidateIdentity(left.Candidate), candidateIdentity(right.Candidate)),
		comparisonChange("validation", validationIdentity(left.Validations), validationIdentity(right.Validations)),
		comparisonChange("failure", failureIdentity(left.Failures), failureIdentity(right.Failures)),
		comparisonChange("cost", executionCost(left.Execution), executionCost(right.Execution)),
		comparisonChange("outcome", left.Status.State, right.Status.State),
	}
	for _, change := range comparison.Changes {
		if change.Changed {
			comparison.Summary = append(comparison.Summary, change.Area+" changed")
		}
	}
	if len(comparison.Summary) == 0 {
		comparison.Summary = []string{"governed attempt inputs and outcomes match"}
	}
	comparison.Links = []Link{
		link("left", "attempt", leftID, "/api/v1/attempts/"+leftID),
		link("right", "attempt", rightID, "/api/v1/attempts/"+rightID),
	}
	return comparison, nil
}

func explainPlanStatus(plan *PlanView) StatusExplanation {
	result := StatusExplanation{State: "current", Reason: "plan is derived from the current declarations and accepted state"}
	if len(plan.Operations) == 0 {
		result.State = "complete"
		result.Reason = "no resource reconciliation operations remain"
		result.RecommendedNext = []string{"Run current project verification after relevant external changes"}
		return result
	}
	if plan.ActiveSessionID == "" {
		result.State = "planned"
		result.Reason = "resource operations remain and no execution session is active"
		result.Missing = []string{fmt.Sprintf("%d planned operations", len(plan.Operations))}
		result.RecommendedNext = []string{"Begin the goal-directed plan"}
		return result
	}
	result.State = "active"
	result.Reason = "an execution session is reconciling this plan"
	for _, operation := range plan.Operations {
		if operation.ExecutionStatus == "blocked" || operation.ExecutionStatus == "errored" {
			result.Blockers = append(result.Blockers, operation.ResourceID+": "+operation.LastError)
		}
	}
	if len(result.Blockers) > 0 {
		result.State = "blocked"
		result.RecommendedNext = []string{"Resolve the first blocked plan operation"}
	}
	return result
}

func failureSummary(row store.FailureClassification) FailureSummary {
	return FailureSummary{
		ID: row.ID, AttemptID: row.AttemptID, ExecutionID: row.ExecutionID, ResourceID: row.ResourceID,
		Category: row.Category, Origin: row.Origin, Confidence: row.Confidence, EvidenceSource: row.EvidenceSource,
		EvidenceReference: row.EvidenceReference, EvidenceHash: row.EvidenceHash, CorrectiveAction: row.CorrectiveAction,
		NextRole: row.NextRole, BlocksRetry: row.BlocksRetry, GoalIDs: append([]string(nil), row.GoalIDs...),
		Resolution: row.Resolution, ResolvedByAttempt: row.ResolvedByAttempt, CreatedAt: row.CreatedAt, ResolvedAt: row.ResolvedAt,
		Links: []Link{
			link("self", "failure", row.ID, "/api/v1/failures/"+row.ID),
			link("attempt", "attempt", row.AttemptID, "/api/v1/attempts/"+row.AttemptID),
			link("resource", "resource", row.ResourceID, "/api/v1/resources/"+row.ResourceID),
		},
	}
}

func explainAttemptStatus(attempt *AttemptDetail) StatusExplanation {
	result := StatusExplanation{State: attempt.Attempt.Status, Reason: "generation attempt state"}
	if attempt.Context != nil && attempt.Context.Blocked {
		result.State, result.Reason = "blocked", attempt.Context.BlockedReason
		result.Blockers = append(result.Blockers, attempt.Context.BlockedReason)
	}
	if attempt.Execution == nil {
		result.Missing = append(result.Missing, "execution manifest")
		result.RecommendedNext = append(result.RecommendedNext, "Start a governed host execution")
	} else {
		result.State, result.Reason = attempt.Execution.Status, attempt.Execution.DispositionReason
	}
	if attempt.Candidate == nil {
		result.Missing = append(result.Missing, "candidate output")
	}
	for _, failure := range attempt.Failures {
		if failure.Resolution == "" {
			result.Blockers = append(result.Blockers, failure.Category+": "+failure.CorrectiveAction)
		}
	}
	return result
}

func sameOperationIDs(left, right []planpkg.PlannedAction) bool {
	leftIDs, rightIDs := make([]string, len(left)), make([]string, len(right))
	for index := range left {
		leftIDs[index] = left[index].OperationID
	}
	for index := range right {
		rightIDs[index] = right[index].OperationID
	}
	sort.Strings(leftIDs)
	sort.Strings(rightIDs)
	return strings.Join(leftIDs, "\x00") == strings.Join(rightIDs, "\x00")
}

func allOperationsInState(ids []string, operations []PlanOperation, state string) bool {
	if len(ids) == 0 {
		return false
	}
	states := make(map[string]string)
	for _, operation := range operations {
		states[operation.ID] = operation.ExecutionStatus
	}
	for _, id := range ids {
		if states[id] != state {
			return false
		}
	}
	return true
}

func anyOperationStarted(ids []string, operations []PlanOperation) bool {
	states := make(map[string]string)
	for _, operation := range operations {
		states[operation.ID] = operation.ExecutionStatus
	}
	for _, id := range ids {
		if states[id] != "" && states[id] != "planned" && states[id] != "pending" {
			return true
		}
	}
	return false
}

func comparisonChange(area, before, after string) AttemptComparisonChange {
	return AttemptComparisonChange{Area: area, Changed: before != after, Before: before, After: after, Reason: area + " identity is compared by governed hashes and terminal state"}
}

func contextIdentity(value *AttemptContext) string {
	if value == nil {
		return "missing"
	}
	return value.ContextHash + "/" + value.SelectorVersion + "/" + fmt.Sprint(value.BudgetTokens)
}

func executionIdentity(value *AttemptExecution) string {
	if value == nil {
		return "missing"
	}
	return strings.Join([]string{value.HostName, value.HostVersion, value.Provider, value.Model, value.Role, value.ContextPolicy, value.InferenceHash, value.AgentConfigHash, value.ToolHash, value.TemplateHash, value.InstructionsHash, value.HostCommitRef}, "/")
}

func candidateIdentity(value *AttemptCandidate) string {
	if value == nil {
		return "missing"
	}
	return value.CandidateHash + "/" + value.Status
}

func validationIdentity(values []ValidationRef) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, value.DefinitionID+":"+value.Classification+":"+value.SourceTreeHash)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func failureIdentity(values []FailureSummary) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, value.Category+":"+value.Resolution)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func executionCost(value *AttemptExecution) string {
	if value == nil {
		return "missing"
	}
	return fmt.Sprintf("tokens=%d/%d,cost=%.6f,duration=%d", value.InputTokens, value.OutputTokens, value.CostUSD, value.DurationMS)
}
