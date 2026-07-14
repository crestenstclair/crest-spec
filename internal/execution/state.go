package execution

import "fmt"

type Status string

const (
	StatusExecuting          Status = "executing"
	StatusCandidateSubmitted Status = "candidate_submitted"
	StatusValidating         Status = "validating"
	StatusAccepted           Status = "accepted"
	StatusRejected           Status = "rejected"
	StatusCompleted          Status = "completed"
	StatusCancelled          Status = "cancelled"
	StatusFailed             Status = "failed"
	StatusTimedOut           Status = "timed_out"
)

var transitions = map[Status]map[Status]bool{
	StatusExecuting: {
		StatusCandidateSubmitted: true, StatusCompleted: true, StatusCancelled: true,
		StatusFailed: true, StatusTimedOut: true,
	},
	StatusCandidateSubmitted: {StatusValidating: true, StatusCancelled: true, StatusFailed: true, StatusTimedOut: true},
	StatusValidating:         {StatusAccepted: true, StatusRejected: true, StatusFailed: true, StatusTimedOut: true},
}

func ValidateTransition(from, to Status) error {
	if from == to {
		return nil
	}
	if !transitions[from][to] {
		return fmt.Errorf("invalid execution transition %q -> %q", from, to)
	}
	return nil
}

func (s Status) Terminal() bool {
	switch s {
	case StatusAccepted, StatusRejected, StatusCompleted, StatusCancelled, StatusFailed, StatusTimedOut:
		return true
	default:
		return false
	}
}
