package spec

import (
	"os"
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
