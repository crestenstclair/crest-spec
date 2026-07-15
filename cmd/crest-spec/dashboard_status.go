package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	cserrors "github.com/crestenstclair/crest-spec/internal/errors"
	"github.com/crestenstclair/crest-spec/internal/store"
)

type statusRepository interface {
	GetActiveSession() (*store.Session, error)
	ListApplies(limit int) ([]store.Apply, error)
	ListSessionResources(sessionID string) ([]store.SessionResource, error)
	ListResources() ([]store.Resource, error)
	GetLock() (*store.Lock, error)
	ListApplyActions(applyID string) ([]store.ApplyAction, error)
}

type resourceStateCounts struct {
	Pending    int `json:"pending"`
	Dispatched int `json:"dispatched"`
	Committed  int `json:"committed"`
	Rejected   int `json:"rejected"`
	Skipped    int `json:"skipped"`
	Errored    int `json:"errored"`
	Blocked    int `json:"blocked"`
	Total      int `json:"total"`
}

type liveStatusSnapshot struct {
	Session          *store.Session           `json:"session"`
	LatestApply      *store.Apply             `json:"latest_apply,omitempty"`
	SessionResources *[]store.SessionResource `json:"session_resources,omitempty"`
}

type statusResponse struct {
	Resources      int                  `json:"resources"`
	Lock           *store.Lock          `json:"lock"`
	Session        *store.Session       `json:"session"`
	LatestApply    *store.Apply         `json:"latest_apply"`
	SessionActions []store.ApplyAction  `json:"session_actions,omitempty"`
	ResourceStates *resourceStateCounts `json:"resource_states,omitempty"`
}

func (d *dashboard) loadLiveStatusSnapshot() (liveStatusSnapshot, error) {
	return readLiveStatusSnapshot(d.store)
}

func readLiveStatusSnapshot(repository statusRepository) (liveStatusSnapshot, error) {
	var snapshot liveStatusSnapshot

	session, err := repository.GetActiveSession()
	if err != nil && !errors.Is(err, cserrors.ErrNotFound) {
		return snapshot, fmt.Errorf("get active session: %w", err)
	}
	snapshot.Session = session

	applies, err := repository.ListApplies(1)
	if err != nil {
		return snapshot, fmt.Errorf("list applies: %w", err)
	}
	if len(applies) > 0 {
		snapshot.LatestApply = &applies[0]
	}

	if session != nil {
		resources, err := repository.ListSessionResources(session.ID)
		if err != nil {
			return snapshot, fmt.Errorf("list resources for session %q: %w", session.ID, err)
		}
		snapshot.SessionResources = &resources
	}

	return snapshot, nil
}

func (d *dashboard) loadStatusResponse() (statusResponse, error) {
	return readStatusResponse(d.store)
}

func readStatusResponse(repository statusRepository) (statusResponse, error) {
	live, err := readLiveStatusSnapshot(repository)
	if err != nil {
		return statusResponse{}, err
	}

	resources, err := repository.ListResources()
	if err != nil {
		return statusResponse{}, fmt.Errorf("list resources: %w", err)
	}
	lock, err := repository.GetLock()
	if err != nil && !errors.Is(err, cserrors.ErrNotFound) {
		return statusResponse{}, fmt.Errorf("get apply lock: %w", err)
	}

	response := statusResponse{
		Resources:   len(resources),
		Lock:        lock,
		Session:     live.Session,
		LatestApply: live.LatestApply,
	}
	if live.Session != nil && live.Session.ApplyID != "" {
		actions, err := repository.ListApplyActions(live.Session.ApplyID)
		if err != nil {
			return statusResponse{}, fmt.Errorf("list actions for apply %q: %w", live.Session.ApplyID, err)
		}
		response.SessionActions = actions
	}
	if live.SessionResources != nil && len(*live.SessionResources) > 0 {
		response.ResourceStates = countResourceStates(*live.SessionResources)
	}

	return response, nil
}

func countResourceStates(resources []store.SessionResource) *resourceStateCounts {
	counts := &resourceStateCounts{Total: len(resources)}
	for _, resource := range resources {
		switch resource.State {
		case "pending":
			counts.Pending++
		case "dispatched":
			counts.Dispatched++
		case "committed":
			counts.Committed++
		case "rejected":
			counts.Rejected++
		case "skipped":
			counts.Skipped++
		case "errored":
			counts.Errored++
		case "blocked":
			counts.Blocked++
		}
	}
	return counts
}

func (d *dashboard) handleStatus(w http.ResponseWriter, _ *http.Request) {
	response, err := d.loadStatusResponse()
	if err != nil {
		d.log.Error().Err(err).Str("endpoint", "/api/status").Msg("dashboard status query failed")
		d.writeError(w, http.StatusInternalServerError, "could not load dashboard status")
		return
	}
	d.writeJSON(w, response)
}

func (d *dashboard) handleLiveStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := w.(http.Flusher); !ok {
		d.writeError(w, 500, "streaming not supported")
		return
	}

	initial, err := d.loadLiveStatusSnapshot()
	if err != nil {
		d.log.Error().Err(err).Str("endpoint", "/api/live-status").Msg("dashboard live status query failed")
		d.writeError(w, http.StatusInternalServerError, "could not load dashboard status")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	if err := d.streamLiveStatus(w, r, ticker.C, initial); err != nil {
		d.log.Warn().Err(err).Str("endpoint", "/api/live-status").Msg("dashboard live status stream ended")
	}
}

func (d *dashboard) streamLiveStatus(w http.ResponseWriter, r *http.Request, ticks <-chan time.Time, initial liveStatusSnapshot) error {
	var lastPayload []byte
	if _, err := writeLiveStatusUpdate(w, initial, &lastPayload); err != nil {
		return fmt.Errorf("write initial live status: %w", err)
	}

	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-ticks:
			snapshot, err := d.loadLiveStatusSnapshot()
			if err != nil {
				return err
			}
			if _, err := writeLiveStatusUpdate(w, snapshot, &lastPayload); err != nil {
				return err
			}
		}
	}
}

func writeLiveStatusUpdate(w http.ResponseWriter, snapshot liveStatusSnapshot, lastPayload *[]byte) (bool, error) {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return false, fmt.Errorf("encode live status: %w", err)
	}
	if bytes.Equal(payload, *lastPayload) {
		return false, nil
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return false, fmt.Errorf("write live status: %w", err)
	}
	if err := http.NewResponseController(w).Flush(); err != nil {
		return false, fmt.Errorf("flush live status: %w", err)
	}
	*lastPayload = append((*lastPayload)[:0], payload...)
	return true, nil
}
