package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

var testUsers = map[string]int{"root": 0, "ohmyjob": 998, "deploy": 1001, "www-data": 33}

func testLookup(name string) (int, error) {
	uid, ok := testUsers[name]
	if !ok {
		return 0, errors.New("unknown user " + name)
	}

	return uid, nil
}

func TestResolveRunAs(t *testing.T) {
	unprivileged := RunAsHost{UID: 998, Username: "ohmyjob", Lookup: testLookup}
	asRoot := RunAsHost{UID: 0, Username: "root", Lookup: testLookup}

	tests := []struct {
		name      string
		allowed   []string
		host      RunAsHost
		wantNames []string
		wantErr   string
	}{
		{
			name:      "no allowlist is the agent's own user",
			host:      unprivileged,
			wantNames: []string{"ohmyjob"},
		},
		{
			name:      "the agent's own user leads the list",
			allowed:   []string{"deploy", "www-data"},
			host:      asRoot,
			wantNames: []string{"root", "deploy", "www-data"},
		},
		{
			name:      "the agent's own user is not repeated",
			allowed:   []string{"ohmyjob"},
			host:      unprivileged,
			wantNames: []string{"ohmyjob"},
		},
		{
			name:      "an unknown user",
			allowed:   []string{"backup"},
			host:      asRoot,
			wantNames: []string{"root"},
			wantErr:   `run_as_allowed lists "backup", which is not a user on this machine`,
		},
		{
			name:      "root without a root agent",
			allowed:   []string{"root"},
			host:      unprivileged,
			wantNames: []string{"ohmyjob"},
			wantErr:   "only an agent running as root can run work as root",
		},
		{
			name:      "another user without a root agent",
			allowed:   []string{"deploy"},
			host:      unprivileged,
			wantNames: []string{"ohmyjob"},
			wantErr:   "only an agent running as root can run work as another user",
		},
		{
			name:      "a root agent may name root",
			allowed:   []string{"root"},
			host:      asRoot,
			wantNames: []string{"root"},
		},
		{
			name:      "an agent that cannot name its own user reports nothing",
			host:      RunAsHost{UID: -1, Lookup: testLookup},
			wantNames: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runAs := ResolveRunAs(Config{RunAsAllowed: tt.allowed}, tt.host)

			err := runAs.Err()

			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Err() = %v, want nil", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("Err() = %v, want it to contain %q", err, tt.wantErr)
			}

			if got := runAs.Names(); !reflect.DeepEqual(got, tt.wantNames) {
				t.Errorf("Names() = %#v, want %#v", got, tt.wantNames)
			}
		})
	}
}

func TestResolveRunAsReportsEveryUser(t *testing.T) {
	runAs := ResolveRunAs(Config{RunAsAllowed: []string{"deploy", "backup"}}, RunAsHost{UID: 0, Username: "root", Lookup: testLookup})

	if len(runAs.Users) != 3 {
		t.Fatalf("Users = %+v, want three entries", runAs.Users)
	}

	if got := runAs.Users[1]; got.Name != "deploy" || got.UID != 1001 || got.Err != nil {
		t.Errorf("Users[1] = %+v, want deploy at uid 1001 with no error", got)
	}

	if got := runAs.Users[2]; got.Name != "backup" || got.Err == nil {
		t.Errorf("Users[2] = %+v, want backup with the reason it cannot be used", got)
	}
}

func TestLookupUser(t *testing.T) {
	if _, err := LookupUser("no-such-user-omj"); err == nil {
		t.Error("LookupUser found a user that does not exist")
	}

	if _, err := LookupUser("root"); err != nil {
		t.Errorf("LookupUser(root) error = %v", err)
	}
}
