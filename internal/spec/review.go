package spec

import (
	"context"
	"fmt"
	"strings"

	"github.com/crestenstclair/crest-spec/internal/engine"
	promptpkg "github.com/crestenstclair/crest-spec/internal/prompt"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// DeepReviewOpts holds the parameters for a deep SOLID/DI/clean code review.
type DeepReviewOpts struct {
	SessionID string
	Target    string
}

// DeepReviewResult holds the aggregated findings from a deep review.
type DeepReviewResult struct {
	ResourcesReviewed int            `json:"resources_reviewed"`
	Findings          []ReviewOutput `json:"findings"`
	Summary           string         `json:"summary"`
}

// DeepReview performs a comprehensive SOLID/DI/clean code review of generated
// files. When target is specified, only that resource is reviewed; otherwise
// all committed resources with generated files are reviewed.
func (s *Spec) DeepReview(ctx context.Context, opts DeepReviewOpts) (*DeepReviewResult, error) {
	resourceIDs, err := s.resolveReviewTargets(opts)
	if err != nil {
		return nil, err
	}

	if len(resourceIDs) == 0 {
		return &DeepReviewResult{Summary: "No resources with generated files found."}, nil
	}

	result := &DeepReviewResult{}
	for _, resourceID := range resourceIDs {
		output, err := s.reviewResource(ctx, resourceID)
		if err != nil {
			continue
		}
		result.Findings = append(result.Findings, *output)
		result.ResourcesReviewed++
	}

	result.Summary = summarizeFindings(result.Findings)
	return result, nil
}

// resolveReviewTargets determines which resources to review based on opts.
func (s *Spec) resolveReviewTargets(opts DeepReviewOpts) ([]string, error) {
	if opts.Target != "" {
		files, err := s.store.GetGeneratedFiles(opts.Target)
		if err != nil {
			return nil, fmt.Errorf("get files for %s: %w", opts.Target, err)
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("no generated files for resource: %s", opts.Target)
		}
		return []string{opts.Target}, nil
	}

	resources, err := s.store.ListResources()
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}

	var ids []string
	for _, r := range resources {
		files, _ := s.store.GetGeneratedFiles(r.ID)
		if len(files) > 0 {
			ids = append(ids, r.ID)
		}
	}
	return ids, nil
}

// reviewResource reads generated files for a resource and dispatches a deep
// review via the LLM engine.
func (s *Spec) reviewResource(ctx context.Context, resourceID string) (*ReviewOutput, error) {
	files, err := s.store.GetGeneratedFiles(resourceID)
	if err != nil {
		return nil, fmt.Errorf("get files: %w", err)
	}

	code := buildCodeFromFiles(s.fs, files, resourceID)
	if code == "" {
		return nil, fmt.Errorf("no file content for resource: %s", resourceID)
	}

	prompt := promptpkg.BuildDeepReviewPrompt(code)

	res, err := s.engine.CodeReview(ctx, engine.CodeReviewOpts{
		Prompt: prompt,
	})
	if err != nil {
		return nil, fmt.Errorf("code review for %s: %w", resourceID, err)
	}

	if ro := parseReviewOutput(res.Output); ro != nil {
		return ro, nil
	}

	// Fallback: treat unstructured output as a single finding.
	passed := !strings.Contains(strings.ToUpper(res.Output), "FAIL")
	return &ReviewOutput{
		Passed:  passed,
		Summary: res.Output,
	}, nil
}

// buildCodeFromFiles reads generated files from disk and concatenates them
// with path annotations for the reviewer.
func buildCodeFromFiles(fs fileSystem, files []store.GeneratedFile, resourceID string) string {
	var b strings.Builder
	for _, f := range files {
		data, err := fs.ReadFile(f.Path)
		if err != nil {
			continue
		}
		b.WriteString(fmt.Sprintf("// path: %s (resource: %s)\n", f.Path, resourceID))
		b.WriteString(string(data))
		b.WriteString("\n\n")
	}
	return b.String()
}

func summarizeFindings(findings []ReviewOutput) string {
	total := 0
	critical := 0
	major := 0
	allPassed := true

	for _, f := range findings {
		if !f.Passed {
			allPassed = false
		}
		for _, finding := range f.Findings {
			total++
			switch finding.Severity {
			case "critical":
				critical++
			case "major":
				major++
			}
		}
	}

	if allPassed && total == 0 {
		return "All resources passed deep review with no findings."
	}

	return fmt.Sprintf(
		"Found %d issues (%d critical, %d major) across %d resources.",
		total, critical, major, len(findings),
	)
}
