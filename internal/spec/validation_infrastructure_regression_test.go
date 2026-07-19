package spec

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValidationInfrastructureFailureRetriesGateWithoutRegeneration captures
// the production failure chain where the host toolchain is available through
// PATH, one validation executable is temporarily unavailable, and the same
// accepted source candidate must survive until validation can be retried.
//
// This deliberately drives a real Begin -> governed Commit -> Verify -> Next
// -> Verify -> Finish session. The only direct store reads assert the external
// invariant that validation infrastructure cannot consume a generation retry
// or turn an accepted candidate back into model work.
func TestValidationInfrastructureFailureRetriesGateWithoutRegeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses executable POSIX shell fixtures")
	}

	const projectCUE = `package regression

project: name: "rt"
project: meta: language: "go"
project: contexts: Runtime: purpose: "validation recovery"
project: contexts: Runtime: valueObjects: AcceptedSource: {
	from: "string"
	contributesTo: [{
		capability: "capability.exercise_resource"
		contribution: "provides the accepted source whose validation is retried"
	}]
}
project: validations: [
	{
		kind: "custom"
		command: ["cargo", "check"]
		environment: ["CREST_CARGO_LOG"]
		description: "host cargo is inherited by the controlled validator"
	},
	{
		kind: "custom"
		command: ["crest-transient-validator"]
		description: "transient validation infrastructure is available"
	},
	{
		kind: "custom"
		command: ["crest-validation-wrapper"]
		resources: ["valueObject.Runtime.AcceptedSource"]
		description: "a wrapper must not disguise a missing nested tool as source damage"
	},
]
`

	s, state := newTestSpecWithSessionCUE(t, projectCUE)
	s.cfg.WaveMaxRetries = 2
	ctx := context.Background()
	projectRoot := filepath.Dir(s.cfg.SpecDir)
	hostBin := filepath.Join(projectRoot, "host-bin")
	require.NoError(t, os.MkdirAll(hostBin, 0o755))
	cargoLog := filepath.Join(projectRoot, "cargo-invocations.log")
	writeExecutableFixture(t, filepath.Join(hostBin, "cargo"),
		"#!/bin/sh\nprintf 'cargo\\n' >> \"$CREST_CARGO_LOG\"\nexit 0\n")
	writeExecutableFixture(t, filepath.Join(hostBin, "crest-validation-wrapper"),
		"#!/bin/sh\ncrest-transient-validator\nstatus=$?\nif [ \"$status\" -eq 127 ]; then exit 2; fi\nexit \"$status\"\n")
	t.Setenv("CREST_CARGO_LOG", cargoLog)
	t.Setenv("PATH", hostBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	commit, err := commitTestAttempt(t, s, ctx, state.sessionID, state.resourceID, []CommitFile{{
		Path: "src/accepted_source.go", Content: "package generated\n",
	}}, "", "test-model")
	require.NoError(t, err)
	require.True(t, commit.Committed)
	require.True(t, commit.ValidationDeferred)

	accepted, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	acceptedAttempts := accepted.Attempts
	require.Equal(t, string(StateCommitted), accepted.State)

	firstGate := s.VerifyWave(ctx, state.sessionID, accepted.WaveIndex)
	require.False(t, firstGate.Passed, "a missing validation executable must fail closed")
	require.Zero(t, firstGate.RetryCount, "validation infrastructure must not consume a closure repair retry")
	require.Equal(t, 2, firstGate.MaxRetries)
	require.True(t, firstGate.IntegrationValidated)
	cargoRuns, err := os.ReadFile(cargoLog)
	require.NoError(t, err)
	require.Equal(t, "cargo\n", string(cargoRuns),
		"the controlled validation process must resolve the host's cargo through PATH")
	require.Empty(t, firstGate.ReopenedResources,
		"validation infrastructure failure must not turn accepted source back into generation work")
	require.Contains(t, joinedWaveErrors(firstGate.Errors), "crest-transient-validator")
	require.Contains(t, joinedWaveErrors(firstGate.Errors), "could not run")

	preserved, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateCommitted), preserved.State)
	require.Equal(t, acceptedAttempts, preserved.Attempts,
		"an infrastructure retry must not consume a model/generation attempt")

	retry, err := s.Next(ctx, state.sessionID)
	require.NoError(t, err)
	require.True(t, retry.ValidationReady, "resume must surface the pending gate directly")
	require.Empty(t, retry.Resources, "resume must not dispatch a source worker")

	writeExecutableFixture(t, filepath.Join(hostBin, "crest-transient-validator"),
		"#!/bin/sh\nexit 0\n")
	secondGate := s.VerifyWave(ctx, state.sessionID, retry.ValidationWaveIndex)
	require.True(t, secondGate.Passed)
	require.Zero(t, secondGate.RetryCount)
	require.Equal(t, 2, secondGate.MaxRetries)
	require.Empty(t, secondGate.ReopenedResources)

	cargoRuns, err = os.ReadFile(cargoLog)
	require.NoError(t, err)
	require.Equal(t, "cargo\ncargo\n", string(cargoRuns),
		"validation retry reruns the gate against the same accepted source")

	finished, err := s.Finish(ctx, state.sessionID, false)
	require.NoError(t, err)
	require.False(t, finished.Blocked)
	require.Equal(t, 1, finished.Committed)
}

// TestWrapperReportedMissingNestedExecutableIsValidationInfrastructure covers
// the less obvious launch failure where the declared validation executable
// itself starts successfully, but a nested tool cannot be resolved. Wrappers
// such as make normalize the nested shell's 127 to their own exit code (often
// 2), leaving stderr as the only reliable infrastructure signal.
func TestWrapperReportedMissingNestedExecutableIsValidationInfrastructure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses an executable POSIX shell fixture")
	}

	const projectCUE = `package regression

project: name: "rt"
project: meta: language: "go"
project: contexts: Runtime: purpose: "validation recovery"
project: contexts: Runtime: valueObjects: AcceptedSource: {
	from: "string"
	contributesTo: [{
		capability: "capability.exercise_resource"
		contribution: "provides accepted source that a wrapper must not reopen"
	}]
}
project: validations: [{
	kind: "custom"
	command: ["crest-make-wrapper"]
	description: "wrapper whose nested cargo executable must be available"
}]
`

	s, state := newTestSpecWithSessionCUE(t, projectCUE)
	s.cfg.WaveMaxRetries = 2
	ctx := context.Background()
	projectRoot := filepath.Dir(s.cfg.SpecDir)
	hostBin := filepath.Join(projectRoot, "host-bin")
	require.NoError(t, os.MkdirAll(hostBin, 0o755))
	wrapper := filepath.Join(hostBin, "crest-make-wrapper")
	writeExecutableFixture(t, wrapper,
		"#!/bin/sh\nprintf '%s\\n' 'cargo: command not found' >&2\nexit 2\n")
	t.Setenv("PATH", hostBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	commit, err := commitTestAttempt(t, s, ctx, state.sessionID, state.resourceID, []CommitFile{{
		Path: "src/accepted_source.go", Content: "package generated\n",
	}}, "", "test-model")
	require.NoError(t, err)
	require.True(t, commit.Committed)
	require.True(t, commit.ValidationDeferred)

	accepted, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateCommitted), accepted.State)
	attemptsBefore, err := s.store.ListGenerationAttemptsBySession(ctx, state.sessionID)
	require.NoError(t, err)
	dispatchesBefore, err := s.store.ListDispatchLifecyclesBySession(ctx, state.sessionID)
	require.NoError(t, err)

	firstGate := s.VerifyWave(ctx, state.sessionID, accepted.WaveIndex)
	require.False(t, firstGate.Passed, "the missing nested executable must fail closed")
	require.Zero(t, firstGate.RetryCount, "nested tool infrastructure must not consume a closure repair retry")
	require.Equal(t, 2, firstGate.MaxRetries)
	require.Empty(t, firstGate.ReopenedResources,
		"a wrapper's missing nested executable must not reopen accepted source")
	require.Len(t, firstGate.Errors, 1)
	require.Equal(t, "validation_infrastructure", firstGate.Errors[0].Kind)
	require.Contains(t, firstGate.Errors[0].Message, "cargo")
	require.Contains(t, firstGate.Errors[0].Message, "could not run")

	runs, err := s.store.ListValidationRuns(ctx, 10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, "error", runs[0].Classification)
	require.Len(t, runs[0].Executions, 1)
	require.Equal(t, 2, runs[0].Executions[0].ExitCode,
		"the wrapper, not the missing nested tool, owns the observed exit code")
	require.Empty(t, runs[0].Executions[0].LaunchError,
		"the declared wrapper executable launched successfully")

	preserved, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateCommitted), preserved.State)
	require.Equal(t, "integration_failed", preserved.Phase)
	require.Equal(t, accepted.Attempts, preserved.Attempts)
	attemptsAfter, err := s.store.ListGenerationAttemptsBySession(ctx, state.sessionID)
	require.NoError(t, err)
	require.Len(t, attemptsAfter, len(attemptsBefore),
		"an infrastructure-only gate failure must not create a generation attempt")
	dispatchesAfter, err := s.store.ListDispatchLifecyclesBySession(ctx, state.sessionID)
	require.NoError(t, err)
	require.Len(t, dispatchesAfter, len(dispatchesBefore),
		"an infrastructure-only gate failure must not create a worker dispatch")

	retry, err := s.Next(ctx, state.sessionID)
	require.NoError(t, err)
	require.True(t, retry.ValidationReady)
	require.Empty(t, retry.Resources, "validation retry must not dispatch a source worker")

	writeExecutableFixture(t, wrapper, "#!/bin/sh\nexit 0\n")
	secondGate := s.VerifyWave(ctx, state.sessionID, retry.ValidationWaveIndex)
	require.True(t, secondGate.Passed)
	require.Zero(t, secondGate.RetryCount)
	require.Equal(t, 2, secondGate.MaxRetries)
	require.Empty(t, secondGate.ReopenedResources)
}

func writeExecutableFixture(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
}

func joinedWaveErrors(errors []WaveError) string {
	parts := make([]string, 0, len(errors))
	for _, waveError := range errors {
		parts = append(parts, waveError.Kind+": "+waveError.Message)
	}
	return strings.Join(parts, "\n")
}
