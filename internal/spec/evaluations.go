package spec

import (
	"context"
	"time"

	"github.com/crestenstclair/crest-spec/internal/store"
)

func (s *Spec) CreateHistoricalEvaluationCase(ctx context.Context, attemptID, projectName string) (*store.EvaluationCaseAssessment, error) {
	return s.store.CreateHistoricalEvaluationCase(ctx, attemptID, projectName)
}

func (s *Spec) CreateCuratedEvaluationCase(ctx context.Context, input store.CuratedEvaluationCase) (*store.EvaluationCase, error) {
	return s.store.CreateCuratedEvaluationCase(ctx, input)
}

func (s *Spec) GetEvaluationCase(ctx context.Context, id string) (*store.EvaluationCase, error) {
	return s.store.GetEvaluationCase(ctx, id)
}

func (s *Spec) ListEvaluationCases(ctx context.Context, limit int) ([]store.EvaluationCase, error) {
	return s.store.ListEvaluationCases(ctx, limit)
}

func (s *Spec) CreateEvaluationDataset(ctx context.Context, name, description string) (*store.EvaluationDataset, error) {
	return s.store.CreateEvaluationDataset(ctx, name, description)
}

func (s *Spec) AddEvaluationDatasetCase(ctx context.Context, datasetID, caseID, split string) error {
	return s.store.AddEvaluationDatasetCase(ctx, datasetID, caseID, split)
}

func (s *Spec) SealEvaluationDataset(ctx context.Context, datasetID string) (*store.EvaluationDataset, error) {
	return s.store.SealEvaluationDataset(ctx, datasetID)
}

func (s *Spec) GetEvaluationDataset(ctx context.Context, id string) (*store.EvaluationDataset, error) {
	return s.store.GetEvaluationDataset(ctx, id)
}

func (s *Spec) ListEvaluationDatasets(ctx context.Context, limit int) ([]store.EvaluationDataset, error) {
	return s.store.ListEvaluationDatasets(ctx, limit)
}

func (s *Spec) CreateEvaluationConfiguration(ctx context.Context, input store.EvaluationConfiguration) (*store.EvaluationConfiguration, error) {
	return s.store.CreateEvaluationConfiguration(ctx, input)
}

func (s *Spec) GetEvaluationConfiguration(ctx context.Context, id string) (*store.EvaluationConfiguration, error) {
	return s.store.GetEvaluationConfiguration(ctx, id)
}

func (s *Spec) ListEvaluationConfigurations(ctx context.Context, limit int) ([]store.EvaluationConfiguration, error) {
	return s.store.ListEvaluationConfigurations(ctx, limit)
}

func (s *Spec) CreateEvaluationRun(ctx context.Context, datasetID, name string, variants []store.EvaluationRunVariantInput, policy store.EvaluationMetricPolicy, minimumSampleSize int, practicalSignificance float64, requireHeldOut bool) (*store.EvaluationRun, error) {
	return s.store.CreateEvaluationRun(ctx, datasetID, name, variants, policy, minimumSampleSize, practicalSignificance, requireHeldOut)
}

func (s *Spec) GetEvaluationRun(ctx context.Context, id string) (*store.EvaluationRun, error) {
	return s.store.GetEvaluationRun(ctx, id)
}

func (s *Spec) ListEvaluationRuns(ctx context.Context, limit int) ([]store.EvaluationRun, error) {
	return s.store.ListEvaluationRuns(ctx, limit)
}

func (s *Spec) ClaimEvaluationAssignment(ctx context.Context, runID, owner, split string, leaseDuration time.Duration) (*store.EvaluationAssignmentClaim, error) {
	return s.store.ClaimEvaluationAssignment(ctx, runID, owner, split, leaseDuration)
}

func (s *Spec) HeartbeatEvaluationAssignment(ctx context.Context, assignmentID, leaseID, owner, token string, extension time.Duration) (time.Time, error) {
	return s.store.HeartbeatEvaluationAssignment(ctx, assignmentID, leaseID, owner, token, extension)
}

func (s *Spec) ReleaseEvaluationAssignment(ctx context.Context, assignmentID, leaseID, owner, token string) error {
	return s.store.ReleaseEvaluationAssignment(ctx, assignmentID, leaseID, owner, token)
}

func (s *Spec) SubmitEvaluationAssignment(ctx context.Context, assignmentID, leaseID, owner, token string, result store.EvaluationAssignmentResult) (*store.EvaluationAssignment, error) {
	return s.store.SubmitEvaluationAssignment(ctx, assignmentID, leaseID, owner, token, result)
}

func (s *Spec) FinalizeEvaluationRun(ctx context.Context, runID string) (*store.EvaluationRun, error) {
	return s.store.FinalizeEvaluationRun(ctx, runID)
}

func (s *Spec) GetEvaluationPromotion(ctx context.Context, id string) (*store.EvaluationPromotionProposal, error) {
	return s.store.GetEvaluationPromotionProposal(ctx, id)
}

func (s *Spec) ListEvaluationPromotions(ctx context.Context, status string, limit int) ([]store.EvaluationPromotionProposal, error) {
	return s.store.ListEvaluationPromotionProposals(ctx, status, limit)
}
