package cue

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadMinimalProject(t *testing.T) *Project {
	t.Helper()
	p, err := Load(testdataDir("minimal"))
	require.NoError(t, err)
	return p
}

func TestNewRegistry_AllResourceIDs(t *testing.T) {
	p := loadMinimalProject(t)
	reg, err := NewRegistry(p)
	require.NoError(t, err)

	expectedIDs := []string{
		"project.test-project",
		"context.Synth",
		"aggregate.Synth.Voice",
		"entity.Synth.Voice.Oscillator",
		"valueObject.Synth.Voice.NoteId",
		"valueObject.Synth.Frequency",
		"repository.Synth.VoicePool",
		"port.Synth.AudioOutput",
		"domainService.Synth.Mixer",
		"adapter.CoreAudioAdapter",
		"assetKind.source_file",
		"asset.README",
		"asset.Synth.VoiceImpl",
	}

	for _, id := range expectedIDs {
		assert.True(t, reg.Has(id), "missing resource: %s", id)
	}

	assert.Equal(t, len(expectedIDs), len(reg.Resources))
}

func TestNewRegistry_ResourceKinds(t *testing.T) {
	p := loadMinimalProject(t)
	reg, err := NewRegistry(p)
	require.NoError(t, err)

	tests := map[string]string{
		"project.test-project":          "project",
		"context.Synth":                 "context",
		"aggregate.Synth.Voice":         "aggregate",
		"entity.Synth.Voice.Oscillator": "entity",
		"valueObject.Synth.Frequency":   "valueObject",
		"repository.Synth.VoicePool":    "repository",
		"port.Synth.AudioOutput":        "port",
		"domainService.Synth.Mixer":     "domainService",
		"adapter.CoreAudioAdapter":      "adapter",
		"assetKind.source_file":         "assetKind",
		"asset.README":                  "asset",
		"asset.Synth.VoiceImpl":         "asset",
	}

	for id, expectedKind := range tests {
		r := reg.Resources[id]
		assert.Equal(t, expectedKind, r.Kind, "wrong kind for %s", id)
	}
}

func TestNewRegistry_ContextName(t *testing.T) {
	p := loadMinimalProject(t)
	reg, err := NewRegistry(p)
	require.NoError(t, err)

	assert.Equal(t, "", reg.Resources["project.test-project"].ContextName)
	assert.Equal(t, "Synth", reg.Resources["context.Synth"].ContextName)
	assert.Equal(t, "Synth", reg.Resources["aggregate.Synth.Voice"].ContextName)
	assert.Equal(t, "", reg.Resources["adapter.CoreAudioAdapter"].ContextName)
}

func TestNewRegistry_BuildsBidirectionalCapabilityTraceability(t *testing.T) {
	p := validIntentProject()
	p.Contexts = map[string]Context{
		"Example": {
			Purpose: "implement the example",
			ValueObjects: map[string]ValueObject{
				"Output": {Description: "observable output", ContributesTo: []Contribution{{Capability: "capability.run_example", Contribution: "defines the observable result"}}},
			},
			ApplicationServices: map[string]ApplicationService{
				"Runner": {Purpose: "run it", ContributesTo: []Contribution{{Capability: "capability.run_example", Contribution: "coordinates the example"}}},
			},
		},
	}

	reg, err := NewRegistry(p)
	require.NoError(t, err)
	require.Equal(t, []string{"applicationService.Example.Runner", "valueObject.Example.Output"}, reg.ResourcesForCapability("capability.run_example"))
	require.Equal(t, []string{"capability.run_example"}, reg.CapabilitiesForResource("applicationService.Example.Runner"))
	require.Equal(t, []string{"applicationService.Example.Runner", "valueObject.Example.Output"}, reg.ResourcesForGoal("goal.complete_example"))
	require.Empty(t, reg.MissingCapabilities)
	// Contributions are grouping edges only, not resource dependencies.
	require.Empty(t, reg.Resources["applicationService.Example.Runner"].Dependencies)
}

func TestNewRegistry_ReportsMissingCapabilitiesWithoutRejectingProject(t *testing.T) {
	reg, err := NewRegistry(validIntentProject())
	require.NoError(t, err)
	require.Equal(t, []string{"capability.run_example"}, reg.MissingCapabilities)
}

func TestNewRegistryValidatesWitnessRepairResources(t *testing.T) {
	newProject := func() *Project {
		project := validIntentProject()
		project.Contexts = map[string]Context{"Example": {
			Purpose: "exercise witness repair ownership",
			ValueObjects: map[string]ValueObject{
				"Observed": {Description: "the observed value"},
				"Repair":   {Description: "the exact repair owner"},
			},
		}}
		project.Witnesses = map[string]Witness{"behavior": {
			Scope: "goal", Goal: "goal.complete_example",
			Resources:                     []string{"valueObject.Example.Observed", "valueObject.Example.Repair"},
			RepairResources:               []string{"valueObject.Example.Repair"},
			CounterexampleRepairResources: []string{"valueObject.Example.Repair"},
			Command:                       []string{"sh", "real.sh"}, NegativeCommand: []string{"sh", "negative.sh"},
			Observation: ObservationDeclaration{Kind: "json_stdout", Marker: "CREST_OBSERVATION ", Schema: map[string]string{"worked": "bool"}},
			Predicates: []WitnessPredicate{{
				Field: "worked", Op: "eq", Value: true,
				RepairResources: []string{"valueObject.Example.Repair"},
			}},
		}}
		return project
	}

	t.Run("accepts canonical subset", func(t *testing.T) {
		registry, err := NewRegistry(newProject())
		require.NoError(t, err)
		require.Equal(t,
			[]string{"valueObject.Example.Repair"},
			registry.Project.Witnesses["behavior"].RepairResources,
		)
		require.Equal(t,
			[]string{"valueObject.Example.Repair"},
			registry.Project.Witnesses["behavior"].Predicates[0].RepairResources,
		)
	})

	t.Run("rejects unknown canonical resource", func(t *testing.T) {
		project := newProject()
		witness := project.Witnesses["behavior"]
		witness.RepairResources = []string{"valueObject.Example.Missing"}
		project.Witnesses["behavior"] = witness

		_, err := NewRegistry(project)
		require.ErrorContains(t, err,
			`project.witnesses.behavior.repairResources references unknown ID "valueObject.Example.Missing"`)
	})

	t.Run("rejects resource outside observed set", func(t *testing.T) {
		project := newProject()
		witness := project.Witnesses["behavior"]
		witness.Resources = []string{"valueObject.Example.Observed"}
		project.Witnesses["behavior"] = witness

		_, err := NewRegistry(project)
		require.ErrorContains(t, err,
			`project.witnesses.behavior.repairResources must be a subset of project.witnesses.behavior.resources; "valueObject.Example.Repair" is not observed`)
	})

	t.Run("rejects predicate owner outside witness repair set", func(t *testing.T) {
		project := newProject()
		witness := project.Witnesses["behavior"]
		witness.Predicates[0].RepairResources = []string{"valueObject.Example.Observed"}
		project.Witnesses["behavior"] = witness

		_, err := NewRegistry(project)
		require.ErrorContains(t, err,
			`project.witnesses.behavior.predicates[0].repairResources must be a subset of project.witnesses.behavior.repairResources; "valueObject.Example.Observed" is not a witness repair owner`)
	})

	t.Run("rejects counterexample owner outside witness repair set", func(t *testing.T) {
		project := newProject()
		witness := project.Witnesses["behavior"]
		witness.CounterexampleRepairResources = []string{"valueObject.Example.Observed"}
		project.Witnesses["behavior"] = witness

		_, err := NewRegistry(project)
		require.ErrorContains(t, err,
			`project.witnesses.behavior.counterexampleRepairResources must be a subset of project.witnesses.behavior.resources and project.witnesses.behavior.repairResources; "valueObject.Example.Observed" is not an observed repair owner`)
	})
}

func TestNewRegistry_RejectsInvalidContributionsAndProfiles(t *testing.T) {
	p := validIntentProject()
	p.Contexts = map[string]Context{"Example": {
		Purpose:      "invalid seams",
		Ports:        map[string]Port{"Input": {Direction: "sideways"}},
		ValueObjects: map[string]ValueObject{"Output": {ContributesTo: []Contribution{{Capability: "capability.missing"}}}},
	}}
	p.Adapters = map[string]Adapter{"Endpoint": {Implements: "port.Example.Input", Profile: BoundaryProfile{Kind: "http"}}}

	_, err := NewRegistry(p)
	require.Error(t, err)
	require.ErrorContains(t, err, `capability "capability.missing" not found`)
	require.ErrorContains(t, err, "contribution description is required")
	require.ErrorContains(t, err, `direction must be "inbound" or "outbound"`)
	require.ErrorContains(t, err, "http adapters require method and path")
}

func TestNewRegistry_MetaMerging(t *testing.T) {
	p := loadMinimalProject(t)
	reg, err := NewRegistry(p)
	require.NoError(t, err)

	voiceMeta := reg.Resources["aggregate.Synth.Voice"].Meta
	assert.Equal(t, "go", voiceMeta.Language)
	assert.Equal(t, "DDD", voiceMeta.Style)
	assert.Equal(t, "Core audio context", voiceMeta.Notes)
}

func TestNewRegistry_DependencyEdges(t *testing.T) {
	p := loadMinimalProject(t)
	reg, err := NewRegistry(p)
	require.NoError(t, err)

	// adapter implements port
	adapter := reg.Resources["adapter.CoreAudioAdapter"]
	assert.Len(t, adapter.Dependencies, 1)
	assert.Equal(t, "port.Synth.AudioOutput", adapter.Dependencies[0].TargetID)
	assert.Equal(t, "implements", adapter.Dependencies[0].Kind)

	// repository of aggregate
	repo := reg.Resources["repository.Synth.VoicePool"]
	assert.Len(t, repo.Dependencies, 1)
	assert.Equal(t, "aggregate.Synth.Voice", repo.Dependencies[0].TargetID)
	assert.Equal(t, "of", repo.Dependencies[0].Kind)

	// domain service uses aggregate
	mixer := reg.Resources["domainService.Synth.Mixer"]
	hasUsesDep := false
	for _, dep := range mixer.Dependencies {
		if dep.TargetID == "aggregate.Synth.Voice" && dep.Kind == "uses" {
			hasUsesDep = true
		}
	}
	assert.True(t, hasUsesDep)

	// asset targets aggregate
	asset := reg.Resources["asset.Synth.VoiceImpl"]
	hasTargetDep := false
	hasKindDep := false
	for _, dep := range asset.Dependencies {
		if dep.TargetID == "aggregate.Synth.Voice" && dep.Kind == "targets" {
			hasTargetDep = true
		}
		if dep.TargetID == "assetKind.source_file" && dep.Kind == "uses" {
			hasKindDep = true
		}
	}
	assert.True(t, hasTargetDep)
	assert.True(t, hasKindDep)
}

func TestNewRegistry_DanglingReference(t *testing.T) {
	p := &Project{
		Name: "bad-refs",
		Contexts: map[string]Context{
			"A": {
				Purpose: "test",
				DomainServices: map[string]DomainService{
					"Svc": {
						Purpose: "test",
						Uses:    []string{"nonexistent.Thing"},
					},
				},
			},
		},
	}

	_, err := NewRegistry(p)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent.Thing")
}

func TestNewRegistry_ConsumesPublishesEdges(t *testing.T) {
	p := &Project{
		Name: "event-test",
		Contexts: map[string]Context{
			"Orders": {
				Purpose: "Order processing",
				Aggregates: map[string]Aggregate{
					"Order": {
						Root:    true,
						Purpose: "Manages an order",
						Publishes: []string{
							"domainService.Orders.Fulfillment",
						},
					},
				},
				DomainServices: map[string]DomainService{
					"Fulfillment": {
						Purpose: "Fulfills orders",
						Consumes: []string{
							"aggregate.Orders.Order",
						},
						Publishes: []string{
							"port.Orders.EventBus",
						},
					},
				},
				Ports: map[string]Port{
					"EventBus": {
						Consumes: []string{
							"aggregate.Orders.Order",
						},
					},
				},
			},
		},
	}

	reg, err := NewRegistry(p)
	require.NoError(t, err)

	// Aggregate publishes
	order := reg.Resources["aggregate.Orders.Order"]
	hasPublishes := false
	for _, dep := range order.Dependencies {
		if dep.TargetID == "domainService.Orders.Fulfillment" && dep.Kind == "publishes" {
			hasPublishes = true
		}
	}
	assert.True(t, hasPublishes, "aggregate should have publishes edge")

	// DomainService consumes and publishes
	svc := reg.Resources["domainService.Orders.Fulfillment"]
	hasConsumes := false
	hasSvcPublishes := false
	for _, dep := range svc.Dependencies {
		if dep.TargetID == "aggregate.Orders.Order" && dep.Kind == "consumes" {
			hasConsumes = true
		}
		if dep.TargetID == "port.Orders.EventBus" && dep.Kind == "publishes" {
			hasSvcPublishes = true
		}
	}
	assert.True(t, hasConsumes, "domain service should have consumes edge")
	assert.True(t, hasSvcPublishes, "domain service should have publishes edge")

	// Port consumes
	port := reg.Resources["port.Orders.EventBus"]
	hasPortConsumes := false
	for _, dep := range port.Dependencies {
		if dep.TargetID == "aggregate.Orders.Order" && dep.Kind == "consumes" {
			hasPortConsumes = true
		}
	}
	assert.True(t, hasPortConsumes, "port should have consumes edge")
}

func TestNewRegistry_Declaration(t *testing.T) {
	p := loadMinimalProject(t)
	reg, err := NewRegistry(p)
	require.NoError(t, err)

	voice := reg.Resources["aggregate.Synth.Voice"]
	agg, ok := voice.Declaration.(Aggregate)
	require.True(t, ok)
	assert.True(t, agg.Root)
	assert.Equal(t, "Manages a single voice", agg.Purpose)
}
