package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
)

// call is one request the fake GitHub saw.
type call struct {
	method, path string
	query        string         // GraphQL query text
	vars         map[string]any // GraphQL variables
	body         map[string]any // REST body
}

// actionServer answers GraphQL with a PR node (or gqlBody) and REST with
// restStatus / restBody, recording every request.
func actionServer(calls *[]call, gqlBody string, restStatus int, restBody string) *Client {
	return NewClient(api.ClientOptions{
		Host: "github.com", AuthToken: "t",
		Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			c := call{method: req.Method, path: req.URL.Path}
			raw, _ := io.ReadAll(req.Body)
			if strings.HasSuffix(req.URL.Path, "/graphql") {
				var b struct {
					Query     string         `json:"query"`
					Variables map[string]any `json:"variables"`
				}
				_ = json.Unmarshal(raw, &b)
				c.query, c.vars = b.Query, b.Variables
				*calls = append(*calls, c)
				body := gqlBody
				if strings.HasPrefix(strings.TrimSpace(b.Query), "mutation") {
					body = `{"data":{"x":{"clientMutationId":null}}}`
				}
				return respond(req, 200, body), nil
			}
			_ = json.Unmarshal(raw, &c.body)
			*calls = append(*calls, c)
			return respond(req, restStatus, restBody), nil
		}),
	})
}

const prNode = `{"data":{"repository":{"pullRequest":{
  "id":"PR_node12","number":12,"title":"Retry","url":"https://github.com/daan/forge/pull/12",
  "isDraft":true,"state":"OPEN","headRefName":"retry","baseRefName":"main","isCrossRepository":true,
  "updatedAt":"2026-11-21T10:00:00Z","author":{"login":"alice"},
  "repository":{"nameWithOwner":"daan/forge"},
  "commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}
}}}}`

func TestClientPR(t *testing.T) {
	var calls []call
	c := actionServer(&calls, prNode, 200, "")
	p, err := c.PR(context.Background(), "daan/forge", 12)
	if err != nil {
		t.Fatal(err)
	}
	want := PR{
		Repo: "daan/forge", Number: 12, Title: "Retry", URL: "https://github.com/daan/forge/pull/12",
		State: StateDraft, Draft: true, Author: "alice", CI: CISuccess, HeadRef: "retry", BaseRef: "main",
		CrossRepo: true, UpdatedAt: ts("2026-11-21T10:00:00Z"),
	}
	if p != want {
		t.Errorf("PR = %+v\nwant %+v", p, want)
	}
	if len(calls) != 1 || calls[0].vars["owner"] != "daan" || calls[0].vars["number"] != float64(12) {
		t.Errorf("calls = %+v", calls)
	}

	for _, body := range []string{
		`{"data":{"repository":{"pullRequest":null}}}`,
		`{"data":null,"errors":[{"message":"Could not resolve to a Repository"}]}`,
	} {
		if _, err := actionServer(&calls, body, 200, "").PR(context.Background(), "daan/forge", 12); err == nil {
			t.Errorf("%s: want an error", body)
		}
	}
	if _, err := c.PR(context.Background(), "forge", 12); err == nil {
		t.Error("bad repo: want an error")
	}
}

func TestClientRESTActions(t *testing.T) {
	tests := []struct {
		name     string
		run      func(*Client) error
		status   int
		wantPath string
		wantBody map[string]any
		wantErr  string
	}{
		{
			"update branch", func(c *Client) error { return c.UpdateBranch(context.Background(), "daan/forge", 12) },
			202, "/repos/daan/forge/pulls/12/update-branch",
			map[string]any{},
			"",
		},
		{
			"merge squash", func(c *Client) error { return c.Merge(context.Background(), "daan/forge", 12, MergeMethodSquash) },
			200, "/repos/daan/forge/pulls/12/merge",
			map[string]any{"merge_method": "squash"},
			"",
		},
		{
			"merge refused", func(c *Client) error { return c.Merge(context.Background(), "daan/forge", 12, MergeMethodMerge) },
			405, "/repos/daan/forge/pulls/12/merge",
			map[string]any{"merge_method": "merge"},
			"Pull Request is not mergeable",
		},
		{
			"bad method", func(c *Client) error { return c.Merge(context.Background(), "daan/forge", 12, "yolo") },
			200, "", nil, "unknown merge method",
		},
		{
			"bad repo", func(c *Client) error { return c.UpdateBranch(context.Background(), "forge", 12) },
			200, "", nil, "owner/repo",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			c := actionServer(&calls, prNode, tt.status, `{"message":"Pull Request is not mergeable"}`)
			err := tt.run(c)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("err = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if tt.wantPath == "" {
				if len(calls) != 0 {
					t.Errorf("calls = %+v, want none", calls)
				}
				return
			}
			if len(calls) != 1 || calls[0].method != http.MethodPut || calls[0].path != tt.wantPath {
				t.Fatalf("calls = %+v", calls)
			}
			if fmt.Sprint(calls[0].body) != fmt.Sprint(tt.wantBody) {
				t.Errorf("body = %v, want %v", calls[0].body, tt.wantBody)
			}
		})
	}
}

func TestClientMutations(t *testing.T) {
	tests := []struct {
		name     string
		run      func(*Client) error
		wantOp   string
		wantVars map[string]any
	}{
		{"auto-merge", func(c *Client) error {
			return c.EnableAutoMerge(context.Background(), "daan/forge", 12, MergeMethodRebase)
		}, "enablePullRequestAutoMerge", map[string]any{"id": "PR_node12", "method": "REBASE"}},
		{
			"to draft", func(c *Client) error { return c.SetDraft(context.Background(), "daan/forge", 12, true) },
			"convertPullRequestToDraft",
			map[string]any{"id": "PR_node12"},
		},
		{
			"ready", func(c *Client) error { return c.SetDraft(context.Background(), "daan/forge", 12, false) },
			"markPullRequestReadyForReview",
			map[string]any{"id": "PR_node12"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			if err := tt.run(actionServer(&calls, prNode, 200, "")); err != nil {
				t.Fatal(err)
			}
			if len(calls) != 2 {
				t.Fatalf("calls = %+v, want node id lookup + mutation", calls)
			}
			m := calls[1]
			if !strings.Contains(m.query, tt.wantOp) || fmt.Sprint(m.vars) != fmt.Sprint(tt.wantVars) {
				t.Errorf("mutation = %q %v", m.query, m.vars)
			}
		})
	}
}

func TestReason(t *testing.T) {
	u, _ := url.Parse("https://api.github.com/repos/daan/forge/pulls/12/merge")
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			"http message", fmt.Errorf("github: daan/forge#12: %w", &api.HTTPError{StatusCode: 405, Message: "Pull Request is not mergeable", RequestURL: u}),
			"Pull Request is not mergeable",
		},
		{
			"http items", &api.HTTPError{StatusCode: 422, Errors: []api.HTTPErrorItem{{Message: "merge conflict"}}, RequestURL: u},
			"merge conflict",
		},
		{
			"forbidden", &api.HTTPError{StatusCode: 403, Message: "Resource not accessible by integration", RequestURL: u},
			"no permission: Resource not accessible by integration",
		},
		{"not found", &api.HTTPError{StatusCode: 404, RequestURL: u}, "not found (or no access)"},
		{
			"auth", &api.HTTPError{StatusCode: 401, Message: "Bad credentials", RequestURL: u},
			"not logged in to GitHub; run `gh auth login`",
		},
		{
			"graphql", &api.GraphQLError{Errors: []api.GraphQLErrorItem{{Message: "Pull request is in clean status"}}},
			"Pull request is in clean status",
		},
		{"timeout", fmt.Errorf("github: x: %w", context.DeadlineExceeded), "GitHub took too long to answer"},
		{"plain", errors.New("github: want owner/repo"), "want owner/repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Reason(tt.err); got != tt.want {
				t.Errorf("Reason = %q, want %q", got, tt.want)
			}
		})
	}
	if got := ActionError("merge", 12, errors.New("nope")).Error(); got != "merge #12 failed: nope" {
		t.Errorf("ActionError = %q", got)
	}
}
