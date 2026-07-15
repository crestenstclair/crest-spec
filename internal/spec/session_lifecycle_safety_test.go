package spec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/crestenstclair/crest-spec/internal/config"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"github.com/crestenstclair/crest-spec/internal/store"
	"github.com/stretchr/testify/require"
)

// lifecycleFaultStore keeps the real persistence behavior for each test and
// overrides only the operation whose failure contract is under examination.
// This avoids a permissive fake accidentally making a lifecycle test pass.
type lifecycleFaultStore struct {
	specStore

	countOtherFileOwnersErr error
	listSessionResourcesErr error
	listWaveResourcesErr    error
	listResourcesErr        error
	updateSessionErr        error
	completeApplyErr        error
	releaseLockErr          error
}

func (s *lifecycleFaultStore) CountOtherFileOwners(path, resourceID string) (int64, error) {
	if s.countOtherFileOwnersErr != nil {
		return 0, s.countOtherFileOwnersErr
	}
	return s.specStore.CountOtherFileOwners(path, resourceID)
}

func (s *lifecycleFaultStore) ListSessionResources(sessionID string) ([]store.SessionResource, error) {
	if s.listSessionResourcesErr != nil {
		return nil, s.listSessionResourcesErr
	}
	return s.specStore.ListSessionResources(sessionID)
}

func (s *lifecycleFaultStore) ListSessionResourcesByWave(sessionID string, wave int) ([]store.SessionResource, error) {
	if s.listWaveResourcesErr != nil {
		return nil, s.listWaveResourcesErr
	}
	return s.specStore.ListSessionResourcesByWave(sessionID, wave)
}

func (s *lifecycleFaultStore) ListResources() ([]store.Resource, error) {
	if s.listResourcesErr != nil {
		return nil, s.listResourcesErr
	}
	return s.specStore.ListResources()
}

func (s *lifecycleFaultStore) UpdateSession(id, status string, currentWave int) error {
	if s.updateSessionErr != nil {
		return s.updateSessionErr
	}
	return s.specStore.UpdateSession(id, status, currentWave)
}

func (s *lifecycleFaultStore) CompleteApply(id string) error {
	if s.completeApplyErr != nil {
		return s.completeApplyErr
	}
	return s.specStore.CompleteApply(id)
}

func (s *lifecycleFaultStore) ReleaseLock() error {
	if s.releaseLockErr != nil {
		return s.releaseLockErr
	}
	return s.specStore.ReleaseLock()
}

func TestConfirmDestroysBlocksOnOwnershipLookupFailure(t *testing.T) {
	ctx := context.Background()
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })

	root := t.TempDir()
	generatedPath := "generated.go"
	require.NoError(t, os.WriteFile(filepath.Join(root, generatedPath), []byte("package generated\n"), 0o644))
	require.NoError(t, st.SetResource(store.Resource{ID: "resource.example", Kind: "asset"}))
	require.NoError(t, st.SetGeneratedFile(store.GeneratedFile{Path: generatedPath, ResourceID: "resource.example"}))
	require.NoError(t, st.CreateApply("apply-1", "spec-hash"))

	planJSON, err := json.Marshal([]planpkg.PlannedAction{{
		ResourceID: "resource.example",
		Kind:       planpkg.ActionDestroy,
	}})
	require.NoError(t, err)
	require.NoError(t, st.CreateSession(store.Session{
		ID:       "session-1",
		ApplyID:  "apply-1",
		PlanJSON: string(planJSON),
	}))

	s := New(st, OSFileSystem{}, &config.Config{SpecDir: filepath.Join(root, "spec")})
	s.store = &lifecycleFaultStore{specStore: s.store, countOtherFileOwnersErr: errors.New("ownership unavailable")}

	destroyed, err := s.ConfirmDestroys(ctx, "session-1", []string{"resource.example"})
	require.ErrorContains(t, err, "count other owners")
	require.Empty(t, destroyed)
	require.FileExists(t, filepath.Join(root, generatedPath), "an unknown ownership count must block filesystem deletion")

	resource, err := st.GetResource("resource.example")
	require.NoError(t, err)
	require.NotNil(t, resource, "a blocked destroy must retain the resource record")
	files, err := st.GetGeneratedFiles("resource.example")
	require.NoError(t, err)
	require.Len(t, files, 1, "a blocked destroy must retain generated-file ownership")
}

func TestFinishPropagatesSessionResourceQueryFailure(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	s.store = &lifecycleFaultStore{specStore: s.store, listSessionResourcesErr: errors.New("session resources unavailable")}

	result, err := s.Finish(context.Background(), state.sessionID, true)
	require.ErrorContains(t, err, "list session resources")
	require.Nil(t, result)
}

func TestFinishPropagatesFinalizationFailures(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*lifecycleFaultStore)
		want      string
	}{
		{
			name: "session status",
			configure: func(st *lifecycleFaultStore) {
				st.updateSessionErr = errors.New("session update unavailable")
			},
			want: "finalize session",
		},
		{
			name: "apply completion",
			configure: func(st *lifecycleFaultStore) {
				st.completeApplyErr = errors.New("apply update unavailable")
			},
			want: "complete apply",
		},
		{
			name: "lock release",
			configure: func(st *lifecycleFaultStore) {
				st.releaseLockErr = errors.New("lock release unavailable")
			},
			want: "release lock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, state := newTestSpecWithSession(t)
			faults := &lifecycleFaultStore{specStore: s.store}
			tt.configure(faults)
			s.store = faults

			result, err := s.Finish(context.Background(), state.sessionID, true)
			require.ErrorContains(t, err, tt.want)
			require.Nil(t, result)
		})
	}
}

func TestVerifyWaveFailsWhenSessionResourcesCannotBeRead(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	s.store = &lifecycleFaultStore{specStore: s.store, listWaveResourcesErr: errors.New("wave state unavailable")}

	result := s.VerifyWave(context.Background(), state.sessionID, 0)
	require.False(t, result.Passed)
	require.Len(t, result.Errors, 1)
	require.Equal(t, "session_resources", result.Errors[0].Kind)
	require.Contains(t, result.Errors[0].Message, "wave state unavailable")
}

func TestVerifyWaveFailsWhenPlanCannotBeBuilt(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	s.store = &lifecycleFaultStore{specStore: s.store, listResourcesErr: errors.New("resource inventory unavailable")}

	result := s.VerifyWave(context.Background(), state.sessionID, 0)
	require.False(t, result.Passed)
	require.NotEmpty(t, result.Errors)
	require.Equal(t, "plan", result.Errors[len(result.Errors)-1].Kind)
	require.Contains(t, result.Errors[len(result.Errors)-1].Message, "resource inventory unavailable")
}

func TestVerifyWaveFailsWhenVerificationCommandCannotLaunch(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	s.cfg.TypeCheckCommand = "true"
	s.cfg.SpecDir = filepath.Join(t.TempDir(), "missing", "spec")

	result := s.VerifyWave(context.Background(), state.sessionID, 0)
	require.False(t, result.Passed)
	require.NotEmpty(t, result.Errors)
	require.Equal(t, "type_check", result.Errors[0].Kind)
	require.Contains(t, result.Errors[0].Message, "could not run")
}
