package spec

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	validationpkg "github.com/crestenstclair/crest-spec/internal/validation"
)

type ValidationResult struct {
	ID             string
	Scope          string
	Passed         bool
	Kind           string
	Classification string
	RunID          string
	Message        string
	Execution      *validationpkg.Execution `json:"-"`
	Assertions     []ValidationResult       `json:"assertions,omitempty"`
}

func RunCommand(ctx context.Context, command []string, cwd string) (stdout, stderr string, exitCode int, err error) {
	if len(command) == 0 {
		return "", "", -1, fmt.Errorf("empty command")
	}

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = cwd

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()

	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			return stdout, stderr, exitErr.ExitCode(), nil
		}
		return stdout, stderr, -1, runErr
	}

	return stdout, stderr, 0, nil
}

func CheckAssertions(assertions []cuepkg.Assertion, stdout, stderr string, exitCode int) []ValidationResult {
	var results []ValidationResult

	for _, a := range assertions {
		var r ValidationResult
		r.Kind = a.Kind

		switch a.Kind {
		case "exit_code":
			r.Passed = exitCode == a.Expected
			if !r.Passed {
				r.Message = fmt.Sprintf("expected exit code %d, got %d", a.Expected, exitCode)
			}
		case "stdout_contains":
			r.Passed = strings.Contains(stdout, a.Pattern)
			if !r.Passed {
				r.Message = fmt.Sprintf("stdout does not contain %q", a.Pattern)
			}
		case "stderr_empty":
			r.Passed = strings.TrimSpace(stderr) == ""
			if !r.Passed {
				r.Message = fmt.Sprintf("stderr not empty: %s", stderr)
			}
		case "file_exists":
			_, err := os.Stat(a.Path)
			r.Passed = err == nil
			if !r.Passed {
				r.Message = fmt.Sprintf("file does not exist: %s", a.Path)
			}
		case "file_not_empty":
			info, err := os.Stat(a.Path)
			r.Passed = err == nil && info.Size() > 0
			if !r.Passed {
				r.Message = fmt.Sprintf("file empty or missing: %s", a.Path)
			}
		case "file_matches":
			content, err := os.ReadFile(a.Path)
			if err != nil {
				r.Passed = false
				r.Message = fmt.Sprintf("cannot read file %s: %v", a.Path, err)
			} else {
				pattern := a.Pattern
				if pattern == "" {
					pattern = a.Regex
				}
				re, err := regexp.Compile(pattern)
				if err != nil {
					r.Passed = false
					r.Message = fmt.Sprintf("invalid regex %q: %v", pattern, err)
				} else {
					r.Passed = re.Match(content)
					if !r.Passed {
						r.Message = fmt.Sprintf("file %s does not match pattern %q", a.Path, pattern)
					}
				}
			}
		default:
			r.Passed = false
			r.Message = fmt.Sprintf("unknown assertion kind: %s", a.Kind)
		}

		results = append(results, r)
	}

	return results
}

// maxOutputChars bounds how much command output is folded into a validation
// failure message. Compiler errors and panics are usually near the end, so we
// keep the tail. Large enough to carry a full rustc error, small enough to keep
// the retry prompt focused.
const maxOutputChars = 6000

var nestedExecutableUnavailableDiagnostics = []*regexp.Regexp{
	// Some wrappers relay only the nested shell diagnostic and omit their own
	// name entirely (for example "cargo: command not found"). Keep this form
	// restricted to the canonical command-not-found phrase.
	regexp.MustCompile(`(?i)^[a-z0-9_./+\-]+:\s*command\s+not\s+found\s*$`),
	// Shells and build wrappers commonly put the missing command between
	// colon-delimited fields. A wrapper may normalize the nested shell's 127 to
	// exit 1 or 2, so the outer exit code alone is not authoritative.
	regexp.MustCompile(`(?i)^(?:(?:g?make|ninja|env)(?:\[[0-9]+\])?|(?:.*[/\\])?(?:ba|da|z|k)?sh):\s*(?:execvp:\s*)?(?:line\s+[0-9]+:\s*|[0-9]+:\s*)?(?:['"‘][^'"’]+['"’]|[^:\s]+):\s*(?:command\s+not\s+found|not\s+found|no\s+such\s+file\s+or\s+directory)\s*$`),
	// When a script is interpreted, some shells identify the script rather than
	// themselves before the line number (for example wrapper: 3: cargo: not
	// found). Requiring the numeric shell field keeps this distinct from normal
	// compiler diagnostics.
	regexp.MustCompile(`(?i)^.+:\s*(?:line\s+)?[0-9]+:\s*[^:\s]+:\s*(?:command\s+not\s+found|not\s+found|no\s+such\s+file\s+or\s+directory)\s*$`),
	// zsh reverses the missing-command fields compared with POSIX sh/bash.
	regexp.MustCompile(`(?i)^(?:.*[/\\])?zsh:\s*command\s+not\s+found:\s*[^:\s]+\s*$`),
}

// truncateOutput keeps the last maxOutputChars of s, prefixing a marker when
// truncation occurred so the model knows earlier output was elided.
func truncateOutput(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxOutputChars {
		return s
	}
	return "...[earlier output truncated]...\n" + s[len(s)-maxOutputChars:]
}

// nestedValidationExecutableUnavailable recognizes diagnostics emitted by a
// successfully launched wrapper when that wrapper cannot launch one of its
// own tools. These are validation-environment failures, not evidence against
// the accepted project source.
func nestedValidationExecutableUnavailable(stderr string) bool {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		for _, diagnostic := range nestedExecutableUnavailableDiagnostics {
			if diagnostic.MatchString(line) {
				return true
			}
		}
	}
	return false
}

func RunValidations(ctx context.Context, validations []cuepkg.Validation, cwd string) ([]ValidationResult, error) {
	results := ExecuteValidations(ctx, validations, cwd)
	for _, result := range results {
		if result.Classification == "error" {
			return results, fmt.Errorf("run validation %s: %s", result.Kind, result.Message)
		}
	}
	return results, nil
}

// ExecuteValidations runs declared commands through the controlled runner and
// always returns the provenance record, including launch and timeout failures.
// Every distinct definition runs so one closure batch can route all independent
// owners simultaneously instead of discovering one failure per retry wave.
func ExecuteValidations(ctx context.Context, validations []cuepkg.Validation, cwd string) []ValidationResult {
	var results []ValidationResult

	for _, v := range validations {
		timeout := time.Duration(0)
		if v.Timeout != "" {
			timeout, _ = time.ParseDuration(v.Timeout)
		}
		execution := validationpkg.Execute(ctx, validationpkg.Options{
			ProjectRoot: cwd, Command: v.Command, WorkingDirectory: v.WorkingDirectory,
			Timeout: timeout, Environment: v.Environment,
		})
		result := ValidationResult{ID: v.ID, Scope: v.Scope, Kind: v.Kind, Execution: &execution}
		if execution.LaunchError != "" || execution.TimedOut {
			result.Classification = "error"
			result.Message = "validation could not run: " + execution.LaunchError
			if execution.TimedOut {
				result.Message = "validation could not run: timed out"
			}
			results = append(results, result)
			continue
		}
		stdout, stderr, exitCode := execution.Stdout, execution.Stderr, execution.ExitCode
		if exitCode != 0 && nestedValidationExecutableUnavailable(stderr) {
			result.Classification = "error"
			result.Message = fmt.Sprintf(
				"validation could not run: nested executable unavailable (wrapper exit %d):\nstderr: %s",
				exitCode, truncateOutput(stderr),
			)
			results = append(results, result)
			continue
		}

		switch v.Kind {
		case "compiles", "test", "custom":
			passed := exitCode == 0
			msg := ""
			if !passed {
				msg = fmt.Sprintf("%s failed (exit %d):\nstdout: %s\nstderr: %s", v.Kind, exitCode, truncateOutput(stdout), truncateOutput(stderr))
			}
			result.Passed, result.Message = passed, msg

		case "integration":
			if len(v.Assertions) > 0 {
				assertionResults := checkAssertionsAt(v.Assertions, stdout, stderr, exitCode, execution.WorkingDirectory)
				allPassed := true
				var msgs []string
				for _, ar := range assertionResults {
					if !ar.Passed {
						allPassed = false
						msgs = append(msgs, ar.Message)
					}
				}
				msg := ""
				if !allPassed {
					// Include the command's stdout/stderr so the retry prompt
					// carries the real failure (compiler error, panic, ...),
					// not just "expected exit code 0, got 1".
					msg = fmt.Sprintf("%s failed: %s\ncommand: %s\nstdout: %s\nstderr: %s",
						v.Kind, strings.Join(msgs, "; "), strings.Join(v.Command, " "),
						truncateOutput(stdout), truncateOutput(stderr))
				}
				result.Passed, result.Message, result.Assertions = allPassed, msg, assertionResults
			} else {
				passed := exitCode == 0
				msg := ""
				if !passed {
					msg = fmt.Sprintf("integration failed (exit %d):\nstdout: %s\nstderr: %s", exitCode, truncateOutput(stdout), truncateOutput(stderr))
				}
				result.Passed, result.Message = passed, msg
			}
		}
		if result.Passed {
			result.Classification = "passed"
		} else if result.Classification == "" {
			result.Classification = "failed"
		}
		results = append(results, result)

	}

	return results
}

func checkAssertionsAt(assertions []cuepkg.Assertion, stdout, stderr string, exitCode int, cwd string) []ValidationResult {
	resolved := make([]cuepkg.Assertion, len(assertions))
	copy(resolved, assertions)
	for index := range resolved {
		if resolved[index].Path != "" && !filepath.IsAbs(resolved[index].Path) {
			resolved[index].Path = filepath.Join(cwd, resolved[index].Path)
		}
	}
	return CheckAssertions(resolved, stdout, stderr, exitCode)
}
