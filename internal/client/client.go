// Package client speaks version 1 of the agent protocol: it builds the
// requests, maps the Server's errors to types the Agent can act on, and
// retries the ones that are safe to retry.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ohmyjob/omj-agent/internal/config"
	"github.com/ohmyjob/omj-agent/internal/protocol"
	"github.com/ohmyjob/omj-agent/internal/version"
)

const (
	DefaultTimeout   = 30 * time.Second
	LongPollHeadroom = 10 * time.Second
	MaxResponseBytes = 1 << 20

	contentTypeJSON = "application/json"
)

type Options struct {
	ServerURL    string
	Credential   config.Credential
	InsecureHTTP bool

	HTTPClient       *http.Client
	Timeout          time.Duration
	LongPollHeadroom time.Duration
	Logger           *slog.Logger
}

type Client struct {
	baseURL      string
	credential   config.Credential
	agentVersion string
	userAgent    string
	http         *http.Client
	timeout      time.Duration
	headroom     time.Duration
	logger       *slog.Logger

	mu            sync.Mutex
	serverVersion string
}

func New(opts Options) (*Client, error) {
	parsed, err := url.Parse(opts.ServerURL)
	if err != nil {
		return nil, fmt.Errorf("server url: %w", err)
	}

	switch {
	case parsed.Scheme == "https" && parsed.Host != "":
	case parsed.Scheme == "http" && parsed.Host != "" && opts.InsecureHTTP:
	case parsed.Scheme == "http" && parsed.Host != "":
		return nil, fmt.Errorf("server url %q is not protected by TLS; use https:// or set insecure_http = true", opts.ServerURL)
	default:
		return nil, fmt.Errorf("server url %q must start with https:// and name a host", opts.ServerURL)
	}

	c := &Client{
		baseURL:      strings.TrimRight(opts.ServerURL, "/") + protocol.BasePath,
		credential:   opts.Credential,
		agentVersion: version.Version,
		userAgent:    version.UserAgent(),
		http:         opts.HTTPClient,
		timeout:      opts.Timeout,
		headroom:     opts.LongPollHeadroom,
		logger:       opts.Logger,
	}

	if c.http == nil {
		c.http = defaultHTTPClient()
	}

	if c.timeout <= 0 {
		c.timeout = DefaultTimeout
	}

	if c.headroom <= 0 {
		c.headroom = LongPollHeadroom
	}

	if c.logger == nil {
		c.logger = slog.Default()
	}

	return c, nil
}

// ServerVersion is the X-OMJ-Server-Version of the last response, or empty
// before the first one.
func (c *Client) ServerVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.serverVersion
}

func (c *Client) Enroll(ctx context.Context, req protocol.EnrollRequest) (protocol.EnrollResponse, error) {
	var out protocol.EnrollResponse

	err := c.call(ctx, http.MethodPost, "/enroll", req, &out, c.timeout, false)

	return out, err
}

func (c *Client) Ping(ctx context.Context) (protocol.PingResponse, error) {
	var out protocol.PingResponse

	err := c.call(ctx, http.MethodGet, "/ping", nil, &out, c.timeout, true)

	return out, err
}

// Work long-polls, so its deadline is the wait the Agent asked for plus the
// headroom the protocol document names.
func (c *Client) Work(ctx context.Context, req protocol.WorkRequest) (protocol.WorkResponse, error) {
	var out protocol.WorkResponse

	timeout := time.Duration(req.WaitSeconds)*time.Second + c.headroom

	err := c.call(ctx, http.MethodPost, "/work", req, &out, timeout, true)

	return out, err
}

func (c *Client) StartRun(ctx context.Context, runID string, req protocol.StartRequest) (protocol.StartResponse, error) {
	var out protocol.StartResponse

	err := c.call(ctx, http.MethodPost, runPath(runID, "start"), req, &out, c.timeout, true)

	return out, err
}

func (c *Client) AppendOutput(ctx context.Context, runID string, req protocol.OutputRequest) (protocol.OutputResponse, error) {
	var out protocol.OutputResponse

	err := c.call(ctx, http.MethodPost, runPath(runID, "output"), req, &out, c.timeout, true)

	return out, err
}

func (c *Client) Heartbeat(ctx context.Context, runID string) (protocol.HeartbeatResponse, error) {
	var out protocol.HeartbeatResponse

	err := c.call(ctx, http.MethodPost, runPath(runID, "heartbeat"), protocol.HeartbeatRequest{}, &out, c.timeout, true)

	return out, err
}

func (c *Client) FinishRun(ctx context.Context, runID string, req protocol.FinishRequest) (protocol.FinishResponse, error) {
	var out protocol.FinishResponse

	err := c.call(ctx, http.MethodPost, runPath(runID, "finish"), req, &out, c.timeout, true)

	return out, err
}

func runPath(runID, action string) string {
	return "/runs/" + url.PathEscape(runID) + "/" + action
}

func (c *Client) call(ctx context.Context, method, path string, in, out any, timeout time.Duration, authenticated bool) error {
	if authenticated && c.credential.IsZero() {
		return ErrNoCredential
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := c.newRequest(ctx, method, path, in, authenticated)
	if err != nil {
		return err
	}

	started := time.Now()

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	c.rememberServerVersion(resp.Header.Get(protocol.HeaderServerVersion))

	body, err := readBody(resp.Body)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}

	c.logger.Debug("agent api call", "method", method, "path", path, "status", resp.StatusCode, "duration", time.Since(started))

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%s %s: %w", method, path, decodeError(resp, body))
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s %s: decode response: %w", method, path, err)
	}

	return nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, in any, authenticated bool) (*http.Request, error) {
	var body io.Reader

	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("%s %s: encode request: %w", method, path, err)
		}

		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}

	req.Header.Set("Accept", contentTypeJSON)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set(protocol.HeaderProtocolVersion, strconv.Itoa(protocol.ProtocolVersion))
	req.Header.Set(protocol.HeaderAgentVersion, c.agentVersion)

	if in != nil {
		req.Header.Set("Content-Type", contentTypeJSON)
	}

	if authenticated {
		req.Header.Set("Authorization", "Bearer "+c.credential.Secret())
	}

	return req, nil
}

func (c *Client) rememberServerVersion(v string) {
	if v == "" {
		return
	}

	c.mu.Lock()
	c.serverVersion = v
	c.mu.Unlock()
}

func readBody(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, MaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if len(body) > MaxResponseBytes {
		return nil, ErrResponseTooLarge
	}

	return body, nil
}

func decodeError(resp *http.Response, body []byte) *APIError {
	var parsed protocol.ErrorResponse

	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Error == "" {
		parsed = protocol.ErrorResponse{Message: strings.TrimSpace(string(body))}
	}

	return newAPIError(resp.StatusCode, parsed, retryAfter(resp.Header.Get("Retry-After")))
}

// retryAfter reads the header in either of its two forms, seconds or an HTTP date.
func retryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}

	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(max(seconds, 0)) * time.Second
	}

	if at, err := http.ParseTime(value); err == nil {
		return max(time.Until(at), 0)
	}

	return 0
}

func defaultHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	// No client-wide Timeout: the long poll needs its own, per-call deadline.
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         dialer.DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			IdleConnTimeout:     90 * time.Second,
			ForceAttemptHTTP2:   true,
		},
	}
}
