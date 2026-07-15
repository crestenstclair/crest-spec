package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	"github.com/crestenstclair/crest-spec/internal/db"
)

type EvaluationMetricDefinition struct {
	Name               string  `json:"name"`
	Direction          string  `json:"direction"`
	Primary            bool    `json:"primary"`
	PracticalThreshold float64 `json:"practical_threshold,omitempty"`
}

type EvaluationMetricPolicy struct {
	Metrics []EvaluationMetricDefinition `json:"metrics"`
}

type EvaluationRunVariantInput struct {
	Name            string `json:"name"`
	ConfigurationID string `json:"configuration_id"`
	Baseline        bool   `json:"baseline"`
}

type EvaluationRunVariant struct {
	Name                      string `json:"name"`
	ConfigurationID           string `json:"configuration_id"`
	ConfigurationName         string `json:"configuration_name"`
	ConfigurationIdentityHash string `json:"configuration_identity_hash"`
	Baseline                  bool   `json:"baseline"`
	Ordinal                   int    `json:"ordinal"`
}

type EvaluationRun struct {
	ID                    string                              `json:"id"`
	DatasetID             string                              `json:"dataset_id"`
	Name                  string                              `json:"name"`
	Status                string                              `json:"status"`
	MetricPolicy          EvaluationMetricPolicy              `json:"metric_policy"`
	MetricPolicyHash      string                              `json:"metric_policy_hash"`
	MinimumSampleSize     int                                 `json:"minimum_sample_size"`
	PracticalSignificance float64                             `json:"practical_significance"`
	RequireHeldOut        bool                                `json:"require_held_out"`
	Conclusion            string                              `json:"conclusion"`
	WinningVariant        string                              `json:"winning_variant,omitempty"`
	ConclusionReason      string                              `json:"conclusion_reason,omitempty"`
	CreatedAt             time.Time                           `json:"created_at"`
	StartedAt             *time.Time                          `json:"started_at,omitempty"`
	CompletedAt           *time.Time                          `json:"completed_at,omitempty"`
	Variants              []EvaluationRunVariant              `json:"variants"`
	Assignments           []EvaluationAssignment              `json:"assignments"`
	Observations          []EvaluationMetricObservationRecord `json:"observations,omitempty"`
	Aggregates            []EvaluationMetricAggregate         `json:"aggregates,omitempty"`
	Comparisons           []EvaluationComparison              `json:"comparisons,omitempty"`
}

type EvaluationAssignment struct {
	ID              string     `json:"id"`
	RunID           string     `json:"run_id"`
	CaseID          string     `json:"case_id"`
	VariantName     string     `json:"variant_name"`
	ConfigurationID string     `json:"configuration_id"`
	Split           string     `json:"split"`
	Status          string     `json:"status"`
	CurrentLeaseID  string     `json:"current_lease_id,omitempty"`
	LeaseOwner      string     `json:"lease_owner,omitempty"`
	LeaseExpiresAt  *time.Time `json:"lease_expires_at,omitempty"`
	AttemptID       string     `json:"attempt_id,omitempty"`
	TerminalStatus  string     `json:"terminal_status,omitempty"`
	TerminalReason  string     `json:"terminal_reason,omitempty"`
	SubmittedAt     *time.Time `json:"submitted_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type EvaluationAssignmentClaim struct {
	Assignment    EvaluationAssignment    `json:"assignment"`
	LeaseID       string                  `json:"lease_id"`
	LeaseToken    string                  `json:"lease_token"`
	LeaseExpires  time.Time               `json:"lease_expires_at"`
	Case          EvaluationCase          `json:"case"`
	Configuration EvaluationConfiguration `json:"configuration"`
}

type EvaluationMetricObservation struct {
	Name          string         `json:"name"`
	Value         *float64       `json:"value,omitempty"`
	MissingReason string         `json:"missing_reason,omitempty"`
	Unit          string         `json:"unit,omitempty"`
	SourceType    string         `json:"source_type"`
	SourceID      string         `json:"source_id,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type EvaluationMetricObservationRecord struct {
	AssignmentID  string         `json:"assignment_id"`
	CaseID        string         `json:"case_id"`
	VariantName   string         `json:"variant_name"`
	Split         string         `json:"split"`
	Name          string         `json:"name"`
	Value         *float64       `json:"value,omitempty"`
	MissingReason string         `json:"missing_reason,omitempty"`
	Unit          string         `json:"unit,omitempty"`
	SourceType    string         `json:"source_type"`
	SourceID      string         `json:"source_id,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

type EvaluationAssignmentResult struct {
	AttemptID      string                        `json:"attempt_id,omitempty"`
	TerminalStatus string                        `json:"terminal_status"`
	Reason         string                        `json:"reason,omitempty"`
	Metrics        []EvaluationMetricObservation `json:"metrics,omitempty"`
}

type EvaluationMetricAggregate struct {
	VariantName  string   `json:"variant_name"`
	Split        string   `json:"split"`
	MetricName   string   `json:"metric_name"`
	SampleCount  int      `json:"sample_count"`
	MissingCount int      `json:"missing_count"`
	Mean         *float64 `json:"mean,omitempty"`
	Minimum      *float64 `json:"minimum,omitempty"`
	Maximum      *float64 `json:"maximum,omitempty"`
}

type EvaluationComparison struct {
	BaselineVariant      string   `json:"baseline_variant"`
	CandidateVariant     string   `json:"candidate_variant"`
	Split                string   `json:"split"`
	MetricName           string   `json:"metric_name"`
	BaselineSampleCount  int      `json:"baseline_sample_count"`
	CandidateSampleCount int      `json:"candidate_sample_count"`
	MissingCount         int      `json:"missing_count"`
	BaselineValue        *float64 `json:"baseline_value,omitempty"`
	CandidateValue       *float64 `json:"candidate_value,omitempty"`
	AbsoluteChange       *float64 `json:"absolute_change,omitempty"`
	RelativeChange       *float64 `json:"relative_change,omitempty"`
	PracticalThreshold   float64  `json:"practical_threshold"`
	Conclusion           string   `json:"conclusion"`
	Regression           bool     `json:"regression"`
	Reason               string   `json:"reason"`
}

func DefaultEvaluationMetricPolicy() EvaluationMetricPolicy {
	return EvaluationMetricPolicy{Metrics: []EvaluationMetricDefinition{
		{Name: "accepted_implementation", Direction: "higher", Primary: true},
		{Name: "goal_completion", Direction: "higher", Primary: true},
		{Name: "integration_success", Direction: "higher", Primary: true},
		{Name: "behavioral_success", Direction: "higher", Primary: true},
		{Name: "regression", Direction: "lower", Primary: true},
		{Name: "human_intervention", Direction: "lower", Primary: true},
		{Name: "retry_count", Direction: "lower"},
		{Name: "diff_bytes", Direction: "lower"},
		{Name: "file_churn", Direction: "lower"},
		{Name: "input_tokens", Direction: "lower"},
		{Name: "output_tokens", Direction: "lower"},
		{Name: "cost_usd", Direction: "lower"},
		{Name: "duration_ms", Direction: "lower"},
		{Name: "security_success", Direction: "higher"},
		{Name: "static_analysis_success", Direction: "higher"},
	}}
}

func (s *Store) CreateEvaluationRun(ctx context.Context, datasetID, name string, variants []EvaluationRunVariantInput, policy EvaluationMetricPolicy, minimumSampleSize int, practicalSignificance float64, requireHeldOut bool) (_ *EvaluationRun, err error) {
	dataset, err := s.GetEvaluationDataset(ctx, datasetID)
	if err != nil {
		return nil, err
	}
	if dataset.Status != "sealed" {
		return nil, fmt.Errorf("create evaluation run: dataset must be sealed")
	}
	if requireHeldOut {
		hasHeldOut := false
		for _, member := range dataset.Cases {
			if member.Split == "held_out" {
				hasHeldOut = true
				break
			}
		}
		if !hasHeldOut {
			return nil, fmt.Errorf("create evaluation run: held-out evidence is required but the dataset has no held-out cases")
		}
	}
	if strings.TrimSpace(name) == "" || len(variants) < 2 {
		return nil, fmt.Errorf("create evaluation run: name and at least two variants are required")
	}
	if minimumSampleSize <= 0 {
		minimumSampleSize = 5
	}
	if practicalSignificance < 0 {
		return nil, fmt.Errorf("create evaluation run: practical significance cannot be negative")
	}
	policy, policyJSON, policyHash, err := normalizeMetricPolicy(policy, practicalSignificance)
	if err != nil {
		return nil, err
	}
	seenNames, seenConfigurations, baselines := map[string]bool{}, map[string]bool{}, 0
	for _, variant := range variants {
		if variant.Name == "" || variant.ConfigurationID == "" || seenNames[variant.Name] || seenConfigurations[variant.ConfigurationID] {
			return nil, fmt.Errorf("create evaluation run: variants require unique names and configurations")
		}
		seenNames[variant.Name], seenConfigurations[variant.ConfigurationID] = true, true
		if variant.Baseline {
			baselines++
		}
		if _, getErr := s.GetEvaluationConfiguration(ctx, variant.ConfigurationID); getErr != nil {
			return nil, fmt.Errorf("create evaluation run: configuration %s: %w", variant.ConfigurationID, getErr)
		}
	}
	if baselines != 1 {
		return nil, fmt.Errorf("create evaluation run: exactly one baseline is required")
	}
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	q := s.queries.WithTx(tx)
	runID, timestamp := uuid.NewString(), now()
	if err = q.PutEvaluationRun(ctx, db.PutEvaluationRunParams{
		ID: runID, DatasetID: datasetID, Name: name, Status: "running",
		MetricPolicyJson: policyJSON, MetricPolicyHash: policyHash,
		MinimumSampleSize: int64(minimumSampleSize), PracticalSignificance: practicalSignificance,
		RequireHeldOut: boolInt(requireHeldOut), Conclusion: "pending", CreatedAt: timestamp, StartedAt: &timestamp,
	}); err != nil {
		return nil, err
	}
	for ordinal, variant := range variants {
		if err = q.PutEvaluationRunVariant(ctx, db.PutEvaluationRunVariantParams{
			RunID: runID, VariantName: variant.Name, ConfigurationID: variant.ConfigurationID,
			IsBaseline: boolInt(variant.Baseline), Ordinal: int64(ordinal),
		}); err != nil {
			return nil, err
		}
		for _, member := range dataset.Cases {
			if err = q.PutEvaluationAssignment(ctx, db.PutEvaluationAssignmentParams{
				ID: uuid.NewString(), RunID: runID, CaseID: member.CaseID, VariantName: variant.Name,
				ConfigurationID: variant.ConfigurationID, Split: member.Split, Status: "pending",
				CreatedAt: timestamp, UpdatedAt: timestamp,
			}); err != nil {
				return nil, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetEvaluationRun(ctx, runID)
}

func normalizeMetricPolicy(policy EvaluationMetricPolicy, defaultThreshold float64) (EvaluationMetricPolicy, string, string, error) {
	if len(policy.Metrics) == 0 {
		policy = DefaultEvaluationMetricPolicy()
	}
	seen := make(map[string]bool)
	for index := range policy.Metrics {
		metric := &policy.Metrics[index]
		if metric.Name == "" || seen[metric.Name] || (metric.Direction != "higher" && metric.Direction != "lower") || metric.PracticalThreshold < 0 {
			return EvaluationMetricPolicy{}, "", "", fmt.Errorf("invalid evaluation metric policy")
		}
		seen[metric.Name] = true
		if metric.PracticalThreshold == 0 {
			metric.PracticalThreshold = defaultThreshold
		}
	}
	sort.Slice(policy.Metrics, func(i, j int) bool { return policy.Metrics[i].Name < policy.Metrics[j].Name })
	encoded, hash, err := executionCanonical(policy)
	return policy, encoded, hash, err
}

func executionCanonical(value any) (string, string, error) {
	// Metric policies contain no credentials, but using the same canonical
	// redaction/hashing contract keeps evaluation identities reproducible.
	return canonicalEvaluation(value)
}

func canonicalEvaluation(value any) (string, string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", "", err
	}
	return string(encoded), contextmanifest.Hash(string(encoded)), nil
}

func (s *Store) GetEvaluationRun(ctx context.Context, id string) (*EvaluationRun, error) {
	row, err := s.queries.GetEvaluationRun(ctx, id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	result := &EvaluationRun{
		ID: row.ID, DatasetID: row.DatasetID, Name: row.Name, Status: row.Status,
		MetricPolicyHash: row.MetricPolicyHash, MinimumSampleSize: int(row.MinimumSampleSize),
		PracticalSignificance: row.PracticalSignificance, RequireHeldOut: row.RequireHeldOut == 1,
		Conclusion: row.Conclusion, WinningVariant: row.WinningVariant, ConclusionReason: row.ConclusionReason,
		CreatedAt: parseTime(row.CreatedAt), StartedAt: parseOptionalTime(row.StartedAt), CompletedAt: parseOptionalTime(row.CompletedAt),
	}
	if err := json.Unmarshal([]byte(row.MetricPolicyJson), &result.MetricPolicy); err != nil {
		return nil, err
	}
	variantRows, err := s.queries.ListEvaluationRunVariants(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, variant := range variantRows {
		result.Variants = append(result.Variants, EvaluationRunVariant{
			Name: variant.VariantName, ConfigurationID: variant.ConfigurationID,
			ConfigurationName: variant.ConfigurationName, ConfigurationIdentityHash: variant.ConfigurationIdentityHash,
			Baseline: variant.IsBaseline == 1, Ordinal: int(variant.Ordinal),
		})
	}
	assignmentRows, err := s.queries.ListEvaluationAssignments(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, assignment := range assignmentRows {
		result.Assignments = append(result.Assignments, dbEvaluationAssignment(assignment))
	}
	observationRows, err := s.queries.ListEvaluationMetricObservationsByRun(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, observation := range observationRows {
		record := EvaluationMetricObservationRecord{
			AssignmentID: observation.AssignmentID, CaseID: observation.CaseID,
			VariantName: observation.VariantName, Split: observation.Split,
			Name: observation.MetricName, Value: observation.Value,
			MissingReason: observation.MissingReason, Unit: observation.Unit,
			SourceType: observation.SourceType, SourceID: observation.SourceID,
			CreatedAt: parseTime(observation.CreatedAt),
		}
		if err := json.Unmarshal([]byte(observation.MetadataJson), &record.Metadata); err != nil {
			return nil, err
		}
		result.Observations = append(result.Observations, record)
	}
	aggregates, err := s.queries.ListEvaluationMetricAggregates(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, aggregate := range aggregates {
		result.Aggregates = append(result.Aggregates, EvaluationMetricAggregate{
			VariantName: aggregate.VariantName, Split: aggregate.Split, MetricName: aggregate.MetricName,
			SampleCount: int(aggregate.SampleCount), MissingCount: int(aggregate.MissingCount),
			Mean: aggregate.MeanValue, Minimum: aggregate.MinimumValue, Maximum: aggregate.MaximumValue,
		})
	}
	comparisons, err := s.queries.ListEvaluationComparisons(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, comparison := range comparisons {
		result.Comparisons = append(result.Comparisons, EvaluationComparison{
			BaselineVariant: comparison.BaselineVariant, CandidateVariant: comparison.CandidateVariant,
			Split: comparison.Split, MetricName: comparison.MetricName,
			BaselineSampleCount: int(comparison.BaselineSampleCount), CandidateSampleCount: int(comparison.CandidateSampleCount),
			MissingCount: int(comparison.MissingCount), BaselineValue: comparison.BaselineValue,
			CandidateValue: comparison.CandidateValue, AbsoluteChange: comparison.AbsoluteChange,
			RelativeChange: comparison.RelativeChange, PracticalThreshold: comparison.PracticalThreshold,
			Conclusion: comparison.Conclusion, Regression: comparison.Regression == 1, Reason: comparison.Reason,
		})
	}
	return result, nil
}

func (s *Store) ListEvaluationRuns(ctx context.Context, limit int) ([]EvaluationRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.queries.ListEvaluationRuns(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	result := make([]EvaluationRun, 0, len(rows))
	for _, row := range rows {
		run, getErr := s.GetEvaluationRun(ctx, row.ID)
		if getErr != nil {
			return nil, getErr
		}
		result = append(result, *run)
	}
	return result, nil
}

func (s *Store) ClaimEvaluationAssignment(ctx context.Context, runID, owner, split string, leaseDuration time.Duration) (_ *EvaluationAssignmentClaim, err error) {
	if runID == "" || owner == "" {
		return nil, fmt.Errorf("claim evaluation assignment: run and owner are required")
	}
	if split != "" && split != "training" && split != "development" && split != "held_out" {
		return nil, fmt.Errorf("claim evaluation assignment: invalid split %q", split)
	}
	if leaseDuration <= 0 {
		leaseDuration = 15 * time.Minute
	}
	if leaseDuration > 24*time.Hour {
		return nil, fmt.Errorf("claim evaluation assignment: lease exceeds 24 hours")
	}
	run, err := s.queries.GetEvaluationRun(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("claim evaluation assignment: load run: %w", mapNotFound(err))
	}
	if run.Status != "running" {
		return nil, fmt.Errorf("claim evaluation assignment: run is not active")
	}
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	q := s.queries.WithTx(tx)
	timestamp := now()
	if _, err = q.ExpireEvaluationAssignmentLeases(ctx, db.ExpireEvaluationAssignmentLeasesParams{
		CompletedAt: &timestamp, ExpiresAt: timestamp, RunID: runID,
	}); err != nil {
		return nil, fmt.Errorf("claim evaluation assignment: expire leases: %w", err)
	}
	if _, err = q.RequeueExpiredEvaluationAssignments(ctx, db.RequeueExpiredEvaluationAssignmentsParams{
		UpdatedAt: timestamp, RunID: runID, LeaseExpiresAt: &timestamp,
	}); err != nil {
		return nil, fmt.Errorf("claim evaluation assignment: requeue expired assignments: %w", err)
	}
	assignment, err := q.GetNextEvaluationAssignment(ctx, db.GetNextEvaluationAssignmentParams{
		RunID: runID, SplitFilter: split,
	})
	if err != nil {
		return nil, mapNotFound(err)
	}
	leaseID, leaseToken := uuid.NewString(), uuid.NewString()
	expiresAt := parseTime(timestamp).Add(leaseDuration).UTC()
	expires := formatTime(expiresAt)
	leaseNumber, err := q.CountEvaluationAssignmentLeases(ctx, assignment.ID)
	if err != nil {
		return nil, err
	}
	if err = q.PutEvaluationAssignmentLease(ctx, db.PutEvaluationAssignmentLeaseParams{
		ID: leaseID, AssignmentID: assignment.ID, LeaseOwner: owner,
		LeaseTokenHash: contextmanifest.Hash(leaseToken), LeaseNumber: leaseNumber + 1,
		Status: "active", ClaimedAt: timestamp, ExpiresAt: expires,
	}); err != nil {
		return nil, err
	}
	affected, err := q.ClaimEvaluationAssignment(ctx, db.ClaimEvaluationAssignmentParams{
		CurrentLeaseID: &leaseID, LeaseOwner: owner, LeaseExpiresAt: &expires,
		UpdatedAt: timestamp, ID: assignment.ID,
	})
	if err != nil || affected != 1 {
		return nil, fmt.Errorf("claim evaluation assignment: concurrent claim")
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	claimed, err := s.queries.GetEvaluationAssignment(ctx, assignment.ID)
	if err != nil {
		return nil, err
	}
	evaluationCase, err := s.GetEvaluationCase(ctx, assignment.CaseID)
	if err != nil {
		return nil, err
	}
	if assignment.Split == "held_out" {
		evaluationCase.ExpectedOutcome = nil
		evaluationCase.ExpectedOutcomeHash = ""
	}
	configuration, err := s.GetEvaluationConfiguration(ctx, assignment.ConfigurationID)
	if err != nil {
		return nil, err
	}
	return &EvaluationAssignmentClaim{
		Assignment: dbEvaluationAssignment(claimed), LeaseID: leaseID, LeaseToken: leaseToken,
		LeaseExpires: expiresAt, Case: *evaluationCase, Configuration: *configuration,
	}, nil
}

func (s *Store) HeartbeatEvaluationAssignment(ctx context.Context, assignmentID, leaseID, owner, token string, extension time.Duration) (time.Time, error) {
	if extension <= 0 {
		extension = 15 * time.Minute
	}
	timestamp := now()
	expiresAt := parseTime(timestamp).Add(extension).UTC()
	expires := formatTime(expiresAt)
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)
	affected, err := q.HeartbeatEvaluationAssignmentLease(ctx, db.HeartbeatEvaluationAssignmentLeaseParams{
		ExpiresAt: expires, ID: leaseID, AssignmentID: assignmentID, LeaseOwner: owner,
		LeaseTokenHash: contextmanifest.Hash(token), ExpiresAt_2: timestamp,
	})
	if err != nil || affected != 1 {
		return time.Time{}, fmt.Errorf("heartbeat evaluation assignment: lease is invalid or expired")
	}
	affected, err = q.UpdateEvaluationAssignmentLeaseExpiry(ctx, db.UpdateEvaluationAssignmentLeaseExpiryParams{
		LeaseExpiresAt: &expires, UpdatedAt: timestamp, ID: assignmentID, CurrentLeaseID: &leaseID,
	})
	if err != nil || affected != 1 {
		return time.Time{}, fmt.Errorf("heartbeat evaluation assignment: assignment is no longer leased")
	}
	if err := tx.Commit(); err != nil {
		return time.Time{}, err
	}
	return expiresAt, nil
}

func (s *Store) ReleaseEvaluationAssignment(ctx context.Context, assignmentID, leaseID, owner, token string) error {
	timestamp := now()
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)
	affected, err := q.CompleteEvaluationAssignmentLease(ctx, db.CompleteEvaluationAssignmentLeaseParams{
		Status: "released", CompletedAt: &timestamp, ID: leaseID, AssignmentID: assignmentID,
		LeaseOwner: owner, LeaseTokenHash: contextmanifest.Hash(token),
	})
	if err != nil || affected != 1 {
		return fmt.Errorf("release evaluation assignment: lease is invalid")
	}
	affected, err = q.ReleaseEvaluationAssignment(ctx, db.ReleaseEvaluationAssignmentParams{
		UpdatedAt: timestamp, ID: assignmentID, CurrentLeaseID: &leaseID,
	})
	if err != nil || affected != 1 {
		return fmt.Errorf("release evaluation assignment: assignment is no longer leased")
	}
	return tx.Commit()
}

func (s *Store) SubmitEvaluationAssignment(ctx context.Context, assignmentID, leaseID, owner, token string, result EvaluationAssignmentResult) (*EvaluationAssignment, error) {
	assignmentRow, err := s.queries.GetEvaluationAssignment(ctx, assignmentID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if assignmentRow.Status == "submitted" {
		terminalMatches := result.TerminalStatus == "" || assignmentRow.TerminalStatus == result.TerminalStatus
		if stringVal(assignmentRow.AttemptID) == result.AttemptID && terminalMatches {
			assignment := dbEvaluationAssignment(assignmentRow)
			return &assignment, nil
		}
		return nil, fmt.Errorf("submit evaluation assignment: authoritative result already exists")
	}
	if result.AttemptID == "" {
		return nil, fmt.Errorf("submit evaluation assignment: an ordinary generation attempt is required")
	}
	run, err := s.GetEvaluationRun(ctx, assignmentRow.RunID)
	if err != nil {
		return nil, err
	}
	observations := append([]EvaluationMetricObservation(nil), result.Metrics...)
	if result.AttemptID != "" {
		derived, terminalStatus, terminalReason, deriveErr := s.deriveEvaluationMetrics(ctx, assignmentRow, result.AttemptID)
		if deriveErr != nil {
			return nil, deriveErr
		}
		if result.TerminalStatus != "" && result.TerminalStatus != terminalStatus {
			return nil, fmt.Errorf("submit evaluation assignment: terminal status does not match execution manifest")
		}
		result.TerminalStatus = terminalStatus
		if result.Reason == "" {
			result.Reason = terminalReason
		}
		observations = mergeEvaluationMetrics(observations, derived)
	}
	if !evaluationTerminalExecution(result.TerminalStatus) {
		return nil, fmt.Errorf("submit evaluation assignment: terminal status is required")
	}
	observations, err = completeEvaluationMetrics(run.MetricPolicy, observations)
	if err != nil {
		return nil, err
	}
	timestamp := now()
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)
	affected, err := q.CompleteEvaluationAssignmentLease(ctx, db.CompleteEvaluationAssignmentLeaseParams{
		Status: "submitted", CompletedAt: &timestamp, ID: leaseID, AssignmentID: assignmentID,
		LeaseOwner: owner, LeaseTokenHash: contextmanifest.Hash(token),
	})
	if err != nil || affected != 1 {
		return nil, fmt.Errorf("submit evaluation assignment: lease is invalid")
	}
	affected, err = q.SubmitEvaluationAssignment(ctx, db.SubmitEvaluationAssignmentParams{
		AttemptID: optionalString(result.AttemptID), TerminalStatus: result.TerminalStatus,
		TerminalReason: result.Reason, SubmittedAt: &timestamp, UpdatedAt: timestamp,
		ID: assignmentID, CurrentLeaseID: &leaseID,
	})
	if err != nil || affected != 1 {
		return nil, fmt.Errorf("submit evaluation assignment: assignment is no longer leased")
	}
	for _, observation := range observations {
		metadataJSON, _, metadataErr := canonicalEvaluation(observation.Metadata)
		if metadataErr != nil {
			return nil, metadataErr
		}
		if err := q.PutEvaluationMetricObservation(ctx, db.PutEvaluationMetricObservationParams{
			AssignmentID: assignmentID, MetricName: observation.Name, Value: observation.Value,
			MissingReason: observation.MissingReason, Unit: observation.Unit,
			SourceType: observation.SourceType, SourceID: observation.SourceID,
			MetadataJson: metadataJSON, CreatedAt: timestamp,
		}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	updated, err := s.queries.GetEvaluationAssignment(ctx, assignmentID)
	if err != nil {
		return nil, err
	}
	assignment := dbEvaluationAssignment(updated)
	return &assignment, nil
}

func (s *Store) CancelEvaluationAssignment(ctx context.Context, assignmentID, leaseID, owner, token, reason string) (*EvaluationAssignment, error) {
	if reason == "" {
		return nil, fmt.Errorf("cancel evaluation assignment: reason is required")
	}
	timestamp := now()
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)
	affected, err := q.CompleteEvaluationAssignmentLease(ctx, db.CompleteEvaluationAssignmentLeaseParams{
		Status: "cancelled", CompletedAt: &timestamp, ID: leaseID, AssignmentID: assignmentID,
		LeaseOwner: owner, LeaseTokenHash: contextmanifest.Hash(token),
	})
	if err != nil || affected != 1 {
		return nil, fmt.Errorf("cancel evaluation assignment: lease is invalid")
	}
	affected, err = q.CancelEvaluationAssignment(ctx, db.CancelEvaluationAssignmentParams{
		TerminalReason: reason, SubmittedAt: &timestamp, UpdatedAt: timestamp, ID: assignmentID,
	})
	if err != nil || affected != 1 {
		return nil, fmt.Errorf("cancel evaluation assignment: assignment is no longer active")
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	row, err := s.queries.GetEvaluationAssignment(ctx, assignmentID)
	if err != nil {
		return nil, err
	}
	assignment := dbEvaluationAssignment(row)
	return &assignment, nil
}

func (s *Store) deriveEvaluationMetrics(ctx context.Context, assignment db.EvaluationAssignment, attemptID string) ([]EvaluationMetricObservation, string, string, error) {
	manifest, err := s.GetExecutionManifestByAttempt(ctx, attemptID)
	if err != nil || !evaluationTerminalExecution(manifest.Status) {
		return nil, "", "", fmt.Errorf("derive evaluation metrics: attempt has no terminal execution")
	}
	evaluationCase, err := s.GetEvaluationCase(ctx, assignment.CaseID)
	if err != nil {
		return nil, "", "", fmt.Errorf("derive evaluation metrics: load case: %w", err)
	}
	if manifest.ResourceID != evaluationCase.ResourceID {
		return nil, "", "", fmt.Errorf("derive evaluation metrics: attempt resource does not match case")
	}
	configuration, err := s.GetEvaluationConfiguration(ctx, assignment.ConfigurationID)
	if err != nil {
		return nil, "", "", err
	}
	contextRecord, err := s.GetContextManifestByAttempt(ctx, attemptID)
	if err != nil {
		return nil, "", "", err
	}
	if manifest.HostName != configuration.HostName || manifest.Provider != configuration.Provider || manifest.Model != configuration.Model ||
		manifest.RolePolicyVersion != configuration.RolePolicyVersion || contextRecord.SelectorVersion != configuration.ContextSelectorVersion ||
		contextRecord.BudgetTokens != configuration.ContextBudgetTokens || !equalStringMaps(manifest.TemplateHashes, configuration.TemplateHashes) {
		return nil, "", "", fmt.Errorf("derive evaluation metrics: execution does not match assignment configuration")
	}
	value := func(number float64) *float64 { return &number }
	accepted := 0.0
	if manifest.Status == "accepted" {
		accepted = 1
	}
	metrics := []EvaluationMetricObservation{
		{Name: "accepted_implementation", Value: value(accepted), SourceType: "execution_manifest", SourceID: manifest.ID},
		{Name: "retry_count", Value: value(float64(maxInt(contextRecord.Attempt.RetryNumber-1, 0))), Unit: "attempts", SourceType: "generation_attempt", SourceID: attemptID},
		{Name: "input_tokens", Value: value(float64(manifest.InputTokens)), Unit: "tokens", SourceType: "execution_manifest", SourceID: manifest.ID},
		{Name: "output_tokens", Value: value(float64(manifest.OutputTokens)), Unit: "tokens", SourceType: "execution_manifest", SourceID: manifest.ID},
		{Name: "cost_usd", Value: value(manifest.CostUSD), Unit: "usd", SourceType: "execution_manifest", SourceID: manifest.ID},
		{Name: "duration_ms", Value: value(float64(manifest.DurationMS)), Unit: "ms", SourceType: "execution_manifest", SourceID: manifest.ID},
	}
	if candidate, candidateErr := s.GetCandidateSetByAttempt(ctx, attemptID); candidateErr == nil {
		bytes, files := 0, 0
		for _, file := range candidate.Files {
			if file.WriteIntent != "preserve" {
				bytes += file.ByteSize
				files++
			}
		}
		metrics = append(metrics,
			EvaluationMetricObservation{Name: "diff_bytes", Value: value(float64(bytes)), Unit: "bytes", SourceType: "candidate_set", SourceID: candidate.ID},
			EvaluationMetricObservation{Name: "file_churn", Value: value(float64(files)), Unit: "files", SourceType: "candidate_set", SourceID: candidate.ID},
		)
	}
	return metrics, manifest.Status, manifest.DispositionReason, nil
}

func mergeEvaluationMetrics(reported, derived []EvaluationMetricObservation) []EvaluationMetricObservation {
	byName := make(map[string]EvaluationMetricObservation, len(reported)+len(derived))
	for _, observation := range reported {
		byName[observation.Name] = observation
	}
	// Engine-derived facts override host-reported values with the same name.
	for _, observation := range derived {
		byName[observation.Name] = observation
	}
	result := make([]EvaluationMetricObservation, 0, len(byName))
	for _, observation := range byName {
		result = append(result, observation)
	}
	return result
}

func completeEvaluationMetrics(policy EvaluationMetricPolicy, observations []EvaluationMetricObservation) ([]EvaluationMetricObservation, error) {
	provided := make(map[string]EvaluationMetricObservation, len(observations))
	allowed := make(map[string]bool, len(policy.Metrics))
	for _, metric := range policy.Metrics {
		allowed[metric.Name] = true
	}
	for _, observation := range observations {
		if !allowed[observation.Name] || provided[observation.Name].Name != "" {
			return nil, fmt.Errorf("submit evaluation assignment: unknown or duplicate metric %q", observation.Name)
		}
		if observation.Value == nil && observation.MissingReason == "" {
			return nil, fmt.Errorf("submit evaluation assignment: metric %s requires value or missing reason", observation.Name)
		}
		if observation.SourceType == "" {
			return nil, fmt.Errorf("submit evaluation assignment: metric %s requires provenance", observation.Name)
		}
		provided[observation.Name] = observation
	}
	result := make([]EvaluationMetricObservation, 0, len(policy.Metrics))
	for _, metric := range policy.Metrics {
		observation, ok := provided[metric.Name]
		if !ok {
			observation = EvaluationMetricObservation{
				Name: metric.Name, MissingReason: "not reported", SourceType: "evaluation_assignment",
			}
		}
		result = append(result, observation)
	}
	return result, nil
}

func (s *Store) FinalizeEvaluationRun(ctx context.Context, runID string) (*EvaluationRun, error) {
	run, err := s.GetEvaluationRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.Status == "completed" {
		return run, nil
	}
	if run.Status != "running" {
		return nil, fmt.Errorf("finalize evaluation run: run is %s", run.Status)
	}
	observations, err := s.queries.ListEvaluationMetricObservationsByRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	aggregates := aggregateEvaluationMetrics(run, observations)
	comparisons, conclusion, winner, reason := compareEvaluationMetrics(run, aggregates)
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)
	if err := q.ClearEvaluationRunAnalysis(ctx, runID); err != nil {
		return nil, err
	}
	if err := q.ClearEvaluationRunComparisons(ctx, runID); err != nil {
		return nil, err
	}
	timestamp := now()
	for _, aggregate := range aggregates {
		if err := q.PutEvaluationMetricAggregate(ctx, db.PutEvaluationMetricAggregateParams{
			RunID: runID, VariantName: aggregate.VariantName, Split: aggregate.Split,
			MetricName: aggregate.MetricName, SampleCount: int64(aggregate.SampleCount),
			MissingCount: int64(aggregate.MissingCount), MeanValue: aggregate.Mean,
			MinimumValue: aggregate.Minimum, MaximumValue: aggregate.Maximum, CreatedAt: timestamp,
		}); err != nil {
			return nil, err
		}
	}
	for _, comparison := range comparisons {
		if err := q.PutEvaluationComparison(ctx, db.PutEvaluationComparisonParams{
			RunID: runID, BaselineVariant: comparison.BaselineVariant, CandidateVariant: comparison.CandidateVariant,
			Split: comparison.Split, MetricName: comparison.MetricName,
			BaselineSampleCount: int64(comparison.BaselineSampleCount), CandidateSampleCount: int64(comparison.CandidateSampleCount),
			MissingCount: int64(comparison.MissingCount), BaselineValue: comparison.BaselineValue,
			CandidateValue: comparison.CandidateValue, AbsoluteChange: comparison.AbsoluteChange,
			RelativeChange: comparison.RelativeChange, PracticalThreshold: comparison.PracticalThreshold,
			Conclusion: comparison.Conclusion, Regression: boolInt(comparison.Regression), Reason: comparison.Reason,
			CreatedAt: timestamp,
		}); err != nil {
			return nil, err
		}
	}
	affected, err := q.CompleteEvaluationRun(ctx, db.CompleteEvaluationRunParams{
		Conclusion: conclusion, WinningVariant: winner, ConclusionReason: reason,
		CompletedAt: &timestamp, ID: runID,
	})
	if err != nil || affected != 1 {
		return nil, fmt.Errorf("finalize evaluation run: concurrent finalization")
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetEvaluationRun(ctx, runID)
}

func aggregateEvaluationMetrics(run *EvaluationRun, rows []db.ListEvaluationMetricObservationsByRunRow) []EvaluationMetricAggregate {
	type key struct{ variant, split, metric string }
	values := make(map[key][]float64)
	expected := make(map[key]int)
	splits := []string{"all", "training", "development", "held_out"}
	for _, assignment := range run.Assignments {
		for _, metric := range run.MetricPolicy.Metrics {
			expected[key{assignment.VariantName, "all", metric.Name}]++
			expected[key{assignment.VariantName, assignment.Split, metric.Name}]++
		}
	}
	for _, row := range rows {
		for _, split := range []string{"all", row.Split} {
			item := key{row.VariantName, split, row.MetricName}
			if row.Value != nil {
				values[item] = append(values[item], *row.Value)
			}
		}
	}
	var result []EvaluationMetricAggregate
	for _, variant := range run.Variants {
		for _, split := range splits {
			for _, metric := range run.MetricPolicy.Metrics {
				item := key{variant.Name, split, metric.Name}
				if expected[item] == 0 {
					continue
				}
				aggregate := EvaluationMetricAggregate{
					VariantName: variant.Name, Split: split, MetricName: metric.Name,
					SampleCount: len(values[item]), MissingCount: expected[item] - len(values[item]),
				}
				if len(values[item]) > 0 {
					mean, minimum, maximum := summarizeValues(values[item])
					aggregate.Mean, aggregate.Minimum, aggregate.Maximum = &mean, &minimum, &maximum
				}
				result = append(result, aggregate)
			}
		}
	}
	return result
}

func summarizeValues(values []float64) (mean, minimum, maximum float64) {
	minimum, maximum = values[0], values[0]
	for _, value := range values {
		mean += value
		minimum = math.Min(minimum, value)
		maximum = math.Max(maximum, value)
	}
	return mean / float64(len(values)), minimum, maximum
}

func compareEvaluationMetrics(run *EvaluationRun, aggregates []EvaluationMetricAggregate) ([]EvaluationComparison, string, string, string) {
	byKey := make(map[string]EvaluationMetricAggregate)
	baseline := ""
	for _, variant := range run.Variants {
		if variant.Baseline {
			baseline = variant.Name
		}
	}
	for _, aggregate := range aggregates {
		byKey[aggregate.VariantName+"\x00"+aggregate.Split+"\x00"+aggregate.MetricName] = aggregate
	}
	metricByName := make(map[string]EvaluationMetricDefinition)
	for _, metric := range run.MetricPolicy.Metrics {
		metricByName[metric.Name] = metric
	}
	var comparisons []EvaluationComparison
	qualified := make(map[string]bool)
	for _, candidate := range run.Variants {
		if candidate.Baseline {
			continue
		}
		candidateQualified, primaryBetter := true, false
		for _, split := range []string{"all", "training", "development", "held_out"} {
			for _, metric := range run.MetricPolicy.Metrics {
				base := byKey[baseline+"\x00"+split+"\x00"+metric.Name]
				cand := byKey[candidate.Name+"\x00"+split+"\x00"+metric.Name]
				if base.SampleCount == 0 && cand.SampleCount == 0 && base.MissingCount == 0 && cand.MissingCount == 0 {
					continue
				}
				comparison := compareMetric(run, baseline, candidate.Name, split, metric, base, cand)
				comparisons = append(comparisons, comparison)
				qualifyingSplit := split == "all" || (split == "held_out" && run.RequireHeldOut)
				if metric.Primary && qualifyingSplit {
					if comparison.Conclusion == "inconclusive" || comparison.Regression {
						candidateQualified = false
					}
					if comparison.Conclusion == "candidate_better" {
						primaryBetter = true
					}
				}
			}
		}
		qualified[candidate.Name] = candidateQualified && primaryBetter
	}
	if !allEvaluationAssignmentsTerminal(run.Assignments) {
		return comparisons, "inconclusive", "", "one or more assignments are incomplete"
	}
	var winners []string
	for candidate, wins := range qualified {
		if wins {
			winners = append(winners, candidate)
		}
	}
	sort.Strings(winners)
	if len(winners) == 1 {
		return comparisons, "candidate_wins", winners[0], "candidate improves a primary metric without a primary regression"
	}
	if len(winners) > 1 {
		return comparisons, "inconclusive", "", "multiple candidates qualify; no unique winner"
	}
	for _, comparison := range comparisons {
		if comparison.Split == "all" && comparison.Regression && metricByName[comparison.MetricName].Primary {
			return comparisons, "baseline_wins", baseline, "candidate regresses a primary metric"
		}
		if comparison.Conclusion == "inconclusive" && metricByName[comparison.MetricName].Primary {
			return comparisons, "inconclusive", "", "primary metrics are incomplete or underpowered"
		}
	}
	return comparisons, "no_material_change", "", "no candidate clears the practical significance threshold"
}

func compareMetric(run *EvaluationRun, baseline, candidate, split string, metric EvaluationMetricDefinition, base, cand EvaluationMetricAggregate) EvaluationComparison {
	threshold := metric.PracticalThreshold
	comparison := EvaluationComparison{
		BaselineVariant: baseline, CandidateVariant: candidate, Split: split, MetricName: metric.Name,
		BaselineSampleCount: base.SampleCount, CandidateSampleCount: cand.SampleCount,
		MissingCount: base.MissingCount + cand.MissingCount, BaselineValue: base.Mean,
		CandidateValue: cand.Mean, PracticalThreshold: threshold, Conclusion: "inconclusive",
	}
	if base.Mean == nil || cand.Mean == nil || base.SampleCount < run.MinimumSampleSize || cand.SampleCount < run.MinimumSampleSize || base.SampleCount != cand.SampleCount {
		comparison.Reason = "missing, unequal, or insufficient samples"
		return comparison
	}
	absolute := *cand.Mean - *base.Mean
	comparison.AbsoluteChange = &absolute
	if *base.Mean != 0 {
		relative := absolute / math.Abs(*base.Mean)
		comparison.RelativeChange = &relative
	}
	improvement := absolute
	if metric.Direction == "lower" {
		improvement = -absolute
	}
	switch {
	case improvement > threshold:
		comparison.Conclusion = "candidate_better"
		comparison.Reason = "candidate improvement exceeds practical threshold"
	case improvement < -threshold:
		comparison.Conclusion = "baseline_better"
		comparison.Regression = true
		comparison.Reason = "candidate regression exceeds practical threshold"
	default:
		comparison.Conclusion = "no_material_change"
		comparison.Reason = "change is within practical threshold"
	}
	return comparison
}

func allEvaluationAssignmentsTerminal(assignments []EvaluationAssignment) bool {
	for _, assignment := range assignments {
		if assignment.Status != "submitted" && assignment.Status != "cancelled" {
			return false
		}
	}
	return true
}

func dbEvaluationAssignment(row db.EvaluationAssignment) EvaluationAssignment {
	return EvaluationAssignment{
		ID: row.ID, RunID: row.RunID, CaseID: row.CaseID, VariantName: row.VariantName,
		ConfigurationID: row.ConfigurationID, Split: row.Split, Status: row.Status,
		CurrentLeaseID: stringVal(row.CurrentLeaseID), LeaseOwner: row.LeaseOwner,
		LeaseExpiresAt: parseOptionalTime(row.LeaseExpiresAt), AttemptID: stringVal(row.AttemptID),
		TerminalStatus: row.TerminalStatus, TerminalReason: row.TerminalReason,
		SubmittedAt: parseOptionalTime(row.SubmittedAt), CreatedAt: parseTime(row.CreatedAt), UpdatedAt: parseTime(row.UpdatedAt),
	}
}

func equalStringMaps(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
