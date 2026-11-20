package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
)

// Merge methods, as the REST merge endpoint names them.
const (
	MergeMethodMerge  = "merge"
	MergeMethodSquash = "squash"
	MergeMethodRebase = "rebase"
)

// MergeMethods are every merge method, in GitHub's order.
var MergeMethods = []string{MergeMethodMerge, MergeMethodSquash, MergeMethodRebase}

// PRActor changes pull requests (P2.3). repo is "owner/name".
type PRActor interface {
	// PR fetches one PR as the list shows it, to refresh its row.
	PR(ctx context.Context, repo string, number int) (PR, error)
	// UpdateBranch merges the base branch into the PR's head.
	UpdateBranch(ctx context.Context, repo string, number int) error
	// Merge merges the PR with method (a MergeMethod* constant).
	Merge(ctx context.Context, repo string, number int, method string) error
	// EnableAutoMerge merges the PR with method once its requirements pass.
	EnableAutoMerge(ctx context.Context, repo string, number int, method string) error
	// SetDraft converts the PR to a draft (true) or marks it ready for
	// review (false).
	SetDraft(ctx context.Context, repo string, number int, draft bool) error
}

var _ PRActor = (*Client)(nil)

func splitRepo(repo string, number int) (owner, name string, err error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || number <= 0 {
		return "", "", fmt.Errorf("github: want owner/repo and a PR number, got %q #%d", repo, number)
	}
	return owner, name, nil
}

func checkMethod(method string) error {
	for _, m := range MergeMethods {
		if m == method {
			return nil
		}
	}
	return fmt.Errorf("github: unknown merge method %q", method)
}

var prQuery = `query ForgePR($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      id
      ` + prFields + `
    }
  }
}`

type gqlOnePR struct {
	Repository *struct {
		PullRequest *struct {
			ID string `json:"id"`
			gqlPull
		} `json:"pullRequest"`
	} `json:"repository"`
}

// fetchPR returns the PR row and its GraphQL node id.
func (c *Client) fetchPR(ctx context.Context, repo string, number int) (PR, string, error) {
	owner, name, err := splitRepo(repo, number)
	if err != nil {
		return PR{}, "", err
	}
	g, err := c.graphQL()
	if err != nil {
		return PR{}, "", fmt.Errorf("github: %w", err)
	}
	var data json.RawMessage
	vars := map[string]any{"owner": owner, "name": name, "number": number}
	if err := g.DoWithContext(ctx, prQuery, vars, &data); err != nil {
		return PR{}, "", fmt.Errorf("github: %s#%d: %w", repo, number, err)
	}
	var raw gqlOnePR
	if err := json.Unmarshal(data, &raw); err != nil {
		return PR{}, "", fmt.Errorf("github: %s#%d: %w", repo, number, err)
	}
	if raw.Repository == nil || raw.Repository.PullRequest == nil {
		return PR{}, "", fmt.Errorf("github: %s#%d: PR not found", repo, number)
	}
	n := raw.Repository.PullRequest
	p, ok := n.pr()
	if !ok || n.ID == "" {
		return PR{}, "", fmt.Errorf("github: %s#%d: PR not found", repo, number)
	}
	return p, n.ID, nil
}

// PR implements PRActor.
func (c *Client) PR(ctx context.Context, repo string, number int) (PR, error) {
	p, _, err := c.fetchPR(ctx, repo, number)
	return p, err
}

func (c *Client) restPut(ctx context.Context, repo string, number int, path string, body any) error {
	return c.restDo(ctx, http.MethodPut, repo, number, path, body)
}

// UpdateBranch implements PRActor.
func (c *Client) UpdateBranch(ctx context.Context, repo string, number int) error {
	return c.restPut(ctx, repo, number, "update-branch", struct{}{})
}

// Merge implements PRActor.
func (c *Client) Merge(ctx context.Context, repo string, number int, method string) error {
	if err := checkMethod(method); err != nil {
		return err
	}
	return c.restPut(ctx, repo, number, "merge", map[string]string{"merge_method": method})
}

const (
	autoMergeMutation = `mutation ForgeAutoMerge($id: ID!, $method: PullRequestMergeMethod!) {
  enablePullRequestAutoMerge(input: {pullRequestId: $id, mergeMethod: $method}) { clientMutationId }
}`
	draftMutation = `mutation ForgeDraft($id: ID!) {
  convertPullRequestToDraft(input: {pullRequestId: $id}) { clientMutationId }
}`
	readyMutation = `mutation ForgeReady($id: ID!) {
  markPullRequestReadyForReview(input: {pullRequestId: $id}) { clientMutationId }
}`
)

// mutate resolves the PR's node id and runs mutation with it as $id.
func (c *Client) mutate(ctx context.Context, repo string, number int, mutation string, vars map[string]any) error {
	_, id, err := c.fetchPR(ctx, repo, number)
	if err != nil {
		return err
	}
	g, err := c.graphQL()
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}
	all := map[string]any{"id": id}
	for k, v := range vars {
		all[k] = v
	}
	var data json.RawMessage
	if err := g.DoWithContext(ctx, mutation, all, &data); err != nil {
		return fmt.Errorf("github: %s#%d: %w", repo, number, err)
	}
	return nil
}

// EnableAutoMerge implements PRActor.
func (c *Client) EnableAutoMerge(ctx context.Context, repo string, number int, method string) error {
	if err := checkMethod(method); err != nil {
		return err
	}
	return c.mutate(ctx, repo, number, autoMergeMutation, map[string]any{"method": strings.ToUpper(method)})
}

// SetDraft implements PRActor.
func (c *Client) SetDraft(ctx context.Context, repo string, number int, draft bool) error {
	m := readyMutation
	if draft {
		m = draftMutation
	}
	return c.mutate(ctx, repo, number, m, nil)
}

// ActionError turns a failed PR action into toast text: "merge #12 failed:
// Pull Request is not mergeable", without go-gh's status codes and URLs.
func ActionError(verb string, number int, err error) error {
	return fmt.Errorf("%s #%d failed: %s", verb, number, Reason(err))
}

// Reason is the readable part of a GitHub error.
func Reason(err error) string {
	if err == nil {
		return ""
	}
	if IsAuthError(err) {
		return "not logged in to GitHub; run `gh auth login`"
	}
	var he *api.HTTPError
	if errors.As(err, &he) {
		var parts []string
		if msg, _, _ := strings.Cut(strings.TrimSpace(he.Message), "\n"); msg != "" {
			parts = append(parts, msg)
		}
		for _, it := range he.Errors {
			if it.Message != "" {
				parts = append(parts, it.Message)
			}
		}
		switch {
		case len(parts) > 0 && he.StatusCode == http.StatusForbidden:
			return "no permission: " + strings.Join(parts, "; ")
		case len(parts) > 0:
			return strings.Join(parts, "; ")
		case he.StatusCode == http.StatusForbidden:
			return "no permission"
		case he.StatusCode == http.StatusNotFound:
			return "not found (or no access)"
		}
		return fmt.Sprintf("GitHub answered %d", he.StatusCode)
	}
	var ge *api.GraphQLError
	if errors.As(err, &ge) {
		msgs := make([]string, 0, len(ge.Errors))
		for _, e := range ge.Errors {
			msgs = append(msgs, e.Message)
		}
		return strings.Join(msgs, "; ")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "GitHub took too long to answer"
	}
	return strings.TrimPrefix(err.Error(), "github: ")
}
