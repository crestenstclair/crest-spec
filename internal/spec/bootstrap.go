package spec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// BootstrapStep reports the outcome of a single bootstrap check.
type BootstrapStep struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "ok", "created", "error"
	Message string `json:"message,omitempty"`
}

// BootstrapResult aggregates all bootstrap steps and an overall readiness flag.
type BootstrapResult struct {
	Steps []BootstrapStep `json:"steps"`
	Ready bool            `json:"ready"`
}

// BootstrapOpts holds optional overrides for the bootstrap process.
type BootstrapOpts struct {
	SpecDir string `json:"spec_dir,omitempty"`
}

// Bootstrap checks and sets up the environment for crest-spec.
// It is idempotent: safe to run multiple times.
func (s *Spec) Bootstrap(ctx context.Context, opts BootstrapOpts) (*BootstrapResult, error) {
	specDir := s.cfg.SpecDir
	if opts.SpecDir != "" {
		specDir = opts.SpecDir
	}

	result := &BootstrapResult{Ready: true}
	result.Steps = append(result.Steps, s.bootstrapSpecDir(specDir))
	result.Steps = append(result.Steps, s.bootstrapDatabase())
	result.Steps = append(result.Steps, s.bootstrapClaudeCLI())
	result.Steps = append(result.Steps, s.bootstrapMCPConfig()...)

	for _, step := range result.Steps {
		if step.Status == "error" {
			result.Ready = false
			break
		}
	}

	return result, nil
}

// bootstrapSpecDir checks whether the spec directory exists and creates it if not.
func (s *Spec) bootstrapSpecDir(specDir string) BootstrapStep {
	info, err := s.fs.Stat(specDir)
	if err == nil && info.IsDir() {
		return BootstrapStep{Name: "spec_directory", Status: "ok", Message: specDir}
	}
	if err := s.fs.MkdirAll(specDir, 0o755); err != nil {
		return BootstrapStep{Name: "spec_directory", Status: "error", Message: fmt.Sprintf("failed to create %s: %v", specDir, err)}
	}
	return BootstrapStep{Name: "spec_directory", Status: "created", Message: specDir}
}

// bootstrapDatabase checks whether the SQLite database is accessible.
func (s *Spec) bootstrapDatabase() BootstrapStep {
	_, err := s.store.ListResources()
	if err != nil {
		return BootstrapStep{Name: "database", Status: "error", Message: fmt.Sprintf("database not accessible: %v", err)}
	}
	return BootstrapStep{Name: "database", Status: "ok", Message: "SQLite database accessible"}
}

// bootstrapClaudeCLI checks whether the claude binary is in PATH.
func (s *Spec) bootstrapClaudeCLI() BootstrapStep {
	path, err := exec.LookPath("claude")
	if err != nil {
		return BootstrapStep{Name: "claude_cli", Status: "error", Message: "claude not found in PATH"}
	}
	return BootstrapStep{Name: "claude_cli", Status: "ok", Message: path}
}

// bootstrapMCPConfig checks and creates the MCP server registration in Claude's config.
func (s *Spec) bootstrapMCPConfig() []BootstrapStep {
	configPath := claudeConfigPath()
	selfBinary, err := os.Executable()
	if err != nil {
		return []BootstrapStep{{Name: "mcp_config", Status: "error", Message: fmt.Sprintf("cannot determine executable path: %v", err)}}
	}

	existing, err := s.fs.ReadFile(configPath)
	if err != nil {
		return s.createMCPConfig(configPath, selfBinary)
	}
	return s.ensureMCPRegistration(configPath, existing, selfBinary)
}

// claudeConfigPath returns the path to Claude's MCP configuration file.
func claudeConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude.json")
}

// createMCPConfig creates a new Claude config file with crest-spec registered.
func (s *Spec) createMCPConfig(configPath, binaryPath string) []BootstrapStep {
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"crest-spec": map[string]any{
				"command": binaryPath,
				"args":    []string{},
			},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")

	dir := filepath.Dir(configPath)
	if err := s.fs.MkdirAll(dir, 0o755); err != nil {
		return []BootstrapStep{{Name: "mcp_config", Status: "error", Message: fmt.Sprintf("cannot create config dir: %v", err)}}
	}
	if err := s.fs.WriteFile(configPath, data, 0o644); err != nil {
		return []BootstrapStep{{Name: "mcp_config", Status: "error", Message: fmt.Sprintf("cannot write config: %v", err)}}
	}
	return []BootstrapStep{{Name: "mcp_config", Status: "created", Message: configPath}}
}

// ensureMCPRegistration checks an existing config and adds crest-spec if missing.
func (s *Spec) ensureMCPRegistration(configPath string, existing []byte, binaryPath string) []BootstrapStep {
	var cfg map[string]any
	if err := json.Unmarshal(existing, &cfg); err != nil {
		return []BootstrapStep{{Name: "mcp_config", Status: "error", Message: fmt.Sprintf("cannot parse %s: %v", configPath, err)}}
	}

	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		servers = make(map[string]any)
		cfg["mcpServers"] = servers
	}

	if _, exists := servers["crest-spec"]; exists {
		return []BootstrapStep{{Name: "mcp_config", Status: "ok", Message: "crest-spec already registered"}}
	}

	servers["crest-spec"] = map[string]any{
		"command": binaryPath,
		"args":    []string{},
	}

	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := s.fs.WriteFile(configPath, data, 0o644); err != nil {
		return []BootstrapStep{{Name: "mcp_config", Status: "error", Message: fmt.Sprintf("cannot update config: %v", err)}}
	}
	return []BootstrapStep{{Name: "mcp_config", Status: "created", Message: "registered crest-spec in " + configPath}}
}
