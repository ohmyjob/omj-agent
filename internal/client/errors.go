package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/ohmyjob/omj-agent/internal/protocol"
)

// ErrResponseTooLarge is returned when a response body exceeds MaxResponseBytes.
var ErrResponseTooLarge = errors.New("response body exceeds 1 MiB")

// ErrNoCredential is returned by every call except Enroll when the client has
// no credential to send.
var ErrNoCredential = errors.New("no credential: the agent is not enrolled")

// APIError is a non-2xx answer from the Server, decoded from the protocol
// error shape.
type APIError struct {
	Status                    int
	Code                      protocol.ErrorCode
	Message                   string
	Retryable                 bool
	RetryAfter                time.Duration
	Errors                    map[string][]string
	SupportedProtocolVersions []int
	MinAgentVersion           string
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("server answered %d %s", e.Status, http.StatusText(e.Status))
	}

	return fmt.Sprintf("server answered %d %s: %s", e.Status, e.Code, e.Message)
}

func newAPIError(status int, body protocol.ErrorResponse, retryAfter time.Duration) *APIError {
	return &APIError{
		Status:                    status,
		Code:                      body.Error,
		Message:                   body.Message,
		Retryable:                 status >= http.StatusInternalServerError || status == http.StatusTooManyRequests,
		RetryAfter:                retryAfter,
		Errors:                    body.Errors,
		SupportedProtocolVersions: body.SupportedProtocolVersions,
		MinAgentVersion:           body.MinAgentVersion,
	}
}

func asAPIError(err error) (*APIError, bool) {
	var apiErr *APIError

	return apiErr, errors.As(err, &apiErr)
}

func IsUnauthorized(err error) bool {
	apiErr, ok := asAPIError(err)

	return ok && apiErr.Status == http.StatusUnauthorized
}

func IsUnsupportedProtocol(err error) bool {
	apiErr, ok := asAPIError(err)

	return ok && apiErr.Status == http.StatusUpgradeRequired
}

func IsConflict(err error, code protocol.ErrorCode) bool {
	apiErr, ok := asAPIError(err)

	return ok && apiErr.Status == http.StatusConflict && apiErr.Code == code
}

func IsNotFound(err error) bool {
	apiErr, ok := asAPIError(err)

	return ok && apiErr.Status == http.StatusNotFound
}

func IsPayloadTooLarge(err error) bool {
	apiErr, ok := asAPIError(err)

	return ok && apiErr.Status == http.StatusRequestEntityTooLarge
}

func IsThrottled(err error) bool {
	apiErr, ok := asAPIError(err)

	return ok && apiErr.Status == http.StatusTooManyRequests
}

// IsRetryable reports whether waiting and sending the same request again can
// succeed: 5xx and 429 answers, timeouts and network failures. A cancelled
// context, a client-side mistake (4xx) and a malformed response are final.
func IsRetryable(err error) bool {
	if apiErr, ok := asAPIError(err); ok {
		return apiErr.Retryable
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, ErrResponseTooLarge) || errors.Is(err, ErrNoCredential) {
		return false
	}

	var netErr net.Error

	return errors.As(err, &netErr) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}

// RetryAfter returns how long the Server asked the Agent to wait, or zero.
func RetryAfter(err error) time.Duration {
	if apiErr, ok := asAPIError(err); ok {
		return apiErr.RetryAfter
	}

	return 0
}
