package graph

import (
	"fmt"
	"sort"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
)

type Edge struct {
	TargetID string
	Kind     string
}

type Graph struct {
	nodes   map[string]bool
	edges   map[string][]Edge
	reverse map[string][]string
}

func Build(resources map[string]cuepkg.Resource) (*Graph, error) {
	g := &Graph{
		nodes:   make(map[string]bool),
		edges:   make(map[string][]Edge),
		reverse: make(map[string][]string),
	}
	for id := range resources {
		g.nodes[id] = true
	}
	for id, r := range resources {
		for _, dep := range r.Dependencies {
			g.edges[id] = append(g.edges[id], Edge{TargetID: dep.TargetID, Kind: dep.Kind})
			g.reverse[dep.TargetID] = append(g.reverse[dep.TargetID], id)
		}
	}
	return g, nil
}

func (g *Graph) Has(id string) bool {
	return g.nodes[id]
}

func (g *Graph) TopologicalSort() ([]string, error) {
	// depCount[id] = how many dependencies this node has
	depCount := make(map[string]int)
	for id := range g.nodes {
		count := 0
		for _, e := range g.edges[id] {
			if g.nodes[e.TargetID] {
				count++
			}
		}
		depCount[id] = count
	}

	var queue []string
	for id, c := range depCount {
		if c == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)

	var result []string
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		result = append(result, node)

		for _, dependent := range g.reverse[node] {
			if !g.nodes[dependent] {
				continue
			}
			depCount[dependent]--
			if depCount[dependent] == 0 {
				queue = append(queue, dependent)
				sort.Strings(queue)
			}
		}
	}

	if len(result) != len(g.nodes) {
		return nil, fmt.Errorf("cycle detected: processed %d of %d nodes", len(result), len(g.nodes))
	}
	return result, nil
}

func (g *Graph) Waves() ([][]string, error) {
	order, err := g.TopologicalSort()
	if err != nil {
		return nil, err
	}

	depth := make(map[string]int)
	maxDepth := 0
	for _, id := range order {
		d := 0
		for _, e := range g.edges[id] {
			if g.nodes[e.TargetID] {
				if depth[e.TargetID]+1 > d {
					d = depth[e.TargetID] + 1
				}
			}
		}
		depth[id] = d
		if d > maxDepth {
			maxDepth = d
		}
	}

	waves := make([][]string, maxDepth+1)
	for id, d := range depth {
		waves[d] = append(waves[d], id)
	}
	for i := range waves {
		sort.Strings(waves[i])
	}
	return waves, nil
}

func (g *Graph) Ancestors(id string) []string {
	visited := make(map[string]bool)
	g.collectAncestors(id, visited)
	delete(visited, id)
	result := make([]string, 0, len(visited))
	for v := range visited {
		result = append(result, v)
	}
	sort.Strings(result)
	return result
}

func (g *Graph) collectAncestors(id string, visited map[string]bool) {
	if visited[id] {
		return
	}
	visited[id] = true
	for _, e := range g.edges[id] {
		if g.nodes[e.TargetID] {
			g.collectAncestors(e.TargetID, visited)
		}
	}
}

func (g *Graph) Dependents(id string) []string {
	visited := make(map[string]bool)
	g.collectDependents(id, visited)
	delete(visited, id)
	result := make([]string, 0, len(visited))
	for v := range visited {
		result = append(result, v)
	}
	sort.Strings(result)
	return result
}

func (g *Graph) collectDependents(id string, visited map[string]bool) {
	if visited[id] {
		return
	}
	visited[id] = true
	for _, dep := range g.reverse[id] {
		if g.nodes[dep] {
			g.collectDependents(dep, visited)
		}
	}
}
