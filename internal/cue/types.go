package cue

type Project struct {
	Name       string                `json:"name"`
	Layers     []string              `json:"layers"`
	LayerRules map[string]LayerRule  `json:"layerRules"`
	Meta       Meta                  `json:"meta"`
	Contexts   map[string]Context    `json:"contexts"`
	Adapters   map[string]Adapter    `json:"adapters"`
	AssetKinds map[string]AssetKind  `json:"assetKinds"`
	Assets     map[string]Asset      `json:"assets"`
	Invariants []Invariant           `json:"invariants"`
	ContextMap []ContextRelationship `json:"contextMap"`
}

type LayerRule struct {
	DependsOn []string `json:"dependsOn"`
}

type Meta struct {
	Language    string   `json:"language,omitempty"`
	Style       string   `json:"style,omitempty"`
	Rules       []string `json:"rules,omitempty"`
	Prompts     []string `json:"prompts,omitempty"`
	References  []string `json:"references,omitempty"`
	Examples    []string `json:"examples,omitempty"`
	Avoid       []string `json:"avoid,omitempty"`
	Notes       string   `json:"notes,omitempty"`
	Rationale   string   `json:"rationale,omitempty"`
	ReviewLevel string   `json:"reviewLevel,omitempty"`
	Framework   string   `json:"framework,omitempty"`
}

type Context struct {
	Purpose              string                        `json:"purpose"`
	UbiquitousLanguage   map[string]string             `json:"ubiquitousLanguage,omitempty"`
	Meta                 Meta                          `json:"meta,omitempty"`
	Aggregates           map[string]Aggregate          `json:"aggregates,omitempty"`
	ValueObjects         map[string]ValueObject        `json:"valueObjects,omitempty"`
	DomainServices       map[string]DomainService      `json:"domainServices,omitempty"`
	ApplicationServices  map[string]ApplicationService `json:"applicationServices,omitempty"`
	Repositories         map[string]Repository         `json:"repositories,omitempty"`
	Ports                map[string]Port               `json:"ports,omitempty"`
	Assets               map[string]Asset              `json:"assets,omitempty"`
}

type Aggregate struct {
	Root         bool                         `json:"root,omitempty"`
	Purpose      string                       `json:"purpose,omitempty"`
	State        map[string]string            `json:"state,omitempty"`
	Commands     map[string]map[string]string `json:"commands,omitempty"`
	Events       map[string]map[string]string `json:"events,omitempty"`
	Invariants   []string                     `json:"invariants,omitempty"`
	Implements   string                       `json:"implements,omitempty"`
	Meta         Meta                         `json:"meta,omitempty"`
	Entities     map[string]Entity            `json:"entities,omitempty"`
	ValueObjects map[string]ValueObject       `json:"valueObjects,omitempty"`
	Validations  []Validation                 `json:"validations,omitempty"`
	Assets       map[string]Asset             `json:"assets,omitempty"`
}

type Entity struct {
	State       map[string]string `json:"state,omitempty"`
	Meta        Meta              `json:"meta,omitempty"`
	Validations []Validation      `json:"validations,omitempty"`
}

type ValueObject struct {
	From        string            `json:"from,omitempty"`
	State       map[string]string `json:"state,omitempty"`
	Description string            `json:"description,omitempty"`
	Invariants  []string          `json:"invariants,omitempty"`
	Meta        Meta              `json:"meta,omitempty"`
	Validations []Validation      `json:"validations,omitempty"`
}

type Port struct {
	Contract map[string]string `json:"contract,omitempty"`
	Meta     Meta              `json:"meta,omitempty"`
}

type Adapter struct {
	Implements  string       `json:"implements"`
	Layer       string       `json:"layer,omitempty"`
	Meta        Meta         `json:"meta,omitempty"`
	Validations []Validation `json:"validations,omitempty"`
}

type Repository struct {
	Of          string            `json:"of"`
	Contract    map[string]string `json:"contract,omitempty"`
	Meta        Meta              `json:"meta,omitempty"`
	Validations []Validation      `json:"validations,omitempty"`
}

type DomainService struct {
	Purpose     string       `json:"purpose,omitempty"`
	Uses        []string     `json:"uses,omitempty"`
	Meta        Meta         `json:"meta,omitempty"`
	Validations []Validation `json:"validations,omitempty"`
}

type ApplicationService struct {
	Purpose     string               `json:"purpose,omitempty"`
	Uses        []string             `json:"uses,omitempty"`
	Operations  map[string]Operation `json:"operations,omitempty"`
	Meta        Meta                 `json:"meta,omitempty"`
	Validations []Validation         `json:"validations,omitempty"`
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
	Kind        string       `json:"kind"`
	Description string       `json:"description,omitempty"`
	Prompts     []string     `json:"prompts,omitempty"`
	Targets     []string     `json:"targets,omitempty"`
	Meta        Meta         `json:"meta,omitempty"`
	Validations []Validation `json:"validations,omitempty"`
}

type Validation struct {
	Kind        string      `json:"kind"`
	Command     []string    `json:"command"`
	Description string      `json:"description,omitempty"`
	Assertions  []Assertion `json:"assertions,omitempty"`
}

type Assertion struct {
	Kind     string `json:"kind"`
	Expected int    `json:"expected,omitempty"`
	Path     string `json:"path,omitempty"`
	Pattern  string `json:"pattern,omitempty"`
	Regex    string `json:"regex,omitempty"`
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
