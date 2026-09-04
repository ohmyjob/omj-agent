package config

import "testing"

func TestDefaultPaths(t *testing.T) {
	tests := []struct {
		name      string
		configDir string
		stateDir  string
		want      Paths
	}{
		{
			name: "system locations",
			want: Paths{
				ConfigDir:      "/etc/ohmyjob",
				ConfigFile:     "/etc/ohmyjob/agent.conf",
				CredentialFile: "/etc/ohmyjob/agent.credential",
				StateDir:       "/var/lib/ohmyjob",
				StateFile:      "/var/lib/ohmyjob/state.json",
			},
		},
		{
			name:      "environment overrides",
			configDir: "/tmp/omj/etc",
			stateDir:  "/tmp/omj/state",
			want: Paths{
				ConfigDir:      "/tmp/omj/etc",
				ConfigFile:     "/tmp/omj/etc/agent.conf",
				CredentialFile: "/tmp/omj/etc/agent.credential",
				StateDir:       "/tmp/omj/state",
				StateFile:      "/tmp/omj/state/state.json",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OMJ_CONFIG_DIR", tt.configDir)
			t.Setenv("OMJ_STATE_DIR", tt.stateDir)

			if got := DefaultPaths(); got != tt.want {
				t.Errorf("DefaultPaths() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
