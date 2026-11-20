package github

import (
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/cli/go-gh/v2/pkg/api"
)

// Client talks to GitHub through go-gh, which reuses `gh auth`: REST for
// the session PR links (P1.9), GraphQL for the PR list (P2.1). Both clients
// are made on first use, so a missing login only fails the calls, not
// startup, and share one transport that reports every response.
type Client struct {
	opts api.ClientOptions
	// OnResponse, if set, sees every HTTP response (also failed ones), so
	// the caller can report the rate limit with Observe.
	OnResponse func(*http.Response)

	restOnce sync.Once
	restC    *api.RESTClient
	restErr  error

	gqlOnce sync.Once
	gqlC    *api.GraphQLClient
	gqlErr  error
}

var (
	_ PullFinder = (*Client)(nil)
	_ PRSearcher = (*Client)(nil)
)

// NewClient returns a client. Zero options use gh's host and token.
func NewClient(opts api.ClientOptions) *Client { return &Client{opts: opts} }

func (c *Client) options() api.ClientOptions {
	opts := c.opts
	base := opts.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	opts.Transport = observer{base: base, on: func(resp *http.Response) {
		if c.OnResponse != nil {
			c.OnResponse(resp)
		}
	}}
	return opts
}

func (c *Client) rest() (*api.RESTClient, error) {
	c.restOnce.Do(func() { c.restC, c.restErr = api.NewRESTClient(c.options()) })
	return c.restC, c.restErr
}

func (c *Client) graphQL() (*api.GraphQLClient, error) {
	c.gqlOnce.Do(func() { c.gqlC, c.gqlErr = api.NewGraphQLClient(c.options()) })
	return c.gqlC, c.gqlErr
}

type observer struct {
	base http.RoundTripper
	on   func(*http.Response)
}

func (o observer) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := o.base.RoundTrip(req)
	if resp != nil {
		o.on(resp)
	}
	return resp, err
}

// IsAuthError reports whether err means gh has no usable login: no token
// at all, or GitHub refused the one it has.
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}
	var he *api.HTTPError
	if errors.As(err, &he) && he.StatusCode == http.StatusUnauthorized {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "authentication token not found")
}
