package observability

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cserrors "github.com/crestenstclair/crest-spec/internal/errors"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"github.com/crestenstclair/crest-spec/internal/store"
)

type faultingRepository struct {
	Repository
	verificationFactsErr error
	activeSessionErr     error
	contextManifestErr   error
	executionManifestErr error
	candidateSetErr      error
}

func (r faultingRepository) GetVerificationCompletionFacts(ctx context.Context, projectName string) (*store.VerificationCompletionFacts, error) {
	if r.verificationFactsErr != nil {
		return nil, r.verificationFactsErr
	}
	return r.Repository.GetVerificationCompletionFacts(ctx, projectName)
}

func (r faultingRepository) GetActiveSession() (*store.Session, error) {
	if r.activeSessionErr != nil {
		return nil, r.activeSessionErr
	}
	return r.Repository.GetActiveSession()
}

func (r faultingRepository) GetContextManifestByAttempt(ctx context.Context, attemptID string) (*store.ContextManifest, error) {
	if r.contextManifestErr != nil {
		return nil, r.contextManifestErr
	}
	return r.Repository.GetContextManifestByAttempt(ctx, attemptID)
}

func (r faultingRepository) GetExecutionManifestByAttempt(ctx context.Context, attemptID string) (*store.ExecutionManifest, error) {
	if r.executionManifestErr != nil {
		return nil, r.executionManifestErr
	}
	return r.Repository.GetExecutionManifestByAttempt(ctx, attemptID)
}

func (r faultingRepository) GetCandidateSetByAttempt(ctx context.Context, attemptID string) (*store.CandidateSet, error) {
	if r.candidateSetErr != nil {
		return nil, r.candidateSetErr
	}
	return r.Repository.GetCandidateSetByAttempt(ctx, attemptID)
}

func TestGoalFailsClosedWhenVerificationFactsCannotBeLoaded(t *testing.T) {
	_, st := seededService(t)
	injected := errors.New("verification facts unavailable")

	_, err := NewService(faultingRepository{Repository: st, verificationFactsErr: injected}).Goal(
		context.Background(), "synth", "goal.play",
	)

	require.ErrorIs(t, err, injected)
	assert.ErrorContains(t, err, "load verification completion facts")
}

func TestGoalTreatsTypedMissingVerificationFactsAsNoEvidence(t *testing.T) {
	_, st := seededService(t)

	goal, err := NewService(faultingRepository{Repository: st, verificationFactsErr: cserrors.ErrNotFound}).Goal(
		context.Background(), "synth", "goal.play",
	)

	require.NoError(t, err)
	require.Len(t, goal.Evidence, 1)
	assert.Equal(t, "missing", goal.Evidence[0].Currency)
}

func TestPlanFailsClosedWhenActiveSessionLookupFails(t *testing.T) {
	_, st := seededService(t)
	injected := errors.New("active session query failed")

	_, err := NewService(faultingRepository{Repository: st, activeSessionErr: injected}).Plan(
		context.Background(), "synth", "spec-v1", nil, nil, nil,
	)

	require.ErrorIs(t, err, injected)
	assert.ErrorContains(t, err, "load active session")
}

func TestPlanTreatsTypedMissingActiveSessionAsIdle(t *testing.T) {
	_, st := seededService(t)

	plan, err := NewService(faultingRepository{Repository: st, activeSessionErr: cserrors.ErrNotFound}).Plan(
		context.Background(), "synth", "spec-v1", nil, nil, nil,
	)

	require.NoError(t, err)
	assert.Empty(t, plan.ActiveSessionID)
}

func TestPlanRejectsMalformedPersistedSessionPlan(t *testing.T) {
	service, st := seededService(t)
	require.NoError(t, st.CreateApply("apply-malformed", "spec-v1"))
	require.NoError(t, st.CreateSession(store.Session{
		ID: "session-malformed", ApplyID: "apply-malformed", PlanJSON: "{", WavesJSON: "[]", HashesJSON: "{}",
	}))

	_, err := service.Plan(context.Background(), "synth", "spec-v1", []planpkg.PlannedAction{}, nil, nil)

	require.Error(t, err)
	assert.ErrorContains(t, err, "decode active session \"session-malformed\" plan")
}

func TestAttemptFailsClosedWhenOptionalRecordLookupFailsOperationally(t *testing.T) {
	_, st := seededService(t)
	require.NoError(t, st.CreateApply("apply-attempt-fault", "spec-v1"))
	require.NoError(t, st.CreateSession(store.Session{
		ID: "session-attempt-fault", ApplyID: "apply-attempt-fault", PlanJSON: "[]", WavesJSON: "[]", HashesJSON: "{}",
	}))
	seedOperationalAttempt(t, st, "session-attempt-fault", "apply-attempt-fault", "aggregate.Voice", "attempt-fault", true)

	for _, test := range []struct {
		name       string
		repository faultingRepository
		message    string
	}{
		{
			name:       "execution",
			repository: faultingRepository{Repository: st, executionManifestErr: errors.New("execution hydration failed")},
			message:    "load execution manifest",
		},
		{
			name:       "candidate",
			repository: faultingRepository{Repository: st, candidateSetErr: errors.New("candidate hydration failed")},
			message:    "load candidate output",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewService(test.repository).Attempt(context.Background(), "attempt-fault")
			require.Error(t, err)
			assert.ErrorContains(t, err, test.message)
		})
	}
}

func TestResourceFailsClosedWhenAttemptProvenanceLookupFailsOperationally(t *testing.T) {
	_, st := seededService(t)
	require.NoError(t, st.CreateApply("apply-resource-fault", "spec-v1"))
	require.NoError(t, st.CreateSession(store.Session{
		ID: "session-resource-fault", ApplyID: "apply-resource-fault", PlanJSON: "[]", WavesJSON: "[]", HashesJSON: "{}",
	}))
	seedOperationalAttempt(t, st, "session-resource-fault", "apply-resource-fault", "aggregate.Voice", "attempt-resource-fault", true)

	for _, test := range []struct {
		name       string
		repository faultingRepository
		message    string
	}{
		{
			name:       "context",
			repository: faultingRepository{Repository: st, contextManifestErr: errors.New("context hydration failed")},
			message:    "load context manifest",
		},
		{
			name:       "execution",
			repository: faultingRepository{Repository: st, executionManifestErr: errors.New("execution hydration failed")},
			message:    "load execution manifest",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewService(test.repository).Resource(context.Background(), "synth", "aggregate.Voice")
			require.Error(t, err)
			assert.ErrorContains(t, err, test.message)
		})
	}
}
