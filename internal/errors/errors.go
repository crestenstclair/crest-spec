package errors

type New string

func (e New) Error() string { return string(e) }

const (
	ErrNotFound     = New("not found")
	ErrAlreadyDone  = New("already done")
	ErrLocked       = New("apply lock held")
	ErrInvalidState = New("invalid state transition")
)
