package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const fixtureDir = "testdata/agent-protocol-v1"

func fixtures() []struct {
	file  string
	value any
} {
	return []struct {
		file  string
		value any
	}{
		{"enroll-request.json", &EnrollRequest{}},
		{"enroll-response.json", &EnrollResponse{}},
		{"ping-response.json", &PingResponse{}},
		{"work-request.json", &WorkRequest{}},
		{"work-response.json", &WorkResponse{}},
		{"start-request.json", &StartRequest{}},
		{"start-response.json", &StartResponse{}},
		{"output-request.json", &OutputRequest{}},
		{"output-response.json", &OutputResponse{}},
		{"heartbeat-request.json", &HeartbeatRequest{}},
		{"heartbeat-response.json", &HeartbeatResponse{}},
		{"finish-request.json", &FinishRequest{}},
		{"finish-response.json", &FinishResponse{}},
		{"error-426.json", &ErrorResponse{}},
		{"error-409-lease-expired.json", &ErrorResponse{}},
	}
}

func readFixture(t *testing.T, file string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(fixtureDir, file))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	return data
}

// normalise decodes a JSON document into plain values and rewrites every
// RFC 3339 string into one canonical form, because the Server emits
// microseconds while time.Time omits trailing zeros; both are valid.
func normalise(t *testing.T, data []byte) any {
	t.Helper()

	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode: %v", err)
	}

	return canonical(value)
}

func canonical(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			v[key] = canonical(item)
		}

		return v
	case []any:
		for i, item := range v {
			v[i] = canonical(item)
		}

		return v
	case string:
		if at, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return at.UTC().Format(time.RFC3339Nano)
		}

		return v
	default:
		return v
	}
}

func TestFixturesRoundTrip(t *testing.T) {
	for _, tt := range fixtures() {
		t.Run(tt.file, func(t *testing.T) {
			data := readFixture(t, tt.file)

			if err := json.Unmarshal(data, tt.value); err != nil {
				t.Fatalf("unmarshal into %T: %v", tt.value, err)
			}

			encoded, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatalf("marshal %T: %v", tt.value, err)
			}

			got, want := normalise(t, encoded), normalise(t, data)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("round trip changed the document\n got: %s\nwant: %s", encoded, data)
			}
		})
	}
}

func TestFixturesListMatchesDirectory(t *testing.T) {
	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	listed := map[string]bool{}
	for _, tt := range fixtures() {
		listed[tt.file] = true
	}

	for _, entry := range entries {
		if !listed[entry.Name()] {
			t.Errorf("fixture %s has no type in the round-trip table", entry.Name())
		}

		delete(listed, entry.Name())
	}

	for file := range listed {
		t.Errorf("fixture %s is in the table but not on disk", file)
	}
}

func TestUnknownFieldsAreTolerated(t *testing.T) {
	for _, tt := range fixtures() {
		t.Run(tt.file, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(readFixture(t, tt.file), &document); err != nil {
				t.Fatalf("decode: %v", err)
			}

			document["added_in_a_later_version"] = map[string]any{"nested": true}

			extended, err := json.Marshal(document)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			if err := json.Unmarshal(extended, tt.value); err != nil {
				t.Errorf("unmarshal with an unknown field: %v", err)
			}
		})
	}
}

func TestRunAsAllowedIsRequestOnly(t *testing.T) {
	for _, request := range []any{EnrollRequest{}, WorkRequest{}} {
		if !carries(reflect.TypeOf(request), "run_as_allowed") {
			t.Errorf("%T does not report run_as_allowed", request)
		}
	}

	responses := []any{EnrollResponse{}, PingResponse{}, WorkResponse{}, StartResponse{}, OutputResponse{}, HeartbeatResponse{}, FinishResponse{}, ErrorResponse{}}

	for _, response := range responses {
		if carries(reflect.TypeOf(response), "run_as_allowed") {
			t.Errorf("%T carries run_as_allowed; the server must not be able to set the allowlist", response)
		}
	}
}

func carries(t reflect.Type, tag string) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		return carries(t.Elem(), tag)
	case reflect.Struct:
		for i := range t.NumField() {
			field := t.Field(i)

			if name, _, _ := strings.Cut(field.Tag.Get("json"), ","); name == tag {
				return true
			}

			if field.Type != t && carries(field.Type, tag) {
				return true
			}
		}
	}

	return false
}

func TestWorkResponseDecodesNullableLeaseFields(t *testing.T) {
	var response WorkResponse
	if err := json.Unmarshal(readFixture(t, "work-response.json"), &response); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(response.Runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(response.Runs))
	}

	lease := response.Runs[0]

	if lease.Shell != nil || lease.WorkingDirectory != nil {
		t.Errorf("shell = %v, working_directory = %v, want both nil", lease.Shell, lease.WorkingDirectory)
	}

	if lease.ScheduledFor == nil || !lease.ScheduledFor.Equal(time.Date(2026, 9, 4, 2, 0, 0, 0, time.UTC)) {
		t.Errorf("scheduled_for = %v, want 2026-09-04T02:00:00Z", lease.ScheduledFor)
	}

	if lease.Trigger != TriggerScheduled || lease.Environment["KEY"] != "value" {
		t.Errorf("trigger = %q, environment = %v", lease.Trigger, lease.Environment)
	}

	if response.CancelRunIDs == nil || len(response.CancelRunIDs) != 0 {
		t.Errorf("cancel_run_ids = %#v, want an empty list", response.CancelRunIDs)
	}

	if response.Config != (AgentConfig{HeartbeatIntervalSeconds: 15, OutputFlushIntervalMS: 500, OutputChunkBytes: 65536, PollWaitSeconds: 25}) {
		t.Errorf("config = %+v", response.Config)
	}
}

func TestOutputChunkDataIsBase64(t *testing.T) {
	var request OutputRequest
	if err := json.Unmarshal(readFixture(t, "output-request.json"), &request); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(request.Chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(request.Chunks))
	}

	chunk := request.Chunks[0]

	if string(chunk.Data) != "Backing up /srv/data\n" {
		t.Errorf("data = %q", chunk.Data)
	}

	if chunk.Seq != 12 || chunk.Stream != StreamStdout {
		t.Errorf("seq = %d, stream = %q", chunk.Seq, chunk.Stream)
	}
}

func TestFinishRequestNullFields(t *testing.T) {
	var request FinishRequest
	if err := json.Unmarshal(readFixture(t, "finish-request.json"), &request); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if request.Reason != nil {
		t.Errorf("reason = %q, want nil", *request.Reason)
	}

	if request.ExitCode == nil || *request.ExitCode != 2 {
		t.Errorf("exit_code = %v, want 2", request.ExitCode)
	}

	reason := ReasonAgentRestarted
	lost := FinishRequest{Status: RunStatusLost, FinishedAt: time.Date(2026, 9, 4, 2, 0, 0, 0, time.UTC), Reason: &reason}

	encoded, err := json.Marshal(lost)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"status":"lost","exit_code":null,"started_at":null,"finished_at":"2026-09-04T02:00:00Z","last_output_seq":0,"output_truncated":false,"reason":"agent_restarted"}`
	if string(encoded) != want {
		t.Errorf("marshal =\n%s\nwant\n%s", encoded, want)
	}
}

func TestHeartbeatRequestIsAnEmptyObject(t *testing.T) {
	encoded, err := json.Marshal(HeartbeatRequest{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if string(encoded) != "{}" {
		t.Errorf("marshal = %s, want {}", encoded)
	}
}

func TestErrorResponseOptionalFields(t *testing.T) {
	var conflict ErrorResponse
	if err := json.Unmarshal(readFixture(t, "error-409-lease-expired.json"), &conflict); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if conflict.Error != ErrLeaseExpired || conflict.SupportedProtocolVersions != nil || conflict.Errors != nil {
		t.Errorf("conflict = %+v", conflict)
	}

	var upgrade ErrorResponse
	if err := json.Unmarshal(readFixture(t, "error-426.json"), &upgrade); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if upgrade.Error != ErrUnsupportedProtocol || !reflect.DeepEqual(upgrade.SupportedProtocolVersions, []int{1}) || upgrade.MinAgentVersion != "0.1.0" {
		t.Errorf("upgrade = %+v", upgrade)
	}

	validation := ErrorResponse{Error: ErrValidationFailed, Message: "The body is invalid.", Errors: map[string][]string{"slots": {"The slots field is required."}}}

	encoded, err := json.Marshal(validation)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"error":"validation_failed","message":"The body is invalid.","errors":{"slots":["The slots field is required."]}}`
	if string(encoded) != want {
		t.Errorf("marshal = %s, want %s", encoded, want)
	}
}
