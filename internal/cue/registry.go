package cue

import (
	"fmt"
	"strings"
)

type Edge struct {
	TargetID string
	Kind     string // "uses", "implements", "of", "targets"
}

type Resource struct {
	ID           string
	Kind         string
	ContextName  string
	ParentID     string
	Declaration  any
	Meta         Meta
	Dependencies []Edge
	Validations  []Validation
}

type Registry struct {
	Project   *Project
	Resources map[string]Resource
}

func (r *Registry) Has(id string) bool {
	_, ok := r.Resources[id]
	return ok
}

func NewRegistry(project *Project) (*Registry, error) {
	reg := &Registry{
		Project:   project,
		Resources: make(map[string]Resource),
	}

	projectMeta := project.Meta

	// Project
	reg.Resources[fmt.Sprintf("project.%s", project.Name)] = Resource{
		ID:          fmt.Sprintf("project.%s", project.Name),
		Kind:        "project",
		Declaration: *project,
		Meta:        projectMeta,
	}

	// AssetKinds
	for name, ak := range project.AssetKinds {
		id := fmt.Sprintf("assetKind.%s", name)
		reg.Resources[id] = Resource{
			ID:          id,
			Kind:        "assetKind",
			Declaration: ak,
			Meta:        mergeMeta(projectMeta, ak.Meta),
		}
	}

	// Contexts
	for ctxName, ctx := range project.Contexts {
		ctxID := fmt.Sprintf("context.%s", ctxName)
		ctxMeta := mergeMeta(projectMeta, ctx.Meta)

		reg.Resources[ctxID] = Resource{
			ID:          ctxID,
			Kind:        "context",
			ContextName: ctxName,
			Declaration: ctx,
			Meta:        ctxMeta,
		}

		// Context-level value objects
		for voName, vo := range ctx.ValueObjects {
			id := fmt.Sprintf("valueObject.%s.%s", ctxName, voName)
			reg.Resources[id] = Resource{
				ID:          id,
				Kind:        "valueObject",
				ContextName: ctxName,
				ParentID:    ctxID,
				Declaration: vo,
				Meta:        mergeMeta(ctxMeta, vo.Meta),
				Validations: vo.Validations,
			}
		}

		// Aggregates
		for aggName, agg := range ctx.Aggregates {
			aggID := fmt.Sprintf("aggregate.%s.%s", ctxName, aggName)
			aggMeta := mergeMeta(ctxMeta, agg.Meta)

			var deps []Edge
			if agg.Implements != "" {
				deps = append(deps, Edge{TargetID: agg.Implements, Kind: "implements"})
			}

			reg.Resources[aggID] = Resource{
				ID:           aggID,
				Kind:         "aggregate",
				ContextName:  ctxName,
				ParentID:     ctxID,
				Declaration:  agg,
				Meta:         aggMeta,
				Dependencies: deps,
				Validations:  agg.Validations,
			}

			// Entities under aggregate
			for entName, ent := range agg.Entities {
				id := fmt.Sprintf("entity.%s.%s.%s", ctxName, aggName, entName)
				reg.Resources[id] = Resource{
					ID:          id,
					Kind:        "entity",
					ContextName: ctxName,
					ParentID:    aggID,
					Declaration: ent,
					Meta:        mergeMeta(aggMeta, ent.Meta),
					Validations: ent.Validations,
				}
			}

			// Value objects under aggregate
			for voName, vo := range agg.ValueObjects {
				id := fmt.Sprintf("valueObject.%s.%s.%s", ctxName, aggName, voName)
				reg.Resources[id] = Resource{
					ID:          id,
					Kind:        "valueObject",
					ContextName: ctxName,
					ParentID:    aggID,
					Declaration: vo,
					Meta:        mergeMeta(aggMeta, vo.Meta),
					Validations: vo.Validations,
				}
			}

			// Assets under aggregate
			for assetName, asset := range agg.Assets {
				id := fmt.Sprintf("asset.%s.%s.%s", ctxName, aggName, assetName)
				assetDeps := assetEdges(asset)
				reg.Resources[id] = Resource{
					ID:           id,
					Kind:         "asset",
					ContextName:  ctxName,
					ParentID:     aggID,
					Declaration:  asset,
					Meta:         mergeMeta(aggMeta, asset.Meta),
					Dependencies: assetDeps,
					Validations:  asset.Validations,
				}
			}
		}

		// Repositories
		for repoName, repo := range ctx.Repositories {
			id := fmt.Sprintf("repository.%s.%s", ctxName, repoName)
			var deps []Edge
			if repo.Of != "" {
				deps = append(deps, Edge{TargetID: repo.Of, Kind: "of"})
			}
			reg.Resources[id] = Resource{
				ID:           id,
				Kind:         "repository",
				ContextName:  ctxName,
				ParentID:     ctxID,
				Declaration:  repo,
				Meta:         mergeMeta(ctxMeta, repo.Meta),
				Dependencies: deps,
				Validations:  repo.Validations,
			}
		}

		// Ports
		for portName, port := range ctx.Ports {
			id := fmt.Sprintf("port.%s.%s", ctxName, portName)
			reg.Resources[id] = Resource{
				ID:          id,
				Kind:        "port",
				ContextName: ctxName,
				ParentID:    ctxID,
				Declaration: port,
				Meta:        mergeMeta(ctxMeta, port.Meta),
			}
		}

		// Domain services
		for svcName, svc := range ctx.DomainServices {
			id := fmt.Sprintf("domainService.%s.%s", ctxName, svcName)
			var deps []Edge
			for _, u := range svc.Uses {
				deps = append(deps, Edge{TargetID: u, Kind: "uses"})
			}
			reg.Resources[id] = Resource{
				ID:           id,
				Kind:         "domainService",
				ContextName:  ctxName,
				ParentID:     ctxID,
				Declaration:  svc,
				Meta:         mergeMeta(ctxMeta, svc.Meta),
				Dependencies: deps,
				Validations:  svc.Validations,
			}
		}

		// Application services
		for svcName, svc := range ctx.ApplicationServices {
			id := fmt.Sprintf("applicationService.%s.%s", ctxName, svcName)
			var deps []Edge
			for _, u := range svc.Uses {
				deps = append(deps, Edge{TargetID: u, Kind: "uses"})
			}
			reg.Resources[id] = Resource{
				ID:           id,
				Kind:         "applicationService",
				ContextName:  ctxName,
				ParentID:     ctxID,
				Declaration:  svc,
				Meta:         mergeMeta(ctxMeta, svc.Meta),
				Dependencies: deps,
				Validations:  svc.Validations,
			}
		}

		// Context-level assets
		for assetName, asset := range ctx.Assets {
			id := fmt.Sprintf("asset.%s.%s", ctxName, assetName)
			assetDeps := assetEdges(asset)
			reg.Resources[id] = Resource{
				ID:           id,
				Kind:         "asset",
				ContextName:  ctxName,
				ParentID:     ctxID,
				Declaration:  asset,
				Meta:         mergeMeta(ctxMeta, asset.Meta),
				Dependencies: assetDeps,
				Validations:  asset.Validations,
			}
		}
	}

	// Adapters
	for name, adapter := range project.Adapters {
		id := fmt.Sprintf("adapter.%s", name)
		var deps []Edge
		if adapter.Implements != "" {
			deps = append(deps, Edge{TargetID: adapter.Implements, Kind: "implements"})
		}
		reg.Resources[id] = Resource{
			ID:           id,
			Kind:         "adapter",
			Declaration:  adapter,
			Meta:         mergeMeta(projectMeta, adapter.Meta),
			Dependencies: deps,
			Validations:  adapter.Validations,
		}
	}

	// Project-level assets
	for name, asset := range project.Assets {
		id := fmt.Sprintf("asset.%s", name)
		assetDeps := assetEdges(asset)
		reg.Resources[id] = Resource{
			ID:           id,
			Kind:         "asset",
			Declaration:  asset,
			Meta:         mergeMeta(projectMeta, asset.Meta),
			Dependencies: assetDeps,
			Validations:  asset.Validations,
		}
	}

	// Validate all dependency targets exist
	var errs []string
	for id, r := range reg.Resources {
		for _, dep := range r.Dependencies {
			if !reg.Has(dep.TargetID) {
				errs = append(errs, fmt.Sprintf("%s: dependency target %q not found", id, dep.TargetID))
			}
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("dangling references:\n  %s", strings.Join(errs, "\n  "))
	}

	return reg, nil
}

func assetEdges(asset Asset) []Edge {
	var deps []Edge
	if asset.Kind != "" {
		deps = append(deps, Edge{TargetID: fmt.Sprintf("assetKind.%s", asset.Kind), Kind: "uses"})
	}
	for _, t := range asset.Targets {
		deps = append(deps, Edge{TargetID: t, Kind: "targets"})
	}
	return deps
}

func mergeMeta(parent, child Meta) Meta {
	out := parent
	if child.Language != "" {
		out.Language = child.Language
	}
	if child.Style != "" {
		out.Style = child.Style
	}
	if child.Notes != "" {
		out.Notes = child.Notes
	}
	if child.Rationale != "" {
		out.Rationale = child.Rationale
	}
	if child.ReviewLevel != "" {
		out.ReviewLevel = child.ReviewLevel
	}
	if child.Framework != "" {
		out.Framework = child.Framework
	}
	out.Rules = appendUnique(parent.Rules, child.Rules)
	out.Prompts = appendUnique(parent.Prompts, child.Prompts)
	out.References = appendUnique(parent.References, child.References)
	out.Examples = appendUnique(parent.Examples, child.Examples)
	out.Avoid = appendUnique(parent.Avoid, child.Avoid)
	return out
}

func appendUnique(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	if len(a) == 0 {
		return b
	}
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[s] = true
	}
	out := make([]string, len(a))
	copy(out, a)
	for _, s := range b {
		if !seen[s] {
			out = append(out, s)
		}
	}
	return out
}
