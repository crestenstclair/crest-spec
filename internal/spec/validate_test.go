package spec

import (
	"os"
	"path/filepath"
	"testing"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCommand_Success(t *testing.T) {
	stdout, stderr, exitCode, err := RunCommand(t.Context(), []string{"echo", "hello"}, ".")
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Contains(t, stdout, "hello")
	assert.Empty(t, stderr)
}

func TestRunCommand_Failure(t *testing.T) {
	_, _, exitCode, err := RunCommand(t.Context(), []string{"false"}, ".")
	require.NoError(t, err)
	assert.NotEqual(t, 0, exitCode)
}

func TestCheckAssertions_ExitCode(t *testing.T) {
	assertions := []cuepkg.Assertion{
		{Kind: "exit_code", Expected: 0},
	}
	results := CheckAssertions(assertions, "", "", 0)
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)

	results = CheckAssertions(assertions, "", "", 1)
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
}

func TestCheckAssertions_StdoutContains(t *testing.T) {
	assertions := []cuepkg.Assertion{
		{Kind: "stdout_contains", Pattern: "hello world"},
	}
	results := CheckAssertions(assertions, "the output says hello world here", "", 0)
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)

	results = CheckAssertions(assertions, "nothing here", "", 0)
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
}

func TestCheckAssertions_FileExists(t *testing.T) {
	assertions := []cuepkg.Assertion{
		{Kind: "file_exists", Path: "validate_test.go"},
	}
	results := CheckAssertions(assertions, "", "", 0)
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)

	assertions = []cuepkg.Assertion{
		{Kind: "file_exists", Path: "nonexistent.txt"},
	}
	results = CheckAssertions(assertions, "", "", 0)
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
}

func TestCheckAssertions_FileMatches(t *testing.T) {
	// Create a temp file with known content
	tmpFile, err := os.CreateTemp("", "file_matches_test_*.txt")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	_, err = tmpFile.WriteString("hello world 123\nfoo bar baz\n")
	require.NoError(t, err)
	tmpFile.Close()

	// Pattern matches
	assertions := []cuepkg.Assertion{
		{Kind: "file_matches", Path: tmpFile.Name(), Pattern: `hello.*123`},
	}
	results := CheckAssertions(assertions, "", "", 0)
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)

	// Pattern does not match
	assertions = []cuepkg.Assertion{
		{Kind: "file_matches", Path: tmpFile.Name(), Pattern: `^no_match$`},
	}
	results = CheckAssertions(assertions, "", "", 0)
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
	assert.Contains(t, results[0].Message, "does not match pattern")

	// Using regex field instead of pattern
	assertions = []cuepkg.Assertion{
		{Kind: "file_matches", Path: tmpFile.Name(), Regex: `foo\s+bar`},
	}
	results = CheckAssertions(assertions, "", "", 0)
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)
}

func TestCheckAssertions_FileMatches_FileNotFound(t *testing.T) {
	assertions := []cuepkg.Assertion{
		{Kind: "file_matches", Path: "/nonexistent/path/file.txt", Pattern: `anything`},
	}
	results := CheckAssertions(assertions, "", "", 0)
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
	assert.Contains(t, results[0].Message, "cannot read file")
}

func TestCheckAssertions_FileMatches_InvalidRegex(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "file_matches_test_*.txt")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString("content")
	tmpFile.Close()

	assertions := []cuepkg.Assertion{
		{Kind: "file_matches", Path: tmpFile.Name(), Pattern: `[invalid`},
	}
	results := CheckAssertions(assertions, "", "", 0)
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
	assert.Contains(t, results[0].Message, "invalid regex")
}

func TestRunValidations_NoValidations(t *testing.T) {
	results, err := RunValidations(t.Context(), nil, ".")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestRunValidations_CompileSuccess(t *testing.T) {
	validations := []cuepkg.Validation{
		{Kind: "compiles", Command: []string{"true"}},
	}
	results, err := RunValidations(t.Context(), validations, ".")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)
}

func TestRunValidations_CompileFailure(t *testing.T) {
	validations := []cuepkg.Validation{
		{Kind: "compiles", Command: []string{"false"}},
	}
	results, err := RunValidations(t.Context(), validations, ".")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
}

func TestExecuteValidationsAcceptanceMarkerRequiresExecutionAndZeroExit(t *testing.T) {
	validation := cuepkg.Validation{
		ID:      "validation.behavioral_acceptance",
		Kind:    "integration",
		Command: []string{"sh", "-c", "printf 'running 0 tests\\n'"},
		Assertions: []cuepkg.Assertion{
			{Kind: "exit_code", Expected: 0},
			{Kind: "stdout_contains", Pattern: "CREST_ACCEPTANCE exercised passed"},
		},
	}

	zeroExecution := ExecuteValidations(t.Context(), []cuepkg.Validation{validation}, t.TempDir())
	require.Len(t, zeroExecution, 1)
	assert.False(t, zeroExecution[0].Passed, "an exit-zero target that executed no acceptance assertion must fail")
	assert.Contains(t, zeroExecution[0].Message, "CREST_ACCEPTANCE exercised passed")

	validation.Command = []string{"sh", "-c", "printf 'CREST_ACCEPTANCE exercised passed\\n'; exit 1"}
	nonzero := ExecuteValidations(t.Context(), []cuepkg.Validation{validation}, t.TempDir())
	require.Len(t, nonzero, 1)
	assert.False(t, nonzero[0].Passed, "a success marker must not hide a failing process")
	assert.Contains(t, nonzero[0].Message, "expected exit code 0, got 1")

	validation.Command = []string{"sh", "-c", "printf 'CREST_ACCEPTANCE exercised passed\\n'"}
	exercised := ExecuteValidations(t.Context(), []cuepkg.Validation{validation}, t.TempDir())
	require.Len(t, exercised, 1)
	assert.True(t, exercised[0].Passed)
}

func TestExecuteValidationsRunsEveryDefinitionAfterFailures(t *testing.T) {
	root := t.TempDir()
	validations := []cuepkg.Validation{
		{ID: "first", Kind: "custom", Command: []string{"sh", "-c", "touch first.marker; exit 1"}},
		{ID: "second", Kind: "custom", Command: []string{"sh", "-c", "touch second.marker; exit 1"}},
	}

	results := ExecuteValidations(t.Context(), validations, root)
	require.Len(t, results, 2)
	require.False(t, results[0].Passed)
	require.False(t, results[1].Passed)
	require.FileExists(t, filepath.Join(root, "first.marker"))
	require.FileExists(t, filepath.Join(root, "second.marker"))
}

func TestNestedValidationExecutableUnavailableDiagnostics(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   bool
	}{
		{name: "relayed nested command", stderr: "cargo: command not found\n", want: true},
		{name: "make command not found", stderr: "make: cargo: command not found\n", want: true},
		{name: "make execvp", stderr: "make: execvp: cargo: No such file or directory\n", want: true},
		{name: "dash nested status", stderr: "/tmp/wrapper: 2: cargo: not found\n", want: true},
		{name: "bash line", stderr: "/bin/bash: line 4: cargo: command not found\n", want: true},
		{name: "zsh field order", stderr: "zsh: command not found: cargo\n", want: true},
		{name: "compiler symbol", stderr: "src/lib.rs:42:5: error: cargo not found in this scope\n", want: false},
		{name: "application failure", stderr: "configuration file: No such file or directory\n", want: false},
		{name: "quoted assertion", stderr: "assertion failed: expected `cargo: command not found`\n", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, nestedValidationExecutableUnavailable(test.stderr))
		})
	}
}
