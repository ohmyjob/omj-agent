package protocol

type RunStatus string

const (
	RunStatusQueued     RunStatus = "queued"
	RunStatusDispatched RunStatus = "dispatched"
	RunStatusRunning    RunStatus = "running"
	RunStatusSuccess    RunStatus = "success"
	RunStatusFailed     RunStatus = "failed"
	RunStatusTimedOut   RunStatus = "timed_out"
	RunStatusCancelled  RunStatus = "cancelled"
	RunStatusLost       RunStatus = "lost"
	RunStatusMissed     RunStatus = "missed"
)

func (s RunStatus) Valid() bool {
	switch s {
	case RunStatusQueued, RunStatusDispatched, RunStatusRunning, RunStatusSuccess,
		RunStatusFailed, RunStatusTimedOut, RunStatusCancelled, RunStatusLost, RunStatusMissed:
		return true
	}

	return false
}

func (s RunStatus) Terminal() bool {
	switch s {
	case RunStatusSuccess, RunStatusFailed, RunStatusTimedOut, RunStatusCancelled, RunStatusLost, RunStatusMissed:
		return true
	}

	return false
}

// Reportable says whether finish accepts the status.
func (s RunStatus) Reportable() bool {
	switch s {
	case RunStatusSuccess, RunStatusFailed, RunStatusTimedOut, RunStatusCancelled, RunStatusLost:
		return true
	}

	return false
}

type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

func (s Stream) Valid() bool {
	return s == StreamStdout || s == StreamStderr
}

type FinishReason string

const (
	ReasonSpawnFailed    FinishReason = "spawn_failed"
	ReasonAgentStopped   FinishReason = "agent_stopped"
	ReasonAgentRestarted FinishReason = "agent_restarted"

	// The Server validates run_as against the list this Agent reported, so a
	// lease it still refuses means the two have drifted apart rather than that
	// a command failed to start (PRD §21).
	ReasonRunAsNotPermitted FinishReason = "run_as_not_permitted"
)

func (r FinishReason) Valid() bool {
	switch r {
	case ReasonSpawnFailed, ReasonAgentStopped, ReasonAgentRestarted, ReasonRunAsNotPermitted:
		return true
	}

	return false
}

type ActiveRunStatus string

const ActiveRunRunning ActiveRunStatus = "running"

func (s ActiveRunStatus) Valid() bool {
	return s == ActiveRunRunning
}

type Trigger string

const (
	TriggerScheduled Trigger = "scheduled"
	TriggerManual    Trigger = "manual"
)

func (t Trigger) Valid() bool {
	return t == TriggerScheduled || t == TriggerManual
}

type ErrorCode string

const (
	ErrInvalidCredential   ErrorCode = "invalid_credential"
	ErrTokenInvalid        ErrorCode = "token_invalid"
	ErrRunNotFound         ErrorCode = "run_not_found"
	ErrLeaseExpired        ErrorCode = "lease_expired"
	ErrNotLeased           ErrorCode = "not_leased"
	ErrRunCancelled        ErrorCode = "run_cancelled"
	ErrRunFinished         ErrorCode = "run_finished"
	ErrRunNotRunning       ErrorCode = "run_not_running"
	ErrTokenExpired        ErrorCode = "token_expired"
	ErrPayloadTooLarge     ErrorCode = "payload_too_large"
	ErrValidationFailed    ErrorCode = "validation_failed"
	ErrUnsupportedOS       ErrorCode = "unsupported_os"
	ErrUnsupportedProtocol ErrorCode = "unsupported_protocol"
	ErrAgentTooOld         ErrorCode = "agent_too_old"
	ErrThrottled           ErrorCode = "throttled"
)
