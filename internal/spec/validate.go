package spec

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
)

type ValidationResult struct {
	Passed  bool
	Kind    string
	Message string
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

// truncateOutput keeps the last maxOutputChars of s, prefixing a marker when
// truncation occurred so the model knows earlier output was elided.
func truncateOutput(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxOutputChars {
		return s
	}
	return "...[earlier output truncated]...\n" + s[len(s)-maxOutputChars:]
}

func RunValidations(ctx context.Context, validations []cuepkg.Validation, cwd string) ([]ValidationResult, error) {
	var results []ValidationResult

	for _, v := range validations {
		stdout, stderr, exitCode, err := RunCommand(ctx, v.Command, cwd)
		if err != nil {
			return nil, fmt.Errorf("run validation %s: %w", v.Kind, err)
		}

		switch v.Kind {
		case "compiles", "test", "custom":
			passed := exitCode == 0
			msg := ""
			if !passed {
				msg = fmt.Sprintf("%s failed (exit %d):\nstdout: %s\nstderr: %s", v.Kind, exitCode, truncateOutput(stdout), truncateOutput(stderr))
			}
			results = append(results, ValidationResult{
				Passed:  passed,
				Kind:    v.Kind,
				Message: msg,
			})

		case "integration":
			if len(v.Assertions) > 0 {
				assertionResults := CheckAssertions(v.Assertions, stdout, stderr, exitCode)
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
				results = append(results, ValidationResult{
					Passed:  allPassed,
					Kind:    v.Kind,
					Message: msg,
				})
			} else {
				passed := exitCode == 0
				msg := ""
				if !passed {
					msg = fmt.Sprintf("integration failed (exit %d):\nstdout: %s\nstderr: %s", exitCode, truncateOutput(stdout), truncateOutput(stderr))
				}
				results = append(results, ValidationResult{
					Passed:  passed,
					Kind:    v.Kind,
					Message: msg,
				})
			}
		}

		if len(results) > 0 && !results[len(results)-1].Passed {
			break
		}
	}

	return results, nil
}
