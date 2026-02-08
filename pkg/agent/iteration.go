package agent

// MaxConsecutiveTimeouts is the threshold for stopping iteration.
// After this many consecutive timeout failures, the controller aborts.
const MaxConsecutiveTimeouts = 2

// IterationState tracks loop state across iterations.
// Shared by ReActController and NativeThinkingController.
type IterationState struct {
	CurrentIteration           int
	MaxIterations              int
	LastInteractionFailed      bool
	LastErrorMessage           string
	ConsecutiveTimeoutFailures int
}

// ShouldAbortOnTimeouts returns true if consecutive timeout failures
// have reached the threshold.
func (s *IterationState) ShouldAbortOnTimeouts() bool {
	return s.ConsecutiveTimeoutFailures >= MaxConsecutiveTimeouts
}

// RecordSuccess resets failure tracking after a successful interaction.
func (s *IterationState) RecordSuccess() {
	s.LastInteractionFailed = false
	s.LastErrorMessage = ""
	s.ConsecutiveTimeoutFailures = 0
}

// RecordFailure records a failed interaction.
func (s *IterationState) RecordFailure(errMsg string, isTimeout bool) {
	s.LastInteractionFailed = true
	s.LastErrorMessage = errMsg
	if isTimeout {
		s.ConsecutiveTimeoutFailures++
	} else {
		s.ConsecutiveTimeoutFailures = 0
	}
}
