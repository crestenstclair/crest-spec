package agent

import (
	"bufio"
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
	AllowedTools         []string
	DisallowedTools      []string
	AppendSystemPrompt   string
	NoSessionPersistence bool
	OnStderr             func(line string)
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
	path         string
	apiKey       string
	defaultModel string
	timeout      time.Duration
	configDir    string
}

func New(path, apiKey, defaultModel string, timeout time.Duration) *Agent {
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".claude")
	}
	return &Agent{
		path:         path,
		apiKey:       apiKey,
		defaultModel: defaultModel,
		timeout:      timeout,
		configDir:    configDir,
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
			if copyConfigFile(src, dst) {
				copiedConfig = true
			}
		} else {
			mirrorEntry(entry, src, dst)
		}
	}

	if !copiedConfig {
		os.WriteFile(filepath.Join(tmpDir, ".claude.json"), []byte("{}"), 0o600)
	}

	return tmpDir, cleanup, nil
}

// copyConfigFile copies the Claude config JSON file. Returns true on success.
func copyConfigFile(src, dst string) bool {
	data, err := os.ReadFile(src)
	if err != nil {
		return false
	}
	os.WriteFile(dst, data, 0o600)
	return true
}

// mirrorEntry mirrors a single config directory entry into the temp dir.
// Backups get an empty dir (stale backups confuse Claude CLI), directories
// get symlinked, symlinks are re-created, and regular files are hard-linked.
func mirrorEntry(entry os.DirEntry, src, dst string) {
	if entry.Name() == "backups" {
		os.MkdirAll(dst, 0o700)
		return
	}
	if entry.IsDir() {
		os.Symlink(src, dst)
		return
	}
	info, err := entry.Info()
	if err != nil {
		return
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		if target, err := os.Readlink(src); err == nil {
			os.Symlink(target, dst)
		}
		return
	}
	os.Link(src, dst)
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
	args, useStdin := a.buildArgs(opts)

	cmd := exec.CommandContext(ctx, a.path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	env, cleanup, err := a.buildEnv()
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	cmd.Env = env

	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	if useStdin {
		cmd.Stdin = strings.NewReader(opts.Prompt)
	}

	stderrBytes, runErr := runWithStderrStreaming(cmd, opts.OnStderr)
	result := parseResult(stdout.Bytes(), stderrBytes)

	if runErr != nil {
		return result, fmt.Errorf("claude exited with error: %w (stderr: %s) (stdout: %s)", runErr, result.Stderr, result.Output)
	}
	if result.IsError {
		return result, fmt.Errorf("claude returned is_error: %s", result.Output)
	}

	return result, nil
}

// runWithStderrStreaming runs the command, streaming stderr line-by-line
// through onLine (if non-nil) while also collecting the full output.
func runWithStderrStreaming(cmd *exec.Cmd, onLine func(string)) ([]byte, error) {
	if onLine == nil {
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stderr.Bytes(), err
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	var collected bytes.Buffer
	scanner := bufio.NewScanner(stderrPipe)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		collected.WriteString(line)
		collected.WriteByte('\n')
		onLine(line)
	}

	waitErr := cmd.Wait()
	return collected.Bytes(), waitErr
}

// buildArgs constructs the CLI argument list from RunOpts. Returns the args
// and whether the prompt should be piped via stdin.
func (a *Agent) buildArgs(opts RunOpts) ([]string, bool) {
	args := []string{"--print", "--output-format", "json", "--strict-mcp-config"}

	model := opts.Model
	if model == "" {
		model = a.defaultModel
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, "--dangerously-skip-permissions")
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

	return args, useStdin
}

// buildEnv constructs a filtered environment, adding API key isolation when
// configured. Returns the env slice and an optional cleanup function.
func (a *Agent) buildEnv() ([]string, func(), error) {
	var env []string
	for _, e := range os.Environ() {
		key := e[:strings.IndexByte(e, '=')]
		switch key {
		case "CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "AI_AGENT",
			"CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_VERSION":
			continue
		}
		env = append(env, e)
	}

	if a.apiKey == "" {
		return env, nil, nil
	}

	tmpConfigDir, cleanup, err := a.setupConfigIsolation()
	if err != nil {
		return nil, nil, fmt.Errorf("config isolation: %w", err)
	}
	env = append(env, "CLAUDE_CONFIG_DIR="+tmpConfigDir)
	env = append(env, "ANTHROPIC_API_KEY="+a.apiKey)

	return env, cleanup, nil
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
