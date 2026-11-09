package minisweagent

import "errors"

// InterruptKind identifies why the agent flow was interrupted.
type InterruptKind string

const (
	// Submitted is raised when the agent has completed its task.
	Submitted InterruptKind = "Submitted"
	// LimitsExceeded is raised when the agent has exceeded its cost or step limit.
	LimitsExceeded InterruptKind = "LimitsExceeded"
	// TimeExceeded is raised when the agent has exceeded its wall-clock time limit (a kind of LimitsExceeded).
	TimeExceeded InterruptKind = "TimeExceeded"
	// UserInterruption is raised when the user interrupts the agent.
	UserInterruption InterruptKind = "UserInterruption"
	// FormatError is raised when the LM's output is not in the expected format.
	FormatError InterruptKind = "FormatError"
)

// Interrupt is returned to interrupt the agent flow and add messages to the trajectory.
type Interrupt struct {
	Kind     InterruptKind
	Messages []Message
}

func NewInterrupt(kind InterruptKind, messages ...Message) *Interrupt {
	return &Interrupt{Kind: kind, Messages: messages}
}

func (e *Interrupt) Error() string { return string(e.Kind) }

// AsInterrupt returns the *Interrupt wrapped in err, if any.
func AsInterrupt(err error) (*Interrupt, bool) {
	var interrupt *Interrupt
	ok := errors.As(err, &interrupt)
	return interrupt, ok
}

// ExitMessage creates the final "exit" message of a trajectory.
func ExitMessage(status, submission string) Message {
	content := submission
	if status != string(Submitted) {
		content = status
	}
	return Message{"role": "exit", "content": content, "extra": map[string]any{"exit_status": status, "submission": submission}}
}
