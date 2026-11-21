package prs

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/cifix"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
)

// logRuns is a github.RunsClient that only serves job logs.
type logRuns struct {
	github.RunsClient // nil: any other call panics
	mu                sync.Mutex
	logs              map[int64]string
	calls             []int64
}

func (f *logRuns) JobLogs(_ context.Context, _ string, jobID int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, jobID)
	return []byte(f.logs[jobID]), nil
}

// failingDetail is #1's detail: a failing Actions job (ids from the
// query), one from an older cache (ids only in the URL), an external
// status, and a passing check.
func failingDetail() github.PRDetail {
	return github.PRDetail{
		ReviewDecision: github.ReviewApproved, Mergeable: github.Mergeable,
		HeadSHA: "abc1234", HeadMessage: "Add the tree example",
		Checks: []github.Check{
			{
				Name: "test", Workflow: "CI", State: github.CheckFail, RunID: 77, JobID: 88,
				URL: "https://github.com/acme/bubbletea/actions/runs/77/job/88",
			},
			{
				Name: "lint", Workflow: "CI", State: github.CheckFail,
				URL: "https://github.com/acme/bubbletea/actions/runs/77/job/89",
			},
			{Name: "ci/buildkite", State: github.CheckFail, URL: "https://buildkite.com/acme/1"},
			{Name: "vet", Workflow: "CI", State: github.CheckPass},
		},
	}
}

func fixRig(t *testing.T) (*actionRig, *logRuns) {
	t.Helper()
	r := newActionRig(t, func(g *fakeGH) {
		g.details = map[string]github.PRDetail{"acme/bubbletea#1": failingDetail()}
	})
	runs := &logRuns{logs: map[int64]string{
		88: "2026-11-21T09:00:00Z ok\n2026-11-21T09:00:01Z --- FAIL: TestTree\n",
		89: "2026-11-21T09:00:00Z ##[error]gofmt: tree.go\n",
	}}
	r.tb.runs = runs
	return r, runs
}

// startSourceOf returns a function taking what keys / feed return, so the
// call reads startSourceOf(t)(r.keys("F")).
func startSourceOf(t *testing.T) func(toasts, []tea.Msg) tabs.Source {
	t.Helper()
	return func(got toasts, other []tea.Msg) tabs.Source {
		t.Helper()
		return sourceIn(t, got, other)
	}
}

func sourceIn(t *testing.T, got toasts, other []tea.Msg) tabs.Source {
	t.Helper()
	if len(got.errs) > 0 {
		t.Fatalf("errors: %q", got.errs)
	}
	for _, m := range other {
		if s, ok := m.(tabs.StartSessionMsg); ok {
			return s.Source
		}
	}
	t.Fatalf("no StartSessionMsg: %+v %+v", got, other)
	return tabs.Source{}
}

func TestFixPRFailingChecks(t *testing.T) {
	r, runs := fixRig(t)
	src := startSourceOf(t)(r.keys("F"))
	if src.Kind != store.KindGitHubPR || src.ID != "acme/bubbletea#1" || src.Number != 1 ||
		src.Branch != "feat-1" || src.Base != "main" || src.Template != cifix.Template {
		t.Errorf("source = %+v", src)
	}
	if src.Fix == nil || !src.Fix.Reuse || !src.Fix.Immediate {
		t.Errorf("fix = %+v", src.Fix)
	}
	v := src.Vars
	if v[cifix.VarWorkflow] != "CI" || v[cifix.VarJob] != "test, lint, ci/buildkite" ||
		v[cifix.VarCommitMessage] != "Add the tree example" ||
		v[cifix.VarRun] != "https://github.com/acme/bubbletea/actions/runs/77" {
		t.Errorf("vars = %q", v)
	}
	log := v[cifix.VarFailingLog]
	for _, want := range []string{"--- FAIL: TestTree", "##[error]gofmt: tree.go", "── ci/buildkite ──", "not a GitHub Actions job; see https://buildkite.com/acme/1"} {
		if !strings.Contains(log, want) {
			t.Errorf("failing_log missing %q:\n%s", want, log)
		}
	}
	if !slices.Equal(runs.calls, []int64{88, 89}) {
		t.Errorf("log downloads = %v", runs.calls)
	}
	// Both Actions jobs' summaries are saved for next time.
	if _, err := r.tb.store.Get(context.Background(), "ci_errors:acme/bubbletea#77:89"); err != nil {
		t.Errorf("summary not saved: %v", err)
	}
	if r.tb.Busy() != "" {
		t.Errorf("still busy: %q", r.tb.Busy())
	}
}

func TestFixPREditKeyAndPalette(t *testing.T) {
	r, _ := fixRig(t)
	_, cmd := r.tb.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModAlt})
	src := startSourceOf(t)(r.feed(cmd))
	if src.Fix == nil || src.Fix.Immediate {
		t.Errorf("fix = %+v", src.Fix)
	}
	var fix tabs.Command
	for _, c := range r.tb.Commands() {
		if c.ID == "pr_fix" {
			fix = c
		}
	}
	if fix.Run == nil {
		t.Fatal("no pr_fix command")
	}
	if src := startSourceOf(t)(r.feed(fix.Run())); !src.Fix.Immediate {
		t.Errorf("palette fix = %+v", src.Fix)
	}
}

func TestFixPRGuards(t *testing.T) {
	r, _ := fixRig(t)
	r.selectPR(t, "acme/lipgloss#3") // detail loaded on rest: no checks
	drain(r.tb, r.tb.syncDetail())
	got, other := r.keys("F")
	if len(other) != 0 || !slices.Contains(got.info, "#3 has no failing checks") {
		t.Errorf("green PR: %+v %+v", got, other)
	}
}

func TestFixPRFetchesDetailWhenMissing(t *testing.T) {
	r, _ := fixRig(t)
	r.tb.det = newDetailState() // nothing loaded yet
	before := len(r.gh.detailCalls())
	src := startSourceOf(t)(r.keys("F"))
	if len(r.gh.detailCalls()) <= before || src.Vars[cifix.VarJob] == "" {
		t.Errorf("detail calls %v, vars %q", r.gh.detailCalls(), src.Vars)
	}
}

func TestFixTargetsCap(t *testing.T) {
	p := github.PR{Repo: "a/b", HeadRef: "x"}
	var d github.PRDetail
	for i := range 5 {
		d.Checks = append(d.Checks, github.Check{Name: string(rune('a' + i)), State: github.CheckFail})
	}
	if got := FixTargets(p, d); len(got) != cifix.MaxJobs {
		t.Errorf("targets = %d", len(got))
	}
}
