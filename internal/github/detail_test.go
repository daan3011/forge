package github

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParsePRDetail(t *testing.T) {
	f, err := os.Open("testdata/detail.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := ParsePRDetail(f)
	if err != nil {
		t.Fatal(err)
	}
	if d.Body != "Adds **retry**.\n\nFixes #3" || d.ReviewDecision != ReviewApproved || d.Mergeable != Mergeable ||
		d.MergeState != MergeStateBlocked || !d.UpdatedAt.Equal(ts("2026-11-20T10:00:00Z")) {
		t.Errorf("fields = %+v", d)
	}
	if want := []string{MergeMethodSquash, MergeMethodRebase}; !slices.Equal(d.MergeMethods, want) {
		t.Errorf("merge methods = %v, want %v", d.MergeMethods, want)
	}
	if len(d.Labels) != 2 || d.Labels[0] != (Label{"bug", "d73a4a"}) {
		t.Errorf("labels = %v", d.Labels)
	}

	// bob approved; carol approved but is re-requested (pending again);
	// the ghost commented; daan's draft is left out; the team is pending.
	wantRev := []Reviewer{
		{"bob", ReviewStateApproved}, {"carol", ReviewStatePending}, {"", ReviewStateCommented}, {"acme/core", ReviewStatePending},
	}
	if len(d.Reviewers) != len(wantRev) {
		t.Fatalf("reviewers = %v", d.Reviewers)
	}
	for i, w := range wantRev {
		if d.Reviewers[i] != w {
			t.Errorf("reviewer %d = %v, want %v", i, d.Reviewers[i], w)
		}
	}
	if d.Approvals != 1 {
		t.Errorf("approvals = %d, want 1", d.Approvals)
	}

	checks := []struct {
		name, workflow, state string
		dur                   time.Duration
	}{
		{"test", "CI", CheckFail, 5 * time.Minute},
		{"e2e", "", CheckRunning, 20 * time.Minute},
		{"ci/legacy", "", CheckPending, 0},
		{"lint", "CI", CheckPass, 2*time.Minute + 14*time.Second},
		{"deploy", "", CheckSkipped, 0},
	}
	if len(d.Checks) != len(checks) {
		t.Fatalf("checks = %+v", d.Checks)
	}
	now := ts("2026-11-20T09:30:00Z")
	for i, w := range checks {
		c := d.Checks[i]
		if c.Name != w.name || c.Workflow != w.workflow || c.State != w.state || c.Duration(now) != w.dur {
			t.Errorf("check %d = %+v (duration %s), want %+v", i, c, c.Duration(now), w)
		}
	}

	if r, j, ok := d.Checks[0].CheckIDs(); !ok || r != 77 || j != 88 {
		t.Errorf("test check ids = %d/%d %v, want 77/88", r, j, ok)
	}
	if _, _, ok := d.Checks[3].CheckIDs(); ok {
		t.Errorf("lint check has no Actions ids: %+v", d.Checks[3])
	}
	if d.HeadSHA != "abc1234def" || d.HeadMessage != "Fix the parser\n\nLonger body." {
		t.Errorf("head = %q %q", d.HeadSHA, d.HeadMessage)
	}

	wantAct := []struct{ kind, author, state, body string }{
		{EventReview, "carol", ReviewStateChangesRequested, "nit"},
		{EventComment, "", "", "ghost here"},
		{EventReview, "bob", ReviewStateApproved, ""},
		{EventComment, "bob", "", "first"},
	}
	if len(d.Activity) != len(wantAct) {
		t.Fatalf("activity = %+v", d.Activity)
	}
	for i, w := range wantAct {
		e := d.Activity[i]
		if e.Kind != w.kind || e.Author != w.author || e.State != w.state || e.Body != w.body {
			t.Errorf("event %d = %+v, want %+v", i, e, w)
		}
	}
}

func TestParsePRDetailEdges(t *testing.T) {
	tests := []struct {
		name, in string
		wantErr  bool
	}{
		{"bad json", "{", true},
		{"no repo", `{"repository":null}`, true},
		{"no pr", `{"repository":{"pullRequest":null}}`, true},
		{"no rollup", `{"repository":{"pullRequest":{"body":"","commits":{"nodes":[{"commit":{"statusCheckRollup":null}}]}}}}`, false},
		{"no commits", `{"repository":{"pullRequest":{"body":""}}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := ParsePRDetail(strings.NewReader(tt.in))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if !tt.wantErr && (d.Checks == nil || d.Labels == nil || d.Reviewers == nil || d.Activity == nil) {
				t.Errorf("want non-nil empty slices, got %+v", d)
			}
		})
	}
}

func TestCheckRunState(t *testing.T) {
	tests := []struct{ status, conclusion, want string }{
		{"QUEUED", "", CheckPending},
		{"WAITING", "", CheckPending},
		{"IN_PROGRESS", "", CheckRunning},
		{"COMPLETED", "SUCCESS", CheckPass},
		{"COMPLETED", "TIMED_OUT", CheckFail},
		{"COMPLETED", "ACTION_REQUIRED", CheckFail},
		{"COMPLETED", "CANCELLED", CheckSkipped},
		{"COMPLETED", "NEUTRAL", CheckSkipped},
	}
	for _, tt := range tests {
		if got := checkRunState(tt.status, tt.conclusion); got != tt.want {
			t.Errorf("checkRunState(%s, %s) = %s, want %s", tt.status, tt.conclusion, got, tt.want)
		}
	}
}

func TestClientPRDetail(t *testing.T) {
	fixture, err := os.ReadFile("testdata/detail.json")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, repo string
		number     int
		status     int
		body       string
		wantErr    bool
		wantCall   bool
	}{
		{"ok", "daan/forge", 12, 200, `{"data":` + string(fixture) + `}`, false, true},
		{"graphql error", "daan/forge", 12, 200, `{"data":null,"errors":[{"message":"nope"}]}`, true, true},
		{"not found", "daan/forge", 12, 200, `{"data":{"repository":{"pullRequest":null}}}`, true, true},
		{"bad repo", "forge", 12, 200, ``, true, false},
		{"bad number", "daan/forge", 0, 200, ``, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var vars map[string]any
			called := false
			c := NewClient(api.ClientOptions{
				Host: "github.com", AuthToken: "t",
				Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
					called = true
					var b struct {
						Variables map[string]any `json:"variables"`
					}
					_ = json.NewDecoder(req.Body).Decode(&b)
					vars = b.Variables
					return respond(req, tt.status, tt.body), nil
				}),
			})
			d, err := c.PRDetail(context.Background(), tt.repo, tt.number)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if called != tt.wantCall {
				t.Fatalf("called = %v", called)
			}
			if err != nil && tt.wantCall && !strings.Contains(err.Error(), "daan/forge#12") {
				t.Errorf("err %q doesn't name the PR", err)
			}
			if tt.wantErr {
				return
			}
			if len(d.Checks) != 5 {
				t.Errorf("checks = %d", len(d.Checks))
			}
			if vars["owner"] != "daan" || vars["name"] != "forge" || vars["number"] != float64(12) {
				t.Errorf("variables = %v", vars)
			}
		})
	}
}

func TestParseCheckIDs(t *testing.T) {
	tests := []struct {
		url      string
		run, job int64
		ok       bool
	}{
		{"https://github.com/o/r/actions/runs/123/job/456", 123, 456, true},
		{"https://github.com/o/r/actions/runs/123/job/456?pr=7", 123, 456, true},
		{"https://github.com/o/r/actions/runs/123", 0, 0, false},
		{"https://ci.example.com/build/9", 0, 0, false},
		{"", 0, 0, false},
		{"https://github.com/o/r/actions/runs/0/job/4", 0, 0, false},
	}
	for _, tt := range tests {
		r, j, ok := ParseCheckIDs(tt.url)
		if r != tt.run || j != tt.job || ok != tt.ok {
			t.Errorf("ParseCheckIDs(%q) = %d, %d, %v; want %d, %d, %v", tt.url, r, j, ok, tt.run, tt.job, tt.ok)
		}
	}
	// Stored ids win over the URL.
	c := Check{URL: "https://github.com/o/r/actions/runs/1/job/2", RunID: 5, JobID: 6}
	if r, j, ok := c.CheckIDs(); !ok || r != 5 || j != 6 {
		t.Errorf("CheckIDs = %d %d %v", r, j, ok)
	}
}
