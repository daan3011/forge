package pipelines

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/cifix"
	"github.com/daan3011/forge/internal/cilog"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

type fakePulls struct {
	mu    sync.Mutex
	pulls map[string][]github.Pull // branch -> PRs
	err   error
	calls []string
}

func (f *fakePulls) PullsForBranch(_ context.Context, repo, branch string) ([]github.Pull, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, repo+" "+branch)
	return f.pulls[branch], f.err
}

// fixRuns is failedRuns with run 10 on a PR branch and a commit message.
func fixRuns(t *testing.T) *fakeRuns {
	gh := failedRuns(t)
	rs := gh.runs["acme/app"]
	rs[1].Branch, rs[1].CommitMessage = "feat/fast", "Speed up the parser\n\nBody."
	return gh
}

func prPulls() *fakePulls {
	return &fakePulls{pulls: map[string][]github.Pull{"feat/fast": {
		{Repo: "acme/app", Number: 9, State: github.StateMerged},
		{Repo: "acme/app", Number: 12, Title: "Speed up", State: github.StateOpen, URL: "https://github.com/acme/app/pull/12", HeadRef: "feat/fast"},
	}}}
}

// startSource presses keys and returns the StartSessionMsg's source.
func startSource(t *testing.T, h *harness, keys ...tea.KeyPressMsg) tabs.Source {
	t.Helper()
	var other []tea.Msg
	for _, k := range keys {
		_, cmd := h.Update(k)
		errs, o := h.drain(cmd)
		if len(errs) > 0 {
			t.Fatalf("errors: %q", errs)
		}
		other = append(other, o...)
	}
	for _, m := range other {
		if s, ok := m.(tabs.StartSessionMsg); ok {
			return s.Source
		}
	}
	t.Fatalf("no StartSessionMsg; got %+v", other)
	return tabs.Source{}
}

func TestFixFailedRunWithPR(t *testing.T) {
	gh := fixRuns(t)
	kv := newKV()
	h := atFailedRun(t, gh, kv)
	h.pulls = prPulls()

	src := startSource(t, h, press("F"))
	if src.Kind != store.KindGitHubPR || src.ID != "acme/app#12" || src.Number != 12 || src.Branch != "feat/fast" ||
		src.Template != cifix.Template || src.Title != "Speed up" {
		t.Errorf("source = %+v", src)
	}
	if src.Fix == nil || !src.Fix.Reuse || !src.Fix.Immediate || src.Fix.Confirm != "" {
		t.Errorf("fix = %+v", src.Fix)
	}
	v := src.Vars
	if v[cifix.VarWorkflow] != "release" || v[cifix.VarJob] != "test, lint" ||
		v[cifix.VarCommitMessage] != "Speed up the parser\n\nBody." ||
		v[cifix.VarRun] != "https://github.com/acme/app/actions/runs/10" {
		t.Errorf("vars = %q", v)
	}
	if !strings.Contains(v[cifix.VarFailingLog], "── release › lint ──") || !strings.Contains(v[cifix.VarFailingLog], "panic: lint exploded") {
		t.Errorf("failing_log = %s", v[cifix.VarFailingLog])
	}
	// Both failed jobs' summaries were saved; the passing job wasn't read.
	for _, job := range []int64{101, 102} {
		if _, err := kv.Get(context.Background(), cilog.SummaryKey("acme/app", 10, job)); err != nil {
			t.Errorf("summary of job %d not saved: %v", job, err)
		}
	}
	for _, id := range gh.logCalls() {
		if id == 103 {
			t.Error("downloaded the passing job's log")
		}
	}
	if h.Busy() != "" {
		t.Errorf("still busy: %q", h.Busy())
	}
}

func TestFixUsesSavedSummary(t *testing.T) {
	gh := fixRuns(t)
	kv := newKV()
	h := atFailedRun(t, gh, kv)
	h.pulls = prPulls()
	saved := cifix.NewSummary(cifix.RunOf(gh.runs["acme/app"][1]), 101, "test", "", cilog.View{Lines: []cilog.Line{{Text: "saved context"}}}, now)
	if err := cifix.Save(context.Background(), kv, "app", saved); err != nil {
		t.Fatal(err)
	}
	src := startSource(t, h, press("F"))
	if !strings.Contains(src.Vars[cifix.VarFailingLog], "saved context") {
		t.Errorf("failing_log = %s", src.Vars[cifix.VarFailingLog])
	}
	for _, id := range gh.logCalls() {
		if id == 101 {
			t.Error("downloaded a log whose summary was saved")
		}
	}
}

func TestFixEditKey(t *testing.T) {
	h := atFailedRun(t, fixRuns(t), nil)
	h.pulls = prPulls()
	src := startSource(t, h, tea.KeyPressMsg{Code: 'f', Mod: tea.ModAlt})
	if src.Fix == nil || src.Fix.Immediate || !src.Fix.Reuse {
		t.Errorf("fix = %+v", src.Fix)
	}
}

func TestFixFromLogViewerTakesShownJob(t *testing.T) {
	h := atFailedRun(t, fixRuns(t), newKV())
	h.pulls = prPulls()
	h.send("l", "]") // the second failed job: lint
	if h.log == nil {
		t.Fatal("viewer not open")
	}
	src := startSource(t, h, press("F"))
	if src.Vars[cifix.VarJob] != "lint" || strings.Contains(src.Vars[cifix.VarFailingLog], "── release › test ──") {
		t.Errorf("vars = %q", src.Vars)
	}
}

func TestFixRunWithoutPR(t *testing.T) {
	h := atFailedRun(t, fixRuns(t), nil)
	h.pulls = &fakePulls{pulls: map[string][]github.Pull{"feat/fast": {{Number: 9, State: github.StateClosed}}}}
	src := startSource(t, h, press("F"))
	if src.Kind != store.KindGitHubRun || src.ID != "acme/app#10" || src.Branch != "fix/release-10" ||
		src.Base != "feat/fast" || src.URL != "https://github.com/acme/app/actions/runs/10" {
		t.Errorf("source = %+v", src)
	}
	f := src.Fix
	if f == nil || f.Reuse || !f.Immediate || f.StartCommit != "abcdef1234" ||
		f.Confirm != "No open PR for feat/fast. Start a fix session on a new branch fix/release-10 from abcdef1?" {
		t.Errorf("fix = %+v", f)
	}
}

func TestFixPRLookupFailsCountsAsNone(t *testing.T) {
	h := atFailedRun(t, fixRuns(t), nil)
	h.pulls = &fakePulls{err: errors.New("HTTP 502")}
	if src := startSource(t, h, press("F")); src.Kind != store.KindGitHubRun {
		t.Errorf("kind = %s", src.Kind)
	}
}

func TestFixGuards(t *testing.T) {
	gh := fixRuns(t)
	h := newHarness(gh, nil, "app")
	if errs := h.init(); len(errs) > 0 {
		t.Fatal(errs)
	}
	// Row 0 is running.
	_, cmd := h.Update(press("F"))
	_, other := h.drain(cmd)
	if len(other) != 1 || other[0] != (ui.InfoMsg{Text: "ci #3 didn't fail; nothing to fix"}) {
		t.Errorf("running run: %+v", other)
	}

	// A failed run whose jobs all passed (a stale list): an error.
	gh.jobs[10] = []github.Job{{ID: 103, Name: "build", State: github.CheckPass}}
	h.send("j")
	_, cmd = h.Update(press("F"))
	errs, _ := h.drain(cmd)
	if len(errs) != 1 || !strings.Contains(errs[0], "no failed jobs") {
		t.Errorf("errs = %q", errs)
	}
}

func TestFixBusyGuard(t *testing.T) {
	h := atFailedRun(t, fixRuns(t), nil)
	_, first := h.Update(press("F"))
	if h.Busy() == "" {
		t.Error("not busy while gathering")
	}
	_, cmd := h.Update(press("F"))
	_, other := h.drain(cmd)
	if len(other) != 1 || !strings.Contains(other[0].(ui.InfoMsg).Text, "already gathering") {
		t.Errorf("second F: %+v", other)
	}
	h.drain(first)
	if h.Busy() != "" {
		t.Error("still busy")
	}
}

func TestFixPaletteCommands(t *testing.T) {
	h := atFailedRun(t, fixRuns(t), nil)
	h.pulls = prPulls()
	ids := map[string]tabs.Command{}
	for _, c := range h.Commands() {
		ids[c.ID] = c
	}
	c, ok := ids["pipeline_fix"]
	if !ok || c.Title != "CI: fix this" || !strings.Contains(strings.Join(c.Key.Keys(), ","), "F") {
		t.Fatalf("pipeline_fix = %+v", c)
	}
	if _, ok := ids["pipeline_fix_edit"]; !ok {
		t.Error("no pipeline_fix_edit")
	}
	_, other := h.drain(c.Run())
	if len(other) != 1 {
		t.Fatalf("palette run: %+v", other)
	}
	if _, ok := other[0].(tabs.StartSessionMsg); !ok {
		t.Errorf("palette run: %+v", other)
	}
}
