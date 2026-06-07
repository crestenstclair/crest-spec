package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type RunOpts struct {
	Prompt               string
	Model                string
	Mode                 string
	Effort               string
	Cwd                  string
	RelevantPaths        []string
	AddDirs              []string
	Continue             bool
	Resume               bool
	SessionID            string
	Force                bool
	AllowedTools         []string
	DisallowedTools      []string
	AppendSystemPrompt   string
	NoSessionPersistence bool
}

type RunResult struct {
	Output     string  `json:"result"`
	Stderr     string  `json:"-"`
	Model      string  `json:"model"`
	SessionID  string  `json:"session_id"`
	DurationMS int64   `json:"duration_ms"`
	NumTurns   int     `json:"num_turns"`
	CostUSD    float64 `json:"cost_usd"`
	IsError    bool    `json:"is_error"`
	Usage      *Usage  `json:"usage"`
}

type Usage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
}

type Agent struct {
	path           string
	apiKey         string
	defaultModel   string
	permissionMode string
	timeout        time.Duration
	configDir      string
}

func New(path, apiKey, defaultModel, permissionMode string, timeout time.Duration) *Agent {
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".claude")
	}
	return &Agent{
		path:           path,
		apiKey:         apiKey,
		defaultModel:   defaultModel,
		permissionMode: permissionMode,
		timeout:        timeout,
		configDir:      configDir,
	}
}

func (a *Agent) setupConfigIsolation() (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "crest-spec-claude-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp config dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	srcInfo, err := os.Stat(a.configDir)
	if err != nil || !srcInfo.IsDir() {
		return tmpDir, cleanup, nil
	}

	entries, err := os.ReadDir(a.configDir)
	if err != nil {
		return tmpDir, cleanup, nil
	}

	copiedConfig := false
	for _, entry := range entries {
		src := filepath.Join(a.configDir, entry.Name())
		dst := filepath.Join(tmpDir, entry.Name())

		if entry.Name() == ".claude.json" {
			data, err := os.ReadFile(src)
			if err != nil {
				continue
			}
			os.WriteFile(dst, data, 0o600)
			copiedConfig = true
			continue
		}

		// Don't symlink backups — stale backups from the source dir
		// confuse Claude CLI into thinking the config was corrupted.
		if entry.Name() == "backups" {
			os.MkdirAll(dst, 0o700)
			continue
		}

		if entry.IsDir() {
			os.Symlink(src, dst)
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(src)
			if err == nil {
				os.Symlink(target, dst)
			}
			continue
		}

		os.Link(src, dst)
	}

	if !copiedConfig {
		os.WriteFile(filepath.Join(tmpDir, ".claude.json"), []byte("{}"), 0o600)
	}

	return tmpDir, cleanup, nil
}

func parseResult(stdout, stderr []byte) *RunResult {
	var result RunResult
	if err := json.Unmarshal(stdout, &result); err != nil {
		result.Output = string(stdout)
	}
	result.Stderr = string(stderr)
	return &result
}

// RunPrompt executes a Claude CLI prompt with the given options and returns the result.
func (a *Agent) RunPrompt(ctx context.Context, opts RunOpts) (*RunResult, error) {
	args := []string{"--print", "--output-format", "json", "--strict-mcp-config"}

	model := opts.Model
	if model == "" {
		model = a.defaultModel
	}
	if model != "" {
		args = append(args, "--model", model)
	}

	if a.permissionMode != "" {
		args = append(args, "--permission-mode", a.permissionMode)
	}

	if opts.Effort != "" {
		args = append(args, "--effort", opts.Effort)
	}

	for _, dir := range opts.AddDirs {
		args = append(args, "--add-dir", dir)
	}

	if opts.Continue {
		args = append(args, "--continue")
	}
	if opts.Resume {
		args = append(args, "--resume")
	}
	if opts.SessionID != "" {
		args = append(args, "--session-id", opts.SessionID)
	}
	if opts.Force {
		args = append(args, "--dangerously-skip-permissions")
	}

	if len(opts.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(opts.AllowedTools, ","))
	}
	if len(opts.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(opts.DisallowedTools, ","))
	}

	if opts.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", opts.AppendSystemPrompt)
	}
	if opts.NoSessionPersistence {
		args = append(args, "--no-session-persistence")
	}

	useStdin := len(opts.Prompt) > 8192
	if !useStdin && opts.Prompt != "" {
		args = append(args, opts.Prompt)
	}

	cmd := exec.CommandContext(ctx, a.path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	env := os.Environ()
	if a.apiKey != "" {
		tmpConfigDir, cleanup, err := a.setupConfigIsolation()
		if err != nil {
			return nil, fmt.Errorf("config isolation: %w", err)
		}
		defer cleanup()
		env = append(env, "CLAUDE_CONFIG_DIR="+tmpConfigDir)
		env = append(env, "ANTHROPIC_API_KEY="+a.apiKey)
	}
	cmd.Env = env

	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}

	if useStdin {
		cmd.Stdin = strings.NewReader(opts.Prompt)
	}

	runErr := cmd.Run()

	result := parseResult(stdout.Bytes(), stderr.Bytes())

	if runErr != nil {
		return result, fmt.Errorf("claude exited with error: %w (stderr: %s) (stdout: %s)", runErr, result.Stderr, result.Output)
	}

	if result.IsError {
		return result, fmt.Errorf("claude returned is_error: %s", result.Output)
	}

	return result, nil
}

// runSimple executes a simple Claude CLI command and returns its stdout.
func (a *Agent) runSimple(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, a.path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w (stderr: %s)", args[0], err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Models returns the list of available models from the Claude CLI.
func (a *Agent) Models(ctx context.Context) (string, error) {
	return a.runSimple(ctx, "models")
}

// About returns the Claude CLI version information.
func (a *Agent) About(ctx context.Context) (string, error) {
	return a.runSimple(ctx, "--version")
}

// Status returns the authentication status from the Claude CLI.
func (a *Agent) Status(ctx context.Context) (string, error) {
	return a.runSimple(ctx, "auth", "status")
}
