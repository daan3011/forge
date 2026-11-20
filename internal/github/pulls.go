package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

// PR states as forge shows them.
const (
	StateOpen   = "open"
	StateDraft  = "draft"
	StateMerged = "merged"
	StateClosed = "closed"
)

// Pull is a pull request as the session links need it (P1.9). P2.1 grows
// the full PR model.
type Pull struct {
	Repo      string // owner/name
	Number    int
	Title     string
	State     string // StateOpen, StateDraft, StateMerged or StateClosed
	URL       string
	HeadRef   string
	UpdatedAt time.Time
}

// PullFinder finds the pull requests opened from a branch.
type PullFinder interface {
	// PullsForBranch returns repo's PRs (any state) whose head is branch
	// in repo itself, most recently updated first.
	PullsForBranch(ctx context.Context, repo, branch string) ([]Pull, error)
}

// REST is the PullFinder backed by the GitHub REST API through go-gh, which
// reuses `gh auth`. The client is made on first use, so a missing login
// only fails the calls, not startup.
type REST struct {
	opts api.ClientOptions
	// OnResponse, if set, sees every HTTP response (also failed ones), so
	// the caller can report the rate limit with Observe.
	OnResponse func(*http.Response)

	once   sync.Once
	client *api.RESTClient
	err    error
}

var _ PullFinder = (*REST)(nil)

// NewREST returns a REST client. Zero options use gh's host and token.
func NewREST(opts api.ClientOptions) *REST { return &REST{opts: opts} }

func (r *REST) rest() (*api.RESTClient, error) {
	r.once.Do(func() {
		opts := r.opts
		base := opts.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		opts.Transport = observer{base: base, on: func(resp *http.Response) {
			if r.OnResponse != nil {
				r.OnResponse(resp)
			}
		}}
		r.client, r.err = api.NewRESTClient(opts)
	})
	return r.client, r.err
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

// maxPulls is how many PRs per branch are fetched.
const maxPulls = 10

// PullsForBranch implements PullFinder.
func (r *REST) PullsForBranch(ctx context.Context, repo, branch string) ([]Pull, error) {
	owner, _, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || branch == "" {
		return nil, fmt.Errorf("github: pulls for %q on %q: need owner/repo and a branch", branch, repo)
	}
	c, err := r.rest()
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	q := url.Values{
		"head": {owner + ":" + branch}, "state": {"all"},
		"sort": {"updated"}, "direction": {"desc"}, "per_page": {fmt.Sprint(maxPulls)},
	}
	resp, err := c.RequestWithContext(ctx, http.MethodGet, "repos/"+repo+"/pulls?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("github: pulls for %s: %w", branch, err)
	}
	defer resp.Body.Close()
	pulls, err := ParsePulls(resp.Body, repo)
	if err != nil {
		return nil, fmt.Errorf("github: pulls for %s: %w", branch, err)
	}
	return pulls, nil
}

type restPull struct {
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	State     string     `json:"state"`
	Draft     bool       `json:"draft"`
	MergedAt  *time.Time `json:"merged_at"`
	HTMLURL   string     `json:"html_url"`
	UpdatedAt time.Time  `json:"updated_at"`
	Head      struct {
		Ref string `json:"ref"`
	} `json:"head"`
}

// ParsePulls reads a REST list-pulls response.
func ParsePulls(r io.Reader, repo string) ([]Pull, error) {
	var raw []restPull
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]Pull, 0, len(raw))
	for _, p := range raw {
		out = append(out, Pull{
			Repo: repo, Number: p.Number, Title: p.Title, State: pullState(p),
			URL: p.HTMLURL, HeadRef: p.Head.Ref, UpdatedAt: p.UpdatedAt,
		})
	}
	return out, nil
}

func pullState(p restPull) string {
	switch {
	case p.MergedAt != nil:
		return StateMerged
	case p.State == "closed":
		return StateClosed
	case p.Draft:
		return StateDraft
	}
	return StateOpen
}

// PullURL is the web URL of repo's PR number n.
func PullURL(repo string, n int) string {
	return fmt.Sprintf("https://github.com/%s/pull/%d", repo, n)
}
