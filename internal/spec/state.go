package spec

type ResourceState string

const (
	StatePending    ResourceState = "pending"
	StateDispatched ResourceState = "dispatched"
	StateCompleted  ResourceState = "completed"
	StateCommitted  ResourceState = "committed"
	StateBlocked    ResourceState = "blocked"
	StateErrored    ResourceState = "errored"
	StateTimedOut   ResourceState = "timed_out"
	StateRejected   ResourceState = "rejected"
	StateSkipped    ResourceState = "skipped"
)

func (s ResourceState) IsTerminal() bool {
	return s == StateCommitted || s == StateSkipped
}

func (s ResourceState) NeedsResolution() bool {
	return s == StateBlocked || s == StateErrored || s == StateTimedOut || s == StateRejected
}

type ResourceStatus struct {
	ResourceID   string
	State        ResourceState
	WaveIndex    int
	Error        *ErrorContext
	Blocked      *BlockedContext
	Attempts     int
	MaxRetries   int
	Files        []CodeBlock
	Notes        string
	UserGuidance string
}

type ErrorContext struct {
	Kind        string
	Message     string
	Files       []string
	RetryCount  int
	MaxRetries  int
	LastAttempt string
	Suggestion  string
}

type BlockedContext struct {
	Reason    string
	BlockedOn string
	Question  string
	Options   []string
}
