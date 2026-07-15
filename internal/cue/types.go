package cue

import (
	"encoding/json"
	"fmt"
	"sort"
)

// FlexMap handles CUE fields that can be either a map or an array of named entries.
// Map form:     {NoteOn: {frequency: "float64"}}
// Array form:   [{name: "NoteOn", payload: {frequency: "float64"}}]
type FlexMap map[string]map[string]string

func (f *FlexMap) UnmarshalJSON(data []byte) error {
	// Try map form first
	var m map[string]map[string]string
	if err := json.Unmarshal(data, &m); err == nil {
		*f = m
		return nil
	}

	// Try array form: [{name: "...", payload: {...}}, ...]
	var arr []struct {
		Name    string            `json:"name"`
		Payload map[string]string `json:"payload"`
	}
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}

	result := make(map[string]map[string]string, len(arr))
	for _, entry := range arr {
		result[entry.Name] = entry.Payload
	}
	*f = result
	return nil
}

// FlexInvariants handles invariants as either an array or a named map of arrays.
// Array form:  [{text: "..."}]
// Map form:    {groupName: [{text: "..."}, ...]}
type FlexInvariants []Invariant

func (f *FlexInvariants) UnmarshalJSON(data []byte) error {
	var arr []Invariant
	if err := json.Unmarshal(data, &arr); err == nil {
		*f = arr
		return nil
	}

	var m map[string][]Invariant
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}

	var result []Invariant
	for _, group := range m {
		result = append(result, group...)
	}
	*f = result
	return nil
}

// FlexContextMap handles contextMap as either an array or a named map of entries.
// Array form:  [{from: "A", to: "B", kind: "..."}]
// Map form:    {name: {from: "A", to: "B", kind: "..."}}
type FlexContextMap []ContextRelationship

func (f *FlexContextMap) UnmarshalJSON(data []byte) error {
	var arr []ContextRelationship
	if err := json.Unmarshal(data, &arr); err == nil {
		*f = arr
		return nil
	}

	var m map[string]ContextRelationship
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}

	result := make([]ContextRelationship, 0, len(m))
	for _, v := range m {
		result = append(result, v)
	}
	*f = result
	return nil
}

// FlexValidations preserves legacy array declarations while allowing new
// validation definitions to use stable map keys. Map keys become canonical
// validation.<key> identifiers in deterministic order.
type FlexValidations []Validation

func (f *FlexValidations) UnmarshalJSON(data []byte) error {
	var definitions map[string]Validation
	if err := json.Unmarshal(data, &definitions); err == nil {
		keys := make([]string, 0, len(definitions))
		for key := range definitions {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		result := make([]Validation, 0, len(keys))
		for _, key := range keys {
			definition := definitions[key]
			canonicalID := "validation." + key
			if definition.ID != "" && definition.ID != canonicalID {
				return fmt.Errorf("validation %q declares conflicting id %q", key, definition.ID)
			}
			definition.ID = canonicalID
			definition.Named = true
			result = append(result, definition)
		}
		*f = result
		return nil
	}

	var legacy []Validation
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	for index := range legacy {
		legacy[index].Named = legacy[index].ID != ""
	}
	*f = legacy
	return nil
}

type Project struct {
	Name string `json:"name"`
	// Mission is the project's why: what is being built, for whom, and the
	// architectural intent. Injected into every generator's system prompt so
	// leaf-level decisions are made against the whole, not in isolation.
	Mission      string                         `json:"mission"`
	Actors       map[string]Actor               `json:"actors"`
	Goals        map[string]Goal                `json:"goals"`
	Capabilities map[string]Capability          `json:"capabilities"`
	Requirements map[string]Requirement         `json:"requirements"`
	Evidence     map[string]EvidenceRequirement `json:"evidence"`
	Witnesses    map[string]Witness             `json:"witnesses,omitempty"`
	NonGoals     map[string]string              `json:"nonGoals,omitempty"`
	Completion   CompletionPolicy               `json:"completion"`
	Layers       []string                       `json:"layers"`
	LayerRules   map[string]LayerRule           `json:"layerRules"`
	Meta         Meta                           `json:"meta"`
	Contexts     map[string]Context             `json:"contexts"`
	Adapters     map[string]Adapter             `json:"adapters"`
	AssetKinds   map[string]AssetKind           `json:"assetKinds"`
	Assets       map[string]Asset               `json:"assets"`
	Invariants   FlexInvariants                 `json:"invariants"`
	ContextMap   FlexContextMap                 `json:"contextMap"`
	Validations  FlexValidations                `json:"validations,omitempty"`
}

// Actor is a stable project participant referenced by goals and acceptance
// scenarios. Map keys become canonical IDs in the form actor.<key>.
type Actor struct {
	Description string `json:"description"`
}

// Goal is a user- or system-level outcome. Its lifecycle status is derived
// from plans and evidence; status is intentionally absent from the CUE model.
type Goal struct {
	Description  string   `json:"description"`
	Priority     string   `json:"priority"`
	Actors       []string `json:"actors,omitempty"`
	DependsOn    []string `json:"dependsOn,omitempty"`
	Capabilities []string `json:"capabilities"`
	Requirements []string `json:"requirements,omitempty"`
}

// Capability is observable functionality that may contribute to more than
// one goal and may be implemented by multiple architectural resources.
type Capability struct {
	Description string                        `json:"description"`
	Goals       []string                      `json:"goals"`
	Acceptance  map[string]AcceptanceScenario `json:"acceptance"`
}

// AcceptanceScenario is an ordered user/system journey with explicit evidence
// requirements. Map keys become acceptance.<capability>.<key> IDs.
type AcceptanceScenario struct {
	Description string           `json:"description"`
	Actor       string           `json:"actor,omitempty"`
	Steps       []AcceptanceStep `json:"steps,omitempty"`
	Evidence    []string         `json:"evidence"`
}

type AcceptanceStep struct {
	Action   string `json:"action"`
	Observes string `json:"observes"`
}

// Requirement captures functional or nonfunctional constraints separately
// from the resources that implement them.
type Requirement struct {
	Kind         string   `json:"kind"`
	Description  string   `json:"description"`
	Goals        []string `json:"goals,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// EvidenceRequirement declares what kind of proof an acceptance scenario
// needs. Concrete executable witness definitions are introduced in WS6.
type EvidenceRequirement struct {
	Kind        string   `json:"kind"`
	Description string   `json:"description"`
	Validations []string `json:"validations,omitempty"`
	Witnesses   []string `json:"witnesses,omitempty"`
}

type CompletionPolicy struct {
	RequiredGoals []string `json:"requiredGoals"`
	ProjectChecks []string `json:"projectChecks,omitempty"`
}

type CompletionStatus string

const (
	CompletionDeclared             CompletionStatus = "declared"
	CompletionPlanned              CompletionStatus = "planned"
	CompletionPartiallyImplemented CompletionStatus = "partially_implemented"
	CompletionLocallyValidated     CompletionStatus = "locally_validated"
	CompletionIntegrated           CompletionStatus = "integrated"
	CompletionBehaviorallyVerified CompletionStatus = "behaviorally_verified"
	CompletionComplete             CompletionStatus = "complete"
	CompletionBlocked              CompletionStatus = "blocked"
	CompletionRegressed            CompletionStatus = "regressed"
)

type LayerRule struct {
	DependsOn []string `json:"dependsOn"`
}

type Meta struct {
	Language    string      `json:"language,omitempty"`
	Style       string      `json:"style,omitempty"`
	Rules       []string    `json:"rules,omitempty"`
	Prompts     []string    `json:"prompts,omitempty"`
	References  []string    `json:"references,omitempty"`
	Examples    []string    `json:"examples,omitempty"`
	Avoid       []string    `json:"avoid,omitempty"`
	Notes       string      `json:"notes,omitempty"`
	Rationale   string      `json:"rationale,omitempty"`
	ReviewLevel string      `json:"reviewLevel,omitempty"`
	Framework   string      `json:"framework,omitempty"`
	Mode        string      `json:"mode,omitempty"`
	Amendments  []Amendment `json:"amendments,omitempty"`
}

type Context struct {
	Purpose             string                        `json:"purpose"`
	UbiquitousLanguage  map[string]string             `json:"ubiquitousLanguage,omitempty"`
	Meta                Meta                          `json:"meta,omitempty"`
	Aggregates          map[string]Aggregate          `json:"aggregates,omitempty"`
	ValueObjects        map[string]ValueObject        `json:"valueObjects,omitempty"`
	DomainServices      map[string]DomainService      `json:"domainServices,omitempty"`
	ApplicationServices map[string]ApplicationService `json:"applicationServices,omitempty"`
	Repositories        map[string]Repository         `json:"repositories,omitempty"`
	Ports               map[string]Port               `json:"ports,omitempty"`
	Assets              map[string]Asset              `json:"assets,omitempty"`
}

type Aggregate struct {
	Root          bool                   `json:"root,omitempty"`
	Purpose       string                 `json:"purpose,omitempty"`
	State         map[string]string      `json:"state,omitempty"`
	Commands      FlexMap                `json:"commands,omitempty"`
	Events        FlexMap                `json:"events,omitempty"`
	Invariants    []string               `json:"invariants,omitempty"`
	Implements    string                 `json:"implements,omitempty"`
	Publishes     []string               `json:"publishes,omitempty"`
	Meta          Meta                   `json:"meta,omitempty"`
	Entities      map[string]Entity      `json:"entities,omitempty"`
	ValueObjects  map[string]ValueObject `json:"valueObjects,omitempty"`
	Validations   []Validation           `json:"validations,omitempty"`
	Assets        map[string]Asset       `json:"assets,omitempty"`
	ContributesTo []Contribution         `json:"contributesTo,omitempty"`
}

type Entity struct {
	State         map[string]string `json:"state,omitempty"`
	Meta          Meta              `json:"meta,omitempty"`
	Validations   []Validation      `json:"validations,omitempty"`
	ContributesTo []Contribution    `json:"contributesTo,omitempty"`
}

type ValueObject struct {
	From          string            `json:"from,omitempty"`
	State         map[string]string `json:"state,omitempty"`
	Description   string            `json:"description,omitempty"`
	Invariants    []string          `json:"invariants,omitempty"`
	Meta          Meta              `json:"meta,omitempty"`
	Validations   []Validation      `json:"validations,omitempty"`
	ContributesTo []Contribution    `json:"contributesTo,omitempty"`
}

type Port struct {
	Direction     string            `json:"direction,omitempty"`
	Contract      map[string]string `json:"contract,omitempty"`
	Consumes      []string          `json:"consumes,omitempty"`
	Meta          Meta              `json:"meta,omitempty"`
	ContributesTo []Contribution    `json:"contributesTo,omitempty"`
}

type Adapter struct {
	Implements    string          `json:"implements"`
	Layer         string          `json:"layer,omitempty"`
	Profile       BoundaryProfile `json:"profile,omitempty"`
	Meta          Meta            `json:"meta,omitempty"`
	Validations   []Validation    `json:"validations,omitempty"`
	ContributesTo []Contribution  `json:"contributesTo,omitempty"`
}

type Repository struct {
	Of            string            `json:"of"`
	Contract      map[string]string `json:"contract,omitempty"`
	Meta          Meta              `json:"meta,omitempty"`
	Validations   []Validation      `json:"validations,omitempty"`
	ContributesTo []Contribution    `json:"contributesTo,omitempty"`
}

type DomainService struct {
	Purpose       string         `json:"purpose,omitempty"`
	Uses          []string       `json:"uses,omitempty"`
	Consumes      []string       `json:"consumes,omitempty"`
	Publishes     []string       `json:"publishes,omitempty"`
	Meta          Meta           `json:"meta,omitempty"`
	Validations   []Validation   `json:"validations,omitempty"`
	ContributesTo []Contribution `json:"contributesTo,omitempty"`
}

type ApplicationService struct {
	Purpose       string               `json:"purpose,omitempty"`
	Uses          []string             `json:"uses,omitempty"`
	Operations    map[string]Operation `json:"operations,omitempty"`
	Meta          Meta                 `json:"meta,omitempty"`
	Validations   []Validation         `json:"validations,omitempty"`
	ContributesTo []Contribution       `json:"contributesTo,omitempty"`
}

type Operation struct {
	Input  map[string]string `json:"input,omitempty"`
	Output map[string]string `json:"output,omitempty"`
}

type AssetKind struct {
	Description string   `json:"description"`
	FilePattern string   `json:"filePattern,omitempty"`
	Prompts     []string `json:"prompts,omitempty"`
	References  []string `json:"references,omitempty"`
	Meta        Meta     `json:"meta,omitempty"`
}

type Asset struct {
	Kind          string         `json:"kind"`
	Description   string         `json:"description,omitempty"`
	Prompts       []string       `json:"prompts,omitempty"`
	Targets       []string       `json:"targets,omitempty"`
	Meta          Meta           `json:"meta,omitempty"`
	Validations   []Validation   `json:"validations,omitempty"`
	Profile       AssetProfile   `json:"profile,omitempty"`
	ContributesTo []Contribution `json:"contributesTo,omitempty"`
}

// Contribution connects an implementation resource to observable project
// functionality without changing resource dependency ordering.
type Contribution struct {
	Capability   string `json:"capability"`
	Contribution string `json:"contribution"`
}

// BoundaryProfile describes delivery mechanics; the implemented port remains
// the owner of the behavioral contract.
type BoundaryProfile struct {
	Kind          string   `json:"kind,omitempty"`
	Method        string   `json:"method,omitempty"`
	Path          string   `json:"path,omitempty"`
	Protocol      string   `json:"protocol,omitempty"`
	Topology      string   `json:"topology,omitempty"`
	Device        string   `json:"device,omitempty"`
	Medium        string   `json:"medium,omitempty"`
	System        string   `json:"system,omitempty"`
	Topic         string   `json:"topic,omitempty"`
	Trigger       string   `json:"trigger,omitempty"`
	Surfaces      []string `json:"surfaces,omitempty"`
	Accessibility []string `json:"accessibility,omitempty"`
}

// AssetProfile gives recurring operational artifacts a structured vocabulary
// while custom AssetKind continues to determine generated file shape.
type AssetProfile struct {
	Kind             string   `json:"kind,omitempty"`
	Ecosystem        string   `json:"ecosystem,omitempty"`
	Witness          string   `json:"witness,omitempty"`
	Source           string   `json:"source,omitempty"`
	SecretPolicy     string   `json:"secretPolicy,omitempty"`
	FailurePolicy    string   `json:"failurePolicy,omitempty"`
	Signals          []string `json:"signals,omitempty"`
	Constraint       string   `json:"constraint,omitempty"`
	Audience         string   `json:"audience,omitempty"`
	RequiredExamples []string `json:"requiredExamples,omitempty"`
	Predecessor      string   `json:"predecessor,omitempty"`
	Compatibility    string   `json:"compatibility,omitempty"`
	Rollback         string   `json:"rollback,omitempty"`
}

type Validation struct {
	ID               string      `json:"id,omitempty"`
	Scope            string      `json:"scope,omitempty"`
	Kind             string      `json:"kind"`
	Command          []string    `json:"command"`
	Description      string      `json:"description,omitempty"`
	WorkingDirectory string      `json:"workingDirectory,omitempty"`
	Timeout          string      `json:"timeout,omitempty"`
	Environment      []string    `json:"environment,omitempty"`
	Resources        []string    `json:"resources,omitempty"`
	Capabilities     []string    `json:"capabilities,omitempty"`
	Goals            []string    `json:"goals,omitempty"`
	Assertions       []Assertion `json:"assertions,omitempty"`
	Named            bool        `json:"-"`
}

// Witness is an executable, falsification-gated behavioral definition. Both
// commands are run by crest-spec and parsed through the same observation
// contract; callers cannot supply the observations accepted as evidence.
type Witness struct {
	ID               string                 `json:"id,omitempty"`
	Scope            string                 `json:"scope"`
	Goal             string                 `json:"goal,omitempty"`
	Capability       string                 `json:"capability,omitempty"`
	Resources        []string               `json:"resources,omitempty"`
	Evidence         []string               `json:"evidence,omitempty"`
	Command          []string               `json:"command"`
	NegativeCommand  []string               `json:"negativeCommand"`
	WorkingDirectory string                 `json:"workingDirectory,omitempty"`
	Timeout          string                 `json:"timeout,omitempty"`
	Environment      []string               `json:"environment,omitempty"`
	Artifacts        []string               `json:"artifacts,omitempty"`
	Observation      ObservationDeclaration `json:"observation"`
	Predicates       []WitnessPredicate     `json:"predicates"`
}

type ObservationDeclaration struct {
	Kind   string            `json:"kind"`
	Marker string            `json:"marker"`
	Schema map[string]string `json:"schema"`
}

type WitnessPredicate struct {
	Field  string   `json:"field"`
	Op     string   `json:"op"`
	Value  any      `json:"value,omitempty"`
	Min    *float64 `json:"min,omitempty"`
	Max    *float64 `json:"max,omitempty"`
	Member any      `json:"member,omitempty"`
}

type Assertion struct {
	Kind     string `json:"kind"`
	Expected int    `json:"expected,omitempty"`
	Path     string `json:"path,omitempty"`
	Pattern  string `json:"pattern,omitempty"`
	Regex    string `json:"regex,omitempty"`
}

// Amendment is a resource-scoped, spec-resident correction (e.g. distilled from
// a deep_review finding). Adding one to a resource's declaration changes its
// hash, which makes the planner re-generate that resource in UPDATE mode.
type Amendment struct {
	Name       string      `json:"name"`              // stable kebab-case id within the resource
	Prompt     string      `json:"prompt"`            // the targeted change instruction (data)
	Origin     string      `json:"origin,omitempty"`  // "deep_review" | "manual" | "bugbot" | ...
	Finding    *Finding    `json:"finding,omitempty"` // provenance
	Validation *Validation `json:"validation,omitempty"`
	Graduated  bool        `json:"graduated,omitempty"`
	CreatedAt  string      `json:"createdAt,omitempty"`
}

// Finding is the provenance of an amendment drawn from a review.
type Finding struct {
	Severity string `json:"severity,omitempty"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Text     string `json:"text,omitempty"`
}

type Invariant struct {
	Text string `json:"text"`
	Meta Meta   `json:"meta,omitempty"`
}

type ContextRelationship struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Kind      string `json:"kind"`
	Direction string `json:"direction,omitempty"`
}
