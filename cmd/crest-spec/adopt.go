package main

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/execution"
	specmod "github.com/crestenstclair/crest-spec/internal/spec"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// cmdAdopt seeds the state DB from existing on-disk files: it begins a session,
// then for every planned resource commits with NO new file content. Commit's
// disk path validates the on-disk files against the resource's own validations
// and settles its effective hash — establishing the current code as the
// baseline WITHOUT regenerating anything. After adoption the planner sees every
// resource as settled, so subsequent spec edits regenerate only what changed.
//
// Run it from the project root with CREST_SPEC_SPEC_DIR pointed at the spec dir,
// e.g.:
//
//	cd ~/workspace/crest-synth
//	CREST_SPEC_SPEC_DIR=$PWD/spec crest-spec adopt
func cmdAdopt() {
	cfg, err := config.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	st, err := store.New(dbPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "store: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	sp := specmod.New(st, specmod.OSFileSystem{}, cfg)
	ctx := context.Background()

	begin, err := sp.Begin(ctx, specmod.BeginOpts{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "begin: %v\n", err)
		os.Exit(1)
	}
	total := 0
	for _, w := range begin.Waves {
		total += len(w)
	}
	if total == 0 {
		fmt.Printf("adopt: spec=%s — nothing to adopt; all resources already settled\n", cfg.SpecDir)
		return
	}
	fmt.Printf("adopt: spec=%s session=%s resources=%d waves=%d\n", cfg.SpecDir, begin.SessionID, total, len(begin.Waves))

	settled, failed := 0, 0
	attempted := map[string]bool{}
	for {
		next, err := sp.Next(ctx, begin.SessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "next: %v\n", err)
			os.Exit(1)
		}
		if next.Done {
			break
		}
		progressed := false
		for _, r := range next.Resources {
			if attempted[r.ResourceID] {
				continue
			}
			attempted[r.ResourceID] = true
			progressed = true
			// Claim on-disk files by naming convention so adopted resources
			// keep UPDATE-mode iteration; unclaimable resources adopt without
			// file tracking and are reported below.
			claims := sp.DiscoverClaims(ctx, r.ResourceID)
			if len(claims) == 0 {
				fmt.Printf("  (no file claim for %s — adopted without file tracking)\n", r.ResourceID)
			}
			prepared, err := sp.PrepareContext(ctx, specmod.ContextOptions{SessionID: begin.SessionID, ResourceID: r.ResourceID})
			if err == nil {
				_, err = sp.StartExecution(ctx, specmod.ExecutionStartOptions{
					AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
					IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
					HostName: "crest-spec-adopt", HostVersion: "built-in", Provider: "local", Model: cfg.GenerateModel,
					Tools:          []store.ExecutionTool{{Name: "filesystem", Permission: "preserve-discovered-files"}},
					TemplateHashes: prepared.TemplateHashes, SystemInstructions: prepared.SystemPrompt,
				})
			}
			var res *specmod.CommitResult
			if err == nil {
				res, err = sp.CommitAttempt(ctx, prepared.AttemptID, claims, "adopted from existing on-disk code", specmod.CommitMetadata{})
			}
			switch {
			case err != nil:
				fmt.Printf("  ERROR    %s: %v\n", r.ResourceID, err)
				_ = sp.Skip(ctx, begin.SessionID, r.ResourceID, "adopt: commit error")
				failed++
			case res.Committed:
				fmt.Printf("  settled  %s\n", r.ResourceID)
				settled++
			default:
				fmt.Printf("  REJECTED %s: %s\n", r.ResourceID, firstFail(res.Validations))
				_ = sp.Skip(ctx, begin.SessionID, r.ResourceID, "adopt: on-disk validation failed")
				failed++
			}
		}
		// Guard against a wave that returns only already-attempted resources
		// (would otherwise loop forever).
		if !progressed {
			fmt.Fprintln(os.Stderr, "adopt: no progress on this wave; stopping")
			break
		}
	}

	fin, err := sp.Finish(ctx, begin.SessionID, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "finish: %v\n", err)
	}
	fmt.Printf("adopt complete: settled=%d failed=%d", settled, failed)
	if fin != nil {
		fmt.Printf(" (finish: committed=%d skipped=%d errored=%d)", fin.Committed, fin.Skipped, fin.Errored)
	}
	fmt.Println()
	if failed > 0 {
		os.Exit(1)
	}
}

func firstFail(results []specmod.ValidationResult) string {
	for _, v := range results {
		if !v.Passed {
			return v.Message
		}
	}
	return ""
}
