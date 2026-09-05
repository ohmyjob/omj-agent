package sysinfo

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/ohmyjob/omj-agent/internal/protocol"
	"github.com/ohmyjob/omj-agent/internal/version"
)

func TestWorkMetadata(t *testing.T) {
	info := Info{Hostname: "nas01", OS: "linux", OSVersion: "Debian GNU/Linux 12", Arch: "arm64", KernelVersion: "6.1.0", ReportedIPs: []string{"192.168.1.20"}, AgentUser: "ohmyjob", AgentUID: 998}

	got := info.WorkMetadata(true, []string{"ohmyjob", "deploy"})

	want := protocol.MachineMetadata{Hostname: "nas01", OS: "linux", OSVersion: "Debian GNU/Linux 12", Arch: "arm64", KernelVersion: "6.1.0", AgentUser: "ohmyjob", AgentUID: 998, InsecureHTTP: true, ReportedIPs: []string{"192.168.1.20"}, RunAsAllowed: []string{"ohmyjob", "deploy"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WorkMetadata() = %+v, want %+v", got, want)
	}
}

func TestWorkMetadataOmitsAnUnknownAllowlist(t *testing.T) {
	encoded, err := json.Marshal(Info{Hostname: "nas01"}.WorkMetadata(false, nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if strings.Contains(string(encoded), "run_as_allowed") {
		t.Errorf("marshal = %s, want no run_as_allowed", encoded)
	}
}

func TestWorkMetadataEncodesMissingAddressesAsAnEmptyList(t *testing.T) {
	encoded, err := json.Marshal(Info{Hostname: "nas01"}.WorkMetadata(false, nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if ips, ok := document["reported_ips"].([]any); !ok || len(ips) != 0 {
		t.Errorf("reported_ips = %v, want []", document["reported_ips"])
	}
}

func TestEnrollRequest(t *testing.T) {
	tests := []struct {
		name     string
		machine  string
		wantName *string
	}{
		{name: "named", machine: "nas-basement", wantName: ptr("nas-basement")},
		{name: "unnamed defaults to the hostname on the server", machine: "", wantName: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := Info{Hostname: "nas01", OS: "linux", Arch: "arm64", AgentUID: UnknownUID}

			got := info.EnrollRequest("omj_enroll_token", tt.machine, false, []string{"ohmyjob"})

			if got.Token != "omj_enroll_token" || got.AgentVersion != version.Version || got.Hostname != "nas01" || got.AgentUID != UnknownUID {
				t.Errorf("EnrollRequest() = %+v", got)
			}

			if !reflect.DeepEqual(got.RunAsAllowed, []string{"ohmyjob"}) {
				t.Errorf("RunAsAllowed = %v, want [ohmyjob]", got.RunAsAllowed)
			}

			if !reflect.DeepEqual(got.Name, tt.wantName) {
				t.Errorf("Name = %v, want %v", got.Name, tt.wantName)
			}
		})
	}
}

func ptr(value string) *string {
	return &value
}
