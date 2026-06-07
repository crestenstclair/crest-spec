package plan

type ActionKind string

const (
	ActionCreate  ActionKind = "create"
	ActionModify  ActionKind = "modify"
	ActionDestroy ActionKind = "destroy"
	ActionDrift   ActionKind = "drift"
)

type PlannedAction struct {
	ResourceID   string
	Kind         ActionKind
	Reason       string
	CascadedFrom string
	Files        []string
}
