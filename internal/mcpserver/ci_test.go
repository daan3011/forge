package mcpserver

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/slashcmd"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/testutil"
)

// fakeGH serves one red PR (o/r#3: an Actions job and a commit status),
// run 77 with a failed and a passed job, and a failed run on forge/fix.
type fakeGH struct {
	logs  map[int64]string
	calls []string
}

func (f *fakeGH) PRDetail(_ context.Context, repo string, n int) (github.PRDetail, error) {
	f.calls = append(f.calls, "pr")
	if repo != "daan3011/forge" || n != 3 {
		return github.PRDetail{}, errors.New("no such PR")
	}
	return github.PRDetail{
		HeadSHA: "abc123", HeadMessage: "Add retries",
		Checks: []github.Check{
			{Name: "test", Workflow: "CI", State: github.CheckFail, URL: "https://github.com/daan3011/forge/actions/runs/77/job/88"},
			{Name: "lint", Workflow: "CI", State: github.CheckPass, URL: "https://github.com/daan3011/forge/actions/runs/77/job/89"},
			{Name: "ext/ci", State: github.CheckFail, URL: "https://ci.example.com/1"},
		},
	}, nil
}

func (f *fakeGH) RunJobs(_ context.Context, _ string, runID int64) ([]github.Job, error) {
	f.calls = append(f.calls, "jobs")
	switch runID {
	case 77:
		return []github.Job{
			{ID: 88, Name: "test", State: github.CheckFail, URL: "u88"},
			{ID: 89, Name: "lint", State: github.CheckPass, URL: "u89"},
		}, nil
	case 99:
		return []github.Job{{ID: 100, Name: "build", State: github.CheckFail, URL: "u100"}}, nil
	}
	return nil, errors.New("no such run")
}

func (f *fakeGH) JobLogs(_ context.Context, _ string, jobID int64) ([]byte, error) {
	f.calls = append(f.calls, "logs")
	if s, ok := f.logs[jobID]; ok {
		return []byte(s), nil
	}
	return nil, github.ErrLogsUnavailable
}

func (f *fakeGH) ListRuns(_ context.Context, _ string, flt github.RunFilter) ([]github.Run, error) {
	f.calls = append(f.calls, "runs:"+flt.Branch+":"+flt.Status)
	return []github.Run{{ID: 99, Repo: "daan3011/forge", Workflow: "Build", Branch: flt.Branch, URL: "https://github.com/daan3011/forge/actions/runs/99"}}, nil
}

func withGitHub(g GitHub) opt { return func(d *Deps) { d.GitHub = g } }

func newCIEnv(t *testing.T, opts ...opt) (*env, *fakeGH) {
	gh := &fakeGH{logs: map[int64]string{
		88:  "2026-11-24T10:00:00.0000000Z ok 1\n2026-11-24T10:00:01.0000000Z --- FAIL: TestRetry (0.01s)\n2026-11-24T10:00:02.0000000Z FAIL\n",
		100: "2026-11-24T10:00:00.0000000Z error: build broke\n",
	}}
	return newEnv(t, append([]opt{withGitHub(gh)}, opts...)...), gh
}

func TestGetCIFailure(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		linkPR   bool
		source   string
		jobs     []string
		contains string
	}{
		{
			"run url",
			map[string]any{"run_url": "https://github.com/daan3011/forge/actions/runs/77"},
			false,
			"run https://github.com/daan3011/forge/actions/runs/77",
			[]string{"test"},
			"FAIL: TestRetry",
		},
		{
			"job url",
			map[string]any{"run_url": "https://github.com/daan3011/forge/actions/runs/77/job/89"},
			false,
			"run",
			[]string{"lint"},
			"",
		},
		{
			"pr url",
			map[string]any{"run_url": "https://github.com/daan3011/forge/pull/3/checks"},
			false,
			"pull request daan3011/forge#3",
			[]string{"test", "ext/ci"},
			"FAIL: TestRetry",
		},
		{"session pr", nil, true, "pull request daan3011/forge#3", []string{"test", "ext/ci"}, "FAIL: TestRetry"},
		{
			"session branch", nil, false, "run https://github.com/daan3011/forge/actions/runs/99 (latest failure on forge/fix)",
			[]string{"build"},
			"build broke",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := newCIEnv(t)
			if tt.linkPR {
				if err := e.st.Links().Add(context.Background(), store.SessionRef(e.x.ID), store.PRRef("daan3011/forge", 3), store.LinkMeta{}); err != nil {
					t.Fatal(err)
				}
			}
			var out CIFailureOut
			e.call("get_ci_failure", tt.args, &out)
			if !strings.HasPrefix(out.Source, tt.source) {
				t.Errorf("source %q", out.Source)
			}
			var jobs []string
			var lines string
			for _, j := range out.Jobs {
				jobs = append(jobs, j.Job)
				lines += strings.Join(j.Lines, "\n") + j.NoLog
			}
			if !slices.Equal(jobs, tt.jobs) {
				t.Errorf("jobs %v, want %v", jobs, tt.jobs)
			}
			if !strings.Contains(lines, tt.contains) {
				t.Errorf("lines lack %q:\n%s", tt.contains, lines)
			}
		})
	}
}

func TestGetCIFailureCachesLogs(t *testing.T) {
	e, gh := newCIEnv(t)
	args := map[string]any{"run_url": "https://github.com/daan3011/forge/actions/runs/77"}
	e.call("get_ci_failure", args, nil)
	e.call("get_ci_failure", args, nil)
	n := 0
	for _, c := range gh.calls {
		if c == "logs" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("logs downloaded %d times; the summary should be cached", n)
	}
}

func TestGetCIFailureErrors(t *testing.T) {
	tests := []struct {
		name string
		opts []opt
		args map[string]any
		want string
	}{
		{"no github", []opt{func(d *Deps) { d.GitHub, d.GitHubErr = nil, errors.New("gh not logged in") }}, nil, "gh not logged in"},
		{"bad url", nil, map[string]any{"run_url": "https://github.com/daan3011/forge/issues/1"}, "neither"},
		{"not a url", nil, map[string]any{"run_url": "77"}, "not a URL"},
		{"no failing checks", nil, map[string]any{"run_url": "https://github.com/daan3011/forge/actions/runs/77/job/5"}, "no failing checks"},
		{"no session", []opt{withSession("nobody")}, nil, "pass run_url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := newCIEnv(t, tt.opts...)
			if msg := e.callErr("get_ci_failure", tt.args); !strings.Contains(msg, tt.want) {
				t.Errorf("error %q lacks %q", msg, tt.want)
			}
		})
	}
}

// The P6.7 "done when": /forge-ticket passes the user's text as capture.
func TestCreateIssueCapture(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	var out CreateIssueOut
	e.call("create_issue", map[string]any{
		"capture": "Add retries to Plane client !high", "description": "Transient 5xx fail the refresh.",
	}, &out)
	if out.Issue.ID != "P-1" || !out.Issue.Proposed || out.Issue.FiledBy != "fix" {
		t.Errorf("out %+v", out)
	}
	p, err := e.st.Proposals().Get(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Add retries to Plane client" || p.Priority != "high" || p.Project != "forge" || p.SessionID != e.x.ID {
		t.Errorf("proposal %+v", p)
	}
	if ls := e.sessionLinks(store.KindProposal); len(ls) != 1 {
		t.Errorf("session links %+v", ls)
	}
}

func TestCreateIssueCaptureTokens(t *testing.T) {
	tests := []struct {
		name    string
		args    map[string]any
		project string
		title   string
		prio    string
		labels  []string
		note    string
		wantErr string
	}{
		{
			name: "labels and prefix priority", args: map[string]any{"capture": "Fix it +bug +Docs !urg", "labels": []string{"bug"}},
			project: "forge", title: "Fix it", prio: "urgent", labels: []string{"bug", "Docs"},
		},
		{
			name: "explicit fields win", args: map[string]any{"capture": "ignored words !low", "title": "Mine", "priority": "medium"},
			project: "forge", title: "Mine", prio: "medium",
		},
		{name: "forge project", args: map[string]any{"capture": "Tidy #Other"}, project: "other", title: "Tidy"},
		{name: "plane identifier", args: map[string]any{"capture": "Tidy #frg"}, project: "forge", title: "Tidy"},
		{name: "person ignored", args: map[string]any{"capture": "Ask @alex"}, project: "forge", title: "Ask", note: "Ignored @alex"},
		{name: "first line only", args: map[string]any{"capture": "One line !h\nmore text !low"}, project: "forge", title: "One line", prio: "high"},
		{name: "unknown project", args: map[string]any{"capture": "x #nope"}, wantErr: "unknown project #nope; forge projects: forge, other"},
		{name: "bad priority", args: map[string]any{"capture": "x !soon"}, wantErr: "unknown priority !soon"},
		{name: "only tokens", args: map[string]any{"capture": "!high"}, wantErr: "title is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, func(d *Deps) {
				d.Config.Projects = append(d.Config.Projects, config.Project{Name: "other", Repo: "daan3011/other"})
				d.Plane.(*testutil.FakePlane).ProjectList[0].Identifier = "FRG"
			})
			if tt.wantErr != "" {
				if msg := e.callErr("create_issue", tt.args); !strings.Contains(msg, tt.wantErr) {
					t.Errorf("error %q lacks %q", msg, tt.wantErr)
				}
				return
			}
			var out CreateIssueOut
			e.call("create_issue", tt.args, &out)
			p, err := e.st.Proposals().Get(context.Background(), 1)
			if err != nil {
				t.Fatal(err)
			}
			if p.Project != tt.project || p.Title != tt.title || p.Priority != tt.prio || !slices.Equal(p.Labels, tt.labels) {
				t.Errorf("proposal project=%q title=%q prio=%q labels=%v", p.Project, p.Title, p.Priority, p.Labels)
			}
			if tt.note != "" && !strings.Contains(out.Message, tt.note) {
				t.Errorf("message %q lacks %q", out.Message, tt.note)
			}
		})
	}
}

// Every MCP tool a built-in skill pre-approves exists on the server.
func TestSkillAllowedToolsExist(t *testing.T) {
	e := newEnv(t)
	res, err := e.cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tl := range res.Tools {
		have["mcp__"+Name+"__"+tl.Name] = true
	}
	for _, c := range slashcmd.Builtins() {
		for _, tool := range slashcmd.AllowedTools(c) {
			if strings.HasPrefix(tool, "mcp__") && !have[tool] {
				t.Errorf("/%s allows %s, which forge mcp doesn't serve", c.Name, tool)
			}
		}
	}
}
