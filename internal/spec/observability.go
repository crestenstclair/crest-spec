package spec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	cserrors "github.com/crestenstclair/crest-spec/internal/errors"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// ---------------------------------------------------------------------------
// Session-level status — overview of an active session
// ---------------------------------------------------------------------------

// SessionStatusResult is the session-level overview returned by SessionStatus.
type SessionStatusResult struct {
	SessionID            string            `json:"session_id"`
	ApplyID              string            `json:"apply_id"`
	CurrentWave          int               `json:"current_wave"`
	TotalWaves           int               `json:"total_waves"`
	ElapsedMS            int64             `json:"elapsed_ms"`
	LastActivityAt       string            `json:"last_activity_at,omitempty"`
	StalledMS            int64             `json:"stalled_ms"`
	ActiveAttempts       int               `json:"active_attempts"`
	QueuedResources      int               `json:"queued_resources"`
	CompletedResources   int               `json:"completed_resources"`
	ActiveWallMS         int64             `json:"active_wall_ms"`
	SummedEffortMS       int64             `json:"summed_effort_ms"`
	IdleMS               int64             `json:"idle_ms"`
	MaxUsefulConcurrency int               `json:"max_useful_concurrency"`
	Timing               TimingBreakdown   `json:"timing"`
	WaveSummaries        []WaveSummary     `json:"wave_summaries"`
	Concurrency          ConcurrencyStatus `json:"concurrency"`
	WorkerPool           WorkerPoolStatus  `json:"worker_pool"`
}

// TimingBreakdown reports only intervals bounded by persisted observations.
// Per-category values are wall-time unions and may overlap when different
// resources are in different phases concurrently. Missing host events remain
// unattributed; they are never guessed to be worker startup.
type TimingBreakdown struct {
	DispatchToAcknowledgedMS   int64 `json:"dispatch_to_acknowledged_wall_ms"`
	SharedContextPreparationMS int64 `json:"shared_context_preparation_wall_ms"`
	ContextPreparationMS       int64 `json:"context_preparation_wall_ms"`
	PreparedToDispatchMS       int64 `json:"prepared_to_dispatch_wall_ms"`
	AcknowledgedToExecutionMS  int64 `json:"acknowledged_to_execution_wall_ms"`
	ModelActiveMS              int64 `json:"model_active_wall_ms"`
	SubmissionCommitMS         int64 `json:"submission_commit_wall_ms"`
	ValidationMS               int64 `json:"validation_wall_ms"`
	LegacyExecutionMS          int64 `json:"legacy_execution_wall_ms"`
	ObservedWallMS             int64 `json:"observed_wall_ms"`
	UnattributedWallMS         int64 `json:"unattributed_wall_ms"`
	MissingAcknowledgements    int   `json:"missing_acknowledgements"`
}

// ConcurrencyStatus reports useful persisted work, never MCP process count.
type ConcurrencyStatus struct {
	Active int `json:"active"`
	Max    int `json:"max"`
	Queued int `json:"queued"`
}

// WorkerPoolStatus summarizes explicit, engine-verified host assignments.
// Legacy implicit contexts have no claimed worker identity and are excluded.
type WorkerPoolStatus struct {
	UniqueWorkers           int `json:"unique_workers"`
	ReusedWorkers           int `json:"reused_workers"`
	Assignments             int `json:"assignments"`
	MaxAssignmentsPerWorker int `json:"max_assignments_per_worker"`
}

// WaveSummary counts resource states within a single wave.
type WaveSummary struct {
	WaveIndex            int             `json:"wave_index"`
	Total                int             `json:"total"`
	Committed            int             `json:"committed"`
	Rejected             int             `json:"rejected"`
	Errored              int             `json:"errored"`
	Pending              int             `json:"pending"`
	DurationMS           int64           `json:"duration_ms,omitempty"`
	ElapsedMS            int64           `json:"elapsed_ms,omitempty"`
	ActiveWallMS         int64           `json:"active_wall_ms,omitempty"`
	SummedEffortMS       int64           `json:"summed_effort_ms,omitempty"`
	MaxUsefulConcurrency int             `json:"max_useful_concurrency,omitempty"`
	Timing               TimingBreakdown `json:"timing"`
}

// SessionStatus returns a session-level overview: current wave, total waves,
// and per-wave resource state counts.
func (s *Spec) SessionStatus(ctx context.Context, sessionID string) (*SessionStatusResult, error) {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	var waves [][]string
	if err := json.Unmarshal([]byte(sess.WavesJSON), &waves); err != nil {
		return nil, fmt.Errorf("unmarshal waves: %w", err)
	}

	allResources, err := s.store.ListSessionResources(sessionID)
	if err != nil {
		return nil, fmt.Errorf("list session resources: %w", err)
	}

	now := time.Now().UTC()
	timing, err := s.loadSessionTiming(ctx, sess, allResources, now)
	if err != nil {
		return nil, err
	}
	summaries := buildWaveSummaries(waves, allResources, timing, now)

	return &SessionStatusResult{
		SessionID:            sessionID,
		ApplyID:              sess.ApplyID,
		CurrentWave:          sess.CurrentWave,
		TotalWaves:           len(waves),
		ElapsedMS:            timing.metrics.ElapsedMS,
		LastActivityAt:       formatObservationTime(timing.lastActivity),
		StalledMS:            timing.stalledMS,
		ActiveAttempts:       timing.activeAttempts,
		QueuedResources:      timing.queuedResources,
		CompletedResources:   timing.completedResources,
		ActiveWallMS:         timing.metrics.ActiveWallMS,
		SummedEffortMS:       timing.metrics.SummedEffortMS,
		IdleMS:               timing.metrics.IdleMS,
		MaxUsefulConcurrency: timing.metrics.MaxUsefulConcurrency,
		Timing:               timing.breakdown,
		WaveSummaries:        summaries,
		Concurrency: ConcurrencyStatus{
			Active: timing.activeAttempts,
			Max:    timing.metrics.MaxUsefulConcurrency,
			Queued: timing.queuedResources,
		},
		WorkerPool: timing.workerPool,
	}, nil
}

// buildWaveSummaries groups session resources by wave and counts states.
func buildWaveSummaries(
	waves [][]string,
	allResources []store.SessionResource,
	timing sessionTiming,
	now time.Time,
) []WaveSummary {
	stateByResource := make(map[string]string, len(allResources))
	for _, r := range allResources {
		stateByResource[r.ResourceID] = r.State
	}

	summaries := make([]WaveSummary, len(waves))
	for i, wave := range waves {
		summary := WaveSummary{WaveIndex: i}
		intervals := append([]workInterval(nil), timing.batchByWave[i]...)
		attributed := append([]workInterval(nil), timing.batchByWave[i]...)
		missingAcknowledgements := 0
		for _, id := range wave {
			stateValue, isSessionAction := stateByResource[id]
			if !isSessionAction {
				continue
			}
			summary.Total++
			intervals = append(intervals, timing.byResource[id]...)
			attributed = append(attributed, timing.attributedByResource[id]...)
			missingAcknowledgements += timing.missingAckByResource[id]
			state := ResourceState(stateValue)
			switch {
			case state == StateCommitted:
				summary.Committed++
			case state == StateRejected:
				summary.Rejected++
			case state == StateErrored || state == StateTimedOut || state == StateBlocked:
				summary.Errored++
			default:
				summary.Pending++
			}
		}
		populateWaveTiming(&summary, intervals, attributed, now)
		summary.Timing.MissingAcknowledgements = missingAcknowledgements
		summaries[i] = summary
	}
	return summaries
}

// ---------------------------------------------------------------------------
// Wave-level status — detailed view of resources in a single wave
// ---------------------------------------------------------------------------

// WaveStatusResult is the detailed per-resource view of a single wave.
type WaveStatusResult struct {
	WaveIndex int              `json:"wave_index"`
	Resources []ResourceDetail `json:"resources"`
}

// ResourceDetail holds per-resource observability data within a wave.
type ResourceDetail struct {
	ResourceID           string          `json:"resource_id"`
	State                string          `json:"state"`
	Phase                string          `json:"phase,omitempty"`
	Attempts             int             `json:"attempts"`
	MaxRetries           int             `json:"max_retries"`
	LastError            string          `json:"last_error,omitempty"`
	DurationMS           int64           `json:"duration_ms,omitempty"`
	ElapsedMS            int64           `json:"elapsed_ms,omitempty"`
	ActiveWallMS         int64           `json:"active_wall_ms,omitempty"`
	SummedEffortMS       int64           `json:"summed_effort_ms,omitempty"`
	MaxUsefulConcurrency int             `json:"max_useful_concurrency,omitempty"`
	Timing               TimingBreakdown `json:"timing"`
	StartedAt            string          `json:"started_at,omitempty"`
	CompletedAt          string          `json:"completed_at,omitempty"`
	DispatchedAt         string          `json:"dispatched_at,omitempty"`
}

// WaveStatus returns detailed resource-level state for a single wave.
func (s *Spec) WaveStatus(ctx context.Context, sessionID string, waveIndex int) (*WaveStatusResult, error) {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	resources, err := s.store.ListSessionResourcesByWave(sessionID, waveIndex)
	if err != nil {
		return nil, fmt.Errorf("list wave resources: %w", err)
	}
	allResources, err := s.store.ListSessionResources(sessionID)
	if err != nil {
		return nil, fmt.Errorf("list session resources: %w", err)
	}
	now := time.Now().UTC()
	timing, err := s.loadSessionTiming(ctx, sess, allResources, now)
	if err != nil {
		return nil, err
	}

	details := make([]ResourceDetail, len(resources))
	for i, r := range resources {
		d := ResourceDetail{
			ResourceID: r.ResourceID,
			State:      r.State,
			Phase:      r.Phase,
			Attempts:   r.Attempts,
			MaxRetries: r.MaxRetries,
			LastError:  r.LastError,
		}
		if !r.DispatchedAt.IsZero() {
			d.DispatchedAt = r.DispatchedAt.Format(time.RFC3339)
		}
		populateResourceTiming(&d, r, timing.byResource[r.ResourceID], now)
		windowStart, windowEnd := resourceTimingWindow(r, timing.attributedByResource[r.ResourceID], now)
		d.Timing = summarizeBreakdown(timing.attributedByResource[r.ResourceID], windowStart, windowEnd)
		d.Timing.MissingAcknowledgements = timing.missingAckByResource[r.ResourceID]
		details[i] = d
	}

	return &WaveStatusResult{
		WaveIndex: waveIndex,
		Resources: details,
	}, nil
}

type workInterval struct {
	resourceID string
	category   string
	identity   string
	startedAt  time.Time
	endedAt    time.Time
}

type timingMetrics struct {
	ElapsedMS            int64
	ActiveWallMS         int64
	SummedEffortMS       int64
	IdleMS               int64
	MaxUsefulConcurrency int
}

type sessionTiming struct {
	metrics              timingMetrics
	breakdown            TimingBreakdown
	lastActivity         time.Time
	stalledMS            int64
	activeAttempts       int
	queuedResources      int
	completedResources   int
	byResource           map[string][]workInterval
	attributedByResource map[string][]workInterval
	missingAckByResource map[string]int
	workerPool           WorkerPoolStatus
	batchByWave          map[int][]workInterval
}

func (s *Spec) loadSessionTiming(
	ctx context.Context,
	sess *store.Session,
	resources []store.SessionResource,
	now time.Time,
) (sessionTiming, error) {
	timing := sessionTiming{
		lastActivity:         sess.CreatedAt,
		byResource:           make(map[string][]workInterval),
		attributedByResource: make(map[string][]workInterval),
		missingAckByResource: make(map[string]int),
		batchByWave:          make(map[int][]workInterval),
	}
	timing.lastActivity = laterTime(timing.lastActivity, sess.UpdatedAt)
	resourceSet := make(map[string]bool, len(resources))
	for _, resource := range resources {
		resourceSet[resource.ResourceID] = true
	}

	attempts, err := s.store.ListGenerationAttemptsBySession(ctx, sess.ID)
	if err != nil {
		return sessionTiming{}, fmt.Errorf("list session attempts: %w", err)
	}
	resourceByAttempt := make(map[string]string, len(attempts))
	manifestByAttempt := make(map[string]*store.ExecutionManifest, len(attempts))
	activeResources := make(map[string]bool)
	var activeIntervals, attributedIntervals []workInterval
	contextBatches, err := s.store.ListContextBatchesBySession(ctx, sess.ID)
	if err != nil {
		return sessionTiming{}, fmt.Errorf("list context batches: %w", err)
	}
	for _, batch := range contextBatches {
		timing.lastActivity = laterTime(timing.lastActivity, batch.StartedAt)
		timing.lastActivity = laterTime(timing.lastActivity, batch.UpdatedAt)
		end := batch.CompletedAt
		if end == nil && batch.Status == "running" {
			if sess.Status == "active" {
				end = &now
			} else {
				end = &sess.UpdatedAt
			}
		}
		if end == nil || !end.After(batch.StartedAt) {
			continue
		}
		interval := workInterval{
			category: "shared_context_preparation", startedAt: batch.StartedAt, endedAt: *end,
		}
		activeIntervals = append(activeIntervals, interval)
		attributedIntervals = append(attributedIntervals, interval)
		timing.batchByWave[batch.WaveIndex] = append(timing.batchByWave[batch.WaveIndex], interval)
	}
	for _, attempt := range attempts {
		resourceByAttempt[attempt.ID] = attempt.ResourceID
		timing.lastActivity = laterTime(timing.lastActivity, attempt.CreatedAt)
		manifest, manifestErr := s.store.GetExecutionManifestByAttempt(ctx, attempt.ID)
		if manifestErr != nil {
			if errors.Is(manifestErr, cserrors.ErrNotFound) {
				continue
			}
			return sessionTiming{}, fmt.Errorf("get execution for attempt %s: %w", attempt.ID, manifestErr)
		}
		manifestByAttempt[attempt.ID] = manifest
		timing.lastActivity = laterTime(timing.lastActivity, manifest.StartedAt)
		timing.lastActivity = laterTime(timing.lastActivity, manifest.UpdatedAt)
		if manifest.CompletedAt != nil {
			timing.lastActivity = laterTime(timing.lastActivity, *manifest.CompletedAt)
		}
		if executionIsActive(*manifest) {
			timing.activeAttempts++
			activeResources[attempt.ResourceID] = true
		}
	}

	lifecycles, err := s.store.ListDispatchLifecyclesBySession(ctx, sess.ID)
	if err != nil {
		return sessionTiming{}, fmt.Errorf("list dispatch lifecycles: %w", err)
	}
	timing.workerPool = summarizeWorkerPool(lifecycles)
	observedAttempts := make(map[string]bool, len(lifecycles))
	for _, lifecycle := range lifecycles {
		timing.lastActivity = laterTime(timing.lastActivity, lifecycle.UpdatedAt)
		if lifecycle.DispatchRequestedAt != nil && lifecycle.WorkerAcknowledgedAt == nil {
			timing.missingAckByResource[lifecycle.ResourceID]++
		}
		manifest := manifestByAttempt[lifecycle.AttemptID]
		lifecycleIntervals := lifecycleWorkIntervals(lifecycle, manifest, now)
		attributedIntervals = append(attributedIntervals, lifecycleIntervals...)
		timing.attributedByResource[lifecycle.ResourceID] = append(
			timing.attributedByResource[lifecycle.ResourceID], lifecycleIntervals...,
		)
		for _, interval := range lifecycleIntervals {
			if intervalIsUseful(interval) {
				activeIntervals = append(activeIntervals, interval)
				timing.byResource[lifecycle.ResourceID] = append(timing.byResource[lifecycle.ResourceID], interval)
			}
		}
		if lifecycle.AttemptID != "" {
			observedAttempts[lifecycle.AttemptID] = true
		}
	}
	for _, attempt := range attempts {
		if observedAttempts[attempt.ID] {
			continue
		}
		manifest := manifestByAttempt[attempt.ID]
		if manifest == nil {
			continue
		}
		endedAt := executionObservationEnd(*manifest, now)
		if !manifest.StartedAt.IsZero() && endedAt.After(manifest.StartedAt) {
			interval := workInterval{
				resourceID: attempt.ResourceID, category: "legacy_execution",
				startedAt: manifest.StartedAt, endedAt: endedAt,
			}
			activeIntervals = append(activeIntervals, interval)
			attributedIntervals = append(attributedIntervals, interval)
			timing.byResource[attempt.ResourceID] = append(timing.byResource[attempt.ResourceID], interval)
			timing.attributedByResource[attempt.ResourceID] = append(timing.attributedByResource[attempt.ResourceID], interval)
		}
	}

	validationRuns, err := s.store.ListValidationRuns(ctx, 10000)
	if err != nil {
		return sessionTiming{}, fmt.Errorf("list validation runs: %w", err)
	}
	for _, run := range validationRuns {
		if run.SessionID != sess.ID {
			continue
		}
		timing.lastActivity = laterTime(timing.lastActivity, run.CreatedAt)
		timing.lastActivity = laterTime(timing.lastActivity, run.StartedAt)
		timing.lastActivity = laterTime(timing.lastActivity, run.CompletedAt)
		owners := validationResourceOwners(run, resourceByAttempt, resourceSet)
		validationIntervals := validationWorkIntervals(run)
		activeIntervals = append(activeIntervals, validationIntervals...)
		attributedIntervals = append(attributedIntervals, validationIntervals...)
		for _, resourceID := range owners {
			owned := append([]workInterval(nil), validationIntervals...)
			for index := range owned {
				owned[index].resourceID = resourceID
			}
			timing.byResource[resourceID] = append(timing.byResource[resourceID], owned...)
			timing.attributedByResource[resourceID] = append(timing.attributedByResource[resourceID], owned...)
		}
	}

	for _, resource := range resources {
		timing.lastActivity = laterTime(timing.lastActivity, resource.UpdatedAt)
		if ResourceState(resource.State).IsTerminal() {
			timing.completedResources++
		}
		state := ResourceState(resource.State)
		if resourceIsQueued(state, activeResources[resource.ResourceID]) {
			timing.queuedResources++
		}
	}

	sessionEnd := now
	if sess.Status != "active" {
		sessionEnd = laterTime(sess.UpdatedAt, timing.lastActivity)
	}
	timing.metrics = summarizeTiming(activeIntervals, sess.CreatedAt, sessionEnd)
	timing.breakdown = summarizeBreakdown(attributedIntervals, sess.CreatedAt, sessionEnd)
	for _, lifecycle := range lifecycles {
		if lifecycle.DispatchRequestedAt != nil && lifecycle.WorkerAcknowledgedAt == nil {
			timing.breakdown.MissingAcknowledgements++
		}
	}
	if sess.Status == "active" && !timing.lastActivity.IsZero() && now.After(timing.lastActivity) {
		timing.stalledMS = now.Sub(timing.lastActivity).Milliseconds()
	}
	return timing, nil
}

func summarizeWorkerPool(lifecycles []store.DispatchLifecycle) WorkerPoolStatus {
	assignmentsByWorker := make(map[string]int)
	result := WorkerPoolStatus{}
	for _, lifecycle := range lifecycles {
		if lifecycle.DispatchRequestedAt == nil || lifecycle.HostSessionID == "" || lifecycle.WorkerID == "" {
			continue
		}
		key := lifecycle.HostSessionID + "\x00" + lifecycle.WorkerID
		assignmentsByWorker[key]++
		result.Assignments++
	}
	result.UniqueWorkers = len(assignmentsByWorker)
	for _, assignments := range assignmentsByWorker {
		if assignments > 1 {
			result.ReusedWorkers++
		}
		if assignments > result.MaxAssignmentsPerWorker {
			result.MaxAssignmentsPerWorker = assignments
		}
	}
	return result
}

func resourceIsQueued(state ResourceState, active bool) bool {
	return !active && (contextStateDispatchable(string(state)) || state == StateDispatched)
}

func executionIsActive(manifest store.ExecutionManifest) bool {
	if manifest.CompletedAt != nil {
		return false
	}
	switch manifest.Status {
	case "executing", "candidate_submitted", "validating":
		return true
	default:
		return false
	}
}

func executionObservationEnd(manifest store.ExecutionManifest, now time.Time) time.Time {
	if manifest.CompletedAt != nil {
		return *manifest.CompletedAt
	}
	if executionIsActive(manifest) {
		return now
	}
	return manifest.UpdatedAt
}

func validationResourceOwners(
	run store.ValidationRun,
	resourceByAttempt map[string]string,
	sessionResources map[string]bool,
) []string {
	owners := make(map[string]bool)
	if resourceID := resourceByAttempt[run.AttemptID]; sessionResources[resourceID] {
		owners[resourceID] = true
	}
	for _, target := range run.Targets {
		if target.Kind == "resource" && sessionResources[target.ID] {
			owners[target.ID] = true
		}
	}
	result := make([]string, 0, len(owners))
	for resourceID := range owners {
		result = append(result, resourceID)
	}
	sort.Strings(result)
	return result
}

func validationWorkIntervals(run store.ValidationRun) []workInterval {
	if len(run.Executions) == 0 {
		if run.StartedAt.IsZero() || !run.CompletedAt.After(run.StartedAt) {
			return nil
		}
		return []workInterval{{
			category: "validation", identity: "validation-run:" + run.ID,
			startedAt: run.StartedAt, endedAt: run.CompletedAt,
		}}
	}
	intervals := make([]workInterval, 0, len(run.Executions))
	for ordinal, execution := range run.Executions {
		if execution.StartedAt.IsZero() || !execution.CompletedAt.After(execution.StartedAt) {
			continue
		}
		intervals = append(intervals, workInterval{
			category: "validation",
			identity: fmt.Sprintf(
				"validation:%d:%s:%s:%s:%s:%s:%s:%s",
				ordinal, execution.Role, execution.CommandHash, execution.WorkingDirectory,
				execution.EnvironmentHash, execution.SourceTreeHash,
				execution.StartedAt.UTC().Format(time.RFC3339Nano),
				execution.CompletedAt.UTC().Format(time.RFC3339Nano),
			),
			startedAt: execution.StartedAt,
			endedAt:   execution.CompletedAt,
		})
	}
	return intervals
}

func lifecycleWorkIntervals(
	lifecycle store.DispatchLifecycle,
	manifest *store.ExecutionManifest,
	now time.Time,
) []workInterval {
	var intervals []workInterval
	add := func(category string, start, end *time.Time) {
		if start == nil || end == nil || !end.After(*start) {
			return
		}
		intervals = append(intervals, workInterval{
			resourceID: lifecycle.ResourceID, category: category,
			startedAt: *start, endedAt: *end,
		})
	}
	add("context_preparation", lifecycle.ContextRequestedAt, lifecycle.ContextPreparedAt)
	add("prepared_to_dispatch", lifecycle.ContextPreparedAt, lifecycle.DispatchRequestedAt)
	add("dispatch_to_acknowledged", lifecycle.DispatchRequestedAt, lifecycle.WorkerAcknowledgedAt)
	add("acknowledged_to_execution", lifecycle.WorkerAcknowledgedAt, lifecycle.ExecutionRegisteredAt)

	executionEnd := lifecycle.CandidateSubmittedAt
	if executionEnd == nil && lifecycle.ExecutionRegisteredAt != nil && manifest != nil {
		if manifest.CompletedAt != nil {
			executionEnd = manifest.CompletedAt
		} else if executionIsActive(*manifest) {
			executionEnd = &now
		}
	}
	add("model_active", lifecycle.ExecutionRegisteredAt, executionEnd)

	dispositionAt := earliestObservedTime(lifecycle.CandidateAcceptedAt, lifecycle.CandidateRejectedAt)
	if dispositionAt == nil && lifecycle.CandidateSubmittedAt != nil && manifest != nil {
		if manifest.CompletedAt != nil {
			dispositionAt = manifest.CompletedAt
		} else if executionIsActive(*manifest) {
			dispositionAt = &now
		}
	}
	add("submission_commit", lifecycle.CandidateSubmittedAt, dispositionAt)
	return intervals
}

func earliestObservedTime(left, right *time.Time) *time.Time {
	if left == nil {
		return right
	}
	if right == nil || left.Before(*right) {
		return left
	}
	return right
}

func intervalIsUseful(interval workInterval) bool {
	switch interval.category {
	case "shared_context_preparation", "context_preparation", "model_active", "submission_commit", "validation", "legacy_execution":
		return true
	default:
		return false
	}
}

func summarizeBreakdown(intervals []workInterval, windowStart, windowEnd time.Time) TimingBreakdown {
	result := TimingBreakdown{}
	byCategory := make(map[string][]workInterval)
	for _, interval := range intervals {
		byCategory[interval.category] = append(byCategory[interval.category], interval)
	}
	categoryMS := func(category string) int64 {
		return summarizeTiming(byCategory[category], windowStart, windowEnd).ActiveWallMS
	}
	result.DispatchToAcknowledgedMS = categoryMS("dispatch_to_acknowledged")
	result.SharedContextPreparationMS = categoryMS("shared_context_preparation")
	result.ContextPreparationMS = categoryMS("context_preparation")
	result.PreparedToDispatchMS = categoryMS("prepared_to_dispatch")
	result.AcknowledgedToExecutionMS = categoryMS("acknowledged_to_execution")
	result.ModelActiveMS = categoryMS("model_active")
	result.SubmissionCommitMS = categoryMS("submission_commit")
	result.ValidationMS = categoryMS("validation")
	result.LegacyExecutionMS = categoryMS("legacy_execution")
	observed := summarizeTiming(intervals, windowStart, windowEnd)
	result.ObservedWallMS = observed.ActiveWallMS
	result.UnattributedWallMS = observed.IdleMS
	return result
}

func summarizeTiming(intervals []workInterval, windowStart, windowEnd time.Time) timingMetrics {
	if windowStart.IsZero() || windowEnd.Before(windowStart) {
		return timingMetrics{}
	}
	metrics := timingMetrics{ElapsedMS: windowEnd.Sub(windowStart).Milliseconds()}
	type timingEvent struct {
		at    time.Time
		delta int
	}
	events := make([]timingEvent, 0, len(intervals)*2)
	clipped := make([]workInterval, 0, len(intervals))
	seenIdentities := make(map[string]bool)
	for _, interval := range intervals {
		if interval.identity != "" {
			if seenIdentities[interval.identity] {
				continue
			}
			seenIdentities[interval.identity] = true
		}
		start := laterTime(interval.startedAt, windowStart)
		end := earlierTime(interval.endedAt, windowEnd)
		if start.IsZero() || !end.After(start) {
			continue
		}
		interval.startedAt = start
		interval.endedAt = end
		clipped = append(clipped, interval)
		metrics.SummedEffortMS += end.Sub(start).Milliseconds()
		events = append(events, timingEvent{at: start, delta: 1}, timingEvent{at: end, delta: -1})
	}
	sort.Slice(clipped, func(i, j int) bool {
		if clipped[i].startedAt.Equal(clipped[j].startedAt) {
			return clipped[i].endedAt.Before(clipped[j].endedAt)
		}
		return clipped[i].startedAt.Before(clipped[j].startedAt)
	})
	var unionStart, unionEnd time.Time
	for _, interval := range clipped {
		if unionStart.IsZero() {
			unionStart, unionEnd = interval.startedAt, interval.endedAt
			continue
		}
		if !interval.startedAt.After(unionEnd) {
			if interval.endedAt.After(unionEnd) {
				unionEnd = interval.endedAt
			}
			continue
		}
		metrics.ActiveWallMS += unionEnd.Sub(unionStart).Milliseconds()
		unionStart, unionEnd = interval.startedAt, interval.endedAt
	}
	if !unionStart.IsZero() {
		metrics.ActiveWallMS += unionEnd.Sub(unionStart).Milliseconds()
	}
	metrics.IdleMS = metrics.ElapsedMS - metrics.ActiveWallMS
	if metrics.IdleMS < 0 {
		metrics.IdleMS = 0
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].at.Equal(events[j].at) {
			return events[i].delta < events[j].delta
		}
		return events[i].at.Before(events[j].at)
	})
	active := 0
	for _, event := range events {
		active += event.delta
		if active > metrics.MaxUsefulConcurrency {
			metrics.MaxUsefulConcurrency = active
		}
	}
	return metrics
}

func populateResourceTiming(
	detail *ResourceDetail,
	resource store.SessionResource,
	intervals []workInterval,
	now time.Time,
) {
	start, end := intervalBounds(intervals)
	if start.IsZero() {
		if resource.DispatchedAt.IsZero() {
			return
		}
		start = resource.DispatchedAt
		end = resource.UpdatedAt
		if !ResourceState(resource.State).IsTerminal() {
			end = now
		}
	}
	detail.StartedAt = formatObservationTime(start)
	metrics := summarizeTiming(intervals, start, end)
	detail.ActiveWallMS = metrics.ActiveWallMS
	detail.SummedEffortMS = metrics.SummedEffortMS
	detail.MaxUsefulConcurrency = metrics.MaxUsefulConcurrency
	spanMS := end.Sub(start).Milliseconds()
	if spanMS < 0 {
		spanMS = 0
	}
	if ResourceState(resource.State).IsTerminal() {
		detail.DurationMS = spanMS
		detail.CompletedAt = formatObservationTime(end)
	} else {
		if end.Before(now) {
			end = now
		}
		detail.ElapsedMS = end.Sub(start).Milliseconds()
	}
}

func populateWaveTiming(summary *WaveSummary, intervals, attributed []workInterval, now time.Time) {
	start, end := intervalBounds(attributed)
	if start.IsZero() {
		start, end = intervalBounds(intervals)
	}
	if start.IsZero() {
		return
	}
	metrics := summarizeTiming(intervals, start, end)
	summary.ActiveWallMS = metrics.ActiveWallMS
	summary.SummedEffortMS = metrics.SummedEffortMS
	summary.MaxUsefulConcurrency = metrics.MaxUsefulConcurrency
	spanMS := end.Sub(start).Milliseconds()
	if summary.Committed == summary.Total {
		summary.DurationMS = spanMS
		summary.Timing = summarizeBreakdown(attributed, start, end)
		return
	}
	if end.Before(now) {
		end = now
	}
	summary.ElapsedMS = end.Sub(start).Milliseconds()
	summary.Timing = summarizeBreakdown(attributed, start, end)
}

func resourceTimingWindow(resource store.SessionResource, intervals []workInterval, now time.Time) (time.Time, time.Time) {
	start, end := intervalBounds(intervals)
	if !resource.DispatchedAt.IsZero() && (start.IsZero() || resource.DispatchedAt.Before(start)) {
		start = resource.DispatchedAt
	}
	if start.IsZero() {
		return time.Time{}, time.Time{}
	}
	if resource.UpdatedAt.After(end) {
		end = resource.UpdatedAt
	}
	if !ResourceState(resource.State).IsTerminal() && now.After(end) {
		end = now
	}
	return start, end
}

func intervalBounds(intervals []workInterval) (time.Time, time.Time) {
	var start, end time.Time
	for _, interval := range intervals {
		if start.IsZero() || interval.startedAt.Before(start) {
			start = interval.startedAt
		}
		if end.IsZero() || interval.endedAt.After(end) {
			end = interval.endedAt
		}
	}
	return start, end
}

func laterTime(left, right time.Time) time.Time {
	if right.After(left) {
		return right
	}
	return left
}

func earlierTime(left, right time.Time) time.Time {
	if left.IsZero() || right.Before(left) {
		return right
	}
	return left
}

func formatObservationTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
