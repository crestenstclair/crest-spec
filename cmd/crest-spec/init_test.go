package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
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

func TestPromptAgentSelectsCodex(t *testing.T) {
	var out bytes.Buffer
	agent, err := promptAgent(strings.NewReader("3\n"), &out)
	if err != nil {
		t.Fatalf("promptAgent returned error: %v", err)
	}
	if agent != "codex" {
		t.Fatalf("agent = %q, want codex", agent)
	}
	if !strings.Contains(out.String(), "Codex") {
		t.Fatalf("prompt did not include Codex option: %s", out.String())
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

func TestBuildInitFilesForAllHosts(t *testing.T) {
	files, err := buildInitFiles("all", "/tmp/crest-spec")
	if err != nil {
		t.Fatalf("buildInitFiles returned error: %v", err)
	}
	paths := map[string]bool{}
	for _, file := range files {
		paths[file.path] = true
	}
	for _, path := range []string{
		"opencode.json",
		".mcp.json",
		".claude/skills/apply-crest-spec/SKILL.md",
		".codex/config.toml",
		".codex/agents/crest-generator.toml",
		".agents/skills/apply-crest-spec/SKILL.md",
	} {
		if !paths[path] {
			t.Fatalf("missing generated path %s", path)
		}
	}
}

func TestBuildCodexInitFilesAreValidAndGoverned(t *testing.T) {
	files, err := buildInitFiles("codex", "/tmp/crest-spec")
	if err != nil {
		t.Fatalf("buildInitFiles returned error: %v", err)
	}
	contents := map[string]string{}
	for _, file := range files {
		contents[file.path] = file.content
	}

	config := contents[".codex/config.toml"]
	var parsed map[string]any
	if err := toml.Unmarshal([]byte(config), &parsed); err != nil {
		t.Fatalf("parse generated Codex config: %v\n%s", err, config)
	}
	for _, fragment := range []string{
		"[mcp_servers.crest-spec]",
		"required = true",
		"tool_timeout_sec = 1800",
		"default_tools_approval_mode = \"approve\"",
		"CREST_SPEC_SPEC_DIR = \"./spec\"",
	} {
		if !strings.Contains(config, fragment) {
			t.Fatalf("Codex config missing %q:\n%s", fragment, config)
		}
	}
	if strings.Contains(config, `approval_mode = "prompt"`) {
		t.Fatalf("generated Codex config unexpectedly prompts for MCP tools:\n%s", config)
	}

	for _, path := range []string{
		".codex/agents/crest-generator.toml",
		".codex/agents/crest-triage.toml",
		".codex/agents/crest-verifier.toml",
		".codex/agents/crest-design.toml",
		".codex/agents/crest-tasks.toml",
	} {
		var agent map[string]any
		if err := toml.Unmarshal([]byte(contents[path]), &agent); err != nil {
			t.Fatalf("parse generated Codex agent %s: %v", path, err)
		}
		for _, field := range []string{"name", "description", "developer_instructions"} {
			if strings.TrimSpace(fmt.Sprint(agent[field])) == "" {
				t.Fatalf("generated Codex agent %s has no %s", path, field)
			}
		}
	}

	skill := contents[".agents/skills/apply-crest-spec/SKILL.md"]
	for _, fragment := range []string{
		"apply this crest-spec",
		"explicitly requires native Codex subagents",
		"spec/context_batch",
		"limit equal to the number of currently idle worker slots",
		"remain unclaimed until dispatch_requested succeeds",
		"spawn the worker only after that event succeeds",
		"deliver a follow-up assignment to the retained agent identity",
		"If dispatch returns rebuild_context",
		"refill the slot with spec/context_batch",
		"worker must record worker_acknowledged with the same identities as its first lifecycle action",
		"worker must record dispatch_requested for the new DispatchID before worker_acknowledged",
		"call spec/dispatch_event dispatch_failed with the same DispatchID and identities",
		"never rely on implicit dispatch",
		"Blocked=true",
		"reuse it for sequential assignments across waves",
		"spec/execution_start",
		"spec/confirm_destroys",
		"spec/verification_inspect",
	} {
		if !strings.Contains(skill, fragment) {
			t.Fatalf("Codex skill missing %q", fragment)
		}
	}
	for _, forbidden := range []string{
		"claims every currently eligible",
		"immutable claimed attempt",
		"Legacy hosts may omit",
	} {
		if strings.Contains(skill, forbidden) {
			t.Fatalf("Codex skill contains obsolete lifecycle guidance %q", forbidden)
		}
	}

	generator := contents[".codex/agents/crest-generator.toml"]
	for _, fragment := range []string{
		"parent must successfully record dispatch_requested before assigning an initial prepared_context",
		"For a context this worker obtained from spec/context, call spec/dispatch_event dispatch_requested",
		"Then call spec/dispatch_event worker_acknowledged with the same DispatchID and identities",
		"If dispatch_requested returns rebuild_context for a worker-fetched context",
		"If execution_start returns rebuild_context",
		"call dispatch_failed with the same identities and a concrete reason",
		"For any other lifecycle error, stop without calling spec/execution_start",
		"repeat the dispatch-request/acknowledgement sequence for that new DispatchID",
		"only the files whose bytes actually need to change",
		"Omit unchanged context and ownership files from the candidate",
	} {
		if !strings.Contains(generator, fragment) {
			t.Fatalf("Codex generator missing %q", fragment)
		}
	}
}

func TestBuildClaudeInitIncludesReusableWorkerGuidance(t *testing.T) {
	files, err := buildInitFiles("claude-code", "/tmp/crest-spec")
	if err != nil {
		t.Fatalf("build Claude init: %v", err)
	}
	contents := map[string]string{}
	for _, file := range files {
		contents[file.path] = file.content
	}
	skill := contents[".claude/skills/apply-crest-spec/SKILL.md"]
	for _, fragment := range []string{
		"spec/context_batch",
		"limit equal to the currently idle worker slots",
		"does not claim them",
		"only after the event succeeds",
		"blocked contexts without dispatching them",
		"retain their identities",
		"validation_ready=true",
		"Every assignment registers its own actual host/model/tools/settings/context identity",
		"records dispatch_requested for that new DispatchID before worker_acknowledged",
		"If dispatch or execution_start returns rebuild_context",
		"Lifecycle calls are mandatory",
	} {
		if !strings.Contains(skill, fragment) {
			t.Fatalf("Claude skill missing %q:\n%s", fragment, skill)
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
	if !strings.Contains(string(orchestrator), "retain their identities for reuse across waves") ||
		!strings.Contains(string(orchestrator), "spec/context_batch") ||
		!strings.Contains(string(orchestrator), "limit equal to the number of idle worker slots") ||
		!strings.Contains(string(orchestrator), "prepares at most that many contexts without claiming them") ||
		!strings.Contains(string(orchestrator), "never use implicit dispatch") {
		t.Fatalf("orchestrator prompt does not require bounded reusable wave workers")
	}
	generator, err := os.ReadFile(filepath.Join(".opencode", "agents", "crest-generator.md"))
	if err != nil {
		t.Fatalf("read generator: %v", err)
	}
	for _, fragment := range []string{
		"orchestrator must successfully record dispatch_requested",
		"including every retry, call spec/dispatch_event dispatch_requested",
		"Then call spec/dispatch_event worker_acknowledged",
		"If dispatch_requested returns rebuild_context",
		"If execution_start returns rebuild_context",
		"implicit dispatch is forbidden",
	} {
		if !strings.Contains(string(generator), fragment) {
			t.Fatalf("generator prompt missing mandatory lifecycle guidance %q", fragment)
		}
	}
	command, err := os.ReadFile(filepath.Join(".opencode", "commands", "apply-crest-spec.md"))
	if err != nil {
		t.Fatalf("read command: %v", err)
	}
	if !strings.Contains(string(command), "limit equal to currently idle worker slots") ||
		!strings.Contains(string(command), "dispatch_requested before assigning") ||
		!strings.Contains(string(command), "Require worker_acknowledged before execution_start") {
		t.Fatalf("apply command is missing capacity or lifecycle guidance")
	}
	if !strings.Contains(out.String(), "restart your agent host") {
		t.Fatalf("output did not mention restart: %s", out.String())
	}
}

func TestRunInitWritesCodexBootstrap(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	err := runInit(initOptions{
		agent:  "codex",
		stdin:  strings.NewReader(""),
		stdout: &out,
	})
	if err != nil {
		t.Fatalf("runInit returned error: %v", err)
	}

	for _, path := range []string{
		".codex/config.toml",
		".codex/agents/crest-generator.toml",
		".codex/agents/crest-triage.toml",
		".codex/agents/crest-verifier.toml",
		".codex/agents/crest-design.toml",
		".codex/agents/crest-tasks.toml",
		".agents/skills/apply-crest-spec/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Clean(path)); err != nil {
			t.Fatalf("expected %s to be written: %v", path, err)
		}
	}
	if !strings.Contains(out.String(), "restart your agent host") {
		t.Fatalf("output did not mention restart: %s", out.String())
	}
	if !strings.Contains(out.String(), "apply this crest-spec") {
		t.Fatalf("output did not include the Codex invocation phrase: %s", out.String())
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
