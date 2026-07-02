package plan

import (
	"crypto/sha256"
	"fmt"
)

// Drift is a generated file whose on-disk content no longer matches the
// content hash recorded at commit time — an out-of-band edit.
type Drift struct {
	ResourceID string `json:"resource_id"`
	Path       string `json:"path"`
}

// DetectDrift compares every tracked generated file's stored content hash
// against the file on disk and reports mismatches. Advisory only: drift does
// not trigger regeneration (once generated, the file is the user's to
// modify), but it must be VISIBLE — silently diverged state is how manual
// edits and generation fight each other. Missing files are not drift; the
// planner already classifies those for regeneration.
func (p *Planner) DetectDrift() ([]Drift, error) {
	resources, err := p.store.ListResources()
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}

	var drift []Drift
	for _, r := range resources {
		files, err := p.store.GetGeneratedFiles(r.ID)
		if err != nil {
			return nil, fmt.Errorf("get generated files for %s: %w", r.ID, err)
		}
		for _, f := range files {
			if f.ContentHash == "" {
				continue
			}
			data, err := p.fs.ReadFile(f.Path)
			if err != nil {
				continue // missing file: handled by the regen path, not drift
			}
			if fmt.Sprintf("%x", sha256.Sum256(data)) != f.ContentHash {
				drift = append(drift, Drift{ResourceID: r.ID, Path: f.Path})
			}
		}
	}
	return drift, nil
}
