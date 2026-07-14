package spec

import (
	"context"
	"fmt"
	"sort"

	"github.com/crestenstclair/crest-spec/internal/store"
)

type ContextManifestComparison struct {
	LeftManifestID    string
	RightManifestID   string
	LeftAttemptID     string
	RightAttemptID    string
	SameContext       bool
	BudgetChanged     bool
	SelectorChanged   bool
	EstimatorChanged  bool
	StrategyChanged   bool
	TemplateChanges   map[string]ContextValueChange
	AddedSections     []ContextSectionChange
	RemovedSections   []ContextSectionChange
	ChangedSections   []ContextSectionChange
	UnchangedSections int
}

type ContextValueChange struct {
	Before string
	After  string
}

type ContextSectionChange struct {
	SourceKey string
	Changes   []string
	Before    *ContextSectionSnapshot
	After     *ContextSectionSnapshot
}

type ContextSectionSnapshot struct {
	Kind            string
	Title           string
	SourceKind      string
	SourceID        string
	SourcePath      string
	Priority        int
	Mandatory       bool
	Decision        string
	Reason          string
	OriginalHash    string
	ContentHash     string
	OriginalBytes   int
	SelectedBytes   int
	EstimatedTokens int
}

func (s *Spec) InspectContext(ctx context.Context, manifestID, attemptID string) (*store.ContextManifest, error) {
	switch {
	case manifestID != "" && attemptID != "":
		return nil, fmt.Errorf("provide either context_manifest_id or attempt_id, not both")
	case manifestID != "":
		return s.store.GetContextManifest(ctx, manifestID)
	case attemptID != "":
		return s.store.GetContextManifestByAttempt(ctx, attemptID)
	default:
		return nil, fmt.Errorf("context_manifest_id or attempt_id is required")
	}
}

func (s *Spec) ListContextAttempts(ctx context.Context, limit int) ([]store.ContextManifestSummary, error) {
	return s.store.ListContextManifestSummaries(ctx, limit)
}

func (s *Spec) CompareContexts(ctx context.Context, leftManifestID, rightManifestID string) (*ContextManifestComparison, error) {
	if leftManifestID == "" || rightManifestID == "" {
		return nil, fmt.Errorf("left_context_manifest_id and right_context_manifest_id are required")
	}
	left, err := s.store.GetContextManifest(ctx, leftManifestID)
	if err != nil {
		return nil, fmt.Errorf("left context manifest: %w", err)
	}
	right, err := s.store.GetContextManifest(ctx, rightManifestID)
	if err != nil {
		return nil, fmt.Errorf("right context manifest: %w", err)
	}
	comparison := &ContextManifestComparison{
		LeftManifestID: left.ID, RightManifestID: right.ID,
		LeftAttemptID: left.Attempt.ID, RightAttemptID: right.Attempt.ID,
		SameContext:      left.ContextHash == right.ContextHash,
		BudgetChanged:    left.BudgetTokens != right.BudgetTokens,
		SelectorChanged:  left.SelectorVersion != right.SelectorVersion,
		EstimatorChanged: left.EstimatorVersion != right.EstimatorVersion,
		StrategyChanged:  left.SelectionStrategy != right.SelectionStrategy,
		TemplateChanges:  compareTemplateHashes(left.TemplateHashes, right.TemplateHashes),
	}

	leftSections := contextSectionsBySource(left.Sections)
	rightSections := contextSectionsBySource(right.Sections)
	keys := make(map[string]bool, len(leftSections)+len(rightSections))
	for key := range leftSections {
		keys[key] = true
	}
	for key := range rightSections {
		keys[key] = true
	}
	orderedKeys := make([]string, 0, len(keys))
	for key := range keys {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)
	for _, key := range orderedKeys {
		before, beforeExists := leftSections[key]
		after, afterExists := rightSections[key]
		switch {
		case !beforeExists:
			afterSnapshot := contextSectionSnapshot(after)
			comparison.AddedSections = append(comparison.AddedSections, ContextSectionChange{SourceKey: contextSectionDisplayKey(after), Changes: []string{"added"}, After: &afterSnapshot})
		case !afterExists:
			beforeSnapshot := contextSectionSnapshot(before)
			comparison.RemovedSections = append(comparison.RemovedSections, ContextSectionChange{SourceKey: contextSectionDisplayKey(before), Changes: []string{"removed"}, Before: &beforeSnapshot})
		default:
			changes := contextSectionChanges(before, after)
			if len(changes) == 0 {
				comparison.UnchangedSections++
				continue
			}
			beforeSnapshot, afterSnapshot := contextSectionSnapshot(before), contextSectionSnapshot(after)
			comparison.ChangedSections = append(comparison.ChangedSections, ContextSectionChange{
				SourceKey: contextSectionDisplayKey(after), Changes: changes, Before: &beforeSnapshot, After: &afterSnapshot,
			})
		}
	}
	return comparison, nil
}

func contextSectionDisplayKey(section store.ContextManifestSection) string {
	key := section.Kind + " | " + section.SourceKind + ":" + section.SourceID
	if section.SourcePath != "" {
		key += " | " + section.SourcePath
	}
	return key
}

func compareTemplateHashes(left, right map[string]string) map[string]ContextValueChange {
	keys := make(map[string]bool, len(left)+len(right))
	for key := range left {
		keys[key] = true
	}
	for key := range right {
		keys[key] = true
	}
	changes := make(map[string]ContextValueChange)
	for key := range keys {
		if left[key] != right[key] {
			changes[key] = ContextValueChange{Before: left[key], After: right[key]}
		}
	}
	return changes
}

func contextSectionsBySource(sections []store.ContextManifestSection) map[string]store.ContextManifestSection {
	result := make(map[string]store.ContextManifestSection, len(sections))
	for _, section := range sections {
		key := section.Kind + "\x00" + section.SourceKind + "\x00" + section.SourceID + "\x00" + section.SourcePath
		result[key] = section
	}
	return result
}

func contextSectionSnapshot(section store.ContextManifestSection) ContextSectionSnapshot {
	return ContextSectionSnapshot{
		Kind: section.Kind, Title: section.Title, SourceKind: section.SourceKind,
		SourceID: section.SourceID, SourcePath: section.SourcePath, Priority: section.Priority,
		Mandatory: section.Mandatory, Decision: section.Decision, Reason: section.Reason,
		OriginalHash: section.OriginalHash, ContentHash: section.ContentHash,
		OriginalBytes: section.OriginalBytes, SelectedBytes: section.SelectedBytes,
		EstimatedTokens: section.EstimatedTokens,
	}
}

func contextSectionChanges(before, after store.ContextManifestSection) []string {
	var changes []string
	if before.Decision != after.Decision {
		changes = append(changes, "decision")
	}
	if before.Reason != after.Reason {
		changes = append(changes, "selection_reason")
	}
	if before.OriginalHash != after.OriginalHash {
		changes = append(changes, "source_content")
	}
	if before.ContentHash != after.ContentHash {
		changes = append(changes, "selected_content")
	}
	if before.Priority != after.Priority {
		changes = append(changes, "priority")
	}
	if before.Mandatory != after.Mandatory {
		changes = append(changes, "mandatory")
	}
	if before.OriginalBytes != after.OriginalBytes || before.SelectedBytes != after.SelectedBytes || before.EstimatedTokens != after.EstimatedTokens {
		changes = append(changes, "allocation")
	}
	return changes
}
