package plan

type ActionKind string

const (
	ActionCreate  ActionKind = "create"
	ActionModify  ActionKind = "modify"
	ActionDestroy ActionKind = "destroy"
)

type PlannedAction struct {
	ResourceID           string
	Kind                 ActionKind
	Reason               string
	CascadedFrom         string
	Files                []string
	OperationID          string            `json:"operation_id"`
	Category             string            `json:"category"`
	Capabilities         []string          `json:"capabilities,omitempty"`
	Goals                []string          `json:"goals,omitempty"`
	Contributions        map[string]string `json:"contributions,omitempty"`
	ExpectedBehavior     []string          `json:"expected_behavior,omitempty"`
	ExpectedEvidence     []string          `json:"expected_evidence,omitempty"`
	SharedInfrastructure bool              `json:"shared_infrastructure,omitempty"`
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
