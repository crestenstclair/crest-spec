package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/db"
	"github.com/crestenstclair/crest-spec/internal/execution"
)

type EvaluationPromotionProposal struct {
	ID                        string                        `json:"id"`
	RunID                     string                        `json:"run_id"`
	ConfigurationID           string                        `json:"configuration_id"`
	ConfigurationName         string                        `json:"configuration_name"`
	ConfigurationIdentityHash string                        `json:"configuration_identity_hash"`
	VariantName               string                        `json:"variant_name"`
	ChangeKind                string                        `json:"change_kind"`
	TargetIdentity            string                        `json:"target_identity"`
	Change                    map[string]any                `json:"change"`
	ChangeHash                string                        `json:"change_hash"`
	RollbackIdentity          string                        `json:"rollback_identity"`
	Status                    string                        `json:"status"`
	EligibilityReason         string                        `json:"eligibility_reason"`
	CreatedAt                 time.Time                     `json:"created_at"`
	UpdatedAt                 time.Time                     `json:"updated_at"`
	Decisions                 []EvaluationPromotionDecision `json:"decisions,omitempty"`
}

type EvaluationPromotionDecision struct {
	ID         string    `json:"id"`
	ProposalID string    `json:"proposal_id"`
	Decision   string    `json:"decision"`
	Actor      string    `json:"actor"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *Store) CreateEvaluationPromotionProposal(ctx context.Context, runID, variantName, changeKind, targetIdentity string, change map[string]any, rollbackIdentity string) (*EvaluationPromotionProposal, error) {
	if runID == "" || variantName == "" || targetIdentity == "" || rollbackIdentity == "" || len(change) == 0 {
		return nil, fmt.Errorf("create evaluation promotion: run, variant, target, change, and rollback identity are required")
	}
	validKind := map[string]bool{
		"learning": true, "template": true, "context_selector": true,
		"planner": true, "role_policy": true,
	}
	if !validKind[changeKind] {
		return nil, fmt.Errorf("create evaluation promotion: unsupported change kind %q", changeKind)
	}
	run, err := s.GetEvaluationRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	configurationID := ""
	for _, variant := range run.Variants {
		if variant.Name == variantName {
			configurationID = variant.ConfigurationID
			break
		}
	}
	if configurationID == "" {
		return nil, fmt.Errorf("create evaluation promotion: variant %q is not part of run", variantName)
	}
	status, reason := evaluationPromotionEligibility(run, variantName)
	changeJSON, changeHash, err := execution.CanonicalRedacted(change)
	if err != nil {
		return nil, fmt.Errorf("create evaluation promotion: canonical change: %w", err)
	}
	timestamp := now()
	if err := s.putEvaluationBlob(ctx, changeHash, changeJSON, timestamp); err != nil {
		return nil, err
	}
	id := uuid.NewString()
	if err := s.queries.PutEvaluationPromotionProposal(ctx, db.PutEvaluationPromotionProposalParams{
		ID: id, RunID: runID, ConfigurationID: configurationID, VariantName: variantName,
		ChangeKind: changeKind, TargetIdentity: targetIdentity, ChangeBlob: changeHash,
		RollbackIdentity: rollbackIdentity, Status: status, EligibilityReason: reason,
		CreatedAt: timestamp, UpdatedAt: timestamp,
	}); err != nil {
		return nil, err
	}
	row, err := s.queries.GetEvaluationPromotionProposalByIdentity(ctx, db.GetEvaluationPromotionProposalByIdentityParams{
		RunID: runID, ConfigurationID: configurationID, VariantName: variantName,
		ChangeKind: changeKind, TargetIdentity: targetIdentity, ChangeBlob: changeHash,
	})
	if err != nil {
		return nil, err
	}
	return s.hydrateEvaluationPromotion(ctx, evaluationPromotionRow{
		ID: row.ID, RunID: row.RunID, ConfigurationID: row.ConfigurationID,
		VariantName: row.VariantName, ChangeKind: row.ChangeKind, TargetIdentity: row.TargetIdentity,
		ChangeBlob: row.ChangeBlob, RollbackIdentity: row.RollbackIdentity, Status: row.Status,
		EligibilityReason: row.EligibilityReason, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		ConfigurationName: row.ConfigurationName, ConfigurationIdentityHash: row.ConfigurationIdentityHash,
		ChangeContent: row.ChangeContent,
	})
}

func evaluationPromotionEligibility(run *EvaluationRun, variantName string) (string, string) {
	if run.Status != "completed" {
		return "proposed", "evaluation run is not complete"
	}
	if run.Conclusion != "candidate_wins" || run.WinningVariant != variantName {
		return "proposed", "variant is not the unique winning candidate"
	}
	primary := make(map[string]bool)
	for _, metric := range run.MetricPolicy.Metrics {
		if metric.Primary {
			primary[metric.Name] = true
		}
	}
	requiredSplits := map[string]bool{"all": true}
	if run.RequireHeldOut {
		requiredSplits["held_out"] = true
	}
	seen := make(map[string]bool)
	for _, comparison := range run.Comparisons {
		if comparison.CandidateVariant != variantName || !requiredSplits[comparison.Split] || !primary[comparison.MetricName] {
			continue
		}
		seen[comparison.Split+"\x00"+comparison.MetricName] = true
		if comparison.Regression || comparison.Conclusion == "inconclusive" {
			return "proposed", "required comparison is regressed or inconclusive"
		}
	}
	for split := range requiredSplits {
		for metric := range primary {
			if !seen[split+"\x00"+metric] {
				return "proposed", "required primary comparison evidence is missing"
			}
		}
	}
	return "eligible", "unique evaluated winner with complete primary comparison evidence"
}

func (s *Store) GetEvaluationPromotionProposal(ctx context.Context, id string) (*EvaluationPromotionProposal, error) {
	row, err := s.queries.GetEvaluationPromotionProposal(ctx, id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return s.hydrateEvaluationPromotion(ctx, evaluationPromotionRow{
		ID: row.ID, RunID: row.RunID, ConfigurationID: row.ConfigurationID,
		VariantName: row.VariantName, ChangeKind: row.ChangeKind, TargetIdentity: row.TargetIdentity,
		ChangeBlob: row.ChangeBlob, RollbackIdentity: row.RollbackIdentity, Status: row.Status,
		EligibilityReason: row.EligibilityReason, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		ConfigurationName: row.ConfigurationName, ConfigurationIdentityHash: row.ConfigurationIdentityHash,
		ChangeContent: row.ChangeContent,
	})
}

func (s *Store) ListEvaluationPromotionProposals(ctx context.Context, status string, limit int) ([]EvaluationPromotionProposal, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.queries.ListEvaluationPromotionProposals(ctx, db.ListEvaluationPromotionProposalsParams{
		StatusFilter: status, RowLimit: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	result := make([]EvaluationPromotionProposal, 0, len(rows))
	for _, row := range rows {
		proposal, hydrateErr := s.hydrateEvaluationPromotion(ctx, evaluationPromotionRow{
			ID: row.ID, RunID: row.RunID, ConfigurationID: row.ConfigurationID,
			VariantName: row.VariantName, ChangeKind: row.ChangeKind, TargetIdentity: row.TargetIdentity,
			ChangeBlob: row.ChangeBlob, RollbackIdentity: row.RollbackIdentity, Status: row.Status,
			EligibilityReason: row.EligibilityReason, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			ConfigurationName: row.ConfigurationName, ConfigurationIdentityHash: row.ConfigurationIdentityHash,
			ChangeContent: row.ChangeContent,
		})
		if hydrateErr != nil {
			return nil, hydrateErr
		}
		result = append(result, *proposal)
	}
	return result, nil
}

func (s *Store) RecordEvaluationPromotionDecision(ctx context.Context, proposalID, decision, actor, reason string) (*EvaluationPromotionProposal, error) {
	if actor == "" || reason == "" {
		return nil, fmt.Errorf("record evaluation promotion decision: actor and reason are required")
	}
	proposal, err := s.GetEvaluationPromotionProposal(ctx, proposalID)
	if err != nil {
		return nil, err
	}
	expected, target := "", decision
	switch decision {
	case "approved":
		expected = "eligible"
	case "rejected":
		if proposal.Status != "eligible" && proposal.Status != "proposed" {
			return nil, fmt.Errorf("record evaluation promotion decision: cannot reject %s proposal", proposal.Status)
		}
		expected = proposal.Status
	case "applied":
		expected = "approved"
	case "rolled_back":
		expected = "applied"
	default:
		return nil, fmt.Errorf("record evaluation promotion decision: invalid decision %q", decision)
	}
	if proposal.Status != expected {
		return nil, fmt.Errorf("record evaluation promotion decision: %s requires %s status, got %s", decision, expected, proposal.Status)
	}
	timestamp := now()
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)
	affected, err := q.UpdateEvaluationPromotionStatus(ctx, db.UpdateEvaluationPromotionStatusParams{
		Status: target, UpdatedAt: timestamp, ID: proposalID, Status_2: expected,
	})
	if err != nil || affected != 1 {
		return nil, fmt.Errorf("record evaluation promotion decision: concurrent status transition")
	}
	if err := q.PutEvaluationPromotionDecision(ctx, db.PutEvaluationPromotionDecisionParams{
		ID: uuid.NewString(), ProposalID: proposalID, Decision: decision,
		Actor: actor, Reason: reason, CreatedAt: timestamp,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetEvaluationPromotionProposal(ctx, proposalID)
}

type evaluationPromotionRow struct {
	ID, RunID, ConfigurationID, VariantName, ChangeKind, TargetIdentity string
	ChangeBlob, RollbackIdentity, Status, EligibilityReason             string
	CreatedAt, UpdatedAt                                                string
	ConfigurationName, ConfigurationIdentityHash                        string
	ChangeContent                                                       []byte
}

func (s *Store) hydrateEvaluationPromotion(ctx context.Context, row evaluationPromotionRow) (*EvaluationPromotionProposal, error) {
	result := &EvaluationPromotionProposal{
		ID: row.ID, RunID: row.RunID, ConfigurationID: row.ConfigurationID,
		ConfigurationName: row.ConfigurationName, ConfigurationIdentityHash: row.ConfigurationIdentityHash,
		VariantName: row.VariantName, ChangeKind: row.ChangeKind, TargetIdentity: row.TargetIdentity,
		ChangeHash: row.ChangeBlob, RollbackIdentity: row.RollbackIdentity, Status: row.Status,
		EligibilityReason: row.EligibilityReason, CreatedAt: parseTime(row.CreatedAt), UpdatedAt: parseTime(row.UpdatedAt),
	}
	if err := json.Unmarshal(row.ChangeContent, &result.Change); err != nil {
		return nil, err
	}
	decisions, err := s.queries.ListEvaluationPromotionDecisions(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	for _, decision := range decisions {
		result.Decisions = append(result.Decisions, EvaluationPromotionDecision{
			ID: decision.ID, ProposalID: decision.ProposalID, Decision: decision.Decision,
			Actor: decision.Actor, Reason: decision.Reason, CreatedAt: parseTime(decision.CreatedAt),
		})
	}
	return result, nil
}
