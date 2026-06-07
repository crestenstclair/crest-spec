package prompt

import (
	"encoding/json"
	"fmt"
	"strings"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
)

func BuildResourcePrompt(resource cuepkg.Resource, registry *cuepkg.Registry) string {
	if resource.Kind == "asset" {
		return buildAssetPrompt(resource, registry)
	}
	return buildDomainPrompt(resource, registry)
}

func buildDomainPrompt(resource cuepkg.Resource, registry *cuepkg.Registry) string {
	var b strings.Builder

	name := extractName(resource.ID)

	b.WriteString(fmt.Sprintf("# Resource: %s — %s\n\n", resource.Kind, name))
	b.WriteString(fmt.Sprintf("ID: %s\n", resource.ID))
	if resource.ContextName != "" {
		b.WriteString(fmt.Sprintf("Context: %s\n", resource.ContextName))
	}
	b.WriteString("\n")

	b.WriteString("## Declaration\n\n")
	declJSON, _ := json.MarshalIndent(resource.Declaration, "", "  ")
	b.WriteString("```json\n")
	b.WriteString(string(declJSON))
	b.WriteString("\n```\n\n")

	// Surface implementation guidance (crate to use, gotchas) as a clean section
	// rather than leaving it buried in the JSON declaration above. Applies to
	// adapters and any resource whose meta carries notes/prompts.
	if resource.Meta.Notes != "" {
		b.WriteString("## Implementation Notes\n\n")
		b.WriteString(resource.Meta.Notes + "\n\n")
	}
	if len(resource.Meta.Prompts) > 0 {
		b.WriteString("## Implementation Guidance\n\n")
		for _, p := range resource.Meta.Prompts {
			b.WriteString("- " + p + "\n")
		}
		b.WriteString("\n")
	}

	if agg, ok := resource.Declaration.(cuepkg.Aggregate); ok {
		if len(agg.Commands) > 0 {
			b.WriteString("## Commands\n\n")
			for cmdName, fields := range agg.Commands {
				b.WriteString(fmt.Sprintf("### %s\n", cmdName))
				for field, typ := range fields {
					b.WriteString(fmt.Sprintf("- %s: %s\n", field, typ))
				}
				b.WriteString("\n")
			}
		}

		if len(agg.Events) > 0 {
			b.WriteString("## Events\n\n")
			for evtName, fields := range agg.Events {
				b.WriteString(fmt.Sprintf("### %s\n", evtName))
				for field, typ := range fields {
					b.WriteString(fmt.Sprintf("- %s: %s\n", field, typ))
				}
				b.WriteString("\n")
			}
		}

		if len(agg.Invariants) > 0 {
			b.WriteString("## Invariants\n\n")
			for _, inv := range agg.Invariants {
				b.WriteString("- " + inv + "\n")
			}
			b.WriteString("\n")
		}
	}

	// Port contract for resources that implement a port
	for _, dep := range resource.Dependencies {
		if dep.Kind == "implements" {
			if target, ok := registry.Resources[dep.TargetID]; ok {
				if port, ok := target.Declaration.(cuepkg.Port); ok {
					b.WriteString("## Port Contract\n\n")
					b.WriteString(fmt.Sprintf("Implements: %s\n\n", dep.TargetID))
					for method, sig := range port.Contract {
						b.WriteString(fmt.Sprintf("- %s: %s\n", method, sig))
					}
					b.WriteString("\n")
				}
			}
		}
	}

	// Event flow
	writeEventFlow(&b, resource)

	// Dependencies
	deps := nonImplementsDeps(resource, registry)
	if len(deps) > 0 {
		b.WriteString("## Dependencies\n\n")
		for _, dep := range deps {
			target, ok := registry.Resources[dep.TargetID]
			if !ok {
				continue
			}
			depJSON, _ := json.MarshalIndent(target.Declaration, "", "  ")
			b.WriteString(fmt.Sprintf("### %s (%s)\n\n", dep.TargetID, dep.Kind))
			b.WriteString("```json\n")
			b.WriteString(string(depJSON))
			b.WriteString("\n```\n\n")
		}
	}

	return b.String()
}

func buildAssetPrompt(resource cuepkg.Resource, registry *cuepkg.Registry) string {
	var b strings.Builder

	name := extractName(resource.ID)
	asset, _ := resource.Declaration.(cuepkg.Asset)

	b.WriteString(fmt.Sprintf("# Asset: %s\n\n", name))
	b.WriteString(fmt.Sprintf("ID: %s\n", resource.ID))
	b.WriteString(fmt.Sprintf("Kind: %s\n\n", asset.Kind))

	// Asset kind info
	for _, dep := range resource.Dependencies {
		if dep.Kind == "uses" {
			if target, ok := registry.Resources[dep.TargetID]; ok {
				if ak, ok := target.Declaration.(cuepkg.AssetKind); ok {
					b.WriteString("## Asset Kind\n\n")
					b.WriteString(fmt.Sprintf("Description: %s\n", ak.Description))
					if ak.FilePattern != "" {
						b.WriteString(fmt.Sprintf("File pattern: %s\n", ak.FilePattern))
					}
					if len(ak.Prompts) > 0 {
						b.WriteString("\n")
						for _, p := range ak.Prompts {
							b.WriteString("- " + p + "\n")
						}
					}
					b.WriteString("\n")
				}
			}
		}
	}

	if asset.Description != "" {
		b.WriteString("## Description\n\n")
		b.WriteString(asset.Description + "\n\n")
	}

	if len(asset.Prompts) > 0 {
		b.WriteString("## Prompts\n\n")
		for _, p := range asset.Prompts {
			b.WriteString("- " + p + "\n")
		}
		b.WriteString("\n")
	}

	// Targets
	var targets []cuepkg.Edge
	for _, dep := range resource.Dependencies {
		if dep.Kind == "targets" {
			targets = append(targets, dep)
		}
	}
	if len(targets) > 0 {
		b.WriteString("## Targets\n\n")
		for _, dep := range targets {
			target, ok := registry.Resources[dep.TargetID]
			if !ok {
				continue
			}
			depJSON, _ := json.MarshalIndent(target.Declaration, "", "  ")
			b.WriteString(fmt.Sprintf("### %s\n\n", dep.TargetID))
			b.WriteString("```json\n")
			b.WriteString(string(depJSON))
			b.WriteString("\n```\n\n")
		}
	}

	return b.String()
}

func extractName(id string) string {
	parts := strings.Split(id, ".")
	return parts[len(parts)-1]
}

func writeEventFlow(b *strings.Builder, resource cuepkg.Resource) {
	var consumes, publishes []string
	for _, dep := range resource.Dependencies {
		switch dep.Kind {
		case "consumes":
			consumes = append(consumes, dep.TargetID)
		case "publishes":
			publishes = append(publishes, dep.TargetID)
		}
	}
	if len(consumes) == 0 && len(publishes) == 0 {
		return
	}
	b.WriteString("## Event Flow\n\n")
	if len(consumes) > 0 {
		b.WriteString("**Consumes:**\n")
		for _, c := range consumes {
			b.WriteString("- " + c + "\n")
		}
		b.WriteString("\n")
	}
	if len(publishes) > 0 {
		b.WriteString("**Publishes:**\n")
		for _, p := range publishes {
			b.WriteString("- " + p + "\n")
		}
		b.WriteString("\n")
	}
}

func nonImplementsDeps(resource cuepkg.Resource, registry *cuepkg.Registry) []cuepkg.Edge {
	var deps []cuepkg.Edge
	for _, dep := range resource.Dependencies {
		switch dep.Kind {
		case "implements", "consumes", "publishes":
			continue
		}
		deps = append(deps, dep)
	}
	return deps
}
