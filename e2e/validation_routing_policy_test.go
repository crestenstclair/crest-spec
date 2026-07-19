package e2e_test

import (
	"bufio"
	"fmt"
	"os/exec"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	specmod "github.com/crestenstclair/crest-spec/internal/spec"
)

const routingDiagnosticCommand = "echo 'Running unittests src/lib.rs (target/debug/deps/routing)' >&2; " +
	"echo \"thread 'routing_demo' panicked at src/panic_owner.rs:9:3:\" >&2; " +
	"echo 'Diff in src/format_owner_a.rs:4:' >&2; " +
	"echo 'Diff in src/format_owner_b.rs:7:' >&2; exit 1"

const routingPolicySpecTemplate = `package routing

project: {
	name:    "validation-routing-acceptance"
	mission: "Route closure validation failures only to source owners named by structured diagnostics"
	layers:  ["domain"]
	meta: {
		language: "rust"
		style:    "small deterministic modules"
	}

	actors: developer: {
		description: "a developer repairing a failed affected-closure validation"
	}
	goals: precise_repairs: {
		description:  "Only resources implicated by structured source diagnostics are reopened"
		priority:     "required"
		actors:       ["actor.developer"]
		capabilities: ["capability.route_repairs"]
		requirements: ["requirement.structured_ownership"]
	}
	capabilities: route_repairs: {
		description: "Attribute compiler, formatter, and panic spans to their unique generated-file owners"
		goals: ["goal.precise_repairs"]
		acceptance: structured_diagnostics: {
			description: "Broad test output does not fan repair work across innocent closure participants"
			actor: "actor.developer"
			steps: [{
				action:   "run one closure gate containing structured and spanless output"
				observes: "only uniquely named source owners become repair work"
			}]
			evidence: ["evidence.routing_gate"]
		}
	}
	requirements: structured_ownership: {
		kind:         "functional"
		description:  "Tool progress paths and spanless broad aliases never imply whole-closure ownership"
		goals:        ["goal.precise_repairs"]
		capabilities: ["capability.route_repairs"]
	}
	evidence: routing_gate: {
		kind:        "integration_validation"
		description: "The real validation batch emits deterministic ownership evidence"
		validations: ["validation.routing"]
	}
	completion: {
		requiredGoals: ["goal.precise_repairs"]
		projectChecks: ["validation.routing"]
	}

	contexts: Routing: {
		purpose: "exercise affected-closure diagnostic ownership"
		valueObjects: PanicOwner: {
			from: "u32"
			contributesTo: [{capability: "capability.route_repairs", contribution: "owns the panicking source span"}]
			validations: [{kind: "test", command: ["sh", "-c", %s]}]
		}
		valueObjects: FormatOwnerA: {
			from: "u32"
			contributesTo: [{capability: "capability.route_repairs", contribution: "owns the first rustfmt span"}]
		}
		valueObjects: FormatOwnerB: {
			from: "u32"
			contributesTo: [{capability: "capability.route_repairs", contribution: "owns the second rustfmt span"}]
		}
		valueObjects: InnocentOwner: {
			from: "u32"
			contributesTo: [{capability: "capability.route_repairs", contribution: "participates in the closure without a diagnostic"}]
		}
	}

	validations: routing: {
		scope:       "project"
		kind:        "test"
		command:     ["sh", "-c", %s]
		description: "emit one panic, two formatter spans, and one non-diagnostic test progress path"
	}
}
`

// TestRealBinaryRoutesOnlyStructuredValidationOwners drives four accepted
// resources through one compiled MCP validation wave. The physical command is
// shared by a resource definition, CREST_SPEC_TEST_CMD, and an ownerless
// project definition. Its output contains three structured source spans plus a
// broad `Running unittests src/lib.rs` progress line. Only the three span owners
// may consume repair cycles or become dispatchable.
func TestRealBinaryRoutesOnlyStructuredValidationOwners(t *testing.T) {
	binary := buildCrestSpecBinary(t, repositoryRoot(t))
	projectRoot := createValidationRoutingProject(t)
	server := startCrestSpecWithOverrides(t, binary, projectRoot, []string{
		"CREST_SPEC_TEST_CMD=" + routingDiagnosticCommand,
	})

	validation := callTool[specmod.ValidateResult](t, server, "spec/validate", map[string]any{})
	require.True(t, validation.Valid, "fixture spec must validate: %+v", validation)
	begin := callTool[specmod.BeginResult](t, server, "spec/begin", map[string]any{})
	require.Len(t, begin.Plan, 4)
	require.Len(t, begin.Waves, 1)
	batch := callTool[specmod.WaveContextResult](t, server, "spec/context_batch", map[string]any{
		"session_id": begin.SessionID, "limit": 4, "budget_tokens": 32768,
	})
	require.Equal(t, 0, batch.WaveIndex)
	require.Len(t, batch.Contexts, 4)

	paths := map[string]string{
		"valueObject.Routing.PanicOwner":    "src/panic_owner.rs",
		"valueObject.Routing.FormatOwnerA":  "src/format_owner_a.rs",
		"valueObject.Routing.FormatOwnerB":  "src/format_owner_b.rs",
		"valueObject.Routing.InnocentOwner": "src/innocent_owner.rs",
	}
	for index, item := range batch.Contexts {
		path, ok := paths[item.ResourceID]
		require.True(t, ok, "unexpected planned resource %s", item.ResourceID)
		commitDeterministicCandidate(
			t, server, begin.SessionID, "routing-policy-host",
			fmt.Sprintf("routing-policy-worker-%d", index+1), item,
			[]map[string]any{{
				"path":    path,
				"content": fmt.Sprintf("pub fn value() -> u32 { %d }\n", index+1),
			}},
		)
	}

	verified := callTool[specmod.WaveVerifyResult](t, server, "spec/verify_wave", map[string]any{
		"session_id": begin.SessionID, "wave_index": 0,
	})
	require.False(t, verified.Passed)
	require.True(t, verified.IntegrationReady)
	require.True(t, verified.IntegrationValidated)
	require.Equal(t, 3, verified.ValidationAliases,
		"resource, configured-test, and project definitions must remain distinct evidence aliases")
	require.Equal(t, 1, verified.ValidationCommands,
		"operationally identical aliases must execute the diagnostic command once")
	require.Equal(t, 1, verified.ResourceValidationDefinitions)
	require.Equal(t, 1, verified.ResourceValidationCommands)
	require.Equal(t, 1, verified.RetryCount)

	wantReopened := []string{
		"valueObject.Routing.FormatOwnerA",
		"valueObject.Routing.FormatOwnerB",
		"valueObject.Routing.PanicOwner",
	}
	require.Equal(t, wantReopened, verified.ReopenedResources)
	require.Len(t, verified.Errors, 3)
	for _, waveErr := range verified.Errors {
		require.Equal(t, wantReopened, waveErr.ResourceIDs, waveErr.Kind)
	}

	for _, resourceID := range wantReopened {
		require.Equal(t, 1, affectedClosureRepairCharge(t, server, begin.SessionID, resourceID), resourceID)
		require.Equal(t, "pending", sessionResourceColumn(t, server, begin.SessionID, resourceID, "state"))
		require.Equal(t, "generate", sessionResourceColumn(t, server, begin.SessionID, resourceID, "execution_strategy"))
	}
	const innocentID = "valueObject.Routing.InnocentOwner"
	require.Zero(t, affectedClosureRepairCharge(t, server, begin.SessionID, innocentID))
	require.Equal(t, "committed", sessionResourceColumn(t, server, begin.SessionID, innocentID, "state"))

	repairBatch := callTool[specmod.WaveContextResult](t, server, "spec/context_batch", map[string]any{
		"session_id": begin.SessionID, "limit": 4, "budget_tokens": 32768,
	})
	require.Len(t, repairBatch.Contexts, 3)
	var repairIDs []string
	for _, item := range repairBatch.Contexts {
		require.Empty(t, item.Error, item.ResourceID)
		require.NotNil(t, item.Context, item.ResourceID)
		require.NotEmpty(t, item.Context.HandoffID, item.ResourceID)
		repairIDs = append(repairIDs, item.ResourceID)
	}
	sort.Strings(repairIDs)
	require.Equal(t, wantReopened, repairIDs)
	require.NotContains(t, repairIDs, innocentID,
		"spanless progress and broad aliases must not make the innocent owner context-eligible")
	server.stop(t)
}

func createValidationRoutingProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, root, "Cargo.toml", "[package]\nname = \"routing-policy\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	writeFixtureFile(t, root, "src/lib.rs", "pub fn existing_module() {}\n")
	specification := fmt.Sprintf(routingPolicySpecTemplate, fmt.Sprintf("%q", routingDiagnosticCommand), fmt.Sprintf("%q", routingDiagnosticCommand))
	writeFixtureFile(t, root, "spec/spec.cue", specification)
	return root
}

func affectedClosureRepairCharge(t *testing.T, server *crestSpecProcess, sessionID, resourceID string) int {
	t.Helper()
	return productionSQLScalar(t, server, `
		SELECT COUNT(*) AS value
		FROM failure_classifications AS failure
		JOIN generation_attempts AS attempt ON attempt.id = failure.attempt_id
		WHERE attempt.session_id = `+sqlLiteral(sessionID)+`
		  AND attempt.resource_id = `+sqlLiteral(resourceID)+`
		  AND failure.origin = 'engine'
		  AND failure.category = 'integration_error'
		  AND failure.evidence_source = 'affected_closure_validation'`)
}

func sessionResourceColumn(
	t *testing.T,
	server *crestSpecProcess,
	sessionID, resourceID, column string,
) string {
	t.Helper()
	require.Contains(t, []string{"state", "execution_strategy"}, column)
	rows := productionSQLRows(t, server,
		"SELECT "+column+" AS value FROM session_resources WHERE session_id = "+sqlLiteral(sessionID)+
			" AND resource_id = "+sqlLiteral(resourceID))
	require.Len(t, rows, 1)
	value, ok := rows[0]["value"].(string)
	require.True(t, ok, "unexpected %s value %#v", column, rows[0]["value"])
	return value
}

func startCrestSpecWithOverrides(
	t *testing.T,
	binary, projectRoot string,
	overrides []string,
) *crestSpecProcess {
	t.Helper()
	command := exec.Command(binary)
	command.Dir = projectRoot
	command.Env = replaceEnvironment(crestSpecEnvironment(projectRoot), overrides)
	stdin, err := command.StdinPipe()
	require.NoError(t, err)
	stdout, err := command.StdoutPipe()
	require.NoError(t, err)
	stderr := &synchronizedBuffer{}
	command.Stderr = stderr
	require.NoError(t, command.Start())

	process := &crestSpecProcess{
		cmd: command, stdin: stdin, stdout: bufio.NewReader(stdout), stderr: stderr,
	}
	var initialized map[string]any
	process.rpc(t, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name": "crest-spec-routing-policy-e2e", "version": "1",
		},
	}, &initialized)
	require.Equal(t, "2024-11-05", initialized["protocolVersion"])
	serverInfo, ok := initialized["serverInfo"].(map[string]any)
	require.True(t, ok)
	require.NotEmpty(t, serverInfo["buildFingerprint"])
	process.initialized = initialized
	t.Cleanup(func() {
		if !process.stopped {
			process.stop(t)
		}
	})
	return process
}
