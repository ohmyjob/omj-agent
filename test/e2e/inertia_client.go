//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// inertiaClient reads and writes through the pages a browser would use, so a test
// asserts what an operator sees rather than what the database holds.
type inertiaClient struct {
	baseURL string
	http    *http.Client
	version string
}

type page struct {
	Component string          `json:"component"`
	Props     json.RawMessage `json:"props"`
	URL       string          `json:"url"`
	Version   string          `json:"version"`
}

// Inertia 3 puts the page in the body of a script element, not in the attribute:
// data-page="app" is only the marker that names it.
var dataPagePattern = regexp.MustCompile(`(?s)<script[^>]*data-page="app"[^>]*>(.*?)</script>`)

func newInertiaClient(baseURL string) (*inertiaClient, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("cookie jar: %w", err)
	}

	return &inertiaClient{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		http:    &http.Client{Jar: jar, Timeout: 30 * time.Second},
	}, nil
}

// bootstrap reads one full HTML response to learn the asset version. Without it every
// XHR navigation answers 409 and asks the browser to reload.
func (c *inertiaClient) bootstrap(ctx context.Context, path string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("bootstrap request: %w", err)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("bootstrap %s: %w", path, err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("bootstrap body: %w", err)
	}

	match := dataPagePattern.FindSubmatch(body)
	if match == nil {
		return fmt.Errorf("bootstrap %s: no Inertia page in the response", path)
	}

	var initial page
	if err := json.Unmarshal([]byte(html.UnescapeString(string(match[1]))), &initial); err != nil {
		return fmt.Errorf("bootstrap page: %w", err)
	}

	c.version = initial.Version

	return nil
}

func (c *inertiaClient) get(ctx context.Context, path string) (*page, error) {
	return c.send(ctx, http.MethodGet, path, nil)
}

func (c *inertiaClient) post(ctx context.Context, path string, body any) (*page, error) {
	return c.send(ctx, http.MethodPost, path, body)
}

// deferred follows the same partial reload as an Inertia browser after opening a tab.
func (c *inertiaClient) deferred(ctx context.Context, path, key string) (*page, error) {
	initial, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	return c.send(ctx, http.MethodGet, path, nil, http.Header{
		"X-Inertia-Partial-Component": []string{initial.Component},
		"X-Inertia-Partial-Data":      []string{key},
	})
}

func (c *inertiaClient) json(ctx context.Context, path string, into any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("json request: %w", err)
	}

	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Requested-With", "XMLHttpRequest")

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", path, response.Status)
	}

	if err := json.NewDecoder(response.Body).Decode(into); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}

	return nil
}

func (c *inertiaClient) send(ctx context.Context, method, path string, body any, headers ...http.Header) (*page, error) {
	var payload io.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode body: %w", err)
		}

		payload = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}

	request.Header.Set("X-Inertia", "true")
	request.Header.Set("X-Inertia-Version", c.version)
	request.Header.Set("X-Requested-With", "XMLHttpRequest")
	request.Header.Set("Accept", "text/html, application/xhtml+xml")

	for _, header := range headers {
		for name, values := range header {
			request.Header[name] = values
		}
	}

	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	if token := c.token(); token != "" {
		request.Header.Set("X-XSRF-TOKEN", token)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	if response.StatusCode >= 400 {
		return nil, fmt.Errorf("%s %s: %s: %s", method, path, response.Status, excerpt(raw))
	}

	var result page
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("%s %s: not an Inertia page: %s", method, path, excerpt(raw))
	}

	return &result, nil
}

// token returns the CSRF value Laravel puts in a cookie, which it expects back in a header.
func (c *inertiaClient) token() string {
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return ""
	}

	for _, cookie := range c.http.Jar.Cookies(parsed) {
		if cookie.Name != "XSRF-TOKEN" {
			continue
		}

		decoded, err := url.QueryUnescape(cookie.Value)
		if err != nil {
			return cookie.Value
		}

		return decoded
	}

	return ""
}

func (c *inertiaClient) login(ctx context.Context, email, password string) error {
	if err := c.bootstrap(ctx, "/login"); err != nil {
		return err
	}

	if _, err := c.post(ctx, "/login", map[string]any{
		"email":    email,
		"password": password,
		"remember": false,
	}); err != nil {
		return fmt.Errorf("log in as %s: %w", email, err)
	}

	return nil
}

func props[T any](p *page) (T, error) {
	var into T

	if err := json.Unmarshal(p.Props, &into); err != nil {
		return into, fmt.Errorf("decode props of %s: %w", p.Component, err)
	}

	return into, nil
}

func excerpt(body []byte) string {
	const limit = 300

	text := strings.TrimSpace(string(body))
	if len(text) > limit {
		return text[:limit] + "…"
	}

	return text
}
