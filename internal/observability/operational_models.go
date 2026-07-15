package observability

import "time"

type PlanView struct {
	Version         string            `json:"version"`
	ProjectName     string            `json:"project_name"`
	SpecHash        string            `json:"spec_hash"`
	Status          StatusExplanation `json:"status"`
	ActiveSessionID string            `json:"active_session_id,omitempty"`
	Slices          []PlanSlice       `json:"slices"`
	Operations      []PlanOperation   `json:"operations"`
	Waves           []PlanWave        `json:"waves"`
	State           RecordState       `json:"record_state"`
	Links           []Link            `json:"links"`
}

type PlanSlice struct {
	Capability       string   `json:"capability"`
	Goals            []string `json:"goals"`
	Required         bool     `json:"required"`
	CurrentGap       string   `json:"current_gap"`
	ExpectedBehavior []string `json:"expected_behavior"`
	ExpectedEvidence []string `json:"expected_evidence"`
	OperationIDs     []string `json:"operation_ids"`
	Status           string   `json:"status"`
	Links            []Link   `json:"links"`
}

type PlanOperation struct {
	ID                string            `json:"id"`
	ResourceID        string            `json:"resource_id"`
	Kind              string            `json:"kind"`
	Category          string            `json:"category"`
	Reason            string            `json:"reason"`
	CascadedFrom      string            `json:"cascaded_from,omitempty"`
	WaveIndex         int               `json:"wave_index"`
	RecommendedRole   string            `json:"recommended_role"`
	Goals             []string          `json:"goals"`
	Capabilities      []string          `json:"capabilities"`
	Contributions     map[string]string `json:"contributions"`
	ExpectedBehavior  []string          `json:"expected_behavior"`
	ExpectedEvidence  []string          `json:"expected_evidence"`
	Shared            bool              `json:"shared_infrastructure"`
	ExecutionStatus   string            `json:"execution_status"`
	ExecutionAttempts int               `json:"execution_attempts"`
	LastError         string            `json:"last_error,omitempty"`
	Links             []Link            `json:"links"`
}

type PlanWave struct {
	Index      int            `json:"index"`
	Operations []string       `json:"operations"`
	Resources  []string       `json:"resources"`
	State      string         `json:"state"`
	Counts     map[string]int `json:"counts"`
	Links      []Link         `json:"links"`
}

type ImpactView struct {
	Version           string   `json:"version"`
	SourceResource    string   `json:"source_resource"`
	AffectedResources []string `json:"affected_resources"`
	Capabilities      []string `json:"capabilities"`
	Goals             []string `json:"goals"`
	Evidence          []string `json:"evidence"`
	RegressedGoals    []string `json:"regressed_goals"`
	Explanation       []string `json:"explanation"`
	Links             []Link   `json:"links"`
}

type FailureSummary struct {
	ID                string      `json:"id"`
	AttemptID         string      `json:"attempt_id"`
	ExecutionID       string      `json:"execution_id,omitempty"`
	ResourceID        string      `json:"resource_id"`
	Category          string      `json:"category"`
	Origin            string      `json:"origin"`
	Confidence        float64     `json:"confidence"`
	EvidenceSource    string      `json:"evidence_source"`
	EvidenceReference string      `json:"evidence_reference"`
	EvidenceHash      string      `json:"evidence_hash,omitempty"`
	CorrectiveAction  string      `json:"corrective_action"`
	NextRole          string      `json:"next_role,omitempty"`
	BlocksRetry       bool        `json:"blocks_retry"`
	GoalIDs           []string    `json:"goal_ids"`
	Resolution        string      `json:"resolution,omitempty"`
	ResolvedByAttempt string      `json:"resolved_by_attempt,omitempty"`
	CreatedAt         time.Time   `json:"created_at"`
	ResolvedAt        *time.Time  `json:"resolved_at,omitempty"`
	State             RecordState `json:"record_state"`
	Links             []Link      `json:"links"`
}

type FailureDetail struct {
	Version  string            `json:"version"`
	Failure  FailureSummary    `json:"failure"`
	Evidence string            `json:"evidence,omitempty"`
	Status   StatusExplanation `json:"status"`
	Links    []Link            `json:"links"`
}

type AttemptDetail struct {
	Version     string            `json:"version"`
	Attempt     AttemptSummary    `json:"attempt"`
	Context     *AttemptContext   `json:"context,omitempty"`
	Execution   *AttemptExecution `json:"execution,omitempty"`
	Candidate   *AttemptCandidate `json:"candidate,omitempty"`
	Validations []ValidationRef   `json:"validations"`
	Failures    []FailureSummary  `json:"failures"`
	Status      StatusExplanation `json:"status"`
	Links       []Link            `json:"links"`
}

type AttemptSummary struct {
	ID              string    `json:"id"`
	SessionID       string    `json:"session_id"`
	ResourceID      string    `json:"resource_id"`
	PlanOperationID string    `json:"plan_operation_id"`
	ParentAttemptID string    `json:"parent_attempt_id,omitempty"`
	RetryNumber     int       `json:"retry_number"`
	Role            string    `json:"role"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	Links           []Link    `json:"links"`
}

type AttemptContext struct {
	ID                string            `json:"id"`
	ContextHash       string            `json:"context_hash"`
	SelectorVersion   string            `json:"selector_version"`
	EstimatorVersion  string            `json:"estimator_version"`
	SelectionStrategy string            `json:"selection_strategy"`
	BudgetTokens      int               `json:"budget_tokens"`
	EstimatedTokens   int               `json:"estimated_tokens"`
	TemplateHashes    map[string]string `json:"template_hashes"`
	Blocked           bool              `json:"blocked"`
	BlockedReason     string            `json:"blocked_reason,omitempty"`
	Links             []Link            `json:"links"`
}

type AttemptExecution struct {
	ID                string         `json:"id"`
	HostName          string         `json:"host_name"`
	HostVersion       string         `json:"host_version"`
	Provider          string         `json:"provider"`
	Model             string         `json:"model"`
	Role              string         `json:"role"`
	ContextPolicy     string         `json:"context_policy"`
	InferenceHash     string         `json:"inference_hash"`
	AgentConfigHash   string         `json:"agent_config_hash"`
	ToolHash          string         `json:"tool_hash"`
	TemplateHash      string         `json:"template_hash"`
	InstructionsHash  string         `json:"instructions_hash"`
	HostCommitRef     string         `json:"host_commit_ref,omitempty"`
	Status            string         `json:"status"`
	DispositionReason string         `json:"disposition_reason,omitempty"`
	DurationMS        int64          `json:"duration_ms"`
	InputTokens       int64          `json:"input_tokens"`
	OutputTokens      int64          `json:"output_tokens"`
	CostUSD           float64        `json:"cost_usd"`
	GoalProgress      map[string]any `json:"goal_progress,omitempty"`
	Links             []Link         `json:"links"`
}

type AttemptCandidate struct {
	ID                string             `json:"id"`
	CandidateHash     string             `json:"candidate_hash"`
	Status            string             `json:"status"`
	DispositionReason string             `json:"disposition_reason,omitempty"`
	Files             []CandidateFileRef `json:"files"`
	Links             []Link             `json:"links"`
}

type CandidateFileRef struct {
	Path        string `json:"path"`
	ContentHash string `json:"content_hash"`
	ByteSize    int    `json:"byte_size"`
	WriteIntent string `json:"write_intent"`
}

type AttemptComparison struct {
	Version string                    `json:"version"`
	Left    AttemptDetail             `json:"left"`
	Right   AttemptDetail             `json:"right"`
	Changes []AttemptComparisonChange `json:"changes"`
	Summary []string                  `json:"summary"`
	Links   []Link                    `json:"links"`
}

type AttemptComparisonChange struct {
	Area    string `json:"area"`
	Changed bool   `json:"changed"`
	Before  string `json:"before,omitempty"`
	After   string `json:"after,omitempty"`
	Reason  string `json:"reason"`
}
