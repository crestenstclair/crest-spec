package spec

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
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
		default:
			r.Passed = false
			r.Message = fmt.Sprintf("unknown assertion kind: %s", a.Kind)
		}

		results = append(results, r)
	}

	return results
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
				msg = fmt.Sprintf("%s failed (exit %d):\nstdout: %s\nstderr: %s", v.Kind, exitCode, stdout, stderr)
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
					msg = strings.Join(msgs, "; ")
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
					msg = fmt.Sprintf("integration failed (exit %d):\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
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
