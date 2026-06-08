package prompt

import (
	"fmt"
	"sort"
	"strings"
)

type RuntimeContext struct {
	ModuleTree      string
	ModuleFiles     map[string]string // path → content of existing module declaration files (lib.rs, mod.rs, __init__.py, etc.)
	DependencyFiles map[string]string
	AgentNotes      map[string]string
	Learnings       []string
	WaveErrors      string
	UserGuidance    string
}

func InjectRuntimeContext(prompt string, ctx RuntimeContext) string {
	var sections []string

	if ctx.ModuleTree != "" {
		sections = append(sections, fmt.Sprintf("## Module Tree\n\n%s", ctx.ModuleTree))
	}

	if len(ctx.ModuleFiles) > 0 {
		var b strings.Builder
		b.WriteString("## Existing Module Declarations & Manifest\n\n")
		b.WriteString("These files already exist. If you change them, include them in your output with your new modules or dependencies ADDED (do not remove existing lines).\n\n")
		keys := sortedKeys(ctx.ModuleFiles)
		for _, path := range keys {
			content := ctx.ModuleFiles[path]
			b.WriteString(fmt.Sprintf("### %s\n\n```\n%s\n```\n\n", path, content))
		}
		sections = append(sections, b.String())
	}

	if len(ctx.DependencyFiles) > 0 {
		var b strings.Builder
		b.WriteString("## Existing Dependencies\n\n")
		keys := sortedKeys(ctx.DependencyFiles)
		for _, id := range keys {
			content := ctx.DependencyFiles[id]
			b.WriteString(fmt.Sprintf("### %s\n\n```\n%s\n```\n\n", id, content))
		}
		sections = append(sections, b.String())
	}

	if len(ctx.AgentNotes) > 0 {
		var b strings.Builder
		b.WriteString("## Notes from Dependencies\n\n")
		keys := sortedKeys(ctx.AgentNotes)
		for _, id := range keys {
			notes := ctx.AgentNotes[id]
			b.WriteString(fmt.Sprintf("### %s\n\n%s\n\n", id, notes))
		}
		sections = append(sections, b.String())
	}

	if len(ctx.Learnings) > 0 {
		var b strings.Builder
		b.WriteString("## Learnings From Past Runs\n\n")
		b.WriteString("Apply these craft guidelines distilled from earlier generations:\n\n")
		for _, l := range ctx.Learnings {
			b.WriteString("- " + l + "\n")
		}
		sections = append(sections, b.String())
	}

	if ctx.WaveErrors != "" {
		sections = append(sections, fmt.Sprintf("## Previous Errors\n\nThe previous generation caused build errors. Fix these errors in your output.\n\n%s", ctx.WaveErrors))
	}

	if ctx.UserGuidance != "" {
		sections = append(sections, fmt.Sprintf("## User Guidance\n\n%s", ctx.UserGuidance))
	}

	if len(sections) == 0 {
		return prompt
	}

	return prompt + "\n\n" + strings.Join(sections, "\n\n")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
