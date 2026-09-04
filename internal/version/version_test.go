package version

import "testing"

func TestUserAgent(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "development build", version: "dev", want: "omj-agent/dev"},
		{name: "release", version: "1.2.3", want: "omj-agent/1.2.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previous := Version
			Version = tt.version
			t.Cleanup(func() { Version = previous })

			if got := UserAgent(); got != tt.want {
				t.Errorf("UserAgent() = %q, want %q", got, tt.want)
			}
		})
	}
}
