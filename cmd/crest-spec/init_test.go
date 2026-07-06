package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptAgentDefaultsToOpenCode(t *testing.T) {
	var out bytes.Buffer
	agent, err := promptAgent(strings.NewReader("\n"), &out)
	if err != nil {
		t.Fatalf("promptAgent returned error: %v", err)
	}
	if agent != "opencode" {
		t.Fatalf("agent = %q, want opencode", agent)
	}
	if !strings.Contains(out.String(), "OpenCode") {
		t.Fatalf("prompt did not include OpenCode option: %s", out.String())
	}
}

func TestBuildInitFilesForBothHosts(t *testing.T) {
	files, err := buildInitFiles("both", "/tmp/crest-spec")
	if err != nil {
		t.Fatalf("buildInitFiles returned error: %v", err)
	}
	paths := map[string]bool{}
	for _, file := range files {
		paths[file.path] = true
	}
	for _, path := range []string{
		"opencode.json",
		".opencode/agents/crest-orchestrator.md",
		".opencode/agents/crest-generator.md",
		".opencode/commands/apply-crest-spec.md",
		".mcp.json",
	} {
		if !paths[path] {
			t.Fatalf("missing generated path %s", path)
		}
	}
}

func TestRunInitWritesOpenCodeBootstrap(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	err := runInit(initOptions{
		agent:  "opencode",
		stdin:  strings.NewReader(""),
		stdout: &out,
	})
	if err != nil {
		t.Fatalf("runInit returned error: %v", err)
	}

	for _, path := range []string{
		"opencode.json",
		".opencode/agents/crest-orchestrator.md",
		".opencode/agents/crest-generator.md",
		".opencode/agents/crest-triage.md",
		".opencode/agents/crest-verifier.md",
		".opencode/agents/crest-design.md",
		".opencode/agents/crest-tasks.md",
		".opencode/commands/apply-crest-spec.md",
	} {
		if _, err := os.Stat(filepath.Clean(path)); err != nil {
			t.Fatalf("expected %s to be written: %v", path, err)
		}
	}

	orchestrator, err := os.ReadFile(filepath.Join(".opencode", "agents", "crest-orchestrator.md"))
	if err != nil {
		t.Fatalf("read orchestrator: %v", err)
	}
	if !strings.Contains(string(orchestrator), "dispatch one crest-generator subagent concurrently") {
		t.Fatalf("orchestrator prompt does not require concurrent wave dispatch")
	}
	if !strings.Contains(out.String(), "restart your agent host") {
		t.Fatalf("output did not mention restart: %s", out.String())
	}
}

func TestRunInitRefusesToOverwriteWithoutForce(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("opencode.json", []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("seed opencode.json: %v", err)
	}
	err := runInit(initOptions{
		agent:  "opencode",
		stdin:  strings.NewReader(""),
		stdout: ioDiscard{},
	})
	if err == nil {
		t.Fatal("expected overwrite error")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error = %q, want --force guidance", err.Error())
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}
