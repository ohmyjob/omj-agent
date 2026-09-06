package protocol

import "time"

// MachineMetadata is the part of the enroll and work requests that describes
// the Machine; both embed it so the two payloads cannot drift apart.
type MachineMetadata struct {
	Hostname      string   `json:"hostname"`
	OS            string   `json:"os"`
	OSVersion     string   `json:"os_version"`
	Arch          string   `json:"arch"`
	KernelVersion string   `json:"kernel_version"`
	AgentUser     string   `json:"agent_user"`
	AgentUID      int      `json:"agent_uid"`
	InsecureHTTP  bool     `json:"insecure_http"`
	ReportedIPs   []string `json:"reported_ips"`

	// RunAsAllowed is the operator's execution-user allowlist. It is reported
	// and never received: no response type carries it, so the Server chooses
	// from this list and can never add to it (PRD §21).
	RunAsAllowed []string `json:"run_as_allowed,omitempty"`
}

type EnrollRequest struct {
	Token string `json:"token"`
	MachineMetadata
	AgentVersion string  `json:"agent_version"`
	Name         *string `json:"name,omitempty"`
}

type EnrollResponse struct {
	MachineID  string    `json:"machine_id"`
	Credential string    `json:"credential"`
	ServerTime time.Time `json:"server_time"`
}

type PingResponse struct {
	MachineID     string    `json:"machine_id"`
	ServerVersion string    `json:"server_version"`
	ServerTime    time.Time `json:"server_time"`
}

type ActiveRun struct {
	RunID  string          `json:"run_id"`
	Status ActiveRunStatus `json:"status"`
}

type WorkRequest struct {
	WaitSeconds int         `json:"wait_seconds"`
	Slots       int         `json:"slots"`
	ActiveRuns  []ActiveRun `json:"active_runs"`
	MachineMetadata
}

type WorkResponse struct {
	Runs         []RunLease  `json:"runs"`
	CancelRunIDs []string    `json:"cancel_run_ids"`
	Config       AgentConfig `json:"config"`
}

type RunLease struct {
	RunID        string     `json:"run_id"`
	MachineID    string     `json:"machine_id"`
	JobID        string     `json:"job_id"`
	JobName      string     `json:"job_name"`
	Trigger      Trigger    `json:"trigger"`
	ScheduledFor *time.Time `json:"scheduled_for"`
	Command      string     `json:"command"`

	// RunAs names the user the work must run as, and null means the Agent's
	// own service user. The Agent only ever honours a name the operator put in
	// run_as_allowed, so this narrows what the Server may pick and can never
	// widen it (PRD §21).
	RunAs            *string           `json:"run_as"`
	Shell            *string           `json:"shell"`
	WorkingDirectory *string           `json:"working_directory"`
	Environment      map[string]string `json:"environment"`
	TimeoutSeconds   int               `json:"timeout_seconds"`
	MaxOutputBytes   int64             `json:"max_output_bytes"`
	LeaseExpiresAt   time.Time         `json:"lease_expires_at"`
}

type AgentConfig struct {
	HeartbeatIntervalSeconds int `json:"heartbeat_interval_seconds"`
	OutputFlushIntervalMS    int `json:"output_flush_interval_ms"`
	OutputChunkBytes         int `json:"output_chunk_bytes"`
	PollWaitSeconds          int `json:"poll_wait_seconds"`
}

type StartRequest struct {
	EffectiveTimeoutSeconds int   `json:"effective_timeout_seconds"`
	EffectiveMaxOutputBytes int64 `json:"effective_max_output_bytes"`
}

type StartResponse struct {
	Status RunStatus `json:"status"`
}

type OutputRequest struct {
	Chunks []OutputChunk `json:"chunks"`
}

type OutputChunk struct {
	Seq    uint64    `json:"seq"`
	Stream Stream    `json:"stream"`
	At     time.Time `json:"at"`
	Data   []byte    `json:"data"`
}

type OutputResponse struct {
	LastOutputSeq   uint64 `json:"last_output_seq"`
	Truncated       bool   `json:"truncated"`
	CancelRequested bool   `json:"cancel_requested"`
}

type HeartbeatRequest struct{}

type HeartbeatResponse struct {
	CancelRequested bool      `json:"cancel_requested"`
	ServerTime      time.Time `json:"server_time"`
}

type FinishRequest struct {
	Status          RunStatus     `json:"status"`
	ExitCode        *int          `json:"exit_code"`
	StartedAt       *time.Time    `json:"started_at"`
	FinishedAt      time.Time     `json:"finished_at"`
	LastOutputSeq   uint64        `json:"last_output_seq"`
	OutputTruncated bool          `json:"output_truncated"`
	Reason          *FinishReason `json:"reason"`
}

type FinishResponse struct {
	Status RunStatus `json:"status"`
}

type ErrorResponse struct {
	Error                     ErrorCode           `json:"error"`
	Message                   string              `json:"message"`
	Errors                    map[string][]string `json:"errors,omitempty"`
	SupportedProtocolVersions []int               `json:"supported_protocol_versions,omitempty"`
	MinAgentVersion           string              `json:"min_agent_version,omitempty"`
}
