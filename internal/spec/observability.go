package spec

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/crestenstclair/crest-spec/internal/store"
)

// ---------------------------------------------------------------------------
// Session-level status — overview of an active session
// ---------------------------------------------------------------------------

// SessionStatusResult is the session-level overview returned by SessionStatus.
type SessionStatusResult struct {
	SessionID     string            `json:"session_id"`
	ApplyID       string            `json:"apply_id"`
	CurrentWave   int               `json:"current_wave"`
	TotalWaves    int               `json:"total_waves"`
	WaveSummaries []WaveSummary     `json:"wave_summaries"`
	Concurrency   ConcurrencyStatus `json:"concurrency"`
}

// ConcurrencyStatus reports the engine's semaphore utilization.
type ConcurrencyStatus struct {
	Active int `json:"active"`
	Max    int `json:"max"`
	Queued int `json:"queued"`
}

// WaveSummary counts resource states within a single wave.
type WaveSummary struct {
	WaveIndex int `json:"wave_index"`
	Total     int `json:"total"`
	Committed int `json:"committed"`
	Rejected  int `json:"rejected"`
	Errored   int `json:"errored"`
	Pending   int `json:"pending"`
}

// SessionStatus returns a session-level overview: current wave, total waves,
// and per-wave resource state counts.
func (s *Spec) SessionStatus(ctx context.Context, sessionID string) (*SessionStatusResult, error) {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	var waves [][]string
	if err := json.Unmarshal([]byte(sess.WavesJSON), &waves); err != nil {
		return nil, fmt.Errorf("unmarshal waves: %w", err)
	}

	allResources, err := s.store.ListSessionResources(sessionID)
	if err != nil {
		return nil, fmt.Errorf("list session resources: %w", err)
	}

	summaries := buildWaveSummaries(waves, allResources)

	active := s.engine.ActiveCount()
	max := s.engine.MaxConcurrency()
	dispatched := countDispatched(allResources)
	queued := dispatched - active
	if queued < 0 {
		queued = 0
	}

	return &SessionStatusResult{
		SessionID:     sessionID,
		ApplyID:       sess.ApplyID,
		CurrentWave:   sess.CurrentWave,
		TotalWaves:    len(waves),
		WaveSummaries: summaries,
		Concurrency: ConcurrencyStatus{
			Active: active,
			Max:    max,
			Queued: queued,
		},
	}, nil
}

// buildWaveSummaries groups session resources by wave and counts states.
func buildWaveSummaries(waves [][]string, allResources []store.SessionResource) []WaveSummary {
	stateByResource := make(map[string]string, len(allResources))
	for _, r := range allResources {
		stateByResource[r.ResourceID] = r.State
	}

	summaries := make([]WaveSummary, len(waves))
	for i, wave := range waves {
		summary := WaveSummary{WaveIndex: i, Total: len(wave)}
		for _, id := range wave {
			state := ResourceState(stateByResource[id])
			switch {
			case state == StateCommitted:
				summary.Committed++
			case state == StateRejected:
				summary.Rejected++
			case state == StateErrored || state == StateTimedOut || state == StateBlocked:
				summary.Errored++
			default:
				summary.Pending++
			}
		}
		summaries[i] = summary
	}
	return summaries
}

func countDispatched(resources []store.SessionResource) int {
	n := 0
	for _, r := range resources {
		if r.State == string(StateDispatched) {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// Wave-level status — detailed view of resources in a single wave
// ---------------------------------------------------------------------------

// WaveStatusResult is the detailed per-resource view of a single wave.
type WaveStatusResult struct {
	WaveIndex int              `json:"wave_index"`
	Resources []ResourceDetail `json:"resources"`
}

// ResourceDetail holds per-resource observability data within a wave.
type ResourceDetail struct {
	ResourceID   string `json:"resource_id"`
	State        string `json:"state"`
	Phase        string `json:"phase,omitempty"`
	Attempts     int    `json:"attempts"`
	MaxRetries   int    `json:"max_retries"`
	LastError    string `json:"last_error,omitempty"`
	DurationMS   int64  `json:"duration_ms,omitempty"`
	ElapsedMS    int64  `json:"elapsed_ms,omitempty"`
	DispatchedAt string `json:"dispatched_at,omitempty"`
}

// WaveStatus returns detailed resource-level state for a single wave.
func (s *Spec) WaveStatus(ctx context.Context, sessionID string, waveIndex int) (*WaveStatusResult, error) {
	resources, err := s.store.ListSessionResourcesByWave(sessionID, waveIndex)
	if err != nil {
		return nil, fmt.Errorf("list wave resources: %w", err)
	}

	details := make([]ResourceDetail, len(resources))
	for i, r := range resources {
		d := ResourceDetail{
			ResourceID: r.ResourceID,
			State:      r.State,
			Phase:      r.Phase,
			Attempts:   r.Attempts,
			MaxRetries: r.MaxRetries,
			LastError:  r.LastError,
		}
		if !r.DispatchedAt.IsZero() {
			d.DispatchedAt = r.DispatchedAt.Format(time.RFC3339)
			if r.State == "dispatched" {
				d.ElapsedMS = time.Since(r.DispatchedAt).Milliseconds()
			}
		}
		details[i] = d
	}

	return &WaveStatusResult{
		WaveIndex: waveIndex,
		Resources: details,
	}, nil
}
