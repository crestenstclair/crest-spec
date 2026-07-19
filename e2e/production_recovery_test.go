package e2e_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	moderncsqlite "modernc.org/sqlite"

	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	"github.com/crestenstclair/crest-spec/internal/execution"
	specmod "github.com/crestenstclair/crest-spec/internal/spec"
	"github.com/crestenstclair/crest-spec/internal/store"
)

const (
	productionRecoveryFixtureEnv   = "CREST_SPEC_PRODUCTION_RECOVERY_FIXTURE"
	productionCrossOwnerFixtureEnv = "CREST_SPEC_CROSS_OWNER_RECOVERY_FIXTURE"
	productionRecoverySessionEnv   = "CREST_SPEC_PRODUCTION_RECOVERY_SESSION_ID"
	productionRecoveryResourcesEnv = "CREST_SPEC_PRODUCTION_RECOVERY_EXPECT_RESOURCES"
	productionRecoveryAttemptsEnv  = "CREST_SPEC_PRODUCTION_RECOVERY_EXPECT_ATTEMPTS"

	// This is the preserved crest-synth session which exposed the legacy
	// reinstatement, failure-provenance, and closure-attribution regressions.
	defaultProductionRecoverySession = "e3360b9c-fac4-4313-9b18-1f2a0b7fc1ed"
	defaultProductionResourceCount   = 17
	defaultProductionAttemptCount    = 34
)

// TestRealBinaryRecoversPreservedProductionSession is an opt-in release gate
// over a real failed production apply. It deliberately does not use an
// in-memory store, a fake command runner, or hand-built session rows.
//
// Run it with:
//
//	CREST_SPEC_PRODUCTION_RECOVERY_FIXTURE=/path/to/preserved/crest-synth \
//	  go test -count=1 -run TestRealBinaryRecoversPreservedProductionSession ./e2e
//
// The fixture is never used as a working directory. The test copies its
// project tree and creates a consistent SQLite online backup in a temporary
// directory, then runs the newly compiled MCP process only against that copy.
func TestRealBinaryRecoversPreservedProductionSession(t *testing.T) {
	fixtureRoot := strings.TrimSpace(os.Getenv(productionRecoveryFixtureEnv))
	if fixtureRoot == "" {
		t.Skipf("set %s to a preserved crest-synth recovery snapshot", productionRecoveryFixtureEnv)
	}
	fixtureRoot = canonicalPath(t, fixtureRoot)
	fixtureDB := filepath.Join(fixtureRoot, ".crest-spec", "state.db")
	require.FileExists(t, fixtureDB)

	sessionID := strings.TrimSpace(os.Getenv(productionRecoverySessionEnv))
	if sessionID == "" {
		sessionID = defaultProductionRecoverySession
	}
	expectedResources := productionExpectedCount(t, productionRecoveryResourcesEnv, defaultProductionResourceCount)
	expectedAttempts := productionExpectedCount(t, productionRecoveryAttemptsEnv, defaultProductionAttemptCount)

	// This fingerprint includes metadata for the entire snapshot and exact
	// content for every release input (including state.db and the usable SF2).
	// The multi-gigabyte archival *.rustysynth-incompatible asset and compiler
	// output are metadata-only because neither is an input copied to the test.
	originalFingerprint := fingerprintProductionFixture(t, fixtureRoot)
	t.Cleanup(func() {
		assert.Equal(t, originalFingerprint, fingerprintProductionFixture(t, fixtureRoot),
			"the release rehearsal must not mutate the preserved production snapshot")
	})

	workRoot := filepath.Join(t.TempDir(), "crest-synth-recovery")
	require.NoError(t, os.MkdirAll(workRoot, 0o755))
	copyProductionFixtureTree(t, fixtureRoot, workRoot)
	require.NoError(t, os.MkdirAll(filepath.Join(workRoot, ".crest-spec"), 0o755))
	isolatedOnlineSQLiteBackup(t, fixtureDB, filepath.Join(workRoot, ".crest-spec", "state.db"))
	require.NotEqual(t, fixtureRoot, canonicalPath(t, workRoot))

	pre := inspectProductionSessionDB(t, filepath.Join(workRoot, ".crest-spec", "state.db"), sessionID)
	require.Equal(t, expectedResources, pre.TotalResources)
	require.Equal(t, expectedResources, pre.CommittedResources)
	require.Equal(t, expectedAttempts, pre.GenerationAttempts)

	repoRoot := repositoryRoot(t)
	binary := buildCrestSpecBinary(t, repoRoot)
	server := startCrestSpec(t, binary, workRoot)

	// Starting the actual binary must migrate the old production DB and leave
	// no half-applied workspace publication waiting for recovery.
	migrations := productionSQLRows(t, server, `
		SELECT filename FROM schema_migrations
		WHERE filename IN (
		    '031_workspace_commit_journal.sql',
		    '032_affected_closure_exhaustion_provenance.sql'
		)
		ORDER BY filename`)
	require.Len(t, migrations, 2, "the copied production DB was not fully migrated by the real binary")
	pendingJournals := productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM workspace_commit_journals WHERE status IN ('prepared','workspace_applied')")
	require.Zero(t, pendingJournals, "startup left an interrupted workspace commit unresolved")

	next := callTool[specmod.NextResult](t, server, "spec/next", map[string]any{"session_id": sessionID})
	require.False(t, next.Done)
	require.True(t, next.ValidationReady,
		"17 accepted resources must proceed directly to closure validation, not another model wave: %+v", next)
	require.Equal(t, 6, next.ValidationWaveIndex)
	require.Empty(t, next.Resources, "recovery must not dispatch unchanged accepted resources")
	require.Equal(t, expectedAttempts, productionAttemptCount(t, server, sessionID),
		"migration/recovery created an unnecessary generation attempt")

	verification := callTool[specmod.WaveVerifyResult](t, server, "spec/verify_wave", map[string]any{
		"session_id": sessionID,
		"wave_index": next.ValidationWaveIndex,
	})
	require.True(t, verification.IntegrationReady)
	require.Truef(t, verification.IntegrationValidated,
		"preserved production closure must execute validation: %+v", verification)
	require.NotEmpty(t, verification.Errors,
		"this preserved snapshot intentionally contains known Rust compiler/rustfmt drift")
	require.NotEmpty(t, verification.ReopenedResources,
		"real closure diagnostics must reopen their governed owners for repair")

	owners := make(map[string]bool)
	for _, validationErr := range verification.Errors {
		require.NotEqual(t, "failure_provenance", validationErr.Kind,
			"a newer abandoned context must not hide an accepted candidate's provenance: %+v", validationErr)
		require.NotEqual(t, "validation_infrastructure", validationErr.Kind,
			"the release environment must execute its validators: %+v", validationErr)
		ids := append([]string(nil), validationErr.ResourceIDs...)
		if validationErr.ResourceID != "" {
			ids = append(ids, validationErr.ResourceID)
		}
		require.NotEmpty(t, ids,
			"every source diagnostic from this production closure must have a governed owner: %+v", validationErr)
		for _, id := range ids {
			owners[id] = true
		}
	}
	require.True(t, owners["asset.CrestSynthMain"],
		"the nested-binary compiler error must be attributed to asset.CrestSynthMain; owners=%v", sortedKeys(owners))
	require.Contains(t, verification.ReopenedResources, "asset.CrestSynthMain")

	require.Equal(t, expectedAttempts, productionAttemptCount(t, server, sessionID),
		"closure validation must route repairs without serially creating model attempts")
	require.Equal(t, expectedResources, productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM session_resources WHERE session_id = "+sqlLiteral(sessionID)))

	// This production fixture contains owners whose immutable execution history
	// already reached max_retries before their accepted candidate was reopened.
	// They must receive normal contexts in a fresh closure-repair epoch; a stale
	// session-wide counter used to return per-item "exhausted max retries"
	// errors here and deadlock the otherwise legal handoffs.
	repairWave := callTool[specmod.NextResult](t, server, "spec/next", map[string]any{"session_id": sessionID})
	require.False(t, repairWave.Done)
	require.False(t, repairWave.ValidationReady)
	require.NotEmpty(t, repairWave.Resources)
	saturatedRows := productionSQLRows(t, server, `
		SELECT resource.resource_id
		FROM session_resources AS resource
		WHERE resource.session_id = `+sqlLiteral(sessionID)+`
		  AND resource.wave_index = `+strconv.Itoa(repairWave.WaveIndex)+`
		  AND resource.state = 'pending'
		  AND (
		      SELECT COUNT(*)
		      FROM execution_manifests AS execution
		      JOIN generation_attempts AS attempt ON attempt.id = execution.attempt_id
		      WHERE attempt.session_id = resource.session_id
		        AND attempt.resource_id = resource.resource_id
		        AND NOT EXISTS (
		            SELECT 1 FROM failure_classifications AS failure
		            WHERE failure.attempt_id = attempt.id
		              AND failure.origin = 'engine'
		              AND failure.category = 'stale_context'
		        )
		  ) >= resource.max_retries
		ORDER BY resource.resource_id`)
	require.NotEmpty(t, saturatedRows,
		"production gate no longer exercises a historically exhausted reopened owner")
	saturated := make(map[string]bool, len(saturatedRows))
	for _, row := range saturatedRows {
		resourceID, ok := row["resource_id"].(string)
		require.True(t, ok, "unexpected resource_id type in %v", row)
		saturated[resourceID] = true
	}

	contextBatch := callTool[specmod.WaveContextResult](t, server, "spec/context_batch", map[string]any{
		"session_id": sessionID,
		"limit":      len(repairWave.Resources),
	})
	require.Equal(t, repairWave.WaveIndex, contextBatch.WaveIndex)
	require.Len(t, contextBatch.Contexts, len(repairWave.Resources))
	preparedByResource := make(map[string]specmod.WaveContextItem, len(contextBatch.Contexts))
	for _, item := range contextBatch.Contexts {
		preparedByResource[item.ResourceID] = item
		require.Empty(t, item.Error, "reopened production owner %s was not dispatchable", item.ResourceID)
		require.NotNil(t, item.Context, "reopened production owner %s received no context", item.ResourceID)
		require.NotEmpty(t, item.Context.AttemptID)
	}
	for resourceID := range saturated {
		item, ok := preparedByResource[resourceID]
		require.True(t, ok, "historically exhausted owner %s was omitted from its repair wave", resourceID)
		require.NotNil(t, item.Context)
		require.NotEmpty(t, item.Context.HandoffID,
			"historically exhausted owner %s lost its legal closure-repair handoff", resourceID)
	}
	require.Equal(t, expectedAttempts+len(contextBatch.Contexts), productionAttemptCount(t, server, sessionID),
		"context_batch must create exactly one real repair attempt per returned owner")

	// Do not stop at context preparation: that was the blind spot which let a
	// context look healthy while execution_start rejected its raw-source
	// provenance. Exercise every returned repair context through the same
	// dispatch boundary used by a real host, but deliberately stop before any
	// model or candidate mutation.
	orderedContexts := append([]specmod.WaveContextItem(nil), contextBatch.Contexts...)
	sort.Slice(orderedContexts, func(i, j int) bool {
		return orderedContexts[i].ResourceID < orderedContexts[j].ResourceID
	})
	hostSessionID := "production-recovery-release-gate"
	sourceSnapshots := make(map[string][]byte)
	selectedOwnedSources := 0
	selectedModuleSources := 0
	attemptIDs := make([]string, 0, len(orderedContexts))
	executionIDs := make([]string, 0, len(orderedContexts))
	for index, item := range orderedContexts {
		prepared := item.Context
		require.NotNil(t, prepared)
		require.False(t, prepared.Blocked, "repair context %s was unexpectedly blocked: %s", item.ResourceID, prepared.BlockedReason)
		require.False(t, prepared.NoDispatch, "repair context %s unexpectedly bypassed its required worker lifecycle", item.ResourceID)
		require.NotEmpty(t, prepared.DispatchID)

		owned, modules := snapshotProductionContextSources(t, workRoot, prepared, sourceSnapshots)
		selectedOwnedSources += owned
		selectedModuleSources += modules
		attemptIDs = append(attemptIDs, prepared.AttemptID)

		workerID := "production-recovery-worker-" + strconv.Itoa(index+1)
		dispatchArgs := map[string]any{
			"dispatch_id":     prepared.DispatchID,
			"session_id":      sessionID,
			"resource_id":     item.ResourceID,
			"host_session_id": hostSessionID,
			"worker_id":       workerID,
		}
		dispatchArgs["event"] = "dispatch_requested"
		requested := callTool[store.DispatchLifecycle](t, server, "spec/dispatch_event", dispatchArgs)
		require.Equal(t, prepared.DispatchID, requested.ID)
		require.Equal(t, prepared.AttemptID, requested.AttemptID)
		require.NotNil(t, requested.DispatchRequestedAt)
		require.Nil(t, requested.WorkerAcknowledgedAt)
		assertProductionSourcesUnchanged(t, workRoot, sourceSnapshots)

		dispatchArgs["event"] = "worker_acknowledged"
		acknowledged := callTool[store.DispatchLifecycle](t, server, "spec/dispatch_event", dispatchArgs)
		require.Equal(t, prepared.DispatchID, acknowledged.ID)
		require.Equal(t, prepared.AttemptID, acknowledged.AttemptID)
		require.NotNil(t, acknowledged.DispatchRequestedAt)
		require.NotNil(t, acknowledged.WorkerAcknowledgedAt)
		assertProductionSourcesUnchanged(t, workRoot, sourceSnapshots)

		started := callTool[store.ExecutionManifest](t, server, "spec/execution_start", map[string]any{
			"attempt_id":          prepared.AttemptID,
			"protocol_version":    execution.ProtocolVersion,
			"idempotency_key":     "production-recovery-" + prepared.AttemptID,
			"context_hash":        prepared.ContextHash,
			"role":                prepared.Role,
			"host_name":           "crest-spec-production-recovery-e2e",
			"host_version":        "1",
			"provider":            "deterministic-production-recovery",
			"model":               "deterministic-no-candidate",
			"host_session_id":     hostSessionID,
			"template_hashes":     prepared.TemplateHashes,
			"system_instructions": prepared.SystemPrompt,
			"tools": []map[string]any{{
				"name": "read-only-production-recovery", "permission": "fixture-copy-only",
			}},
		})
		require.NotEmpty(t, started.ID)
		require.Equal(t, prepared.AttemptID, started.AttemptID)
		require.Equal(t, item.ResourceID, started.ResourceID)
		require.Equal(t, string(execution.StatusExecuting), started.Status)
		require.Equal(t, prepared.ContextManifestID, started.ContextManifestID)
		require.Equal(t, prepared.ContextHash, started.ContextHash)
		require.Equal(t, hostSessionID, started.HostSessionID)
		executionIDs = append(executionIDs, started.ID)
		assertProductionSourcesUnchanged(t, workRoot, sourceSnapshots)
	}
	require.Positive(t, selectedOwnedSources,
		"the production recovery gate must exercise selected owned_code provenance")
	require.Positive(t, selectedModuleSources,
		"the production recovery gate must exercise selected module_configuration provenance")
	require.Equal(t, len(orderedContexts), productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM execution_manifests WHERE attempt_id IN ("+sqlStringList(attemptIDs)+") AND status = 'executing'"),
		"every prepared production repair must register a real execution")
	require.Zero(t, productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM failure_classifications WHERE attempt_id IN ("+sqlStringList(attemptIDs)+") AND origin = 'engine' AND category = 'stale_context'"),
		"dispatch/execution_start returned a stale-context rebuild instead of accepting exact raw provenance")
	require.Zero(t, productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM generation_attempts WHERE id IN ("+sqlStringList(attemptIDs)+") AND status = 'abandoned'"),
		"a production repair context was abandoned during dispatch/execution_start")
	require.Zero(t, productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM candidate_sets WHERE attempt_id IN ("+sqlStringList(attemptIDs)+")"),
		"the pre-model lifecycle must not persist presentation text as a candidate")
	require.Zero(t, productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM workspace_commit_journals WHERE execution_id IN ("+sqlStringList(executionIDs)+")"),
		"the pre-model lifecycle must not start a source publication")
	assertProductionSourcesUnchanged(t, workRoot, sourceSnapshots)
	server.stop(t)
}

const crossOwnerRecoveryValidationCommand = "if grep -q 'BROKEN_TRUE_OWNER' src/z_true_owner.rs; then " +
	"echo 'Diff in src/a_innocent.rs:1:' >&2; " +
	"echo \"thread 'cross_owner_recovery' panicked at src/z_true_owner.rs:1:1:\" >&2; " +
	"exit 1; fi"

const crossOwnerRecoverySpecTemplate = `package recovery

project: {
	name:    "cross-owner-recovery-acceptance"
	mission: "Recover an innocent accepted candidate after validation ownership is corrected"
	layers:  ["domain"]
	meta: {
		language: "rust"
		style:    "small deterministic modules"
	}

	actors: developer: {
		description: "a developer resuming a failed affected-closure repair"
	}
	goals: safe_recovery: {
		description:  "Resolved ownership mistakes never create model work for innocent resources"
		priority:     "required"
		actors:       ["actor.developer"]
		capabilities: ["capability.recover_owner"]
		requirements: ["requirement.cross_owner_provenance"]
	}
	capabilities: recover_owner: {
		description: "Reinstate exact accepted bytes while leaving the actual owner dispatchable"
		goals:       ["goal.safe_recovery"]
		acceptance: restart_recovery: {
			description: "A process restart recovers an unclaimed wrong-owner context without model execution"
			actor:       "actor.developer"
			steps: [{
				action:   "resolve the innocent failure to a different accepted owner and restart"
				observes: "only the actual owner remains repair work"
			}]
			evidence: ["evidence.recovery_gate"]
		}
	}
	requirements: cross_owner_provenance: {
		kind:         "functional"
		description:  "Cross-owner resolution must preserve exact source, candidate, and handoff provenance"
		goals:        ["goal.safe_recovery"]
		capabilities: ["capability.recover_owner"]
	}
	evidence: recovery_gate: {
		kind:        "integration_validation"
		description: "A deterministic closure command names the innocent and actual source owners"
		validations: ["validation.recovery"]
	}
	completion: {
		requiredGoals: ["goal.safe_recovery"]
		projectChecks: ["validation.recovery"]
	}

	contexts: Recovery: {
		purpose: "exercise durable cross-owner recovery"
		valueObjects: AInnocent: {
			from: "u32"
			contributesTo: [{
				capability:   "capability.recover_owner"
				contribution: "owns accepted bytes that require no repair"
			}]
		}
		valueObjects: ZTrueOwner: {
			from: "u32"
			contributesTo: [{
				capability:   "capability.recover_owner"
				contribution: "owns the defect that must remain dispatchable"
			}]
		}
	}

	validations: recovery: {
		scope:       "project"
		kind:        "test"
		command:     ["sh", "-c", %s]
		description: "emit one historical false-owner span and one true-owner panic while the defect exists"
	}
}
`

// TestRealBinaryRestartReinstatesCrossOwnerResolvedCandidate is the compiled-
// MCP regression for the production deadlock captured in CrestSynth. Both
// resources initially receive handoff-backed closure failures. Triage resolves
// the innocent failure with the accepted attempt of a different resource, but
// an unclaimed wrong-owner context has already accepted its handoff. A fresh
// process must abandon that context, reinstate the exact innocent candidate,
// and expose only the actual owner as model work. The test then repairs that
// owner and completes the lifecycle so a locally healthy projection cannot
// hide a broken final gate.
func TestRealBinaryRestartReinstatesCrossOwnerResolvedCandidate(t *testing.T) {
	const (
		innocentID   = "valueObject.Recovery.AInnocent"
		trueOwnerID  = "valueObject.Recovery.ZTrueOwner"
		innocentPath = "src/a_innocent.rs"
		truePath     = "src/z_true_owner.rs"
		innocentCode = "pub fn innocent_value() -> u32 { 1 }\n"
		brokenCode   = "// BROKEN_TRUE_OWNER\npub fn true_value() -> u32 { 2 }\n"
		fixedCode    = "pub fn true_value() -> u32 { 2 }\n"
	)

	binary := buildCrestSpecBinary(t, repositoryRoot(t))
	projectRoot := t.TempDir()
	writeFixtureFile(t, projectRoot, "spec/spec.cue", fmt.Sprintf(
		crossOwnerRecoverySpecTemplate, fmt.Sprintf("%q", crossOwnerRecoveryValidationCommand),
	))
	server := startCrestSpec(t, binary, projectRoot)

	validated := callTool[specmod.ValidateResult](t, server, "spec/validate", map[string]any{})
	require.True(t, validated.Valid, "cross-owner fixture spec must validate: %+v", validated)
	begin := callTool[specmod.BeginResult](t, server, "spec/begin", map[string]any{})
	require.Len(t, begin.Plan, 2)
	require.Len(t, begin.Waves, 1)
	initialBatch := callTool[specmod.WaveContextResult](t, server, "spec/context_batch", map[string]any{
		"session_id": begin.SessionID, "limit": 2, "budget_tokens": 32768,
	})
	require.Len(t, initialBatch.Contexts, 2)

	initialAttempt := make(map[string]string, 2)
	for index, item := range initialBatch.Contexts {
		require.NotNil(t, item.Context, item.ResourceID)
		initialAttempt[item.ResourceID] = item.Context.AttemptID
		var files []map[string]any
		switch item.ResourceID {
		case innocentID:
			files = []map[string]any{{"path": innocentPath, "content": innocentCode}}
		case trueOwnerID:
			files = []map[string]any{{"path": truePath, "content": brokenCode}}
		default:
			t.Fatalf("unexpected recovery resource %s", item.ResourceID)
		}
		commitDeterministicCandidate(
			t, server, begin.SessionID, "cross-owner-recovery-host",
			fmt.Sprintf("cross-owner-initial-worker-%d", index+1), item, files,
		)
	}
	require.NotEmpty(t, initialAttempt[innocentID])
	require.NotEmpty(t, initialAttempt[trueOwnerID])

	failedGate := callTool[specmod.WaveVerifyResult](t, server, "spec/verify_wave", map[string]any{
		"session_id": begin.SessionID, "wave_index": 0,
	})
	require.False(t, failedGate.Passed)
	require.True(t, failedGate.IntegrationValidated)
	require.ElementsMatch(t, []string{innocentID, trueOwnerID}, failedGate.ReopenedResources)

	innocentFailures := productionSQLRows(t, server, `
		SELECT failure.id
		FROM failure_classifications AS failure
		WHERE failure.attempt_id = `+sqlLiteral(initialAttempt[innocentID])+`
		  AND failure.origin = 'engine'
		  AND failure.category = 'integration_error'
		  AND failure.evidence_source = 'affected_closure_validation'
		  AND failure.resolved_at IS NULL`)
	require.Len(t, innocentFailures, 1)
	innocentFailureID, ok := innocentFailures[0]["id"].(string)
	require.True(t, ok)
	require.NotEmpty(t, innocentFailureID)
	innocentHandoffs := productionSQLRows(t, server, `
		SELECT id, status, COALESCE(target_attempt_id, '') AS target_attempt_id
		FROM attempt_handoffs
		WHERE failure_id = `+sqlLiteral(innocentFailureID))
	require.Len(t, innocentHandoffs, 1)
	require.Equal(t, "pending", innocentHandoffs[0]["status"])
	require.Empty(t, innocentHandoffs[0]["target_attempt_id"])

	resolved := callTool[map[string]bool](t, server, "spec/failure_resolve", map[string]any{
		"failure_id":          innocentFailureID,
		"resolution":          "triage proved the diagnostic belongs to the accepted true-owner attempt",
		"resolved_by_attempt": initialAttempt[trueOwnerID],
	})
	require.True(t, resolved["resolved"])

	// Keep the routed error on the projection until after preparation. This
	// reproduces the durable production shape through public MCP calls while
	// preventing the fixed engine from taking its earlier no-dispatch fast path.
	wrongBatch := callTool[specmod.WaveContextResult](t, server, "spec/context_batch", map[string]any{
		"session_id": begin.SessionID, "limit": 1, "budget_tokens": 32768,
	})
	require.Len(t, wrongBatch.Contexts, 1)
	require.Equal(t, innocentID, wrongBatch.Contexts[0].ResourceID)
	wrongContext := wrongBatch.Contexts[0].Context
	require.NotNil(t, wrongContext)
	require.False(t, wrongContext.NoDispatch)
	require.NotEmpty(t, wrongContext.AttemptID)
	require.NotEmpty(t, wrongContext.HandoffID)
	require.Equal(t, 3, productionAttemptCount(t, server, begin.SessionID))
	require.Zero(t, productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM execution_manifests WHERE attempt_id = "+sqlLiteral(wrongContext.AttemptID)))
	require.Zero(t, productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM candidate_sets WHERE attempt_id = "+sqlLiteral(wrongContext.AttemptID)))
	require.Equal(t, "accepted", productionSQLRows(t, server,
		"SELECT status FROM attempt_handoffs WHERE id = "+sqlLiteral(wrongContext.HandoffID))[0]["status"])

	callTool[map[string]bool](t, server, "spec/resolve", map[string]any{
		"session_id":  begin.SessionID,
		"resource_id": innocentID,
		"guidance":    "preserve the exact accepted innocent candidate; only the true owner requires repair",
	})
	innocentBeforeRestart, err := os.ReadFile(filepath.Join(projectRoot, innocentPath))
	require.NoError(t, err)
	require.Equal(t, innocentCode, string(innocentBeforeRestart))

	server.crash(t)
	server = startCrestSpecWithOverrides(t, binary, projectRoot, []string{
		"CREST_SPEC_DISPATCH_TIMEOUT=1ns",
	})

	// This was the production red state: old behavior left both resources
	// pending because it could not use cross-owner resolution provenance once
	// the handoff's unclaimed target context had been abandoned.
	afterRestart := callTool[specmod.NextResult](t, server, "spec/next", map[string]any{
		"session_id": begin.SessionID,
	})
	require.False(t, afterRestart.Done)
	require.False(t, afterRestart.ValidationReady)
	require.Len(t, afterRestart.Resources, 1,
		"restart recovery must remove the innocent resource from dispatchable work: %+v", afterRestart.Resources)
	require.Equal(t, trueOwnerID, afterRestart.Resources[0].ResourceID)
	require.Equal(t, 3, productionAttemptCount(t, server, begin.SessionID),
		"reconciliation must not create another generation attempt")
	require.Equal(t, "abandoned", productionSQLRows(t, server,
		"SELECT status FROM generation_attempts WHERE id = "+sqlLiteral(wrongContext.AttemptID))[0]["status"])
	require.Zero(t, productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM execution_manifests WHERE attempt_id = "+sqlLiteral(wrongContext.AttemptID)),
		"the abandoned innocent context must never become model execution")

	innocentProjection := productionSQLRows(t, server, `
		SELECT state, phase, COALESCE(last_error, '') AS last_error
		FROM session_resources
		WHERE session_id = `+sqlLiteral(begin.SessionID)+`
		  AND resource_id = `+sqlLiteral(innocentID))
	require.Len(t, innocentProjection, 1)
	require.Equal(t, "committed", innocentProjection[0]["state"])
	require.Equal(t, "integration_pending", innocentProjection[0]["phase"])
	require.Empty(t, innocentProjection[0]["last_error"])
	require.Equal(t, "cancelled", productionSQLRows(t, server,
		"SELECT status FROM attempt_handoffs WHERE id = "+sqlLiteral(wrongContext.HandoffID))[0]["status"])
	require.Equal(t, initialAttempt[trueOwnerID], productionSQLRows(t, server,
		"SELECT resolved_by_attempt FROM failure_classifications WHERE id = "+sqlLiteral(innocentFailureID))[0]["resolved_by_attempt"])
	innocentAfterRestart, err := os.ReadFile(filepath.Join(projectRoot, innocentPath))
	require.NoError(t, err)
	require.Equal(t, innocentBeforeRestart, innocentAfterRestart,
		"reinstatement must preserve the innocent candidate's exact bytes")

	trueRepair := callTool[specmod.WaveContextResult](t, server, "spec/context_batch", map[string]any{
		"session_id": begin.SessionID, "limit": 2, "budget_tokens": 32768,
	})
	require.Len(t, trueRepair.Contexts, 1)
	require.Equal(t, trueOwnerID, trueRepair.Contexts[0].ResourceID)
	commitDeterministicCandidate(
		t, server, begin.SessionID, "cross-owner-recovery-host", "cross-owner-true-worker",
		trueRepair.Contexts[0], []map[string]any{{"path": truePath, "content": fixedCode}},
	)

	passedGate := callTool[specmod.WaveVerifyResult](t, server, "spec/verify_wave", map[string]any{
		"session_id": begin.SessionID, "wave_index": trueRepair.WaveIndex,
	})
	require.True(t, passedGate.Passed, "true-owner repair did not close the real gate: %+v", passedGate.Errors)
	require.True(t, passedGate.IntegrationReady)
	require.True(t, passedGate.IntegrationValidated)
	require.Empty(t, passedGate.ReopenedResources)
	require.Equal(t, 4, productionAttemptCount(t, server, begin.SessionID))
	require.Equal(t, 3, productionSQLScalar(t, server, `
		SELECT COUNT(*) AS value
		FROM execution_manifests AS execution
		JOIN generation_attempts AS attempt ON attempt.id = execution.attempt_id
		WHERE attempt.session_id = `+sqlLiteral(begin.SessionID)),
		"only the two initial candidates and the true-owner repair may execute")

	next := callTool[specmod.NextResult](t, server, "spec/next", map[string]any{"session_id": begin.SessionID})
	require.True(t, next.Done)
	finished := callTool[specmod.FinishResult](t, server, "spec/finish", map[string]any{
		"session_id": begin.SessionID,
	})
	require.False(t, finished.Blocked)
	require.Equal(t, "complete", finished.ProjectCompletion)
	server.stop(t)
}

// TestRealBinaryRecoversCapturedCrossOwnerProductionSession is the opt-in
// release rehearsal over the immutable CrestSynth snapshot that exposed this
// bug in production. The synthetic test above owns exhaustive lifecycle
// coverage; this test proves the same binary recognizes the real 17-resource,
// 56-attempt shape without manufacturing model work or touching source bytes.
//
// Run it with:
//
//	CREST_SPEC_CROSS_OWNER_RECOVERY_FIXTURE=/path/to/captured/crest-synth \
//	  go test -count=1 -run TestRealBinaryRecoversCapturedCrossOwnerProductionSession ./e2e
func TestRealBinaryRecoversCapturedCrossOwnerProductionSession(t *testing.T) {
	fixtureRoot := strings.TrimSpace(os.Getenv(productionCrossOwnerFixtureEnv))
	if fixtureRoot == "" {
		t.Skipf("set %s to the captured CrestSynth cross-owner recovery snapshot", productionCrossOwnerFixtureEnv)
	}
	fixtureRoot = canonicalPath(t, fixtureRoot)
	fixtureDB := filepath.Join(fixtureRoot, ".crest-spec", "state.db")
	require.FileExists(t, fixtureDB)

	sessionID := strings.TrimSpace(os.Getenv(productionRecoverySessionEnv))
	if sessionID == "" {
		sessionID = defaultProductionRecoverySession
	}
	originalFingerprint := fingerprintProductionFixture(t, fixtureRoot)
	t.Cleanup(func() {
		assert.Equal(t, originalFingerprint, fingerprintProductionFixture(t, fixtureRoot),
			"the cross-owner release rehearsal must not mutate its captured snapshot")
	})

	workRoot := filepath.Join(t.TempDir(), "crest-synth-cross-owner-recovery")
	require.NoError(t, os.MkdirAll(workRoot, 0o755))
	copyProductionFixtureTree(t, fixtureRoot, workRoot)
	require.NoError(t, os.MkdirAll(filepath.Join(workRoot, ".crest-spec"), 0o755))
	isolatedOnlineSQLiteBackup(t, fixtureDB, filepath.Join(workRoot, ".crest-spec", "state.db"))

	binary := buildCrestSpecBinary(t, repositoryRoot(t))
	server := startCrestSpec(t, binary, workRoot)

	const trueOwnerID = "applicationService.Shell.StandaloneApplication"
	recoveryIDs := []string{
		"applicationService.Shell.KeyboardInputTranslator",
		"adapter.EframeTextWindow",
		"applicationService.Testing.ExhaustiveGuiDemo",
	}
	resolverRows := productionSQLRows(t, server, `
		SELECT attempt.id
		FROM generation_attempts AS attempt
		WHERE attempt.session_id = `+sqlLiteral(sessionID)+`
		  AND attempt.resource_id = `+sqlLiteral(trueOwnerID)+`
		  AND attempt.status = 'accepted'
		ORDER BY attempt.retry_number DESC
		LIMIT 1`)
	require.Len(t, resolverRows, 1, "captured session has no accepted true-owner resolver")
	resolverAttemptID, ok := resolverRows[0]["id"].(string)
	require.True(t, ok)
	require.NotEmpty(t, resolverAttemptID)

	for _, resourceID := range recoveryIDs {
		rows := productionSQLRows(t, server, `
			SELECT resource.state, resource.phase, failure.resolved_by_attempt,
			       handoff.status AS handoff_status
			FROM session_resources AS resource
			JOIN generation_attempts AS source
			  ON source.session_id = resource.session_id
			 AND source.resource_id = resource.resource_id
			JOIN failure_classifications AS failure ON failure.attempt_id = source.id
			JOIN attempt_handoffs AS handoff ON handoff.failure_id = failure.id
			WHERE resource.session_id = `+sqlLiteral(sessionID)+`
			  AND resource.resource_id = `+sqlLiteral(resourceID)+`
			  AND failure.origin = 'engine'
			  AND failure.category = 'integration_error'
			  AND failure.evidence_source = 'affected_closure_validation'
			  AND failure.resolved_at IS NOT NULL
			  AND failure.resolved_by_attempt = `+sqlLiteral(resolverAttemptID)+`
			ORDER BY failure.created_at DESC
			LIMIT 1`)
		require.Len(t, rows, 1, "fixture no longer contains cross-owner provenance for %s", resourceID)
		require.Equal(t, "pending", rows[0]["state"])
		require.Equal(t, "integration_failed", rows[0]["phase"])
	}

	preexistingAbandonedTargets := productionSQLScalar(t, server, `
		SELECT COUNT(*) AS value
		FROM attempt_handoffs AS handoff
		JOIN failure_classifications AS failure ON failure.id = handoff.failure_id
		JOIN generation_attempts AS target ON target.id = handoff.target_attempt_id
		WHERE failure.resolved_by_attempt = `+sqlLiteral(resolverAttemptID)+`
		  AND handoff.status = 'accepted'
		  AND target.status = 'abandoned'
		  AND NOT EXISTS (SELECT 1 FROM execution_manifests AS execution WHERE execution.attempt_id = target.id)
		  AND NOT EXISTS (SELECT 1 FROM candidate_sets AS candidate WHERE candidate.attempt_id = target.id)`)
	require.Equal(t, 1, preexistingAbandonedTargets,
		"the production gate must exercise the exact accepted-to-abandoned wrong-owner context")

	attemptsBefore := productionAttemptCount(t, server, sessionID)
	executionsBefore := productionSQLScalar(t, server, `
		SELECT COUNT(*) AS value
		FROM execution_manifests AS execution
		JOIN generation_attempts AS attempt ON attempt.id = execution.attempt_id
		WHERE attempt.session_id = `+sqlLiteral(sessionID))
	candidatesBefore := productionSQLScalar(t, server, `
		SELECT COUNT(*) AS value
		FROM candidate_sets AS candidate
		JOIN generation_attempts AS attempt ON attempt.id = candidate.attempt_id
		WHERE attempt.session_id = `+sqlLiteral(sessionID))
	sourcesBefore := snapshotProductionGovernedFiles(t, workRoot, server)

	for _, resourceID := range recoveryIDs {
		recovered := callTool[specmod.ContextResult](t, server, "spec/context", map[string]any{
			"session_id": sessionID, "resource_id": resourceID,
		})
		require.True(t, recovered.NoDispatch,
			"%s must be reinstated from accepted provenance, not assigned to a worker", resourceID)
		require.Empty(t, recovered.AttemptID)
		require.NotEmpty(t, recovered.ReinstatedCandidateID)
		require.NotEmpty(t, recovered.HandoffID)
		require.Equal(t, attemptsBefore, productionAttemptCount(t, server, sessionID), resourceID)
		require.Equal(t, executionsBefore, productionSQLScalar(t, server, `
			SELECT COUNT(*) AS value
			FROM execution_manifests AS execution
			JOIN generation_attempts AS attempt ON attempt.id = execution.attempt_id
			WHERE attempt.session_id = `+sqlLiteral(sessionID)), resourceID)
		require.Equal(t, candidatesBefore, productionSQLScalar(t, server, `
			SELECT COUNT(*) AS value
			FROM candidate_sets AS candidate
			JOIN generation_attempts AS attempt ON attempt.id = candidate.attempt_id
			WHERE attempt.session_id = `+sqlLiteral(sessionID)), resourceID)
		assertProductionSourcesUnchanged(t, workRoot, sourcesBefore)
	}

	frontier := callTool[specmod.NextResult](t, server, "spec/next", map[string]any{
		"session_id": sessionID,
	})
	require.False(t, frontier.Done)
	require.False(t, frontier.ValidationReady)
	require.Len(t, frontier.Resources, 1,
		"real recovery must expose one true-owner frontier without dispatching downstream consumers: %+v", frontier.Resources)
	require.Equal(t, trueOwnerID, frontier.Resources[0].ResourceID)
	require.Equal(t, attemptsBefore, productionAttemptCount(t, server, sessionID))
	require.Equal(t, executionsBefore, productionSQLScalar(t, server, `
		SELECT COUNT(*) AS value
		FROM execution_manifests AS execution
		JOIN generation_attempts AS attempt ON attempt.id = execution.attempt_id
		WHERE attempt.session_id = `+sqlLiteral(sessionID)))
	require.Equal(t, candidatesBefore, productionSQLScalar(t, server, `
		SELECT COUNT(*) AS value
		FROM candidate_sets AS candidate
		JOIN generation_attempts AS attempt ON attempt.id = candidate.attempt_id
		WHERE attempt.session_id = `+sqlLiteral(sessionID)))
	assertProductionSourcesUnchanged(t, workRoot, sourcesBefore)
	server.stop(t)
}

func snapshotProductionGovernedFiles(
	t *testing.T,
	workRoot string,
	server *crestSpecProcess,
) map[string][]byte {
	t.Helper()
	rows := productionSQLRows(t, server, "SELECT DISTINCT path FROM generated_files ORDER BY path")
	snapshots := make(map[string][]byte, len(rows))
	for _, row := range rows {
		path, ok := row["path"].(string)
		require.True(t, ok, "unexpected generated path type in %v", row)
		raw, err := os.ReadFile(productionRepositoryPath(t, workRoot, path))
		require.NoError(t, err, "read governed production source %s", path)
		snapshots[path] = append([]byte(nil), raw...)
	}
	require.NotEmpty(t, snapshots, "captured production session has no governed source files")
	return snapshots
}

// snapshotProductionContextSources proves that the provenance attached to
// owned/module context presentation is the exact repository byte stream. The
// owned_code section may intentionally be rendered in an UPDATE MODE envelope
// for the model; that envelope must contain the raw bytes exactly and must
// never become the repository or a candidate itself.
func snapshotProductionContextSources(
	t *testing.T,
	workRoot string,
	prepared *specmod.ContextResult,
	snapshots map[string][]byte,
) (selectedOwned int, selectedModules int) {
	t.Helper()
	for _, section := range prepared.Sections {
		if section.SourceKind != "repository_file" || section.SourcePath == "" ||
			(section.Kind != "owned_code" && section.Kind != "module_configuration") {
			continue
		}
		path := productionRepositoryPath(t, workRoot, section.SourcePath)
		raw, err := os.ReadFile(path)
		require.NoError(t, err, "read context source %s", section.SourcePath)
		require.Equal(t, contextmanifest.Hash(string(raw)), section.OriginalHash,
			"%s provenance must hash exact raw repository bytes", section.SourcePath)
		require.Equal(t, len(raw), section.OriginalBytes,
			"%s provenance must record the exact raw byte length", section.SourcePath)
		require.False(t, strings.HasPrefix(string(raw), productionUpdateModePrefix),
			"presentation envelope was persisted as repository source %s", section.SourcePath)
		if previous, ok := snapshots[section.SourcePath]; ok {
			require.Equal(t, previous, raw, "contexts disagree about raw source %s", section.SourcePath)
		} else {
			snapshots[section.SourcePath] = append([]byte(nil), raw...)
		}

		if section.Decision != contextmanifest.Included {
			continue
		}
		require.Equal(t, contextmanifest.Hash(section.Content), section.SelectedHash,
			"selected presentation hash drifted for %s", section.SourcePath)
		switch section.Kind {
		case "module_configuration":
			selectedModules++
			require.Equal(t, string(raw), section.Content,
				"module_configuration must present exact raw repository bytes")
		case "owned_code":
			selectedOwned++
			if section.Content == string(raw) {
				continue
			}
			presentedPath, presentedSource, ok := parseProductionUpdateModeContent(section.Content)
			require.True(t, ok, "owned_code %s is neither raw source nor the canonical presentation envelope", section.SourcePath)
			require.Equal(t, filepath.ToSlash(section.SourcePath), filepath.ToSlash(presentedPath))
			require.Equal(t, string(raw), presentedSource,
				"owned_code presentation for %s must embed exact raw repository bytes", section.SourcePath)
		}
	}
	return selectedOwned, selectedModules
}

func assertProductionSourcesUnchanged(t *testing.T, workRoot string, snapshots map[string][]byte) {
	t.Helper()
	for sourcePath, expected := range snapshots {
		actual, err := os.ReadFile(productionRepositoryPath(t, workRoot, sourcePath))
		require.NoError(t, err, "read context source %s", sourcePath)
		require.Equal(t, expected, actual,
			"dispatch/execution_start mutated raw repository source %s", sourcePath)
		require.False(t, strings.HasPrefix(string(actual), productionUpdateModePrefix),
			"presentation envelope became repository source %s", sourcePath)
	}
}

func productionRepositoryPath(t *testing.T, root, sourcePath string) string {
	t.Helper()
	relative := filepath.Clean(filepath.FromSlash(sourcePath))
	require.False(t, filepath.IsAbs(relative), "context source path is absolute: %s", sourcePath)
	require.NotEqual(t, "..", relative, "context source escapes fixture: %s", sourcePath)
	require.False(t, strings.HasPrefix(relative, ".."+string(filepath.Separator)),
		"context source escapes fixture: %s", sourcePath)
	return filepath.Join(root, relative)
}

const productionUpdateModePrefix = "UPDATE MODE: preserve the existing implementation and make the smallest change that satisfies the task.\n\nFile: "

func parseProductionUpdateModeContent(content string) (path, source string, ok bool) {
	const (
		opening       = "\n\n```\n"
		closing       = "\n```\n"
		changesMarker = "\nCHANGES TO MAKE\n\n"
	)
	if !strings.HasPrefix(content, productionUpdateModePrefix) {
		return "", "", false
	}
	rest := content[len(productionUpdateModePrefix):]
	openingIndex := strings.Index(rest, opening)
	if openingIndex <= 0 {
		return "", "", false
	}
	path = rest[:openingIndex]
	framed := rest[openingIndex+len(opening):]
	for closingIndex := strings.LastIndex(framed, closing); closingIndex >= 0; closingIndex = strings.LastIndex(framed[:closingIndex], closing) {
		suffix := framed[closingIndex+len(closing):]
		if suffix == "" || strings.HasPrefix(suffix, changesMarker) {
			return path, framed[:closingIndex], true
		}
	}
	return "", "", false
}

func sqlStringList(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = sqlLiteral(value)
	}
	return strings.Join(quoted, ",")
}

func productionExpectedCount(t *testing.T, environmentName string, fallback int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(environmentName))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	require.NoError(t, err, "%s must be an integer", environmentName)
	require.Positive(t, value, "%s must be positive", environmentName)
	return value
}

type productionSessionFacts struct {
	TotalResources     int
	CommittedResources int
	GenerationAttempts int
}

func inspectProductionSessionDB(t *testing.T, databasePath, sessionID string) productionSessionFacts {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })

	var facts productionSessionFacts
	require.NoError(t, database.QueryRow(
		"SELECT COUNT(*) FROM session_resources WHERE session_id = ?", sessionID,
	).Scan(&facts.TotalResources))
	require.NoError(t, database.QueryRow(
		"SELECT COUNT(*) FROM session_resources WHERE session_id = ? AND state = 'committed'", sessionID,
	).Scan(&facts.CommittedResources))
	require.NoError(t, database.QueryRow(
		"SELECT COUNT(*) FROM generation_attempts WHERE session_id = ?", sessionID,
	).Scan(&facts.GenerationAttempts))
	return facts
}

func productionAttemptCount(t *testing.T, server *crestSpecProcess, sessionID string) int {
	t.Helper()
	return productionSQLScalar(t, server,
		"SELECT COUNT(*) AS value FROM generation_attempts WHERE session_id = "+sqlLiteral(sessionID))
}

func productionSQLScalar(t *testing.T, server *crestSpecProcess, query string) int {
	t.Helper()
	rows := productionSQLRows(t, server, query)
	require.Len(t, rows, 1, "scalar query returned %d rows: %s", len(rows), query)
	value, ok := rows[0]["value"]
	require.True(t, ok, "scalar query did not return a value column: %v", rows[0])
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(typed)
		require.NoError(t, err)
		return parsed
	default:
		t.Fatalf("unexpected scalar type %T for %v", value, value)
		return 0
	}
}

func productionSQLRows(t *testing.T, server *crestSpecProcess, query string) []map[string]any {
	t.Helper()
	return callTool[[]map[string]any](t, server, "spec/sql", map[string]any{"query": query})
}

func sqlLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

type sqliteOnlineBackuper interface {
	NewBackup(string) (*moderncsqlite.Backup, error)
}

// isolatedOnlineSQLiteBackup never asks SQLite to open the preserved source.
// Even a nominally read-only WAL connection may create or update -shm, while
// immutable=1 would silently ignore committed frames in a non-empty -wal. Copy
// the complete DB family first, prove it was stable during that read, and run
// SQLite's online backup API only against the disposable intermediate copy.
func isolatedOnlineSQLiteBackup(t *testing.T, source, destination string) {
	t.Helper()
	require.NoFileExists(t, destination)
	before := fingerprintSQLiteFamily(t, source)
	stagingDir := t.TempDir()
	stagedSource := filepath.Join(stagingDir, filepath.Base(source))
	for _, suffix := range []string{"", "-wal", "-shm"} {
		input := source + suffix
		info, err := os.Stat(input)
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		require.NoError(t, copyProductionFixtureFile(input, stagedSource+suffix, info.Mode().Perm()))
	}
	journalInfo, journalErr := os.Stat(source + "-journal")
	if journalErr == nil {
		require.Zero(t, journalInfo.Size(),
			"production fixture has an active rollback journal and is not a stable release-test input")
	} else {
		require.True(t, os.IsNotExist(journalErr), "stat SQLite rollback journal: %v", journalErr)
	}
	require.Equal(t, before, fingerprintSQLiteFamily(t, source),
		"production SQLite files changed while the isolated backup input was copied")
	onlineSQLiteBackup(t, stagedSource, destination)
}

func onlineSQLiteBackup(t *testing.T, source, destination string) {
	t.Helper()
	database, err := sql.Open("sqlite", source)
	require.NoError(t, err)
	database.SetMaxOpenConns(1)
	require.NoError(t, database.PingContext(context.Background()))
	defer func() { require.NoError(t, database.Close()) }()

	connection, err := database.Conn(context.Background())
	require.NoError(t, err)
	defer func() { require.NoError(t, connection.Close()) }()
	require.NoError(t, connection.Raw(func(driverConnection any) error {
		backuper, ok := driverConnection.(sqliteOnlineBackuper)
		if !ok {
			return fmt.Errorf("sqlite driver %T does not expose online backup", driverConnection)
		}
		backup, err := backuper.NewBackup(destination)
		if err != nil {
			return err
		}
		for more := true; more; {
			more, err = backup.Step(-1)
			if err != nil {
				_ = backup.Finish()
				return err
			}
		}
		return backup.Finish()
	}))
}

type sqliteFamilyFingerprint struct {
	Files []string
}

func fingerprintSQLiteFamily(t *testing.T, databasePath string) sqliteFamilyFingerprint {
	t.Helper()
	var files []string
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		path := databasePath + suffix
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			files = append(files, suffix+":absent")
			continue
		}
		require.NoError(t, err)
		require.True(t, info.Mode().IsRegular(), "SQLite family entry %s is not a regular file", path)
		contents, err := os.Open(path)
		require.NoError(t, err)
		digest := sha256.New()
		_, copyErr := io.Copy(digest, contents)
		closeErr := contents.Close()
		require.NoError(t, copyErr)
		require.NoError(t, closeErr)
		files = append(files, fmt.Sprintf("%s:%s:%d:%d:%d", suffix,
			hex.EncodeToString(digest.Sum(nil)), info.Mode().Perm(), info.Size(), info.ModTime().UnixNano()))
	}
	return sqliteFamilyFingerprint{Files: files}
}

func copyProductionFixtureTree(t *testing.T, sourceRoot, destinationRoot string) {
	t.Helper()
	err := filepath.WalkDir(sourceRoot, func(sourcePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceRoot, sourcePath)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if excludedProductionFixturePath(relative) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		destinationPath := filepath.Join(destinationRoot, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return os.MkdirAll(destinationPath, info.Mode().Perm())
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(sourcePath)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
				return err
			}
			return os.Symlink(target, destinationPath)
		case info.Mode().IsRegular():
			return copyProductionFixtureFile(sourcePath, destinationPath, info.Mode().Perm())
		default:
			return fmt.Errorf("unsupported fixture entry %s (%s)", relative, info.Mode())
		}
	})
	require.NoError(t, err)
}

func copyProductionFixtureFile(source, destination string, mode fs.FileMode) (returnErr error) {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() {
		if err := input.Close(); returnErr == nil {
			returnErr = err
		}
	}()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() {
		if err := output.Close(); returnErr == nil {
			returnErr = err
		}
	}()
	_, err = io.Copy(output, input)
	return err
}

func excludedProductionFixturePath(relative string) bool {
	normalized := filepath.ToSlash(relative)
	first, _, _ := strings.Cut(normalized, "/")
	switch first {
	case ".git", ".test-cache", "target":
		return true
	}
	if strings.HasPrefix(normalized, ".crest-spec/state.db") || normalized == ".crest-spec/commit.lock" {
		return true
	}
	return strings.HasSuffix(normalized, ".rustysynth-incompatible")
}

type productionFixtureFingerprint struct {
	MetadataSHA256 string
	InputsSHA256   string
}

func fingerprintProductionFixture(t *testing.T, root string) productionFixtureFingerprint {
	t.Helper()
	metadata := sha256.New()
	inputs := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		normalized := filepath.ToSlash(relative)
		_, _ = fmt.Fprintf(metadata, "%s\x00%s\x00%d\x00%d\x00%d\n",
			normalized, info.Mode().String(), info.Size(), info.ModTime().UnixNano(), info.Mode().Type())
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(metadata, "link\x00%s\x00%s\n", normalized, target)
			_, _ = fmt.Fprintf(inputs, "link\x00%s\x00%s\n", normalized, target)
			return nil
		}
		if !info.Mode().IsRegular() || !contentFingerprintProductionPath(normalized) {
			return nil
		}
		_, _ = fmt.Fprintf(inputs, "file\x00%s\x00%d\n", normalized, info.Mode().Perm())
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(inputs, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	require.NoError(t, err)
	return productionFixtureFingerprint{
		MetadataSHA256: hex.EncodeToString(metadata.Sum(nil)),
		InputsSHA256:   hex.EncodeToString(inputs.Sum(nil)),
	}
}

func contentFingerprintProductionPath(normalized string) bool {
	if normalized == ".crest-spec/state.db" || strings.HasPrefix(normalized, ".crest-spec/state.db-") {
		return true
	}
	if excludedProductionFixturePath(filepath.FromSlash(normalized)) {
		return false
	}
	return !strings.HasPrefix(normalized, "target/")
}

func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	require.NoError(t, err)
	canonical, err := filepath.EvalSymlinks(abs)
	require.NoError(t, err)
	return canonical
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
