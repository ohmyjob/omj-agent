package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ohmyjob/omj-agent/internal/discovery"
	"github.com/ohmyjob/omj-agent/internal/protocol"
)

func discoverer(result discovery.Result, err error) func(context.Context) (discovery.Result, error) {
	return func(context.Context) (discovery.Result, error) { return result, err }
}

func TestDiscoverPrintsThePayloadAndSendsNothing(t *testing.T) {
	result := discovery.Result{
		Entries: []discovery.Entry{{
			Source:       "crontab:root",
			Raw:          "0 3 * * * /usr/local/bin/backup.sh",
			Schedule:     "0 3 * * *",
			ScheduleKind: discovery.KindCron,
			Command:      "/usr/local/bin/backup.sh",
			User:         "root",
		}},
		Unreadable: []discovery.Unreadable{{Source: "crontab:deploy", Reason: "permission denied"}},
	}

	var stdout, stderr bytes.Buffer

	// The command is given no server and no credential, so anything it printed
	// it produced without being able to send.
	if code := (discoverCommand{collect: discoverer(result, nil)}).discover(nil, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr: %s", code, ExitOK, stderr.String())
	}

	var payload protocol.DiscoveryRequest
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not the discovery body: %v\n%s", err, stdout.String())
	}

	if len(payload.Entries) != 1 || payload.Entries[0].Source != "crontab:root" {
		t.Errorf("entries = %+v, want the one crontab line", payload.Entries)
	}

	if len(payload.UnreadableSources) != 1 {
		t.Errorf("unreadable_sources = %+v, want the one it could not read", payload.UnreadableSources)
	}

	if !strings.Contains(stderr.String(), "Nothing was sent.") {
		t.Errorf("stderr does not say nothing was sent:\n%s", stderr.String())
	}
}

func TestDiscoverReportsACollectionThatFailed(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := (discoverCommand{collect: discoverer(discovery.Result{}, errors.New("systemctl is not installed"))}).discover(nil, &stdout, &stderr)

	if code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}

	if !strings.Contains(stderr.String(), "systemctl is not installed") {
		t.Errorf("stderr does not name the failure:\n%s", stderr.String())
	}
}

func TestDiscoverTakesNoArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := (discoverCommand{collect: discoverer(discovery.Result{}, nil)}).discover([]string{"now"}, &stdout, &stderr)

	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
}
