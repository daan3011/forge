package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SectionLimit is how many PRs one PR-list section fetches.
const SectionLimit = 50

// Review decisions (GraphQL PullRequestReviewDecision); "" = none needed.
const (
	ReviewApproved         = "APPROVED"
	ReviewChangesRequested = "CHANGES_REQUESTED"
	ReviewRequired         = "REVIEW_REQUIRED"
)

// CI states (GraphQL StatusState of the head commit's rollup); "" = no checks.
const (
	CISuccess  = "SUCCESS"
	CIFailure  = "FAILURE"
	CIError    = "ERROR"
	CIPending  = "PENDING"
	CIExpected = "EXPECTED"
)

// Mergeable states (GraphQL MergeableState).
const (
	Mergeable    = "MERGEABLE"
	Conflicting  = "CONFLICTING"
	MergeUnknown = "UNKNOWN"
)

// PR is a pull request as the PR list shows it. It is also the cache
// format, so fields keep their JSON names.
type PR struct {
	Repo           string `json:"repo"` // owner/name
	Number         int    `json:"number"`
	Title          string `json:"title"`
	Author         string `json:"author"` // login; "" for a deleted user
	URL            string `json:"url"`
	State          string `json:"state"` // StateOpen, StateDraft, StateMerged or StateClosed
	Draft          bool   `json:"draft"`
	ReviewDecision string `json:"review_decision"`
	CI             string `json:"ci"`
	Mergeable      string `json:"mergeable"`
	Additions      int    `json:"additions"`
	Deletions      int    `json:"deletions"`
	HeadRef        string `json:"head_ref"`
	BaseRef        string `json:"base_ref"`
	// CrossRepo is set for a PR from a fork, whose head isn't on origin.
	CrossRepo bool      `json:"cross_repo"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ID is the link id of the PR: "owner/repo#12".
func (p PR) ID() string { return p.Repo + "#" + strconv.Itoa(p.Number) }

// SearchResult is one section's search.
type SearchResult struct {
	PRs []PR `json:"prs"`
	// Total is GitHub's match count, which may exceed len(PRs).
	Total int `json:"total"`
	// Viewer is the login gh is authenticated as.
	Viewer string `json:"viewer"`
}

// PRSearcher runs GitHub PR searches.
type PRSearcher interface {
	// SearchPRs returns up to limit PRs matching a GitHub search query
	// (use BuildQuery), most recently updated first.
	SearchPRs(ctx context.Context, query string, limit int) (SearchResult, error)
}

// BuildQuery is the search query sent for a section: the section's query,
// with is:pr added when it has no is:pr / type:pr, and limited to repo
// ("owner/name") when repo is set. Results come newest-updated first
// unless the query sorts itself.
func BuildQuery(section, repo string) string {
	fields := strings.Fields(section)
	hasPR, hasSort := false, false
	for _, f := range fields {
		switch lf := strings.ToLower(f); {
		case lf == "is:pr" || lf == "type:pr":
			hasPR = true
		case strings.HasPrefix(lf, "sort:"):
			hasSort = true
		}
	}
	if !hasPR {
		fields = append([]string{"is:pr"}, fields...)
	}
	if repo != "" {
		fields = append(fields, "repo:"+repo)
	}
	if !hasSort {
		fields = append(fields, "sort:updated-desc")
	}
	return strings.Join(fields, " ")
}

// prFields are the PullRequest fields a list row needs, shared by the
// search and the single-PR refresh (P2.3).
const prFields = `number title url isDraft state reviewDecision mergeable
        additions deletions createdAt updatedAt headRefName baseRefName isCrossRepository
        author { login }
        repository { nameWithOwner }
        commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }`

const searchQuery = `query ForgePRSearch($q: String!, $first: Int!) {
  viewer { login }
  search(type: ISSUE, query: $q, first: $first) {
    issueCount
    nodes {
      ... on PullRequest {
        ` + prFields + `
      }
    }
  }
}`

// SearchPRs implements PRSearcher with one GraphQL search.
func (c *Client) SearchPRs(ctx context.Context, query string, limit int) (SearchResult, error) {
	g, err := c.graphQL()
	if err != nil {
		return SearchResult{}, fmt.Errorf("github: %w", err)
	}
	var data json.RawMessage
	if err := g.DoWithContext(ctx, searchQuery, map[string]any{"q": query, "first": limit}, &data); err != nil {
		return SearchResult{}, fmt.Errorf("github: search %q: %w", query, err)
	}
	res, err := ParseSearch(bytes.NewReader(data))
	if err != nil {
		return SearchResult{}, fmt.Errorf("github: search %q: %w", query, err)
	}
	return res, nil
}

type gqlSearch struct {
	Viewer struct {
		Login string `json:"login"`
	} `json:"viewer"`
	Search struct {
		IssueCount int       `json:"issueCount"`
		Nodes      []gqlPull `json:"nodes"`
	} `json:"search"`
}

type gqlPull struct {
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	URL            string    `json:"url"`
	IsDraft        bool      `json:"isDraft"`
	State          string    `json:"state"`
	ReviewDecision string    `json:"reviewDecision"`
	Mergeable      string    `json:"mergeable"`
	Additions      int       `json:"additions"`
	Deletions      int       `json:"deletions"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	HeadRefName    string    `json:"headRefName"`
	BaseRefName    string    `json:"baseRefName"`
	IsCross        bool      `json:"isCrossRepository"`
	Author         *struct {
		Login string `json:"login"`
	} `json:"author"`
	Repository *struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

// ParseSearch reads the data object of a search response. Nodes that
// aren't pull requests (empty objects) are skipped.
func ParseSearch(r io.Reader) (SearchResult, error) {
	var raw gqlSearch
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return SearchResult{}, err
	}
	res := SearchResult{Total: raw.Search.IssueCount, Viewer: raw.Viewer.Login, PRs: []PR{}}
	for _, n := range raw.Search.Nodes {
		if p, ok := n.pr(); ok {
			res.PRs = append(res.PRs, p)
		}
	}
	return res, nil
}

// pr converts a node; false for nodes that aren't pull requests.
func (n gqlPull) pr() (PR, bool) {
	if n.Number == 0 || n.Repository == nil {
		return PR{}, false
	}
	p := PR{
		Repo: n.Repository.NameWithOwner, Number: n.Number, Title: n.Title, URL: n.URL,
		Draft: n.IsDraft, State: gqlState(n.State, n.IsDraft),
		ReviewDecision: n.ReviewDecision, Mergeable: n.Mergeable,
		Additions: n.Additions, Deletions: n.Deletions,
		HeadRef: n.HeadRefName, BaseRef: n.BaseRefName, CrossRepo: n.IsCross,
		CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
	}
	if n.Author != nil {
		p.Author = n.Author.Login
	}
	if len(n.Commits.Nodes) > 0 {
		if r := n.Commits.Nodes[0].Commit.StatusCheckRollup; r != nil {
			p.CI = r.State
		}
	}
	return p, true
}

func gqlState(state string, draft bool) string {
	switch state {
	case "MERGED":
		return StateMerged
	case "CLOSED":
		return StateClosed
	}
	if draft {
		return StateDraft
	}
	return StateOpen
}

var prRefRe = regexp.MustCompile(`^([\w.-]+)/([\w.-]+)#(\d+)$`)

// ParsePRRef reads "owner/repo#12" or a https://github.com/owner/repo/pull/12
// URL.
func ParsePRRef(ref string) (repo string, number int, err error) {
	ref = strings.TrimSpace(ref)
	if u, ok := strings.CutPrefix(ref, "https://github.com/"); ok {
		parts := strings.Split(strings.Trim(u, "/"), "/")
		if len(parts) >= 4 && parts[2] == "pull" {
			ref = parts[0] + "/" + parts[1] + "#" + parts[3]
		}
	}
	m := prRefRe.FindStringSubmatch(ref)
	if m == nil {
		return "", 0, fmt.Errorf("github: PR ref %q: want owner/repo#number or a PR URL", ref)
	}
	n, _ := strconv.Atoi(m[3])
	return m[1] + "/" + m[2], n, nil
}

const prContextQuery = `query ForgePRContext($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) { title body headRefName }
  }
}`

// PRContext resolves the {pr_title} and {pr_body} template variables
// for ref ("owner/repo#12" or a PR URL). It is the templates provider
// for github_pr links.
func (c *Client) PRContext(ctx context.Context, ref string) (map[string]string, error) {
	repo, n, err := ParsePRRef(ref)
	if err != nil {
		return nil, err
	}
	g, err := c.graphQL()
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	owner, name, _ := strings.Cut(repo, "/")
	var data json.RawMessage
	vars := map[string]any{"owner": owner, "name": name, "number": n}
	if err := g.DoWithContext(ctx, prContextQuery, vars, &data); err != nil {
		return nil, fmt.Errorf("github: %s: %w", ref, err)
	}
	return ParsePRContext(bytes.NewReader(data))
}

// ParsePRContext reads the data object of a PRContext response.
func ParsePRContext(r io.Reader) (map[string]string, error) {
	var raw struct {
		Repository *struct {
			PullRequest *struct {
				Title string `json:"title"`
				Body  string `json:"body"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, err
	}
	if raw.Repository == nil || raw.Repository.PullRequest == nil {
		return nil, fmt.Errorf("github: PR not found")
	}
	pr := raw.Repository.PullRequest
	return map[string]string{"pr_title": pr.Title, "pr_body": pr.Body}, nil
}
