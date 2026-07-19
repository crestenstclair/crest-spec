package plan

type ActionKind string

const (
	ActionCreate  ActionKind = "create"
	ActionModify  ActionKind = "modify"
	ActionDestroy ActionKind = "destroy"
)

const (
	ExecutionStrategyGenerate         = "generate"
	ExecutionStrategyValidateExisting = "validate_existing"
	ExecutionStrategyReuseCandidate   = "reuse_candidate"
)

type PlannedAction struct {
	ResourceID   string
	Kind         ActionKind
	Reason       string
	CascadedFrom string
	Files        []string
	OperationID  string `json:"operation_id"`
	// The generation, dependency, and narrow repair-verification fingerprints are part of the immutable
	// operation identity. A context prepared from an older CUE declaration must
	// never be accepted under fingerprints loaded after that context was built.
	GenerationInputHash      string            `json:"generation_input_hash,omitempty"`
	DependencyContractHash   string            `json:"dependency_contract_hash,omitempty"`
	VerificationContractHash string            `json:"verification_contract_hash,omitempty"`
	Category                 string            `json:"category"`
	RecommendedRole          string            `json:"recommended_role"`
	Capabilities             []string          `json:"capabilities,omitempty"`
	Goals                    []string          `json:"goals,omitempty"`
	Contributions            map[string]string `json:"contributions,omitempty"`
	ExpectedBehavior         []string          `json:"expected_behavior,omitempty"`
	ExpectedEvidence         []string          `json:"expected_evidence,omitempty"`
	SharedInfrastructure     bool              `json:"shared_infrastructure,omitempty"`
	ExecutionStrategy        string            `json:"execution_strategy"`
	SourceCandidateID        string            `json:"source_candidate_id,omitempty"`
}

type CapabilitySlice struct {
	Capability       string   `json:"capability"`
	Goals            []string `json:"goals"`
	Required         bool     `json:"required"`
	CurrentGap       string   `json:"current_gap"`
	ExpectedBehavior []string `json:"expected_behavior"`
	ExpectedEvidence []string `json:"expected_evidence"`
	OperationIDs     []string `json:"operation_ids"`
}
