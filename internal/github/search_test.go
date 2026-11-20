package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

func TestParseSearch(t *testing.T) {
	f, err := os.Open("testdata/search.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	res, err := ParseSearch(f)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 57 || res.Viewer != "daan" || len(res.PRs) != 3 {
		t.Fatalf("got total %d viewer %q, %d PRs", res.Total, res.Viewer, len(res.PRs))
	}
	tests := []struct {
		idx                                    int
		id, author, state, review, ci, mergeOK string
		draft                                  bool
		adds, dels                             int
	}{
		{0, "daan/forge#12", "daan", StateOpen, ReviewApproved, CIFailure, Mergeable, false, 391, 4},
		{1, "acme/ui#7", "", StateDraft, "", "", Conflicting, true, 1200, 2},
		{2, "acme/ui#3", "bob", StateMerged, ReviewRequired, "", MergeUnknown, false, 0, 9},
	}
	for _, tt := range tests {
		p := res.PRs[tt.idx]
		if p.ID() != tt.id || p.Author != tt.author || p.State != tt.state || p.ReviewDecision != tt.review ||
			p.CI != tt.ci || p.Mergeable != tt.mergeOK || p.Draft != tt.draft || p.Additions != tt.adds || p.Deletions != tt.dels {
			t.Errorf("PR %d = %+v", tt.idx, p)
		}
	}
	first := res.PRs[0]
	if first.HeadRef != "agent/retry" || first.BaseRef != "main" || first.URL != "https://github.com/daan/forge/pull/12" ||
		!first.UpdatedAt.Equal(time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("first PR fields = %+v", first)
	}
	if _, err := ParseSearch(strings.NewReader("{")); err == nil {
		t.Error("want error for bad JSON")
	}
	empty, err := ParseSearch(strings.NewReader(`{"viewer":{"login":"x"},"search":{"issueCount":0,"nodes":[]}}`))
	if err != nil || empty.PRs == nil || len(empty.PRs) != 0 {
		t.Errorf("empty = %+v, %v (want non-nil empty slice)", empty, err)
	}
}

func TestBuildQuery(t *testing.T) {
	tests := []struct {
		section, repo, want string
	}{
		{"is:open author:@me", "", "is:pr is:open author:@me sort:updated-desc"},
		{"is:open author:@me", "daan/forge", "is:pr is:open author:@me repo:daan/forge sort:updated-desc"},
		{"is:pr  is:open", "", "is:pr is:open sort:updated-desc"},
		{"type:pr involves:@me sort:created-asc", "", "type:pr involves:@me sort:created-asc"},
		{"IS:PR review-requested:@me", "a/b", "IS:PR review-requested:@me repo:a/b sort:updated-desc"},
	}
	for _, tt := range tests {
		if got := BuildQuery(tt.section, tt.repo); got != tt.want {
			t.Errorf("BuildQuery(%q, %q) = %q, want %q", tt.section, tt.repo, got, tt.want)
		}
	}
}

func TestClientSearchPRs(t *testing.T) {
	fixture, err := os.ReadFile("testdata/search.json")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		status  int
		body    string
		wantN   int
		wantErr bool
	}{
		{"ok", 200, `{"data":` + string(fixture) + `}`, 3, false},
		{"graphql error", 200, `{"data":null,"errors":[{"message":"bad query"}]}`, 0, true},
		{"http error", 502, `{"message":"bad gateway"}`, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body map[string]any
			seen := 0
			c := NewClient(api.ClientOptions{
				Host: "github.com", AuthToken: "t",
				Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
					b, _ := io.ReadAll(req.Body)
					_ = json.Unmarshal(b, &body)
					return respond(req, tt.status, tt.body), nil
				}),
			})
			c.OnResponse = func(*http.Response) { seen++ }
			res, err := c.SearchPRs(context.Background(), "is:pr author:@me", 50)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if len(res.PRs) != tt.wantN {
				t.Errorf("got %d PRs", len(res.PRs))
			}
			if seen != 1 {
				t.Errorf("OnResponse called %d times", seen)
			}
			vars, _ := body["variables"].(map[string]any)
			if vars["q"] != "is:pr author:@me" || vars["first"] != float64(50) {
				t.Errorf("variables = %v", vars)
			}
		})
	}
}

func TestParsePRRef(t *testing.T) {
	tests := []struct {
		ref, repo string
		n         int
		wantErr   bool
	}{
		{"daan/forge#12", "daan/forge", 12, false},
		{" https://github.com/acme/ui.js/pull/7/files ", "acme/ui.js", 7, false},
		{"#12", "", 0, true},
		{"12", "", 0, true},
		{"daan/forge", "", 0, true},
	}
	for _, tt := range tests {
		repo, n, err := ParsePRRef(tt.ref)
		if (err != nil) != tt.wantErr || repo != tt.repo || n != tt.n {
			t.Errorf("ParsePRRef(%q) = %q, %d, %v", tt.ref, repo, n, err)
		}
	}
}

func TestClientPRContext(t *testing.T) {
	tests := []struct {
		name, ref, body string
		want            map[string]string
		wantErr         bool
	}{
		{
			"ok", "daan/forge#12", `{"data":{"repository":{"pullRequest":{"title":"Add retry","body":"Fixes flakes","headRefName":"x"}}}}`,
			map[string]string{"pr_title": "Add retry", "pr_body": "Fixes flakes"},
			false,
		},
		{"not found", "daan/forge#99", `{"data":{"repository":{"pullRequest":null}}}`, nil, true},
		{"bad ref", "#12", ``, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var vars map[string]any
			c := NewClient(api.ClientOptions{
				Host: "github.com", AuthToken: "t",
				Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
					var b struct {
						Variables map[string]any `json:"variables"`
					}
					_ = json.NewDecoder(req.Body).Decode(&b)
					vars = b.Variables
					return respond(req, 200, tt.body), nil
				}),
			})
			got, err := c.PRContext(context.Background(), tt.ref)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if tt.wantErr {
				return
			}
			if got["pr_title"] != tt.want["pr_title"] || got["pr_body"] != tt.want["pr_body"] {
				t.Errorf("got %v", got)
			}
			if vars["owner"] != "daan" || vars["name"] != "forge" || vars["number"] != float64(12) {
				t.Errorf("variables = %v", vars)
			}
		})
	}
}

func TestIsAuthError(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{&api.HTTPError{StatusCode: 401}, true},
		{&api.HTTPError{StatusCode: 500}, false},
		{io.EOF, false},
		{errString("authentication token not found for host github.com"), true},
	}
	for _, tt := range tests {
		if got := IsAuthError(tt.err); got != tt.want {
			t.Errorf("IsAuthError(%v) = %v", tt.err, got)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
