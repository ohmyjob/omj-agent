package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ohmyjob/omj-agent/internal/protocol"
)

// fakeServer speaks the agent protocol from memory: leases are queued one
// batch per work call, cancellations are delivered on the next work call,
// failures are queued per endpoint, and every request is recorded. Tasks 012
// and 013 reuse it for the reporter and shutdown scenarios.
type fakeServer struct {
	server    *httptest.Server
	MachineID string
	Secret    string

	mu            sync.Mutex
	batches       [][]protocol.RunLease
	cancels       []string
	config        protocol.AgentConfig
	failures      map[string][]failure
	startRefusals map[string]failure
	workRequests  []protocol.WorkRequest
	workBodies    []string
	starts        []startRecord
	heartbeats    []string
	outputs       []outputRecord
	finishes      []finishRecord
	onWork        func(count int, request protocol.WorkRequest)
	rejectAuth    bool
	rejectProto   *protocol.ErrorResponse
	cancelWanted  map[string]bool
	truncated     map[string]bool
	holdFinish    chan struct{}
}

type failure struct {
	status int
	body   protocol.ErrorResponse
}

type startRecord struct {
	RunID   string
	Request protocol.StartRequest
}

type outputRecord struct {
	RunID   string
	Request protocol.OutputRequest
}

type finishRecord struct {
	RunID   string
	Request protocol.FinishRequest
}

const (
	fakeMachineID = "0f7a1a3c-4c1c-4a4e-9d2d-4b7a4b3f0f11"
	fakeSecret    = "omj_agent_test-credential"
)

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()

	f := &fakeServer{
		MachineID:     fakeMachineID,
		Secret:        fakeSecret,
		config:        protocol.AgentConfig{HeartbeatIntervalSeconds: 15, OutputFlushIntervalMS: 500, OutputChunkBytes: 65536, PollWaitSeconds: 25},
		failures:      map[string][]failure{},
		startRefusals: map[string]failure{},
		cancelWanted:  map[string]bool{},
		truncated:     map[string]bool{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agent/v1/ping", f.ping)
	mux.HandleFunc("POST /api/agent/v1/work", f.work)
	mux.HandleFunc("POST /api/agent/v1/runs/{id}/start", f.start)
	mux.HandleFunc("POST /api/agent/v1/runs/{id}/output", f.output)
	mux.HandleFunc("POST /api/agent/v1/runs/{id}/heartbeat", f.heartbeat)
	mux.HandleFunc("POST /api/agent/v1/runs/{id}/finish", f.finish)

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)

	return f
}

func (f *fakeServer) URL() string {
	return f.server.URL
}

// Enqueue hands the leases out together on the next work call.
func (f *fakeServer) Enqueue(leases ...protocol.RunLease) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.batches = append(f.batches, leases)
}

// Cancel lists the ids in cancel_run_ids of the next work call.
func (f *fakeServer) Cancel(runIDs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.cancels = append(f.cancels, runIDs...)
}

// RequestCancel makes every output and heartbeat answer for the Run carry
// cancel_requested, the way the Server relays a user's cancellation.
func (f *fakeServer) RequestCancel(runID string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.cancelWanted[runID] = true
}

// TruncateOutput makes every output answer for the Run say the cap was hit.
func (f *fakeServer) TruncateOutput(runID string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.truncated[runID] = true
}

// HoldFinish makes every finish wait until the returned function is called,
// so a test can see what else happens while an outcome is in flight.
func (f *fakeServer) HoldFinish() (release func()) {
	f.mu.Lock()
	defer f.mu.Unlock()

	hold := make(chan struct{})
	f.holdFinish = hold

	return func() { close(hold) }
}

func (f *fakeServer) SetConfig(config protocol.AgentConfig) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.config = config
}

// FailNext makes the next call to the endpoint ("work", "start", "output",
// "heartbeat", "finish", "ping") answer the given status.
func (f *fakeServer) FailNext(endpoint string, status int, code protocol.ErrorCode) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.failures[endpoint] = append(f.failures[endpoint], failure{status: status, body: protocol.ErrorResponse{Error: code, Message: string(code)}})
}

// RefuseStart answers every start for the Run with the given status.
func (f *fakeServer) RefuseStart(runID string, status int, code protocol.ErrorCode) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.startRefusals[runID] = failure{status: status, body: protocol.ErrorResponse{Error: code, Message: string(code)}}
}

// OnWork runs after each work request is recorded, with its ordinal
// starting at 1; tests use it to stop the loop.
func (f *fakeServer) OnWork(hook func(count int, request protocol.WorkRequest)) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.onWork = hook
}

func (f *fakeServer) RejectCredential() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.rejectAuth = true
}

func (f *fakeServer) RejectProtocol(supported []int, minAgentVersion string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.rejectProto = &protocol.ErrorResponse{
		Error:                     protocol.ErrUnsupportedProtocol,
		Message:                   "protocol version unsupported",
		SupportedProtocolVersions: supported,
		MinAgentVersion:           minAgentVersion,
	}
}

func (f *fakeServer) WorkRequests() []protocol.WorkRequest {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]protocol.WorkRequest(nil), f.workRequests...)
}

func (f *fakeServer) WorkBodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.workBodies...)
}

func (f *fakeServer) Starts() []startRecord {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]startRecord(nil), f.starts...)
}

func (f *fakeServer) Heartbeats() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.heartbeats...)
}

func (f *fakeServer) Outputs() []outputRecord {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]outputRecord(nil), f.outputs...)
}

func (f *fakeServer) Finishes() []finishRecord {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]finishRecord(nil), f.finishes...)
}

// guard applies the checks every endpoint shares and reports whether the
// handler may go on.
func (f *fakeServer) guard(w http.ResponseWriter, r *http.Request, endpoint string) bool {
	w.Header().Set(protocol.HeaderServerVersion, "0.1.0-test")

	f.mu.Lock()
	rejectProto := f.rejectProto
	rejectAuth := f.rejectAuth

	var queued *failure

	if queue := f.failures[endpoint]; len(queue) > 0 {
		queued = &queue[0]
		f.failures[endpoint] = queue[1:]
	}
	f.mu.Unlock()

	switch {
	case rejectProto != nil:
		writeError(w, http.StatusUpgradeRequired, *rejectProto)
	case rejectAuth || r.Header.Get("Authorization") != "Bearer "+f.Secret:
		writeError(w, http.StatusUnauthorized, protocol.ErrorResponse{Error: protocol.ErrInvalidCredential, Message: "credential rejected"})
	case queued != nil:
		writeError(w, queued.status, queued.body)
	default:
		return true
	}

	return false
}

func (f *fakeServer) ping(w http.ResponseWriter, r *http.Request) {
	if !f.guard(w, r, "ping") {
		return
	}

	writeJSON(w, http.StatusOK, protocol.PingResponse{MachineID: f.MachineID, ServerVersion: "0.1.0-test", ServerTime: time.Now().UTC()})
}

func (f *fakeServer) work(w http.ResponseWriter, r *http.Request) {
	var (
		request protocol.WorkRequest
		body    strings.Builder
	)

	if err := json.NewDecoder(io.TeeReader(r.Body, &body)).Decode(&request); err != nil {
		writeError(w, http.StatusUnprocessableEntity, protocol.ErrorResponse{Error: protocol.ErrValidationFailed, Message: err.Error()})

		return
	}

	f.mu.Lock()
	f.workRequests = append(f.workRequests, request)
	f.workBodies = append(f.workBodies, body.String())
	count := len(f.workRequests)
	hook := f.onWork
	f.mu.Unlock()

	if hook != nil {
		hook(count, request)
	}

	if !f.guard(w, r, "work") {
		return
	}

	f.mu.Lock()
	runs := []protocol.RunLease{}
	if len(f.batches) > 0 {
		runs = f.batches[0]
		f.batches = f.batches[1:]
	}

	cancels := f.cancels
	if cancels == nil {
		cancels = []string{}
	}
	f.cancels = nil
	config := f.config
	f.mu.Unlock()

	writeJSON(w, http.StatusOK, protocol.WorkResponse{Runs: runs, CancelRunIDs: cancels, Config: config})
}

func (f *fakeServer) start(w http.ResponseWriter, r *http.Request) {
	if !f.guard(w, r, "start") {
		return
	}

	var request protocol.StartRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusUnprocessableEntity, protocol.ErrorResponse{Error: protocol.ErrValidationFailed, Message: err.Error()})

		return
	}

	runID := r.PathValue("id")

	f.mu.Lock()
	f.starts = append(f.starts, startRecord{RunID: runID, Request: request})
	refusal, refused := f.startRefusals[runID]
	f.mu.Unlock()

	if refused {
		writeError(w, refusal.status, refusal.body)

		return
	}

	writeJSON(w, http.StatusOK, protocol.StartResponse{Status: protocol.RunStatusRunning})
}

func (f *fakeServer) output(w http.ResponseWriter, r *http.Request) {
	if !f.guard(w, r, "output") {
		return
	}

	var request protocol.OutputRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusUnprocessableEntity, protocol.ErrorResponse{Error: protocol.ErrValidationFailed, Message: err.Error()})

		return
	}

	var last uint64
	for _, chunk := range request.Chunks {
		last = max(last, chunk.Seq)
	}

	runID := r.PathValue("id")

	f.mu.Lock()
	f.outputs = append(f.outputs, outputRecord{RunID: runID, Request: request})
	response := protocol.OutputResponse{LastOutputSeq: last, Truncated: f.truncated[runID], CancelRequested: f.cancelWanted[runID]}
	f.mu.Unlock()

	writeJSON(w, http.StatusOK, response)
}

func (f *fakeServer) heartbeat(w http.ResponseWriter, r *http.Request) {
	if !f.guard(w, r, "heartbeat") {
		return
	}

	runID := r.PathValue("id")

	f.mu.Lock()
	f.heartbeats = append(f.heartbeats, runID)
	response := protocol.HeartbeatResponse{CancelRequested: f.cancelWanted[runID], ServerTime: time.Now().UTC()}
	f.mu.Unlock()

	writeJSON(w, http.StatusOK, response)
}

func (f *fakeServer) finish(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	hold := f.holdFinish
	f.mu.Unlock()

	if hold != nil {
		<-hold
	}

	if !f.guard(w, r, "finish") {
		return
	}

	var request protocol.FinishRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusUnprocessableEntity, protocol.ErrorResponse{Error: protocol.ErrValidationFailed, Message: err.Error()})

		return
	}

	f.mu.Lock()
	f.finishes = append(f.finishes, finishRecord{RunID: r.PathValue("id"), Request: request})
	f.mu.Unlock()

	writeJSON(w, http.StatusOK, protocol.FinishResponse{Status: request.Status})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, body protocol.ErrorResponse) {
	writeJSON(w, status, body)
}
