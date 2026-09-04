package protocol

import "testing"

func TestRunStatus(t *testing.T) {
	tests := []struct {
		status     RunStatus
		valid      bool
		terminal   bool
		reportable bool
	}{
		{RunStatusQueued, true, false, false},
		{RunStatusDispatched, true, false, false},
		{RunStatusRunning, true, false, false},
		{RunStatusSuccess, true, true, true},
		{RunStatusFailed, true, true, true},
		{RunStatusTimedOut, true, true, true},
		{RunStatusCancelled, true, true, true},
		{RunStatusLost, true, true, true},
		{RunStatusMissed, true, true, false},
		{RunStatus("exploded"), false, false, false},
		{RunStatus(""), false, false, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.Valid(); got != tt.valid {
				t.Errorf("Valid() = %v, want %v", got, tt.valid)
			}

			if got := tt.status.Terminal(); got != tt.terminal {
				t.Errorf("Terminal() = %v, want %v", got, tt.terminal)
			}

			if got := tt.status.Reportable(); got != tt.reportable {
				t.Errorf("Reportable() = %v, want %v", got, tt.reportable)
			}
		})
	}
}

func TestOtherEnums(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
		got   bool
	}{
		{"stdout stream", true, StreamStdout.Valid()},
		{"stderr stream", true, StreamStderr.Valid()},
		{"unknown stream", false, Stream("both").Valid()},
		{"spawn_failed reason", true, ReasonSpawnFailed.Valid()},
		{"agent_stopped reason", true, ReasonAgentStopped.Valid()},
		{"agent_restarted reason", true, ReasonAgentRestarted.Valid()},
		{"unknown reason", false, FinishReason("bored").Valid()},
		{"running active run", true, ActiveRunRunning.Valid()},
		{"unknown active run status", false, ActiveRunStatus("paused").Valid()},
		{"scheduled trigger", true, TriggerScheduled.Valid()},
		{"manual trigger", true, TriggerManual.Valid()},
		{"unknown trigger", false, Trigger("webhook").Valid()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.valid {
				t.Errorf("Valid() = %v, want %v", tt.got, tt.valid)
			}
		})
	}
}
