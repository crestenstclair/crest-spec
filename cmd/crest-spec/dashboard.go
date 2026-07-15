package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
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
	cserrors "github.com/crestenstclair/crest-spec/internal/errors"
	"github.com/crestenstclair/crest-spec/internal/observability"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
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

	sp := specmod.New(st, specmod.OSFileSystem{}, cfg)

	d := &dashboard{store: st, spec: sp, views: observability.NewService(st), cfg: cfg, log: log}

	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("GET /api/status", d.handleStatus)
	mux.HandleFunc("GET /api/v1/project", d.handleProjectOverview)
	mux.HandleFunc("GET /api/v1/goals", d.handleGoals)
	mux.HandleFunc("GET /api/v1/goals/{id}", d.handleGoal)
	mux.HandleFunc("GET /api/v1/capabilities", d.handleCapabilities)
	mux.HandleFunc("GET /api/v1/capabilities/{id}", d.handleCapability)
	mux.HandleFunc("GET /api/v1/resources", d.handleV1Resources)
	mux.HandleFunc("GET /api/v1/resources/{id}/impact", d.handleResourceImpact)
	mux.HandleFunc("GET /api/v1/resources/{id}", d.handleV1Resource)
	mux.HandleFunc("GET /api/v1/plan", d.handleV1Plan)
	mux.HandleFunc("GET /api/v1/plan/operations/{id}", d.handleV1PlanOperation)
	mux.HandleFunc("GET /api/v1/attempts/{id}", d.handleAttempt)
	mux.HandleFunc("GET /api/v1/attempt-comparison", d.handleAttemptComparison)
	mux.HandleFunc("GET /api/v1/contexts", d.handleContextAttempts)
	mux.HandleFunc("GET /api/v1/contexts/{id}", d.handleContextManifest)
	mux.HandleFunc("GET /api/v1/context-comparison", d.handleContextComparison)
	mux.HandleFunc("GET /api/v1/executions", d.handleExecutions)
	mux.HandleFunc("GET /api/v1/executions/{id}", d.handleExecutionTrace)
	mux.HandleFunc("GET /api/v1/execution-comparison", d.handleExecutionComparison)
	mux.HandleFunc("GET /api/v1/failures", d.handleFailures)
	mux.HandleFunc("GET /api/v1/failures/{id}", d.handleFailure)
	mux.HandleFunc("GET /api/v1/handoffs", d.handleHandoffs)
	mux.HandleFunc("GET /api/v1/verifications", d.handleVerifications)
	mux.HandleFunc("GET /api/v1/verifications/definitions", d.handleVerificationDefinitions)
	mux.HandleFunc("GET /api/v1/verifications/{id}", d.handleVerificationRun)
	mux.HandleFunc("GET /api/v1/evaluations/summary", d.handleEvaluationSummary)
	mux.HandleFunc("GET /api/v1/evaluations/cases", d.handleEvaluationCases)
	mux.HandleFunc("GET /api/v1/evaluations/cases/{id}", d.handleEvaluationCase)
	mux.HandleFunc("GET /api/v1/evaluations/datasets", d.handleEvaluationDatasets)
	mux.HandleFunc("GET /api/v1/evaluations/datasets/{id}", d.handleEvaluationDataset)
	mux.HandleFunc("GET /api/v1/evaluations/configurations", d.handleEvaluationConfigurations)
	mux.HandleFunc("GET /api/v1/evaluations/configurations/{id}", d.handleEvaluationConfiguration)
	mux.HandleFunc("GET /api/v1/evaluations/runs", d.handleEvaluationRuns)
	mux.HandleFunc("GET /api/v1/evaluations/runs/{id}", d.handleEvaluationRun)
	mux.HandleFunc("GET /api/v1/evaluations/promotions", d.handleEvaluationPromotions)
	mux.HandleFunc("GET /api/v1/evaluations/promotions/{id}", d.handleEvaluationPromotion)
	mux.HandleFunc("GET /api/plan", d.handlePlan)
	mux.HandleFunc("GET /api/resources", d.handleResources)
	mux.HandleFunc("GET /api/applies", d.handleApplies)
	mux.HandleFunc("GET /api/applies/{id}/actions", d.handleApplyActions)
	mux.HandleFunc("GET /api/generations/{resourceID}", d.handleGenerations)
	mux.HandleFunc("GET /api/notes/{applyID}", d.handleNotes)
	mux.HandleFunc("GET /api/session-resources/{sessionID}", d.handleSessionResources)
	mux.HandleFunc("GET /api/session-resources/{sessionID}/wave/{waveIndex}", d.handleSessionResourcesByWave)
	mux.HandleFunc("GET /api/invariant-checks/{applyID}", d.handleInvariantChecks)
	mux.HandleFunc("GET /api/generations-recent", d.handleRecentGenerations)
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
	views *observability.Service
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

func (d *dashboard) writeAPIError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	d.writeJSON(w, observability.ErrorEnvelope{
		Version: observability.APIVersion,
		Error:   observability.APIError{Code: code, Message: message, Retryable: status >= http.StatusInternalServerError, Details: details},
	})
}

func (d *dashboard) queryViews() *observability.Service {
	if d.views == nil {
		d.views = observability.NewService(d.store)
	}
	return d.views
}

func (d *dashboard) projectIdentity(ctx context.Context) (string, bool, error) {
	overview, err := d.spec.ProjectOverview(ctx)
	if err != nil {
		return "", false, err
	}
	return overview.Project.ProjectName, overview.CompletionEnforced, nil
}

func pageRequest(r *http.Request) (observability.PageRequest, error) {
	request := observability.PageRequest{
		Cursor: r.URL.Query().Get("cursor"), Status: r.URL.Query().Get("status"),
		Kind: r.URL.Query().Get("kind"), Query: r.URL.Query().Get("q"),
		GoalID: r.URL.Query().Get("goal"), CapabilityID: r.URL.Query().Get("capability"),
		AttemptID: r.URL.Query().Get("attempt"),
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return request, fmt.Errorf("limit must be an integer")
		}
		request.Limit = limit
	}
	return observability.NormalizePageRequest(request)
}

func (d *dashboard) writeQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, cserrors.ErrNotFound) {
		d.writeAPIError(w, http.StatusNotFound, "not_found", "the requested observability record does not exist", nil)
		return
	}
	d.writeAPIError(w, http.StatusInternalServerError, "query_failed", err.Error(), nil)
}

func (d *dashboard) handleStatus(w http.ResponseWriter, r *http.Request) {
	resources, _ := d.store.ListResources()
	lock, _ := d.store.GetLock()
	session, _ := d.store.GetActiveSession()
	applies, _ := d.store.ListApplies(1)

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
		Resources      int                  `json:"resources"`
		Lock           *store.Lock          `json:"lock"`
		Session        *store.Session       `json:"session"`
		LatestApply    *store.Apply         `json:"latest_apply"`
		SessionActions []store.ApplyAction  `json:"session_actions,omitempty"`
		ResourceStates *resourceStateCounts `json:"resource_states,omitempty"`
	}

	resp := statusResp{
		Resources: len(resources),
		Lock:      lock,
		Session:   session,
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

func (d *dashboard) handleProjectOverview(w http.ResponseWriter, r *http.Request) {
	projectName, completionEnforced, err := d.projectIdentity(r.Context())
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	overview, err := d.queryViews().Project(r.Context(), projectName, completionEnforced)
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	d.writeJSON(w, overview)
}

func (d *dashboard) handleGoals(w http.ResponseWriter, r *http.Request) {
	request, err := pageRequest(r)
	if err != nil {
		d.writeAPIError(w, http.StatusBadRequest, "invalid_page", err.Error(), nil)
		return
	}
	projectName, _, err := d.projectIdentity(r.Context())
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	result, err := d.queryViews().Goals(r.Context(), projectName, request)
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	d.writeJSON(w, result)
}

func (d *dashboard) handleGoal(w http.ResponseWriter, r *http.Request) {
	projectName, _, err := d.projectIdentity(r.Context())
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	result, err := d.queryViews().Goal(r.Context(), projectName, r.PathValue("id"))
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	d.writeJSON(w, result)
}

func (d *dashboard) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	request, err := pageRequest(r)
	if err != nil {
		d.writeAPIError(w, http.StatusBadRequest, "invalid_page", err.Error(), nil)
		return
	}
	projectName, _, err := d.projectIdentity(r.Context())
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	result, err := d.queryViews().Capabilities(r.Context(), projectName, request)
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	d.writeJSON(w, result)
}

func (d *dashboard) handleCapability(w http.ResponseWriter, r *http.Request) {
	projectName, _, err := d.projectIdentity(r.Context())
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	result, err := d.queryViews().Capability(r.Context(), projectName, r.PathValue("id"))
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	d.writeJSON(w, result)
}

func (d *dashboard) handleV1Resources(w http.ResponseWriter, r *http.Request) {
	request, err := pageRequest(r)
	if err != nil {
		d.writeAPIError(w, http.StatusBadRequest, "invalid_page", err.Error(), nil)
		return
	}
	projectName, _, err := d.projectIdentity(r.Context())
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	result, err := d.queryViews().Resources(r.Context(), projectName, request)
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	d.writeJSON(w, result)
}

func (d *dashboard) handleV1Resource(w http.ResponseWriter, r *http.Request) {
	projectName, _, err := d.projectIdentity(r.Context())
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	result, err := d.queryViews().Resource(r.Context(), projectName, r.PathValue("id"))
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	d.writeJSON(w, result)
}

func (d *dashboard) currentPlan(ctx context.Context) (*observability.PlanView, error) {
	overview, err := d.spec.ProjectOverview(ctx)
	if err != nil {
		return nil, err
	}
	result, err := d.spec.Plan(ctx)
	if err != nil {
		return nil, err
	}
	return d.queryViews().Plan(ctx, overview.Project.ProjectName, overview.Project.SpecHash, result.Actions, result.Waves, result.Slices)
}

func (d *dashboard) handleV1Plan(w http.ResponseWriter, r *http.Request) {
	result, err := d.currentPlan(r.Context())
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	d.writeJSON(w, result)
}

func (d *dashboard) handleV1PlanOperation(w http.ResponseWriter, r *http.Request) {
	plan, err := d.currentPlan(r.Context())
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	result, err := observability.PlanOperationByID(plan, r.PathValue("id"))
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	d.writeJSON(w, struct {
		Version   string                      `json:"version"`
		Operation observability.PlanOperation `json:"operation"`
		Plan      []observability.Link        `json:"links"`
	}{Version: observability.APIVersion, Operation: *result, Plan: plan.Links})
}

func (d *dashboard) handleResourceImpact(w http.ResponseWriter, r *http.Request) {
	result, err := d.spec.Impact(r.Context(), r.PathValue("id"))
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	d.writeJSON(w, observability.Impact(*result))
}

func (d *dashboard) handleAttempt(w http.ResponseWriter, r *http.Request) {
	result, err := d.queryViews().Attempt(r.Context(), r.PathValue("id"))
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	d.writeJSON(w, result)
}

func (d *dashboard) handleAttemptComparison(w http.ResponseWriter, r *http.Request) {
	result, err := d.queryViews().CompareAttempts(r.Context(), r.URL.Query().Get("left"), r.URL.Query().Get("right"))
	if err != nil {
		d.writeAPIError(w, http.StatusBadRequest, "invalid_comparison", err.Error(), nil)
		return
	}
	d.writeJSON(w, result)
}

func (d *dashboard) handleContextAttempts(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}
	attempts, err := d.spec.ListContextAttempts(r.Context(), limit)
	if err != nil {
		d.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.writeJSON(w, attempts)
}

func (d *dashboard) handleContextManifest(w http.ResponseWriter, r *http.Request) {
	manifest, err := d.spec.InspectContext(r.Context(), r.PathValue("id"), "")
	if err != nil {
		d.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	d.writeJSON(w, manifest)
}

func (d *dashboard) handleContextComparison(w http.ResponseWriter, r *http.Request) {
	comparison, err := d.spec.CompareContexts(r.Context(), r.URL.Query().Get("left"), r.URL.Query().Get("right"))
	if err != nil {
		d.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	d.writeJSON(w, comparison)
}

func dashboardLimit(r *http.Request) int {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}
	return limit
}

type dashboardEvaluationSummary struct {
	Cases                int `json:"cases"`
	Datasets             int `json:"datasets"`
	Configurations       int `json:"configurations"`
	Runs                 int `json:"runs"`
	Running              int `json:"running"`
	Completed            int `json:"completed"`
	CandidateWins        int `json:"candidate_wins"`
	Inconclusive         int `json:"inconclusive"`
	Promotions           int `json:"promotions"`
	ActionablePromotions int `json:"actionable_promotions"`
}

func (d *dashboard) handleEvaluationSummary(w http.ResponseWriter, r *http.Request) {
	cases, casesErr := d.store.ListEvaluationCases(r.Context(), 10000)
	datasets, datasetsErr := d.store.ListEvaluationDatasets(r.Context(), 10000)
	configurations, configurationsErr := d.store.ListEvaluationConfigurations(r.Context(), 10000)
	runs, runsErr := d.store.ListEvaluationRuns(r.Context(), 10000)
	promotions, promotionsErr := d.store.ListEvaluationPromotionProposals(r.Context(), "", 10000)
	if err := firstDashboardError(casesErr, datasetsErr, configurationsErr, runsErr, promotionsErr); err != nil {
		d.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	result := dashboardEvaluationSummary{
		Cases: len(cases), Datasets: len(datasets), Configurations: len(configurations),
		Runs: len(runs), Promotions: len(promotions),
	}
	for _, run := range runs {
		switch run.Status {
		case "running":
			result.Running++
		case "completed":
			result.Completed++
		}
		switch run.Conclusion {
		case "candidate_wins":
			result.CandidateWins++
		case "inconclusive":
			result.Inconclusive++
		}
	}
	for _, proposal := range promotions {
		if proposal.Status == "eligible" || proposal.Status == "approved" {
			result.ActionablePromotions++
		}
	}
	d.writeJSON(w, result)
}

func firstDashboardError(errors ...error) error {
	for _, err := range errors {
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *dashboard) handleEvaluationCases(w http.ResponseWriter, r *http.Request) {
	rows, err := d.store.ListEvaluationCases(r.Context(), dashboardLimit(r))
	if err != nil {
		d.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.writeJSON(w, rows)
}

func (d *dashboard) handleEvaluationCase(w http.ResponseWriter, r *http.Request) {
	row, err := d.store.GetEvaluationCase(r.Context(), r.PathValue("id"))
	if err != nil {
		d.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	d.writeJSON(w, row)
}

func (d *dashboard) handleEvaluationDatasets(w http.ResponseWriter, r *http.Request) {
	rows, err := d.store.ListEvaluationDatasets(r.Context(), dashboardLimit(r))
	if err != nil {
		d.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.writeJSON(w, rows)
}

func (d *dashboard) handleEvaluationDataset(w http.ResponseWriter, r *http.Request) {
	row, err := d.store.GetEvaluationDataset(r.Context(), r.PathValue("id"))
	if err != nil {
		d.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	d.writeJSON(w, row)
}

func (d *dashboard) handleEvaluationConfigurations(w http.ResponseWriter, r *http.Request) {
	rows, err := d.store.ListEvaluationConfigurations(r.Context(), dashboardLimit(r))
	if err != nil {
		d.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.writeJSON(w, rows)
}

func (d *dashboard) handleEvaluationConfiguration(w http.ResponseWriter, r *http.Request) {
	row, err := d.store.GetEvaluationConfiguration(r.Context(), r.PathValue("id"))
	if err != nil {
		d.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	d.writeJSON(w, row)
}

func (d *dashboard) handleEvaluationRuns(w http.ResponseWriter, r *http.Request) {
	rows, err := d.store.ListEvaluationRuns(r.Context(), dashboardLimit(r))
	if err != nil {
		d.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.writeJSON(w, rows)
}

func (d *dashboard) handleEvaluationRun(w http.ResponseWriter, r *http.Request) {
	row, err := d.store.GetEvaluationRun(r.Context(), r.PathValue("id"))
	if err != nil {
		d.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	d.writeJSON(w, row)
}

func (d *dashboard) handleEvaluationPromotions(w http.ResponseWriter, r *http.Request) {
	rows, err := d.store.ListEvaluationPromotionProposals(r.Context(), r.URL.Query().Get("status"), dashboardLimit(r))
	if err != nil {
		d.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.writeJSON(w, rows)
}

func (d *dashboard) handleEvaluationPromotion(w http.ResponseWriter, r *http.Request) {
	row, err := d.store.GetEvaluationPromotionProposal(r.Context(), r.PathValue("id"))
	if err != nil {
		d.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	d.writeJSON(w, row)
}

func (d *dashboard) handleExecutions(w http.ResponseWriter, r *http.Request) {
	executions, err := d.store.ListExecutionManifests(r.Context(), dashboardLimit(r))
	if err != nil {
		d.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.writeJSON(w, executions)
}

type dashboardExecutionTrace struct {
	Execution  *store.ExecutionManifest      `json:"execution"`
	Context    *store.ContextManifest        `json:"context"`
	Candidate  *store.CandidateSet           `json:"candidate,omitempty"`
	Failures   []store.FailureClassification `json:"failures"`
	Handoffs   []store.AttemptHandoff        `json:"handoffs"`
	Generation *store.Generation             `json:"generation,omitempty"`
}

func (d *dashboard) executionTrace(ctx context.Context, executionID string) (*dashboardExecutionTrace, error) {
	manifest, err := d.store.GetExecutionManifest(ctx, executionID)
	if err != nil {
		return nil, err
	}
	contextRecord, err := d.store.GetContextManifestByAttempt(ctx, manifest.AttemptID)
	if err != nil {
		return nil, err
	}
	trace := &dashboardExecutionTrace{Execution: manifest, Context: contextRecord}
	trace.Candidate, _ = d.store.GetCandidateSetByAttempt(ctx, manifest.AttemptID)
	trace.Failures, _ = d.store.ListFailureClassificationsByAttempt(ctx, manifest.AttemptID)
	trace.Handoffs, _ = d.store.ListAttemptHandoffsBySource(ctx, manifest.AttemptID)
	generations, _ := d.store.ListGenerations(contextRecord.Attempt.ResourceID, 100)
	for index := range generations {
		if generations[index].ExecutionID == manifest.ID {
			trace.Generation = &generations[index]
			break
		}
	}
	return trace, nil
}

func (d *dashboard) handleExecutionTrace(w http.ResponseWriter, r *http.Request) {
	trace, err := d.executionTrace(r.Context(), r.PathValue("id"))
	if err != nil {
		d.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	d.writeJSON(w, trace)
}

func (d *dashboard) handleExecutionComparison(w http.ResponseWriter, r *http.Request) {
	leftID, rightID := r.URL.Query().Get("left"), r.URL.Query().Get("right")
	if leftID == "" || rightID == "" || leftID == rightID {
		d.writeError(w, http.StatusBadRequest, "left and right must identify two different executions")
		return
	}
	left, err := d.executionTrace(r.Context(), leftID)
	if err != nil {
		d.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	right, err := d.executionTrace(r.Context(), rightID)
	if err != nil {
		d.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	d.writeJSON(w, map[string]any{
		"left": left, "right": right,
		"changes": map[string]bool{
			"host":            left.Execution.HostName != right.Execution.HostName,
			"model":           left.Execution.Model != right.Execution.Model,
			"role":            left.Execution.Role != right.Execution.Role,
			"context":         left.Execution.ContextHash != right.Execution.ContextHash,
			"templates":       left.Execution.TemplateHashesHash != right.Execution.TemplateHashesHash,
			"tools":           left.Execution.ToolPermissionsHash != right.Execution.ToolPermissionsHash,
			"settings":        left.Execution.InferenceConfigHash != right.Execution.InferenceConfigHash || left.Execution.AgentConfigHash != right.Execution.AgentConfigHash,
			"goal_impact":     strings.Join(left.Execution.GoalIDs, "\x00") != strings.Join(right.Execution.GoalIDs, "\x00") || strings.Join(left.Execution.CapabilityIDs, "\x00") != strings.Join(right.Execution.CapabilityIDs, "\x00"),
			"goal_progress":   left.Execution.GoalProgressHash != right.Execution.GoalProgressHash,
			"host_commit_ref": left.Execution.HostCommitRef != right.Execution.HostCommitRef,
			"outcome":         left.Execution.Status != right.Execution.Status,
		},
	})
}

func (d *dashboard) handleFailures(w http.ResponseWriter, r *http.Request) {
	request, err := pageRequest(r)
	if err != nil {
		d.writeAPIError(w, http.StatusBadRequest, "invalid_page", err.Error(), nil)
		return
	}
	failures, err := d.queryViews().Failures(r.Context(), request)
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	d.writeJSON(w, failures)
}

func (d *dashboard) handleFailure(w http.ResponseWriter, r *http.Request) {
	result, err := d.queryViews().Failure(r.Context(), r.PathValue("id"))
	if err != nil {
		d.writeQueryError(w, err)
		return
	}
	d.writeJSON(w, result)
}

func (d *dashboard) handleHandoffs(w http.ResponseWriter, r *http.Request) {
	handoffs, err := d.store.ListPendingAttemptHandoffs(r.Context(), dashboardLimit(r))
	if err != nil {
		d.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.writeJSON(w, handoffs)
}

func (d *dashboard) handleVerifications(w http.ResponseWriter, r *http.Request) {
	runs, err := d.spec.ListValidationRuns(r.Context(), dashboardLimit(r))
	if err != nil {
		d.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.writeJSON(w, runs)
}

func (d *dashboard) handleVerificationRun(w http.ResponseWriter, r *http.Request) {
	run, err := d.spec.GetValidationRun(r.Context(), r.PathValue("id"))
	if err != nil {
		d.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	d.writeJSON(w, run)
}

func (d *dashboard) handleVerificationDefinitions(w http.ResponseWriter, r *http.Request) {
	definitions, err := d.spec.ListVerificationDefinitions(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		d.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.writeJSON(w, definitions)
}

func (d *dashboard) handlePlan(w http.ResponseWriter, r *http.Request) {
	result, err := d.spec.Plan(r.Context())
	if err != nil {
		d.writeError(w, 500, err.Error())
		return
	}

	type planResource struct {
		ResourceID       string            `json:"resource_id"`
		Kind             string            `json:"kind"`
		Reason           string            `json:"reason"`
		WaveIndex        int               `json:"wave_index"`
		OperationID      string            `json:"operation_id"`
		Category         string            `json:"category"`
		RecommendedRole  string            `json:"recommended_role"`
		Capabilities     []string          `json:"capabilities"`
		Goals            []string          `json:"goals"`
		Contributions    map[string]string `json:"contributions"`
		ExpectedBehavior []string          `json:"expected_behavior"`
		ExpectedEvidence []string          `json:"expected_evidence"`
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
			ResourceID:       a.ResourceID,
			Kind:             string(a.Kind),
			Reason:           a.Reason,
			WaveIndex:        waveMap[a.ResourceID],
			OperationID:      a.OperationID,
			Category:         a.Category,
			RecommendedRole:  a.RecommendedRole,
			Capabilities:     a.Capabilities,
			Goals:            a.Goals,
			Contributions:    a.Contributions,
			ExpectedBehavior: a.ExpectedBehavior,
			ExpectedEvidence: a.ExpectedEvidence,
		})
	}

	type planResp struct {
		Actions    []planResource            `json:"actions"`
		Waves      [][]string                `json:"waves"`
		TotalCount int                       `json:"total_count"`
		WaveCount  int                       `json:"wave_count"`
		Slices     []planpkg.CapabilitySlice `json:"slices"`
	}

	d.writeJSON(w, planResp{
		Actions:    resources,
		Waves:      result.Waves,
		TotalCount: len(resources),
		WaveCount:  len(result.Waves),
		Slices:     result.Slices,
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
		applies, _ := d.store.ListApplies(1)

		payload := map[string]interface{}{
			"session": session,
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
