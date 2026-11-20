package github

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
)

const pullsJSON = `[
  {"number": 3, "title": "Draft work", "state": "open", "draft": true, "merged_at": null,
   "html_url": "https://github.com/daan/forge/pull/3", "updated_at": "2026-11-20T10:00:00Z", "head": {"ref": "agent/x"}},
  {"number": 2, "title": "Shipped", "state": "closed", "draft": false, "merged_at": "2026-11-19T10:00:00Z",
   "html_url": "https://github.com/daan/forge/pull/2", "updated_at": "2026-11-19T10:00:00Z", "head": {"ref": "agent/x"}},
  {"number": 1, "title": "Abandoned", "state": "closed", "draft": false, "merged_at": null,
   "html_url": "https://github.com/daan/forge/pull/1", "updated_at": "2026-11-18T10:00:00Z", "head": {"ref": "agent/x"}},
  {"number": 4, "title": "Ready", "state": "open", "draft": false, "merged_at": null,
   "html_url": "https://github.com/daan/forge/pull/4", "updated_at": "2026-11-17T10:00:00Z", "head": {"ref": "agent/x"}}
]`

func TestParsePulls(t *testing.T) {
	got, err := ParsePulls(strings.NewReader(pullsJSON), "daan/forge")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		n     int
		state string
	}{{3, StateDraft}, {2, StateMerged}, {1, StateClosed}, {4, StateOpen}}
	if len(got) != len(want) {
		t.Fatalf("got %d pulls", len(got))
	}
	for i, w := range want {
		if got[i].Number != w.n || got[i].State != w.state || got[i].Repo != "daan/forge" || got[i].HeadRef != "agent/x" {
			t.Errorf("pull %d = %+v, want #%d %s", i, got[i], w.n, w.state)
		}
	}
	if _, err := ParsePulls(strings.NewReader("{"), "x/y"); err == nil {
		t.Error("want error for bad JSON")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func respond(req *http.Request, status int, body string) *http.Response {
	h := http.Header{"Content-Type": {"application/json"}, "X-Ratelimit-Limit": {"5000"}, "X-Ratelimit-Remaining": {"4999"}}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: req}
}

func TestClientPullsForBranch(t *testing.T) {
	tests := []struct {
		name    string
		repo    string
		branch  string
		status  int
		body    string
		wantN   int
		wantErr bool
	}{
		{"ok", "daan/forge", "agent/x", 200, pullsJSON, 4, false},
		{"none", "daan/forge", "agent/y", 200, `[]`, 0, false},
		{"api error", "daan/forge", "agent/x", 404, `{"message":"Not Found"}`, 0, true},
		{"bad repo", "forge", "agent/x", 200, `[]`, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotURL string
			seen := 0
			r := NewClient(api.ClientOptions{
				Host: "github.com", AuthToken: "t",
				Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
					gotURL = req.URL.String()
					return respond(req, tt.status, tt.body), nil
				}),
			})
			r.OnResponse = func(*http.Response) { seen++ }
			got, err := r.PullsForBranch(context.Background(), tt.repo, tt.branch)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if len(got) != tt.wantN {
				t.Errorf("got %d pulls", len(got))
			}
			if tt.repo == "forge" {
				return
			}
			if seen != 1 {
				t.Errorf("OnResponse called %d times", seen)
			}
			for _, part := range []string{"/repos/daan/forge/pulls?", "head=daan%3A" + strings.ReplaceAll(tt.branch, "/", "%2F"), "state=all"} {
				if !strings.Contains(gotURL, part) {
					t.Errorf("url %s lacks %s", gotURL, part)
				}
			}
		})
	}
}
