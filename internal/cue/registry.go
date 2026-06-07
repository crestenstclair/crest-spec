package cue

import (
	"fmt"
	"strings"
)

type Edge struct {
	TargetID string
	Kind     string // "uses", "implements", "of", "targets", "consumes", "publishes"
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

	reg.registerProject(project)
	reg.registerAssetKinds(project)
	reg.registerContexts(project)
	reg.registerAdapters(project)
	reg.registerProjectAssets(project)

	if err := reg.validateDependencies(); err != nil {
		return nil, err
	}

	return reg, nil
}

func (reg *Registry) registerProject(project *Project) {
	id := fmt.Sprintf("project.%s", project.Name)
	reg.Resources[id] = Resource{
		ID:          id,
		Kind:        "project",
		Declaration: *project,
		Meta:        project.Meta,
	}
}

func (reg *Registry) registerAssetKinds(project *Project) {
	for name, ak := range project.AssetKinds {
		id := fmt.Sprintf("assetKind.%s", name)
		reg.Resources[id] = Resource{
			ID:          id,
			Kind:        "assetKind",
			Declaration: ak,
			Meta:        mergeMeta(project.Meta, ak.Meta),
		}
	}
}

func (reg *Registry) registerContexts(project *Project) {
	for ctxName, ctx := range project.Contexts {
		ctxID := fmt.Sprintf("context.%s", ctxName)
		ctxMeta := mergeMeta(project.Meta, ctx.Meta)

		reg.Resources[ctxID] = Resource{
			ID:          ctxID,
			Kind:        "context",
			ContextName: ctxName,
			Declaration: ctx,
			Meta:        ctxMeta,
		}

		reg.registerContextValueObjects(ctxName, ctxID, ctxMeta, ctx)
		reg.registerAggregates(ctxName, ctxID, ctxMeta, ctx)
		reg.registerRepositories(ctxName, ctxID, ctxMeta, ctx)
		reg.registerPorts(ctxName, ctxID, ctxMeta, ctx)
		reg.registerServices(ctxName, ctxID, ctxMeta, ctx)
		reg.registerContextAssets(ctxName, ctxID, ctxMeta, ctx)
	}
}

func (reg *Registry) registerContextValueObjects(ctxName, ctxID string, ctxMeta Meta, ctx Context) {
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
}

func (reg *Registry) registerAggregates(ctxName, ctxID string, ctxMeta Meta, ctx Context) {
	for aggName, agg := range ctx.Aggregates {
		aggID := fmt.Sprintf("aggregate.%s.%s", ctxName, aggName)
		aggMeta := mergeMeta(ctxMeta, agg.Meta)

		var deps []Edge
		if agg.Implements != "" {
			deps = append(deps, Edge{TargetID: agg.Implements, Kind: "implements"})
		}
		deps = append(deps, publishesEdges(agg.Publishes)...)

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

		reg.registerAggregateChildren(ctxName, aggID, aggName, aggMeta, agg)
	}
}

func (reg *Registry) registerAggregateChildren(ctxName, aggID, aggName string, aggMeta Meta, agg Aggregate) {
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

	for assetName, asset := range agg.Assets {
		id := fmt.Sprintf("asset.%s.%s.%s", ctxName, aggName, assetName)
		reg.Resources[id] = Resource{
			ID:           id,
			Kind:         "asset",
			ContextName:  ctxName,
			ParentID:     aggID,
			Declaration:  asset,
			Meta:         mergeMeta(aggMeta, asset.Meta),
			Dependencies: assetEdges(asset),
			Validations:  asset.Validations,
		}
	}
}

func (reg *Registry) registerRepositories(ctxName, ctxID string, ctxMeta Meta, ctx Context) {
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
}

func (reg *Registry) registerPorts(ctxName, ctxID string, ctxMeta Meta, ctx Context) {
	for portName, port := range ctx.Ports {
		id := fmt.Sprintf("port.%s.%s", ctxName, portName)
		reg.Resources[id] = Resource{
			ID:           id,
			Kind:         "port",
			ContextName:  ctxName,
			ParentID:     ctxID,
			Declaration:  port,
			Meta:         mergeMeta(ctxMeta, port.Meta),
			Dependencies: consumesEdges(port.Consumes),
		}
	}
}

func (reg *Registry) registerServices(ctxName, ctxID string, ctxMeta Meta, ctx Context) {
	for svcName, svc := range ctx.DomainServices {
		id := fmt.Sprintf("domainService.%s.%s", ctxName, svcName)
		deps := usesEdges(svc.Uses)
		deps = append(deps, consumesEdges(svc.Consumes)...)
		deps = append(deps, publishesEdges(svc.Publishes)...)
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

	for svcName, svc := range ctx.ApplicationServices {
		id := fmt.Sprintf("applicationService.%s.%s", ctxName, svcName)
		reg.Resources[id] = Resource{
			ID:           id,
			Kind:         "applicationService",
			ContextName:  ctxName,
			ParentID:     ctxID,
			Declaration:  svc,
			Meta:         mergeMeta(ctxMeta, svc.Meta),
			Dependencies: usesEdges(svc.Uses),
			Validations:  svc.Validations,
		}
	}
}

func (reg *Registry) registerContextAssets(ctxName, ctxID string, ctxMeta Meta, ctx Context) {
	for assetName, asset := range ctx.Assets {
		id := fmt.Sprintf("asset.%s.%s", ctxName, assetName)
		reg.Resources[id] = Resource{
			ID:           id,
			Kind:         "asset",
			ContextName:  ctxName,
			ParentID:     ctxID,
			Declaration:  asset,
			Meta:         mergeMeta(ctxMeta, asset.Meta),
			Dependencies: assetEdges(asset),
			Validations:  asset.Validations,
		}
	}
}

func (reg *Registry) registerAdapters(project *Project) {
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
			Meta:         mergeMeta(project.Meta, adapter.Meta),
			Dependencies: deps,
			Validations:  adapter.Validations,
		}
	}
}

func (reg *Registry) registerProjectAssets(project *Project) {
	for name, asset := range project.Assets {
		id := fmt.Sprintf("asset.%s", name)
		reg.Resources[id] = Resource{
			ID:           id,
			Kind:         "asset",
			Declaration:  asset,
			Meta:         mergeMeta(project.Meta, asset.Meta),
			Dependencies: assetEdges(asset),
			Validations:  asset.Validations,
		}
	}
}

func (reg *Registry) validateDependencies() error {
	var errs []string
	for id, r := range reg.Resources {
		for _, dep := range r.Dependencies {
			if !reg.Has(dep.TargetID) {
				errs = append(errs, fmt.Sprintf("%s: dependency target %q not found", id, dep.TargetID))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("dangling references:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

func usesEdges(uses []string) []Edge {
	var deps []Edge
	for _, u := range uses {
		deps = append(deps, Edge{TargetID: u, Kind: "uses"})
	}
	return deps
}

func consumesEdges(consumes []string) []Edge {
	var deps []Edge
	for _, c := range consumes {
		deps = append(deps, Edge{TargetID: c, Kind: "consumes"})
	}
	return deps
}

func publishesEdges(publishes []string) []Edge {
	var deps []Edge
	for _, p := range publishes {
		deps = append(deps, Edge{TargetID: p, Kind: "publishes"})
	}
	return deps
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
