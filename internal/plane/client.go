package plane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/daan3011/forge/internal/config"
)

// Client talks to the Plane v2 REST API (/api/v2/) of one workspace. The
// base URL comes from config, so self-hosted instances with v2 work too.
// List responses are written through to the cache when one is set.
type Client struct {
	base      string // e.g. https://api.plane.so, no trailing slash
	workspace string
	key       string
	hc        *http.Client

	cache    Cache
	cacheTTL time.Duration

	now        func() time.Time
	sleep      func(context.Context, time.Duration) error
	maxRetries int

	// OnResponse, if set, sees every HTTP response (also failed ones and
	// 429s that are retried).
	OnResponse func(*http.Response)
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets the HTTP client (default: one with a 30s timeout).
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// WithCache writes list responses through to cache with the given TTL.
func WithCache(cache Cache, ttl time.Duration) Option {
	return func(c *Client) { c.cache, c.cacheTTL = cache, ttl }
}

// WithClock replaces time.Now.
func WithClock(now func() time.Time) Option { return func(c *Client) { c.now = now } }

// WithSleep replaces the context-aware wait used between 429 retries.
func WithSleep(sleep func(context.Context, time.Duration) error) Option {
	return func(c *Client) { c.sleep = sleep }
}

// WithMaxRetries sets how often a rate-limited request is retried (default 3).
func WithMaxRetries(n int) Option { return func(c *Client) { c.maxRetries = n } }

// New returns a client for cfg's base URL and workspace, authenticating
// with key (resolve it with cfg.ResolveAPIKey).
func New(cfg config.Plane, key string, opts ...Option) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	switch {
	case base == "":
		return nil, errors.New("plane: base_url is not set")
	case strings.TrimSpace(cfg.Workspace) == "":
		return nil, errors.New("plane: workspace_slug is not set")
	case strings.TrimSpace(key) == "":
		return nil, errors.New("plane: no API key")
	}
	c := &Client{
		base:       base,
		workspace:  strings.TrimSpace(cfg.Workspace),
		key:        strings.TrimSpace(key),
		hc:         &http.Client{Timeout: 30 * time.Second},
		now:        time.Now,
		sleep:      sleepCtx,
		maxRetries: 3,
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// Workspace is the workspace slug the client is bound to.
func (c *Client) Workspace() string { return c.workspace }

// wsPath is the path of a workspace-scoped resource, e.g.
// wsPath("projects", id, "states") → workspaces/<slug>/projects/<id>/states.
func (c *Client) wsPath(parts ...string) string {
	segs := []string{"workspaces", url.PathEscape(c.workspace)}
	for _, p := range parts {
		segs = append(segs, url.PathEscape(p))
	}
	return strings.Join(segs, "/")
}

func (c *Client) endpoint(path string, q url.Values) string {
	u := c.base + "/api/v2/" + strings.Trim(path, "/") + "/"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// do sends one request, retrying on 429, and decodes a 2xx JSON body into
// out (if non-nil). Other statuses become an *APIError.
func (c *Client) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("plane: encode %s %s: %w", method, path, err)
		}
	}
	for attempt := 0; ; attempt++ {
		resp, err := c.send(ctx, method, c.endpoint(path, q), payload)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < c.maxRetries {
			wait := retryDelay(resp.Header, c.now(), attempt)
			drain(resp)
			if err := c.sleep(ctx, wait); err != nil {
				return err
			}
			continue
		}
		return decode(resp, method, path, out)
	}
}

func (c *Client) send(ctx context.Context, method, u string, payload []byte) (*http.Response, error) {
	var rd io.Reader
	if payload != nil {
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, fmt.Errorf("plane: %w", err)
	}
	req.Header.Set("X-API-Key", c.key)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plane: %w", err)
	}
	if c.OnResponse != nil {
		c.OnResponse(resp)
	}
	return resp, nil
}

func decode(resp *http.Response, method, path string, out any) error {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return parseAPIError(resp, method, path)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("plane: decode %s %s: %w", method, path, err)
	}
	return nil
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
