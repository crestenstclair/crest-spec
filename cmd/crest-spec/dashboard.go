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
	"strings"
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

	type statusResp struct {
		Resources    int           `json:"resources"`
		Lock         *store.Lock   `json:"lock"`
		Session      *store.Session `json:"session"`
		LatestApply  *store.Apply  `json:"latest_apply"`
		RunningJobs  int           `json:"running_jobs"`
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
