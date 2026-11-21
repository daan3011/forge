package github

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

func TestParseRuns(t *testing.T) {
	f, err := os.Open("testdata/runs.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	runs, err := ParseRuns(f, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 4 {
		t.Fatalf("got %d runs, want 4", len(runs))
	}
	tests := []struct {
		i                                     int
		state, title, actor, workflow, branch string
		active                                bool
	}{
		{0, CheckRunning, "Add pipelines tab", "daan", "ci", "feat/pipes", true},
		{1, CheckFail, "Bump version", "bot", "release", "main", false},
		{2, CheckPending, "Fix lint", "alice", "ci", "main", true},
		{3, CheckSkipped, "Old", "alice", "ci", "main", false},
	}
	for _, tt := range tests {
		r := runs[tt.i]
		if r.State != tt.state || r.Title != tt.title || r.Actor != tt.actor || r.Workflow != tt.workflow || r.Branch != tt.branch || r.Active() != tt.active {
			t.Errorf("run %d = %+v, want state %s title %q actor %s workflow %s branch %s active %v",
				tt.i, r, tt.state, tt.title, tt.actor, tt.workflow, tt.branch, tt.active)
		}
		if r.Repo != "acme/app" {
			t.Errorf("run %d repo = %q", tt.i, r.Repo)
		}
	}
	r := runs[1]
	if r.ID != 1000 || r.RunNumber != 7 || r.Attempt != 2 || r.Event != "workflow_dispatch" || r.Key() != "acme/app#1000" {
		t.Errorf("run 1 fields = %+v", r)
	}
}

func TestRunDuration(t *testing.T) {
	now := ts("2026-11-21T10:02:05Z")
	tests := []struct {
		name string
		run  Run
		want time.Duration
	}{
		{"running counts to now", Run{State: CheckRunning, StartedAt: ts("2026-11-21T10:00:05Z")}, 2 * time.Minute},
		{"completed counts to update", Run{State: CheckPass, StartedAt: ts("2026-11-21T09:00:00Z"), UpdatedAt: ts("2026-11-21T09:04:30Z")}, 270 * time.Second},
		{"queued is zero", Run{State: CheckPending, CreatedAt: ts("2026-11-21T10:00:00Z")}, 0},
		{"no start falls back to created", Run{State: CheckRunning, CreatedAt: ts("2026-11-21T10:02:00Z")}, 5 * time.Second},
		{"never started", Run{State: CheckFail}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.run.Duration(now); got != tt.want {
				t.Errorf("Duration = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseJobs(t *testing.T) {
	f, err := os.Open("testdata/jobs.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	jobs, err := ParseJobs(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs", len(jobs))
	}
	now := ts("2026-11-21T10:01:10Z")
	test := jobs[0]
	if test.State != CheckRunning || test.Duration(now) != time.Minute || len(test.Steps) != 4 {
		t.Errorf("test job = %+v (duration %v)", test, test.Duration(now))
	}
	wantSteps := []struct {
		state string
		dur   time.Duration
	}{
		{CheckPass, 2 * time.Second},
		{CheckRunning, 58 * time.Second},
		{CheckPending, 0},
		{CheckSkipped, 0},
	}
	for i, w := range wantSteps {
		s := test.Steps[i]
		if s.State != w.state || s.Duration(now) != w.dur || s.Number != i+1 {
			t.Errorf("step %d = %+v (duration %v), want %s %v", i, s, s.Duration(now), w.state, w.dur)
		}
	}
	lint := jobs[1]
	if lint.State != CheckFail || lint.Duration(now) != 30*time.Second || lint.URL == "" {
		t.Errorf("lint job = %+v", lint)
	}
}

func TestParseRunsBadJSON(t *testing.T) {
	if _, err := ParseRuns(strings.NewReader("{"), "a/b"); err == nil {
		t.Error("want error")
	}
	if _, err := ParseJobs(strings.NewReader("[")); err == nil {
		t.Error("want error")
	}
}

func restServer(seen *[]string, status int, body string) *Client {
	return NewClient(api.ClientOptions{
		Host: "github.com", AuthToken: "t",
		Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			*seen = append(*seen, req.URL.Path+"?"+req.URL.RawQuery)
			return respond(req, status, body), nil
		}),
	})
}

func TestClientListRuns(t *testing.T) {
	body, _ := os.ReadFile("testdata/runs.json")
	tests := []struct {
		name   string
		filter RunFilter
		want   []string // query parts that must be present
		absent []string
	}{
		{"no filter", RunFilter{}, []string{"per_page=30"}, []string{"branch=", "status=", "actor="}},
		{
			"all filters",
			RunFilter{Branch: "feat/x", Status: "failure", Actor: "daan"},
			[]string{"branch=feat%2Fx", "status=failure", "actor=daan", "per_page=30"},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seen []string
			c := restServer(&seen, 200, string(body))
			runs, err := c.ListRuns(context.Background(), "acme/app", tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			if len(runs) != 4 || len(seen) != 1 {
				t.Fatalf("runs %d, calls %v", len(runs), seen)
			}
			if !strings.HasPrefix(seen[0], "/repos/acme/app/actions/runs?") {
				t.Errorf("path = %s", seen[0])
			}
			for _, w := range tt.want {
				if !strings.Contains(seen[0], w) {
					t.Errorf("query %s lacks %s", seen[0], w)
				}
			}
			for _, a := range tt.absent {
				if strings.Contains(seen[0], a) {
					t.Errorf("query %s has %s", seen[0], a)
				}
			}
		})
	}
}

func TestClientListRunsErrors(t *testing.T) {
	var seen []string
	if _, err := restServer(&seen, 200, "{}").ListRuns(context.Background(), "norepo", RunFilter{}); err == nil {
		t.Error("want error for a repo without owner")
	}
	_, err := restServer(&seen, 404, `{"message":"Not Found"}`).ListRuns(context.Background(), "a/b", RunFilter{})
	if err == nil || !strings.Contains(err.Error(), "runs of a/b") {
		t.Errorf("err = %v", err)
	}
}

func TestClientRunJobs(t *testing.T) {
	body, _ := os.ReadFile("testdata/jobs.json")
	var seen []string
	jobs, err := restServer(&seen, 200, string(body)).RunJobs(context.Background(), "acme/app", 1001)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || seen[0] != "/repos/acme/app/actions/runs/1001/jobs?per_page=100" {
		t.Errorf("jobs %d, call %v", len(jobs), seen)
	}
}

func TestClientViewerMemoised(t *testing.T) {
	var n atomic.Int32
	c := NewClient(api.ClientOptions{
		Host: "github.com", AuthToken: "t",
		Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			n.Add(1)
			if req.URL.Path != "/user" {
				t.Errorf("path = %s", req.URL.Path)
			}
			return respond(req, 200, `{"login":"daan"}`), nil
		}),
	})
	for range 3 {
		login, err := c.Viewer(context.Background())
		if err != nil || login != "daan" {
			t.Fatalf("Viewer = %q, %v", login, err)
		}
	}
	if n.Load() != 1 {
		t.Errorf("calls = %d, want 1", n.Load())
	}
	var seen []string
	if _, err := restServer(&seen, 200, `{}`).Viewer(context.Background()); err == nil {
		t.Error("want error without a login")
	}
}
