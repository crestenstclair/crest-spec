package graph

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
)

func ComputeEffectiveHashes(
	resources map[string]cuepkg.Resource,
	g *Graph,
	model string,
	mode string,
) map[string]string {
	order, err := g.TopologicalSort()
	if err != nil {
		order = make([]string, 0, len(resources))
		for id := range resources {
			order = append(order, id)
		}
		sort.Strings(order)
	}

	hashes := make(map[string]string, len(resources))

	for _, id := range order {
		r, ok := resources[id]
		if !ok {
			continue
		}

		declJSON, _ := json.Marshal(r.Declaration)

		var depHashes []string
		for _, e := range r.Dependencies {
			if h, ok := hashes[e.TargetID]; ok {
				depHashes = append(depHashes, h)
			}
		}
		sort.Strings(depHashes)

		h := sha256.New()
		h.Write(declJSON)
		for _, dh := range depHashes {
			h.Write([]byte(dh))
		}
		h.Write([]byte(model))
		h.Write([]byte(mode))

		hashes[id] = fmt.Sprintf("%x", h.Sum(nil))
	}

	return hashes
}
