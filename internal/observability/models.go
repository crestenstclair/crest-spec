package observability

import "time"

const APIVersion = "v1"

type Link struct {
	Rel  string `json:"rel"`
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
	HREF string `json:"href"`
}

type RecordState struct {
	Legacy   bool   `json:"legacy"`
	Stale    bool   `json:"stale"`
	Redacted bool   `json:"redacted"`
	Reason   string `json:"reason,omitempty"`
}

type PageInfo struct {
	Limit      int    `json:"limit"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

type Page[T any] struct {
	Version string   `json:"version"`
	Items   []T      `json:"items"`
	Page    PageInfo `json:"page"`
	Links   []Link   `json:"links,omitempty"`
}

type ErrorEnvelope struct {
	Version string   `json:"version"`
	Error   APIError `json:"error"`
	Links   []Link   `json:"links,omitempty"`
}

type APIError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

type StatusExplanation struct {
	State           string   `json:"state"`
	Reason          string   `json:"reason"`
	Missing         []string `json:"missing,omitempty"`
	Blockers        []string `json:"blockers,omitempty"`
	Regressions     []string `json:"regressions,omitempty"`
	RecommendedNext []string `json:"recommended_next,omitempty"`
}

type ProjectOverview struct {
	Version            string             `json:"version"`
	Project            Project            `json:"project"`
	CompletionEnforced bool               `json:"completion_enforced"`
	Status             StatusExplanation  `json:"status"`
	Blockers           []Blocker          `json:"blockers"`
	RecentHistory      []StatusTransition `json:"recent_history"`
	Links              []Link             `json:"links"`
}

type Project struct {
	Name             string              `json:"name"`
	Mission          string              `json:"mission"`
	SpecHash         string              `json:"spec_hash"`
	CompletionStatus string              `json:"completion_status"`
	CompletionReason string              `json:"completion_reason"`
	Actors           []Actor             `json:"actors"`
	Goals            []GoalSummary       `json:"goals"`
	Capabilities     []CapabilitySummary `json:"capabilities"`
	RequiredGoals    []string            `json:"required_goals"`
	NonGoals         []NonGoal           `json:"non_goals"`
	State            RecordState         `json:"record_state"`
	Links            []Link              `json:"links"`
}

type Actor struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

type NonGoal struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

type GoalSummary struct {
	ID           string   `json:"id"`
	Description  string   `json:"description"`
	Priority     string   `json:"priority"`
	Status       string   `json:"status"`
	StatusReason string   `json:"status_reason"`
	DependsOn    []string `json:"depends_on"`
	Capabilities []string `json:"capabilities"`
	Resources    []string `json:"resources"`
	Links        []Link   `json:"links"`
}

type CapabilitySummary struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Goals       []string `json:"goals"`
	Resources   []string `json:"resources"`
	Links       []Link   `json:"links"`
}

type CapabilityDetail struct {
	Version      string               `json:"version"`
	Capability   CapabilitySummary    `json:"capability"`
	Goals        []GoalSummary        `json:"goals"`
	Requirements []Requirement        `json:"requirements"`
	Acceptance   []AcceptanceScenario `json:"acceptance"`
	Resources    []ResourceSummary    `json:"resources"`
	Status       StatusExplanation    `json:"status"`
	Links        []Link               `json:"links"`
}

type GoalDetail struct {
	Version      string               `json:"version"`
	Goal         GoalSummary          `json:"goal"`
	Actors       []Actor              `json:"actors"`
	Requirements []Requirement        `json:"requirements"`
	Acceptance   []AcceptanceScenario `json:"acceptance"`
	Evidence     []EvidenceStatus     `json:"evidence"`
	Blockers     []Blocker            `json:"blockers"`
	History      []StatusTransition   `json:"history"`
	Status       StatusExplanation    `json:"status"`
	Links        []Link               `json:"links"`
}

type Requirement struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Description  string   `json:"description"`
	Goals        []string `json:"goals"`
	Capabilities []string `json:"capabilities"`
}

type AcceptanceScenario struct {
	ID           string           `json:"id"`
	CapabilityID string           `json:"capability_id"`
	ActorID      string           `json:"actor_id"`
	Description  string           `json:"description"`
	Steps        []AcceptanceStep `json:"steps"`
	Evidence     []string         `json:"evidence"`
}

type AcceptanceStep struct {
	Action   string `json:"action"`
	Observes string `json:"observes"`
}

type EvidenceStatus struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	Description    string `json:"description"`
	Currency       string `json:"currency"`
	Classification string `json:"classification,omitempty"`
	SourceTreeHash string `json:"source_tree_hash,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	Links          []Link `json:"links"`
}

type Blocker struct {
	ID         string    `json:"id"`
	GoalID     string    `json:"goal_id,omitempty"`
	Category   string    `json:"category"`
	Reason     string    `json:"reason"`
	SourceType string    `json:"source_type,omitempty"`
	SourceID   string    `json:"source_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	Links      []Link    `json:"links"`
}

type StatusTransition struct {
	ID         string    `json:"id"`
	FromStatus string    `json:"from_status"`
	ToStatus   string    `json:"to_status"`
	Reason     string    `json:"reason"`
	SourceType string    `json:"source_type"`
	SourceID   string    `json:"source_id"`
	SessionID  string    `json:"session_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	Links      []Link    `json:"links"`
}

type ResourceSummary struct {
	ID              string      `json:"id"`
	Kind            string      `json:"kind"`
	ContextName     string      `json:"context_name"`
	DeclarationHash string      `json:"declaration_hash"`
	EffectiveHash   string      `json:"effective_hash"`
	Model           string      `json:"model"`
	SettledAt       time.Time   `json:"settled_at"`
	Goals           []string    `json:"goals"`
	Capabilities    []string    `json:"capabilities"`
	State           RecordState `json:"record_state"`
	Links           []Link      `json:"links"`
}

type ResourceDetail struct {
	Version       string            `json:"version"`
	Resource      ResourceSummary   `json:"resource"`
	Contributions []Contribution    `json:"contributions"`
	Dependencies  []RelationshipRef `json:"dependencies"`
	Consumers     []RelationshipRef `json:"consumers"`
	Files         []FileRef         `json:"files"`
	Attempts      []AttemptRef      `json:"attempts"`
	Validations   []ValidationRef   `json:"validations"`
	Status        StatusExplanation `json:"status"`
	Links         []Link            `json:"links"`
}

type Contribution struct {
	CapabilityID string `json:"capability_id"`
	Description  string `json:"description"`
	Links        []Link `json:"links"`
}

type RelationshipRef struct {
	ResourceID string `json:"resource_id"`
	Kind       string `json:"kind"`
	Links      []Link `json:"links"`
}

type FileRef struct {
	Path        string    `json:"path"`
	ContentHash string    `json:"content_hash"`
	PromptHash  string    `json:"prompt_hash"`
	Model       string    `json:"model"`
	CreatedAt   time.Time `json:"created_at"`
}

type AttemptRef struct {
	ID                string    `json:"id"`
	RetryNumber       int       `json:"retry_number"`
	Role              string    `json:"role"`
	Status            string    `json:"status"`
	ContextManifestID string    `json:"context_manifest_id,omitempty"`
	ExecutionID       string    `json:"execution_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	Links             []Link    `json:"links"`
}

type ValidationRef struct {
	ID             string    `json:"id"`
	DefinitionID   string    `json:"definition_id"`
	Classification string    `json:"classification"`
	SourceTreeHash string    `json:"source_tree_hash"`
	Currency       string    `json:"currency"`
	CreatedAt      time.Time `json:"created_at"`
	Links          []Link    `json:"links"`
}
