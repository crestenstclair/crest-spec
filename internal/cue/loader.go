package cue

import (
	"encoding/json"
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
)

func Load(specDir string) (*Project, error) {
	ctx := cuecontext.New()

	// Try loading with "_" (anonymous/no-package files) first,
	// fall back to default (any named package) if that fails.
	cfg := &load.Config{
		Dir:     specDir,
		Package: "_",
	}

	instances := load.Instances(nil, cfg)
	if len(instances) == 0 || instances[0].Err != nil {
		cfg = &load.Config{Dir: specDir}
		instances = load.Instances(nil, cfg)
	}

	if len(instances) == 0 {
		return nil, fmt.Errorf("no CUE files found in %s", specDir)
	}

	inst := instances[0]
	if inst.Err != nil {
		return nil, fmt.Errorf("load error: %w", inst.Err)
	}

	val := ctx.BuildInstance(inst)
	if val.Err() != nil {
		return nil, fmt.Errorf("build error: %w", val.Err())
	}

	projectVal := val.LookupPath(cue.ParsePath("project"))
	if projectVal.Err() != nil {
		return nil, fmt.Errorf("no 'project' field: %w", projectVal.Err())
	}

	jsonBytes, err := projectVal.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("marshal JSON: %w", err)
	}

	var p Project
	if err := json.Unmarshal(jsonBytes, &p); err != nil {
		return nil, fmt.Errorf("unmarshal project: %w", err)
	}

	return &p, nil
}
