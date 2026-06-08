package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"github.com/crestenstclair/crest-spec/internal/config"
	specmod "github.com/crestenstclair/crest-spec/internal/spec"
	"github.com/crestenstclair/crest-spec/internal/store"
)

//go:embed static
var staticFiles embed.FS

func cmdDashboard(flags cliFlags) {
	addr := flags.addr
	if addr == "" {
		addr = ":8080"
	}

	cfg, err := config.New()
	if err != nil {
		fatal(fmt.Errorf("config: %w", err))
	}

	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()

	dbPath := dbPath()
	st, err := store.New(dbPath)
	if err != nil {
		fatal(fmt.Errorf("store: %w", err))
	}
	defer st.Close()

	sp := specmod.New(nil, st, specmod.OSFileSystem{}, cfg)

	d := &dashboard{store: st, spec: sp, cfg: cfg, log: log}

	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("GET /api/status", d.handleStatus)
	mux.HandleFunc("GET /api/plan", d.handlePlan)
	mux.HandleFunc("GET /api/resources", d.handleResources)
	mux.HandleFunc("GET /api/applies", d.handleApplies)
	mux.HandleFunc("GET /api/applies/{id}/actions", d.handleApplyActions)
	mux.HandleFunc("GET /api/generations/{resourceID}", d.handleGenerations)
	mux.HandleFunc("GET /api/jobs", d.handleJobs)
	mux.HandleFunc("GET /api/notes/{applyID}", d.handleNotes)
	mux.HandleFunc("GET /api/session-resources/{sessionID}", d.handleSessionResources)
	mux.HandleFunc("GET /api/session-resources/{sessionID}/wave/{waveIndex}", d.handleSessionResourcesByWave)
	mux.HandleFunc("GET /api/invariant-checks/{applyID}", d.handleInvariantChecks)
	mux.HandleFunc("GET /api/generations-recent", d.handleRecentGenerations)
	mux.HandleFunc("GET /api/agent-events/{resourceID}", d.handleAgentEvents)
	mux.HandleFunc("GET /api/agent-events-recent", d.handleRecentAgentEvents)
	mux.HandleFunc("GET /api/agent-events-stream/{resourceID}", d.handleAgentEventsStream)
	mux.HandleFunc("GET /api/live-status", d.handleLiveStatus)
	mux.HandleFunc("GET /api/learnings", d.handleLearnings)

	// Serve embedded static files
	staticFS, _ := fs.Sub(staticFiles, "static")
	mux.Handle("GET /", http.FileServer(http.FS(staticFS)))

	srv := &http.Server{Addr: addr, Handler: mux}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	log.Info().Str("addr", addr).Str("db", dbPath).Msg("dashboard ready")
	fmt.Fprintf(os.Stderr, "\n  Dashboard: http://localhost%s\n\n", addr)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatal(err)
	}
}

type dashboard struct {
	store *store.Store
	spec  *specmod.Spec
	cfg   *config.Config
	log   zerolog.Logger
}

func (d *dashboard) writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (d *dashboard) writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (d *dashboard) handleStatus(w http.ResponseWriter, r *http.Request) {
	resources, _ := d.store.ListResources()
	lock, _ := d.store.GetLock()
	session, _ := d.store.GetActiveSession()
	applies, _ := d.store.ListApplies(1)
	jobs, _ := d.store.ListJobs(50)

	runningJobs := 0
	for _, j := range jobs {
		if j.Status == "running" {
			runningJobs++
		}
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

	type statusResp struct {
		Resources       int                  `json:"resources"`
		Lock            *store.Lock          `json:"lock"`
		Session         *store.Session       `json:"session"`
		LatestApply     *store.Apply         `json:"latest_apply"`
		RunningJobs     int                  `json:"running_jobs"`
		SessionActions  []store.ApplyAction  `json:"session_actions,omitempty"`
		ResourceStates  *resourceStateCounts `json:"resource_states,omitempty"`
	}

	resp := statusResp{
		Resources:   len(resources),
		Lock:        lock,
		Session:     session,
		RunningJobs: runningJobs,
	}
	if len(applies) > 0 {
		resp.LatestApply = &applies[0]
	}
	if session != nil && session.ApplyID != "" {
		actions, _ := d.store.ListApplyActions(session.ApplyID)
		resp.SessionActions = actions
	}
	if session != nil {
		sessResources, _ := d.store.ListSessionResources(session.ID)
		if len(sessResources) > 0 {
			counts := &resourceStateCounts{Total: len(sessResources)}
			for _, sr := range sessResources {
				switch sr.State {
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
			resp.ResourceStates = counts
		}
	}

	d.writeJSON(w, resp)
}

func (d *dashboard) handlePlan(w http.ResponseWriter, r *http.Request) {
	result, err := d.spec.Plan(r.Context())
	if err != nil {
		d.writeError(w, 500, err.Error())
		return
	}

	type planResource struct {
		ResourceID string `json:"resource_id"`
		Kind       string `json:"kind"`
		Reason     string `json:"reason"`
		WaveIndex  int    `json:"wave_index"`
	}

	waveMap := make(map[string]int)
	for i, wave := range result.Waves {
		for _, id := range wave {
			waveMap[id] = i
		}
	}

	var resources []planResource
	for _, a := range result.Actions {
		resources = append(resources, planResource{
			ResourceID: a.ResourceID,
			Kind:       string(a.Kind),
			Reason:     a.Reason,
			WaveIndex:  waveMap[a.ResourceID],
		})
	}

	type planResp struct {
		Actions    []planResource `json:"actions"`
		Waves      [][]string     `json:"waves"`
		TotalCount int            `json:"total_count"`
		WaveCount  int            `json:"wave_count"`
	}

	d.writeJSON(w, planResp{
		Actions:    resources,
		Waves:      result.Waves,
		TotalCount: len(resources),
		WaveCount:  len(result.Waves),
	})
}

func (d *dashboard) handleResources(w http.ResponseWriter, r *http.Request) {
	resources, err := d.store.ListResources()
	if err != nil {
		d.writeError(w, 500, err.Error())
		return
	}

	type resourceResp struct {
		store.Resource
		Files []store.GeneratedFile `json:"files"`
	}

	var resp []resourceResp
	for _, res := range resources {
		files, _ := d.store.GetGeneratedFiles(res.ID)
		resp = append(resp, resourceResp{Resource: res, Files: files})
	}

	d.writeJSON(w, resp)
}

func (d *dashboard) handleApplies(w http.ResponseWriter, r *http.Request) {
	applies, err := d.store.ListApplies(20)
	if err != nil {
		d.writeError(w, 500, err.Error())
		return
	}
	d.writeJSON(w, applies)
}

func (d *dashboard) handleApplyActions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	actions, err := d.store.ListApplyActions(id)
	if err != nil {
		d.writeError(w, 500, err.Error())
		return
	}
	d.writeJSON(w, actions)
}

func (d *dashboard) handleGenerations(w http.ResponseWriter, r *http.Request) {
	resourceID := r.PathValue("resourceID")
	// URL-decode path segments with slashes
	resourceID = strings.ReplaceAll(resourceID, "%2F", "/")
	gens, err := d.store.ListGenerations(resourceID, 20)
	if err != nil {
		d.writeError(w, 500, err.Error())
		return
	}
	d.writeJSON(w, gens)
}

func (d *dashboard) handleJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := d.store.ListJobs(50)
	if err != nil {
		d.writeError(w, 500, err.Error())
		return
	}
	d.writeJSON(w, jobs)
}

func (d *dashboard) handleNotes(w http.ResponseWriter, r *http.Request) {
	applyID := r.PathValue("applyID")
	notes, err := d.store.ListNotes(applyID)
	if err != nil {
		d.writeError(w, 500, err.Error())
		return
	}
	d.writeJSON(w, notes)
}

func (d *dashboard) handleSessionResources(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionID")
	resources, err := d.store.ListSessionResources(sessionID)
	if err != nil {
		d.writeError(w, 500, err.Error())
		return
	}
	d.writeJSON(w, resources)
}

func (d *dashboard) handleSessionResourcesByWave(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionID")
	waveStr := r.PathValue("waveIndex")
	waveIndex, err := strconv.Atoi(waveStr)
	if err != nil {
		d.writeError(w, 400, "invalid wave index")
		return
	}
	resources, err := d.store.ListSessionResourcesByWave(sessionID, waveIndex)
	if err != nil {
		d.writeError(w, 500, err.Error())
		return
	}
	d.writeJSON(w, resources)
}

func (d *dashboard) handleInvariantChecks(w http.ResponseWriter, r *http.Request) {
	applyID := r.PathValue("applyID")
	checks, err := d.store.ListInvariantChecks(applyID)
	if err != nil {
		d.writeError(w, 500, err.Error())
		return
	}
	d.writeJSON(w, checks)
}

func (d *dashboard) handleRecentGenerations(w http.ResponseWriter, r *http.Request) {
	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	query := fmt.Sprintf(
		`SELECT id, apply_id, resource_id, model, outcome, rejection_reason, `+
			`retry_count, duration_ms, input_tokens, output_tokens, cost_usd, created_at `+
			`FROM generations ORDER BY created_at DESC LIMIT %d`, limit,
	)
	rows, err := d.store.ReadOnlyQuery(query)
	if err != nil {
		d.writeError(w, 500, err.Error())
		return
	}
	d.writeJSON(w, rows)
}

func (d *dashboard) handleAgentEvents(w http.ResponseWriter, r *http.Request) {
	resourceID := r.PathValue("resourceID")
	resourceID = strings.ReplaceAll(resourceID, "%2F", "/")
	events, err := d.store.ListAgentEventsByResource(resourceID)
	if err != nil {
		d.writeError(w, 500, err.Error())
		return
	}
	d.writeJSON(w, events)
}

func (d *dashboard) handleRecentAgentEvents(w http.ResponseWriter, r *http.Request) {
	limitStr := r.URL.Query().Get("limit")
	limit := 200
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	events, err := d.store.ListRecentAgentEvents(limit)
	if err != nil {
		d.writeError(w, 500, err.Error())
		return
	}
	d.writeJSON(w, events)
}

func (d *dashboard) handleAgentEventsStream(w http.ResponseWriter, r *http.Request) {
	resourceID := r.PathValue("resourceID")
	resourceID = strings.ReplaceAll(resourceID, "%2F", "/")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		d.writeError(w, 500, "streaming not supported")
		return
	}

	ctx := r.Context()
	var lastCount int

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	sendUpdate := func() {
		events, _ := d.store.ListAgentEventsByResource(resourceID)
		if len(events) == lastCount {
			return
		}
		newEvents := events
		if lastCount > 0 && lastCount < len(events) {
			newEvents = events[lastCount:]
		}
		lastCount = len(events)

		data, _ := json.Marshal(newEvents)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	sendUpdate()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sendUpdate()
		}
	}
}

func (d *dashboard) handleLearnings(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "active"
	}
	learnings, err := d.store.ListLearnings(status)
	if err != nil {
		d.writeError(w, 500, err.Error())
		return
	}

	type learningResp struct {
		ID           string  `json:"id"`
		ScopeLang    string  `json:"scope_lang"`
		ScopeKind    string  `json:"scope_kind"`
		Text         string  `json:"text"`
		Rationale    string  `json:"rationale"`
		Confidence   float64 `json:"confidence"`
		Status       string  `json:"status"`
		TimesApplied int     `json:"times_applied"`
		CreatedAt    string  `json:"created_at"`
	}

	resp := make([]learningResp, 0, len(learnings))
	for _, l := range learnings {
		resp = append(resp, learningResp{
			ID:           l.ID,
			ScopeLang:    l.ScopeLang,
			ScopeKind:    l.ScopeKind,
			Text:         l.Text,
			Rationale:    l.Rationale,
			Confidence:   l.Confidence,
			Status:       l.Status,
			TimesApplied: l.TimesApplied,
			CreatedAt:    l.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	d.writeJSON(w, resp)
}

func (d *dashboard) handleLiveStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		d.writeError(w, 500, "streaming not supported")
		return
	}

	ctx := r.Context()
	var mu sync.Mutex
	var lastHash string

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	sendUpdate := func() {
		session, _ := d.store.GetActiveSession()
		jobs, _ := d.store.ListJobs(50)
		applies, _ := d.store.ListApplies(1)
		recentEvents, _ := d.store.ListRecentAgentEvents(50)

		payload := map[string]interface{}{
			"session":      session,
			"jobs":         jobs,
			"agent_events": recentEvents,
		}
		if len(applies) > 0 {
			payload["latest_apply"] = applies[0]
		}
		if session != nil {
			sr, _ := d.store.ListSessionResources(session.ID)
			payload["session_resources"] = sr
		}

		data, _ := json.Marshal(payload)
		hash := fmt.Sprintf("%x", len(data))

		mu.Lock()
		defer mu.Unlock()
		if hash == lastHash {
			return
		}
		lastHash = hash

		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	sendUpdate()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sendUpdate()
		}
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
