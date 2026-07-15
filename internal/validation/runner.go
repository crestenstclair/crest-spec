// Package validation executes specification-declared commands under a bounded,
// provenance-rich policy. It has no persistence concerns; callers atomically
// store the returned record through the canonical SQLite repository.
package validation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	DefaultTimeout      = 5 * time.Minute
	DefaultCaptureBytes = 4 << 20
)

type Execution struct {
	CommandJSON      string
	CommandHash      string
	WorkingDirectory string
	EnvironmentJSON  string
	EnvironmentHash  string
	SourceTreeHash   string
	ExecutableHash   string
	Stdout           string
	StdoutHash       string
	StdoutBytes      int64
	StdoutTruncated  bool
	Stderr           string
	StderrHash       string
	StderrBytes      int64
	StderrTruncated  bool
	ExitCode         int
	TimedOut         bool
	LaunchError      string
	StartedAt        time.Time
	CompletedAt      time.Time
	DurationMS       int64
	Artifacts        []Artifact
}

type Artifact struct {
	Path          string
	Content       string
	ContentHash   string
	ByteSize      int64
	CapturedBytes int64
	Truncated     bool
	Missing       bool
}

type Options struct {
	ProjectRoot      string
	Command          []string
	WorkingDirectory string
	Timeout          time.Duration
	Environment      []string
	Artifacts        []string
	CaptureBytes     int
}

func Execute(ctx context.Context, options Options) Execution {
	result := Execution{ExitCode: -1, StartedAt: time.Now().UTC()}
	root, err := filepath.Abs(options.ProjectRoot)
	if err != nil {
		result.LaunchError = fmt.Sprintf("resolve project root: %v", err)
		return finish(result)
	}
	workingDirectory, err := resolveInside(root, options.WorkingDirectory)
	if err != nil {
		result.LaunchError = err.Error()
		return finish(result)
	}
	result.WorkingDirectory = workingDirectory
	result.SourceTreeHash, err = HashSourceTree(root)
	if err != nil {
		result.LaunchError = fmt.Sprintf("hash source tree: %v", err)
		return finish(result)
	}
	if len(options.Command) == 0 || strings.TrimSpace(options.Command[0]) == "" {
		result.LaunchError = "empty command"
		return finish(result)
	}
	commandJSON, _ := json.Marshal(options.Command)
	result.CommandJSON = string(commandJSON)
	result.CommandHash = hashBytes(commandJSON)

	commandPath, lookErr := exec.LookPath(options.Command[0])
	if lookErr == nil {
		if executable, readErr := os.ReadFile(commandPath); readErr == nil {
			result.ExecutableHash = hashBytes(executable)
		}
	}
	environment, environmentManifest := selectedEnvironment(options.Environment)
	environmentJSON, _ := json.Marshal(environmentManifest)
	result.EnvironmentJSON = string(environmentJSON)
	result.EnvironmentHash = hashBytes(environmentJSON)

	timeout := options.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, options.Command[0], options.Command[1:]...)
	command.Dir = workingDirectory
	command.Env = environment
	captureBytes := options.CaptureBytes
	if captureBytes <= 0 {
		captureBytes = DefaultCaptureBytes
	}
	stdout := newCapture(captureBytes)
	stderr := newCapture(captureBytes)
	command.Stdout = stdout
	command.Stderr = stderr
	runErr := command.Run()
	result.Stdout, result.StdoutHash, result.StdoutBytes, result.StdoutTruncated = stdout.result()
	result.Stderr, result.StderrHash, result.StderrBytes, result.StderrTruncated = stderr.result()
	result.TimedOut = errors.Is(runCtx.Err(), context.DeadlineExceeded)
	switch value := runErr.(type) {
	case nil:
		result.ExitCode = 0
	case *exec.ExitError:
		result.ExitCode = value.ExitCode()
	default:
		result.LaunchError = runErr.Error()
	}
	for _, artifactPath := range options.Artifacts {
		result.Artifacts = append(result.Artifacts, captureArtifact(root, artifactPath, captureBytes))
	}
	return finish(result)
}

func finish(result Execution) Execution {
	result.CompletedAt = time.Now().UTC()
	result.DurationMS = result.CompletedAt.Sub(result.StartedAt).Milliseconds()
	if result.DurationMS < 0 {
		result.DurationMS = 0
	}
	return result
}

type environmentEntry struct {
	Name      string `json:"name"`
	Present   bool   `json:"present"`
	ValueHash string `json:"value_hash,omitempty"`
}

func selectedEnvironment(requested []string) ([]string, []environmentEntry) {
	baseline := []string{"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "CARGO_HOME", "RUSTUP_HOME", "GOCACHE", "GOMODCACHE"}
	names := append(baseline, requested...)
	sort.Strings(names)
	seen := make(map[string]bool)
	values := make([]string, 0, len(names))
	manifest := make([]environmentEntry, 0, len(names))
	for _, name := range names {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		value, present := os.LookupEnv(name)
		entry := environmentEntry{Name: name, Present: present}
		if present {
			entry.ValueHash = hashBytes([]byte(value))
			values = append(values, name+"="+value)
		}
		manifest = append(manifest, entry)
	}
	return values, manifest
}

func resolveInside(root, relative string) (string, error) {
	if relative == "" {
		relative = "."
	}
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("working directory must be relative to the project root")
	}
	resolved := filepath.Clean(filepath.Join(root, relative))
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("working directory escapes the project root")
	}
	return resolved, nil
}

func captureArtifact(root, artifactPath string, limit int) Artifact {
	artifact := Artifact{Path: filepath.ToSlash(filepath.Clean(artifactPath))}
	path, err := resolveInside(root, artifactPath)
	if err != nil {
		artifact.Missing = true
		return artifact
	}
	file, err := os.Open(path)
	if err != nil {
		artifact.Missing = true
		return artifact
	}
	defer file.Close()
	capture := newCapture(limit)
	_, _ = io.Copy(capture, file)
	artifact.Content, artifact.ContentHash, artifact.ByteSize, artifact.Truncated = capture.result()
	artifact.CapturedBytes = int64(len([]byte(artifact.Content)))
	return artifact
}

// HashSourceTree hashes project-controlled inputs while excluding VCS data,
// dependency/build caches, and crest-spec's operational SQLite files.
func HashSourceTree(root string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	type entry struct {
		path string
		mode fs.FileMode
	}
	var entries []entry
	err = filepath.WalkDir(root, func(path string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if relative == "." {
			return nil
		}
		base := item.Name()
		if item.IsDir() && (base == ".git" || base == "target" || base == "node_modules" || base == ".crest-spec") {
			return filepath.SkipDir
		}
		if item.IsDir() || strings.HasSuffix(base, ".db") || strings.HasSuffix(base, ".db-wal") || strings.HasSuffix(base, ".db-shm") {
			return nil
		}
		info, infoErr := item.Info()
		if infoErr != nil {
			return infoErr
		}
		entries = append(entries, entry{path: filepath.ToSlash(relative), mode: info.Mode()})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	digest := sha256.New()
	for _, entry := range entries {
		_, _ = io.WriteString(digest, entry.path)
		_, _ = digest.Write([]byte{0})
		_, _ = io.WriteString(digest, entry.mode.String())
		_, _ = digest.Write([]byte{0})
		path := filepath.Join(root, filepath.FromSlash(entry.path))
		if entry.mode&os.ModeSymlink != 0 {
			target, readErr := os.Readlink(path)
			if readErr != nil {
				return "", readErr
			}
			_, _ = io.WriteString(digest, target)
		} else {
			file, openErr := os.Open(path)
			if openErr != nil {
				return "", openErr
			}
			_, copyErr := io.Copy(digest, file)
			closeErr := file.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if closeErr != nil {
				return "", closeErr
			}
		}
		_, _ = digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

type captureWriter struct {
	limit int
	data  []byte
	total int64
	hash  hash.Hash
}

func newCapture(limit int) *captureWriter {
	return &captureWriter{limit: limit, hash: sha256.New()}
}

func (writer *captureWriter) Write(data []byte) (int, error) {
	written, err := writer.hash.Write(data)
	writer.total += int64(len(data))
	remaining := writer.limit - len(writer.data)
	if remaining > 0 {
		if remaining > len(data) {
			remaining = len(data)
		}
		writer.data = append(writer.data, data[:remaining]...)
	}
	return written, err
}

func (writer *captureWriter) result() (string, string, int64, bool) {
	return string(writer.data), hex.EncodeToString(writer.hash.Sum(nil)), writer.total, writer.total > int64(len(writer.data))
}

func hashBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
