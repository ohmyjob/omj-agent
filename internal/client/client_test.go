package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ohmyjob/omj-agent/internal/config"
	"github.com/ohmyjob/omj-agent/internal/protocol"
	"github.com/ohmyjob/omj-agent/internal/version"
)

const (
	secret     = "omj_agent_secret"
	pathPrefix = "/omj"
	runID      = "0f7a1a3c-4c1c-4a4e-9d2d-4b7a4b3f0f22"
)

type recorded struct {
	mu     sync.Mutex
	method string
	path   string
	header http.Header
	body   []byte
}

func (r *recorded) capture(req *http.Request) {
	body, _ := io.ReadAll(req.Body)

	r.mu.Lock()
	defer r.mu.Unlock()

	r.method = req.Method
	r.path = req.URL.EscapedPath()
	r.header = req.Header.Clone()
	r.body = body
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "protocol", "testdata", "agent-protocol-v1", name))
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func decodeFixture[T any](t *testing.T, name string) T {
	t.Helper()

	var value T
	if err := json.Unmarshal(fixture(t, name), &value); err != nil {
		t.Fatal(err)
	}

	return value
}

func asMap(t *testing.T, data []byte) map[string]any {
	t.Helper()

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("invalid JSON %q: %v", data, err)
	}

	return m
}

func credential(t *testing.T) config.Credential {
	t.Helper()

	c, err := config.NewCredential(secret)
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func newServer(t *testing.T, status int, body []byte, extra http.Header) (*recorded, *Client) {
	t.Helper()

	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.capture(r)

		for k, v := range extra {
			w.Header()[k] = v
		}

		w.Header().Set(protocol.HeaderServerVersion, "0.1.0")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	c, err := New(Options{ServerURL: srv.URL + pathPrefix, Credential: credential(t), InsecureHTTP: true})
	if err != nil {
		t.Fatal(err)
	}

	return rec, c
}

type call struct {
	name          string
	fixture       string
	method        string
	path          string
	authenticated bool
	do            func(context.Context, *Client) error
}

func calls() []call {
	ctxDo := func(f func(ctx context.Context, c *Client) error) func(context.Context, *Client) error { return f }

	return []call{
		{name: "enroll", fixture: "enroll-response.json", method: http.MethodPost, path: "/enroll", do: ctxDo(func(ctx context.Context, c *Client) error {
			_, err := c.Enroll(ctx, protocol.EnrollRequest{Token: "omj_enroll_x"})

			return err
		})},
		{name: "ping", fixture: "ping-response.json", method: http.MethodGet, path: "/ping", authenticated: true, do: ctxDo(func(ctx context.Context, c *Client) error {
			_, err := c.Ping(ctx)

			return err
		})},
		{name: "work", fixture: "work-response.json", method: http.MethodPost, path: "/work", authenticated: true, do: ctxDo(func(ctx context.Context, c *Client) error {
			_, err := c.Work(ctx, protocol.WorkRequest{ActiveRuns: []protocol.ActiveRun{}})

			return err
		})},
		{name: "discovery", fixture: "discovery-response.json", method: http.MethodPost, path: "/discovery", authenticated: true, do: ctxDo(func(ctx context.Context, c *Client) error {
			_, err := c.Discovery(ctx, protocol.DiscoveryRequest{Entries: []protocol.DiscoveredEntry{}, UnreadableSources: []protocol.UnreadableSource{}})

			return err
		})},
		{name: "start", fixture: "start-response.json", method: http.MethodPost, path: "/runs/" + runID + "/start", authenticated: true, do: ctxDo(func(ctx context.Context, c *Client) error {
			_, err := c.StartRun(ctx, runID, protocol.StartRequest{})

			return err
		})},
		{name: "output", fixture: "output-response.json", method: http.MethodPost, path: "/runs/" + runID + "/output", authenticated: true, do: ctxDo(func(ctx context.Context, c *Client) error {
			_, err := c.AppendOutput(ctx, runID, protocol.OutputRequest{Chunks: []protocol.OutputChunk{}})

			return err
		})},
		{name: "heartbeat", fixture: "heartbeat-response.json", method: http.MethodPost, path: "/runs/" + runID + "/heartbeat", authenticated: true, do: ctxDo(func(ctx context.Context, c *Client) error {
			_, err := c.Heartbeat(ctx, runID)

			return err
		})},
		{name: "finish", fixture: "finish-response.json", method: http.MethodPost, path: "/runs/" + runID + "/finish", authenticated: true, do: ctxDo(func(ctx context.Context, c *Client) error {
			_, err := c.FinishRun(ctx, runID, protocol.FinishRequest{Status: protocol.RunStatusSuccess})

			return err
		})},
	}
}

func TestEveryCallSendsTheProtocolHeadersToThePrefixedURL(t *testing.T) {
	for _, tt := range calls() {
		t.Run(tt.name, func(t *testing.T) {
			rec, c := newServer(t, http.StatusOK, fixture(t, tt.fixture), nil)

			if err := tt.do(context.Background(), c); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}

			if rec.method != tt.method || rec.path != pathPrefix+protocol.BasePath+tt.path {
				t.Errorf("request = %s %s, want %s %s", rec.method, rec.path, tt.method, pathPrefix+protocol.BasePath+tt.path)
			}

			want := map[string]string{
				"Accept":                       contentTypeJSON,
				"User-Agent":                   version.UserAgent(),
				protocol.HeaderProtocolVersion: "1",
				protocol.HeaderAgentVersion:    version.Version,
			}
			if tt.method == http.MethodPost {
				want["Content-Type"] = contentTypeJSON
			}
			if tt.authenticated {
				want["Authorization"] = "Bearer " + secret
			}

			for name, value := range want {
				if got := rec.header.Get(name); got != value {
					t.Errorf("header %s = %q, want %q", name, got, value)
				}
			}

			if !tt.authenticated && rec.header.Get("Authorization") != "" {
				t.Errorf("enroll sent Authorization %q, want none", rec.header.Get("Authorization"))
			}

			if got := c.ServerVersion(); got != "0.1.0" {
				t.Errorf("ServerVersion() = %q, want the response header", got)
			}
		})
	}
}

func TestRequestBodiesMatchTheFixtures(t *testing.T) {
	t.Run("enroll", func(t *testing.T) {
		rec, c := newServer(t, http.StatusCreated, fixture(t, "enroll-response.json"), nil)

		_, err := c.Enroll(context.Background(), decodeFixture[protocol.EnrollRequest](t, "enroll-request.json"))
		if err != nil {
			t.Fatal(err)
		}

		if got, want := asMap(t, rec.body), asMap(t, fixture(t, "enroll-request.json")); !reflect.DeepEqual(got, want) {
			t.Errorf("enroll body = %v, want %v", got, want)
		}
	})

	t.Run("work", func(t *testing.T) {
		rec, c := newServer(t, http.StatusOK, fixture(t, "work-response.json"), nil)

		_, err := c.Work(context.Background(), decodeFixture[protocol.WorkRequest](t, "work-request.json"))
		if err != nil {
			t.Fatal(err)
		}

		if got, want := asMap(t, rec.body), asMap(t, fixture(t, "work-request.json")); !reflect.DeepEqual(got, want) {
			t.Errorf("work body = %v, want %v", got, want)
		}
	})

	t.Run("discovery", func(t *testing.T) {
		rec, c := newServer(t, http.StatusOK, fixture(t, "discovery-response.json"), nil)

		_, err := c.Discovery(context.Background(), decodeFixture[protocol.DiscoveryRequest](t, "discovery-request.json"))
		if err != nil {
			t.Fatal(err)
		}

		if got, want := asMap(t, rec.body), asMap(t, fixture(t, "discovery-request.json")); !reflect.DeepEqual(got, want) {
			t.Errorf("discovery body = %v, want %v", got, want)
		}
	})

	t.Run("start", func(t *testing.T) {
		rec, c := newServer(t, http.StatusOK, fixture(t, "start-response.json"), nil)

		_, err := c.StartRun(context.Background(), runID, decodeFixture[protocol.StartRequest](t, "start-request.json"))
		if err != nil {
			t.Fatal(err)
		}

		if got, want := asMap(t, rec.body), asMap(t, fixture(t, "start-request.json")); !reflect.DeepEqual(got, want) {
			t.Errorf("start body = %v, want %v", got, want)
		}
	})

	t.Run("output", func(t *testing.T) {
		rec, c := newServer(t, http.StatusOK, fixture(t, "output-response.json"), nil)
		sent := decodeFixture[protocol.OutputRequest](t, "output-request.json")

		if _, err := c.AppendOutput(context.Background(), runID, sent); err != nil {
			t.Fatal(err)
		}

		var got protocol.OutputRequest
		if err := json.Unmarshal(rec.body, &got); err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(got, sent) {
			t.Errorf("output body = %+v, want %+v", got, sent)
		}
	})

	t.Run("heartbeat", func(t *testing.T) {
		rec, c := newServer(t, http.StatusOK, fixture(t, "heartbeat-response.json"), nil)

		if _, err := c.Heartbeat(context.Background(), runID); err != nil {
			t.Fatal(err)
		}

		if string(rec.body) != "{}" {
			t.Errorf("heartbeat body = %q, want {}", rec.body)
		}
	})

	t.Run("finish", func(t *testing.T) {
		rec, c := newServer(t, http.StatusOK, fixture(t, "finish-response.json"), nil)
		sent := decodeFixture[protocol.FinishRequest](t, "finish-request.json")

		if _, err := c.FinishRun(context.Background(), runID, sent); err != nil {
			t.Fatal(err)
		}

		var got protocol.FinishRequest
		if err := json.Unmarshal(rec.body, &got); err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(got, sent) {
			t.Errorf("finish body = %+v, want %+v", got, sent)
		}
	})
}

func TestResponsesAreDecoded(t *testing.T) {
	_, c := newServer(t, http.StatusOK, fixture(t, "ping-response.json"), nil)

	ping, err := c.Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	want := protocol.PingResponse{
		MachineID:     "0f7a1a3c-4c1c-4a4e-9d2d-4b7a4b3f0f11",
		ServerVersion: "0.1.0",
		ServerTime:    time.Date(2026, 9, 4, 2, 0, 0, 0, time.UTC),
	}
	if ping.MachineID != want.MachineID || ping.ServerVersion != want.ServerVersion || !ping.ServerTime.Equal(want.ServerTime) {
		t.Errorf("Ping() = %+v, want %+v", ping, want)
	}

	_, c = newServer(t, http.StatusOK, fixture(t, "work-response.json"), nil)

	work, err := c.Work(context.Background(), protocol.WorkRequest{ActiveRuns: []protocol.ActiveRun{}})
	if err != nil {
		t.Fatal(err)
	}

	expected := decodeFixture[protocol.WorkResponse](t, "work-response.json")
	if !reflect.DeepEqual(work, expected) {
		t.Errorf("Work() = %+v, want %+v", work, expected)
	}
}

func TestErrorsAreMapped(t *testing.T) {
	conflict := func(code protocol.ErrorCode) func(error) bool {
		return func(err error) bool { return IsConflict(err, code) }
	}

	tests := []struct {
		name       string
		status     int
		body       string
		header     http.Header
		matches    func(error) bool
		code       protocol.ErrorCode
		retryable  bool
		retryAfter time.Duration
	}{
		{name: "401 invalid credential", status: 401, body: `{"error":"invalid_credential","message":"Unknown credential."}`, matches: IsUnauthorized, code: protocol.ErrInvalidCredential},
		{name: "401 token invalid", status: 401, body: `{"error":"token_invalid","message":"Unknown token."}`, matches: IsUnauthorized, code: protocol.ErrTokenInvalid},
		{name: "404", status: 404, body: `{"error":"run_not_found","message":"No such Run."}`, matches: IsNotFound, code: protocol.ErrRunNotFound},
		{name: "409 lease expired", status: 409, body: string(fixture(t, "error-409-lease-expired.json")), matches: conflict(protocol.ErrLeaseExpired), code: protocol.ErrLeaseExpired},
		{name: "409 not leased", status: 409, body: `{"error":"not_leased","message":"m"}`, matches: conflict(protocol.ErrNotLeased), code: protocol.ErrNotLeased},
		{name: "409 run cancelled", status: 409, body: `{"error":"run_cancelled","message":"m"}`, matches: conflict(protocol.ErrRunCancelled), code: protocol.ErrRunCancelled},
		{name: "409 run finished", status: 409, body: `{"error":"run_finished","message":"m"}`, matches: conflict(protocol.ErrRunFinished), code: protocol.ErrRunFinished},
		{name: "409 run not running", status: 409, body: `{"error":"run_not_running","message":"m"}`, matches: conflict(protocol.ErrRunNotRunning), code: protocol.ErrRunNotRunning},
		{name: "410 token expired", status: 410, body: `{"error":"token_expired","message":"m"}`, matches: func(err error) bool { return !IsRetryable(err) }, code: protocol.ErrTokenExpired},
		{name: "413", status: 413, body: `{"error":"payload_too_large","message":"m"}`, matches: IsPayloadTooLarge, code: protocol.ErrPayloadTooLarge},
		{name: "422", status: 422, body: `{"error":"validation_failed","message":"m","errors":{"slots":["The slots field is required."]}}`, matches: func(err error) bool {
			apiErr, ok := asAPIError(err)

			return ok && len(apiErr.Errors["slots"]) == 1
		}, code: protocol.ErrValidationFailed},
		{name: "426", status: 426, body: string(fixture(t, "error-426.json")), matches: func(err error) bool {
			apiErr, _ := asAPIError(err)

			return IsUnsupportedProtocol(err) && reflect.DeepEqual(apiErr.SupportedProtocolVersions, []int{1}) && apiErr.MinAgentVersion == "0.1.0"
		}, code: protocol.ErrUnsupportedProtocol},
		{name: "429 with Retry-After", status: 429, body: `{"error":"throttled","message":"m"}`, header: http.Header{"Retry-After": {"7"}}, matches: IsThrottled, code: protocol.ErrThrottled, retryable: true, retryAfter: 7 * time.Second},
		{name: "500", status: 500, body: `{"error":"server_error","message":"m"}`, matches: IsRetryable, code: "server_error", retryable: true},
		{name: "503 without JSON", status: 503, body: "Service Unavailable", matches: IsRetryable, retryable: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, c := newServer(t, tt.status, []byte(tt.body), tt.header)

			_, err := c.Ping(context.Background())
			if err == nil {
				t.Fatal("Ping() returned no error")
			}

			apiErr, ok := asAPIError(err)
			if !ok {
				t.Fatalf("Ping() error = %v, want *APIError", err)
			}

			if !tt.matches(err) {
				t.Errorf("Ping() error = %v does not match the expected helper", err)
			}

			if apiErr.Status != tt.status || apiErr.Code != tt.code || apiErr.Retryable != tt.retryable || apiErr.RetryAfter != tt.retryAfter {
				t.Errorf("APIError = %+v, want status %d code %q retryable %t retryAfter %s", apiErr, tt.status, tt.code, tt.retryable, tt.retryAfter)
			}

			if IsRetryable(err) != tt.retryable {
				t.Errorf("IsRetryable() = %t, want %t", IsRetryable(err), tt.retryable)
			}

			if c.ServerVersion() != "0.1.0" {
				t.Error("ServerVersion() not captured from an error response")
			}
		})
	}
}

func TestANonJSONErrorKeepsTheBodyAsTheMessage(t *testing.T) {
	_, c := newServer(t, http.StatusBadGateway, []byte("<html>bad gateway</html>"), nil)

	_, err := c.Ping(context.Background())

	apiErr, ok := asAPIError(err)
	if !ok || apiErr.Code != "" || apiErr.Message != "<html>bad gateway</html>" {
		t.Errorf("error = %v, want the raw body as the message and no code", err)
	}

	if !strings.Contains(err.Error(), "502 Bad Gateway") {
		t.Errorf("Error() = %q, want the status text", err)
	}
}

func TestNetworkErrorsAreRetryable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	c, err := New(Options{ServerURL: url, Credential: credential(t), InsecureHTTP: true})
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Ping(context.Background())
	if err == nil {
		t.Fatal("Ping() returned no error against a closed server")
	}

	if _, ok := asAPIError(err); ok {
		t.Errorf("Ping() error = %v is an APIError, want a transport error", err)
	}

	if !IsRetryable(err) {
		t.Errorf("IsRetryable(%v) = false, want true", err)
	}
}

func TestResponsesOverOneMebibyteAreRefused(t *testing.T) {
	_, c := newServer(t, http.StatusOK, bytes.Repeat([]byte("x"), MaxResponseBytes+1), nil)

	_, err := c.Ping(context.Background())

	if !errors.Is(err, ErrResponseTooLarge) {
		t.Errorf("Ping() error = %v, want ErrResponseTooLarge", err)
	}

	if IsRetryable(err) {
		t.Error("an oversized response must not be retried")
	}
}

func TestPlainHTTPNeedsTheInsecureFlag(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		insecure bool
		wantErr  string
	}{
		{name: "https", url: "https://omj.example.com"},
		{name: "https with prefix", url: "https://omj.example.com/omj/"},
		{name: "http allowed", url: "http://omj.internal:8000", insecure: true},
		{name: "http refused", url: "http://omj.internal:8000", wantErr: "TLS"},
		{name: "other scheme", url: "ftp://omj.example.com", wantErr: "https://"},
		{name: "no host", url: "https://", wantErr: "host"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(Options{ServerURL: tt.url, InsecureHTTP: tt.insecure})

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("New() error = %v", err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New() error = %v, want one mentioning %q", err, tt.wantErr)
			}
		})
	}
}

func TestCallsWithoutACredentialFailBeforeTheNetwork(t *testing.T) {
	rec, c := newServer(t, http.StatusOK, fixture(t, "ping-response.json"), nil)
	c.credential = config.Credential{}

	_, err := c.Ping(context.Background())

	if !errors.Is(err, ErrNoCredential) || IsRetryable(err) {
		t.Errorf("Ping() error = %v, want ErrNoCredential and not retryable", err)
	}

	if rec.method != "" {
		t.Error("a request was sent without a credential")
	}
}

func TestWorkWaitsForTheLongPollPlusTheHeadroom(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := New(Options{ServerURL: srv.URL, Credential: credential(t), InsecureHTTP: true, LongPollHeadroom: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	_, err = c.Work(context.Background(), protocol.WorkRequest{WaitSeconds: 0, ActiveRuns: []protocol.ActiveRun{}})

	if err == nil || !IsRetryable(err) {
		t.Fatalf("Work() error = %v, want a retryable timeout", err)
	}

	if time.Since(started) > time.Second {
		t.Errorf("Work() took %s, want the deadline of wait plus headroom", time.Since(started))
	}
}

func TestRunIDsAreEscapedInPaths(t *testing.T) {
	rec, c := newServer(t, http.StatusOK, fixture(t, "start-response.json"), nil)

	if _, err := c.StartRun(context.Background(), "a/b", protocol.StartRequest{}); err != nil {
		t.Fatal(err)
	}

	if want := pathPrefix + protocol.BasePath + "/runs/a%2Fb/start"; rec.path != want {
		t.Errorf("path = %s, want %s", rec.path, want)
	}
}

func TestRetryAfterAcceptsAnHTTPDate(t *testing.T) {
	at := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)

	got := retryAfter(at)
	if got < 25*time.Second || got > 30*time.Second {
		t.Errorf("retryAfter(%q) = %s, want about 30s", at, got)
	}

	if retryAfter("nonsense") != 0 || retryAfter("-5") != 0 {
		t.Error("retryAfter() must ignore values it cannot read")
	}
}
