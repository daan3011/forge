package pipelines

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

var now = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

// fakeRuns serves runs per repo, filtered like GitHub would, and jobs per
// run id. It records every call.
type fakeRuns struct {
	mu      sync.Mutex
	runs    map[string][]github.Run // repo -> runs
	jobs    map[int64][]github.Job
	err     error
	jobsErr error
	viewer  string
	lists   []string // "repo branch=… status=… actor=…"
	jobCall []int64
	viewers int
	logs    map[int64][]byte // job id -> raw log
	logErr  error
	logCall []int64
	actions []string // "rerun-failed 10", …
	actErr  error
}

func (f *fakeRuns) JobLogs(_ context.Context, _ string, id int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logCall = append(f.logCall, id)
	if f.logErr != nil {
		return nil, f.logErr
	}
	return f.logs[id], nil
}

func (f *fakeRuns) act(name string, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, fmt.Sprintf("%s %d", name, id))
	return f.actErr
}

func (f *fakeRuns) RerunFailedJobs(_ context.Context, _ string, id int64) error {
	return f.act("rerun-failed", id)
}

func (f *fakeRuns) RerunRun(_ context.Context, _ string, id int64) error { return f.act("rerun", id) }

func (f *fakeRuns) CancelRun(_ context.Context, _ string, id int64) error { return f.act("cancel", id) }

func (f *fakeRuns) logCalls() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.logCall)
}

func (f *fakeRuns) actionCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.actions)
}

func (f *fakeRuns) ListRuns(_ context.Context, repo string, fl github.RunFilter) ([]github.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists = append(f.lists, fmt.Sprintf("%s branch=%s status=%s actor=%s", repo, fl.Branch, fl.Status, fl.Actor))
	if f.err != nil {
		return nil, f.err
	}
	var out []github.Run
	for _, r := range f.runs[repo] {
		if fl.Branch != "" && r.Branch != fl.Branch || fl.Actor != "" && r.Actor != fl.Actor {
			continue
		}
		if fl.Status == "failure" && r.State != github.CheckFail {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeRuns) RunJobs(_ context.Context, _ string, id int64) ([]github.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobCall = append(f.jobCall, id)
	if f.jobsErr != nil {
		return nil, f.jobsErr
	}
	return f.jobs[id], nil
}

func (f *fakeRuns) Viewer(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.viewers++
	if f.viewer == "" {
		return "", errors.New("no login")
	}
	return f.viewer, nil
}

func (f *fakeRuns) listCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.lists)
}

func (f *fakeRuns) jobCalls() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.jobCall)
}

func (f *fakeRuns) set(repo string, runs ...github.Run) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs[repo] = runs
}

func run(repo string, id int64, workflow, branch string, mod func(*github.Run)) github.Run {
	r := github.Run{
		Repo: repo, ID: id, RunNumber: int(id % 1000), Workflow: workflow, Branch: branch,
		HeadSHA: "abcdef1234", Title: "commit " + fmt.Sprint(id), Event: "push", Actor: "daan",
		State: github.CheckPass, Status: "completed", Conclusion: "success",
		URL:       fmt.Sprintf("https://github.com/%s/actions/runs/%d", repo, id),
		CreatedAt: now.Add(-time.Duration(id) * time.Minute), StartedAt: now.Add(-time.Duration(id) * time.Minute),
	}
	r.UpdatedAt = r.StartedAt.Add(134 * time.Second)
	if mod != nil {
		mod(&r)
	}
	return r
}

func running(r *github.Run) {
	r.State, r.Status, r.Conclusion = github.CheckRunning, "in_progress", ""
	r.UpdatedAt = r.StartedAt.Add(time.Second)
}

func sampleRuns() *fakeRuns {
	return &fakeRuns{
		viewer: "daan",
		runs: map[string][]github.Run{
			"acme/app": {
				run("acme/app", 3, "ci", "feat/pipes", running),
				run("acme/app", 10, "release", "main", func(r *github.Run) {
					r.State, r.Conclusion, r.Actor = github.CheckFail, "failure", "bob"
				}),
				run("acme/app", 20, "ci", "main", nil),
			},
			"acme/lib": {run("acme/lib", 5, "test", "main", nil)},
		},
		jobs: map[int64][]github.Job{
			3: {{ID: 1, Name: "build", State: github.CheckRunning, StartedAt: now.Add(-2 * time.Minute), Steps: []github.Step{
				{Number: 1, Name: "Checkout", State: github.CheckPass, StartedAt: now.Add(-2 * time.Minute), CompletedAt: now.Add(-110 * time.Second)},
				{Number: 2, Name: "Run tests", State: github.CheckRunning, StartedAt: now.Add(-110 * time.Second)},
				{Number: 3, Name: "Upload", State: github.CheckPending},
			}}},
		},
	}
}

type memKV struct {
	mu sync.Mutex
	m  map[string]store.CacheEntry
}

func newKV() *memKV { return &memKV{m: map[string]store.CacheEntry{}} }

func (s *memKV) Get(_ context.Context, k string) (store.CacheEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[k]
	if !ok {
		return store.CacheEntry{}, store.ErrNotFound
	}
	return e, nil
}

func (s *memKV) Put(_ context.Context, k, kind, project string, v []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = store.CacheEntry{Key: k, Kind: kind, Project: project, Value: v, FetchedAt: now, TTL: ttl}
	return nil
}

func testConfig() *config.Config {
	cfg := config.Default()
	cfg.Projects = []config.Project{
		{Name: "app", Repo: "acme/app"},
		{Name: "lib", Repo: "acme/lib"},
		{Name: "notes"},
	}
	return cfg
}

// harness is a tab with a recorded fast refresh timer.
type harness struct {
	*Tab
	fast []tea.Msg // armed fast ticks, not yet fired
}

func newHarness(gh github.RunsClient, kv tabs.KVStore, project string) *harness {
	cfg := testConfig()
	d := tabkit.Deps{Theme: ui.NewTheme(true, nil), Keys: ui.DefaultKeyMap(), Store: kv, Config: cfg}
	if gh != nil {
		d.Runs = gh
	}
	h := &harness{Tab: newTab(d)}
	h.now = func() time.Time { return now }
	h.tick = func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
		if d == detailDebounce {
			return func() tea.Msg { return fn(now) }
		}
		h.fast = append(h.fast, fn(now))
		return nil
	}
	for i := range cfg.Projects {
		if cfg.Projects[i].Name == project {
			h.SetProject(&cfg.Projects[i])
		}
	}
	h.SetSize(150, 30)
	return h
}

// run executes cmd and returns its messages, flattening batches.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, runCmd(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// drain feeds cmd's messages back into the tab until none are left and
// returns the error toasts and any other messages meant for the app.
func (h *harness) drain(cmd tea.Cmd) (errs []string, other []tea.Msg) {
	queue := runCmd(cmd)
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		if t := reflect.TypeOf(msg); t != nil && strings.HasSuffix(t.PkgPath(), "bubbles/v2/cursor") {
			continue // a text input's blink would tick forever
		}
		switch m := msg.(type) {
		case ui.ErrMsg:
			errs = append(errs, m.Err.Error())
			continue
		case tabs.ShowRunsMsg, tabs.StartSessionMsg, ui.InfoMsg:
			other = append(other, m)
			continue
		}
		_, next := h.Update(msg)
		queue = append(queue, runCmd(next)...)
	}
	return errs, other
}

func (h *harness) init() []string {
	errs, _ := h.drain(h.Init())
	return errs
}

func press(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "ctrl+r":
		return tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func (h *harness) send(keys ...string) []string {
	var errs []string
	for _, k := range keys {
		_, cmd := h.Update(press(k))
		e, _ := h.drain(cmd)
		errs = append(errs, e...)
	}
	return errs
}

// fireFast fires every armed fast tick.
func (h *harness) fireFast() []string {
	var errs []string
	ticks := h.fast
	h.fast = nil
	for _, m := range ticks {
		_, cmd := h.Update(m)
		e, _ := h.drain(cmd)
		errs = append(errs, e...)
	}
	return errs
}

func (h *harness) view() string { return ansi.Strip(h.View()) }

func mustContain(t *testing.T, v string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(v, w) {
			t.Fatalf("view lacks %q:\n%s", w, v)
		}
	}
}

func mustLack(t *testing.T, v string, nots ...string) {
	t.Helper()
	for _, n := range nots {
		if strings.Contains(v, n) {
			t.Fatalf("view has %q:\n%s", n, v)
		}
	}
}

// --- tests -------------------------------------------------------------------------

func TestRowsForProject(t *testing.T) {
	gh := sampleRuns()
	h := newHarness(gh, nil, "app")
	if errs := h.init(); len(errs) > 0 {
		t.Fatal(errs)
	}
	if got := gh.listCalls(); !slices.Equal(got, []string{"acme/app branch= status= actor="}) {
		t.Fatalf("calls = %v", got)
	}
	rows := h.table.Rows()
	if len(rows) != 3 {
		t.Fatalf("rows = %v", rows)
	}
	// Newest first: run 3 (3m ago), 10, 20.
	if rows[0][len(rows[0])-1] != "acme/app#3" || rows[2][len(rows[2])-1] != "acme/app#20" {
		t.Errorf("order = %v", rows)
	}
	v := h.view()
	mustContain(t, v, "⌕ acme/app", "Workflow", "Branch", "Commit", "Event", "Actor", "Duration", "Started",
		"feat/pipes", "commit 3", "push", "running 3m00s", "release", "bob", "2m14s", "3m", "◐", "✗", "✓")
	mustLack(t, v, "Repo ")
}

func TestAllProjectsMergesRepos(t *testing.T) {
	gh := sampleRuns()
	h := newHarness(gh, nil, "")
	h.init()
	calls := gh.listCalls()
	slices.Sort(calls)
	if !slices.Equal(calls, []string{"acme/app branch= status= actor=", "acme/lib branch= status= actor="}) {
		t.Fatalf("calls = %v", calls)
	}
	var keys []string
	for _, r := range h.table.Rows() {
		keys = append(keys, r[len(r)-1])
	}
	want := []string{"acme/app#3", "acme/lib#5", "acme/app#10", "acme/app#20"}
	if !slices.Equal(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
	mustContain(t, h.view(), "Repo", "lib", "2 repos")
}

func TestDetailShowsJobsAndSteps(t *testing.T) {
	gh := sampleRuns()
	h := newHarness(gh, nil, "app")
	h.init()
	v := h.view()
	mustContain(t, v, "acme/app · ci #3", "running", "feat/pipes @ abcdef1",
		"build", "running 2m00s", "1. Checkout", "10s", "2. Run tests", "running 1m50s", "3. Upload", "queued")
	if got := gh.jobCalls(); !slices.Equal(got, []int64{3}) {
		t.Errorf("job calls = %v", got)
	}
	// Moving on fetches the next run; coming back is served from memory.
	h.send("j")
	h.send("k")
	if got := gh.jobCalls(); !slices.Equal(got, []int64{3, 10}) {
		t.Errorf("job calls = %v", got)
	}
}

func TestDetailDurationsTickWithoutFetch(t *testing.T) {
	gh := sampleRuns()
	h := newHarness(gh, nil, "app")
	h.init()
	h.now = func() time.Time { return now.Add(30 * time.Second) }
	mustContain(t, h.view(), "running 3m30s", "running 2m20s")
	if len(gh.jobCalls()) != 1 || len(gh.listCalls()) != 1 {
		t.Error("re-rendering fetched")
	}
}

func TestLiveProgressFastRefresh(t *testing.T) {
	gh := sampleRuns()
	h := newHarness(gh, nil, "app")
	h.drain(h.SetVisible(true))
	h.init()
	if len(h.fast) != 1 {
		t.Fatalf("fast ticks armed = %d, want 1", len(h.fast))
	}
	// A second fetch landing doesn't stack another timer.
	h.drain(h.Refresh())
	if len(h.fast) != 1 {
		t.Fatalf("fast ticks armed = %d, want 1", len(h.fast))
	}

	// The run moves on: step 2 done, step 3 running.
	gh.mu.Lock()
	gh.jobs[3][0].Steps[1].State = github.CheckPass
	gh.jobs[3][0].Steps[1].CompletedAt = now
	gh.jobs[3][0].Steps[2].State = github.CheckRunning
	gh.jobs[3][0].Steps[2].StartedAt = now
	gh.mu.Unlock()
	h.fireFast()
	if got := gh.jobCalls(); !slices.Equal(got, []int64{3, 3}) {
		t.Errorf("job calls = %v, want a refetch of the active run", got)
	}
	mustContain(t, h.view(), "3. Upload", "running 0s")
	if len(h.fast) != 1 {
		t.Fatalf("not re-armed while still running: %d", len(h.fast))
	}

	// It finishes: the row turns green and the loop stops.
	done := run("acme/app", 3, "ci", "feat/pipes", nil)
	gh.set("acme/app", done, run("acme/app", 20, "ci", "main", nil))
	h.fireFast()
	v := h.view()
	mustContain(t, v, "2m14s")
	mustLack(t, v, "running 3m")
	if len(h.fast) != 0 {
		t.Errorf("fast refresh kept going after every run finished")
	}
}

func TestFastRefreshOnlyWhileVisible(t *testing.T) {
	gh := sampleRuns()
	h := newHarness(gh, nil, "app")
	h.init()
	if len(h.fast) != 0 {
		t.Fatal("armed while hidden")
	}
	h.drain(h.SetVisible(true))
	if len(h.fast) != 1 {
		t.Fatal("not armed on show")
	}
	h.drain(h.SetVisible(false))
	calls := len(gh.listCalls())
	h.fireFast() // the stale timer fires after hiding
	if len(gh.listCalls()) != calls {
		t.Error("fast tick refreshed a hidden tab")
	}
}

func TestFastRefreshOffWhenBaseIntervalIsShort(t *testing.T) {
	gh := sampleRuns()
	h := newHarness(gh, nil, "app")
	h.cfg.GitHub.RefreshInterval.Duration = 10 * time.Second
	h.drain(h.SetVisible(true))
	h.init()
	if len(h.fast) != 0 {
		t.Error("fast timer armed although the normal refresh is faster")
	}
}

func TestMineFilter(t *testing.T) {
	gh := sampleRuns()
	h := newHarness(gh, nil, "app")
	h.init()
	h.send("m")
	calls := gh.listCalls()
	if calls[len(calls)-1] != "acme/app branch= status= actor=daan" {
		t.Fatalf("calls = %v", calls)
	}
	mustContain(t, h.view(), "actor: @daan")
	if len(h.table.Rows()) != 2 {
		t.Errorf("rows = %d, want daan's 2", len(h.table.Rows()))
	}
	h.send("m", "m")
	if gh.viewers != 1 {
		t.Errorf("viewer fetched %d times", gh.viewers)
	}
}

func TestMineWithoutLogin(t *testing.T) {
	gh := sampleRuns()
	gh.viewer = ""
	h := newHarness(gh, nil, "app")
	h.init()
	errs := h.send("m")
	if len(errs) != 1 || !strings.Contains(errs[0], "who am I") {
		t.Fatalf("errs = %v", errs)
	}
	if h.filt.mine {
		t.Error("mine stayed on")
	}
}

func TestFilterForm(t *testing.T) {
	gh := sampleRuns()
	kv := newKV()
	h := newHarness(gh, kv, "app")
	h.init()
	h.send("f")
	if h.dialog == nil {
		t.Fatal("no dialog")
	}
	if !h.CapturingInput() {
		t.Error("dialog doesn't capture input")
	}
	// Branch: type main; workflow: pick "ci" (Any, ci, release); status:
	// pick Failed (Any, In progress, Queued, Failed); mine: No.
	for _, k := range []string{"m", "a", "i", "n", "enter", "down", "enter", "down", "down", "down", "enter", "enter"} {
		h.send(k)
	}
	if h.dialog != nil {
		t.Fatalf("dialog still open:\n%s", h.view())
	}
	want := filters{branch: "main", workflow: "ci", status: "failure"}
	if h.filt != want {
		t.Fatalf("filters = %+v, want %+v", h.filt, want)
	}
	calls := gh.listCalls()
	if calls[len(calls)-1] != "acme/app branch=main status=failure actor=" {
		t.Errorf("calls = %v", calls)
	}
	// GitHub returned release (failed on main); the workflow filter hides it.
	v := h.view()
	mustContain(t, v, "branch: main", "workflow: ci", "status: failed", "no runs match · x clears filters")
	if len(kv.m) != 2 {
		t.Errorf("cache keys = %d, want one per filter set", len(kv.m))
	}
	h.send("x")
	if h.filt != (filters{}) || len(h.table.Rows()) != 3 {
		t.Errorf("clear left %+v / %d rows", h.filt, len(h.table.Rows()))
	}
}

func TestWorkflowFilterIsClientSide(t *testing.T) {
	gh := sampleRuns()
	h := newHarness(gh, nil, "app")
	h.init()
	calls := len(gh.listCalls())
	h.drain(h.setFilters(filters{workflow: "release"}))
	if len(h.table.Rows()) != 1 {
		t.Errorf("rows = %d", len(h.table.Rows()))
	}
	if got := gh.listCalls(); len(got) != calls+1 || got[len(got)-1] != "acme/app branch= status= actor=" {
		t.Errorf("calls = %v", got)
	}
}

func TestShowRuns(t *testing.T) {
	gh := sampleRuns()
	h := newHarness(gh, nil, "app")
	h.init()
	h.drain(h.ShowRuns("acme/app", "feat/pipes"))
	if h.override != "" || h.filt != (filters{branch: "feat/pipes"}) {
		t.Fatalf("override %q filters %+v", h.override, h.filt)
	}
	if rows := h.table.Rows(); len(rows) != 1 || h.selectedKey() != "acme/app#3" {
		t.Errorf("rows = %v", rows)
	}
	mustContain(t, h.view(), "branch: feat/pipes", "2. Run tests")

	// A repo outside the project is listed on its own.
	gh.set("other/x", run("other/x", 1, "ci", "fix", nil))
	h.drain(h.ShowRuns("other/x", "fix"))
	mustContain(t, h.view(), "repo: other/x", "branch: fix")
	if h.selectedKey() != "other/x#1" {
		t.Errorf("selected = %q", h.selectedKey())
	}
	h.send("x")
	if h.override != "" || len(h.table.Rows()) != 3 {
		t.Error("clear kept the override")
	}
}

func TestCacheFirstAndOneErrorToast(t *testing.T) {
	gh := sampleRuns()
	kv := newKV()
	h := newHarness(gh, kv, "")
	h.init()
	gh.err = errors.New("boom")
	h2 := newHarness(gh, kv, "")
	errs := h2.init()
	if len(errs) != 1 || !strings.Contains(errs[0], "boom") {
		t.Fatalf("errs = %v, want one toast for two repos", errs)
	}
	if len(h2.table.Rows()) != 4 {
		t.Errorf("cached rows = %d", len(h2.table.Rows()))
	}
}

func TestEmptyStates(t *testing.T) {
	tests := []struct {
		name    string
		gh      func() *fakeRuns
		project string
		want    string
	}{
		{"no client", nil, "app", "GitHub isn't available"},
		{"no repo", sampleRuns, "notes", "project notes has no GitHub repo"},
		{"auth", func() *fakeRuns {
			f := sampleRuns()
			f.err = &api.HTTPError{StatusCode: 401, Message: "Bad credentials"}
			return f
		}, "app", "gh auth login"},
		{"failed", func() *fakeRuns {
			f := sampleRuns()
			f.err = errors.New("network down")
			return f
		}, "app", "couldn't load runs"},
		{"no runs", func() *fakeRuns { return &fakeRuns{runs: map[string][]github.Run{}} }, "app", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gh github.RunsClient
			if tt.gh != nil {
				gh = tt.gh()
			}
			h := newHarness(gh, nil, tt.project)
			h.init()
			if tt.want != "" {
				mustContain(t, h.view(), tt.want)
			}
		})
	}
}

func TestStaleFetchAfterProjectChange(t *testing.T) {
	gh := sampleRuns()
	h := newHarness(gh, nil, "app")
	cmd := h.Refresh()
	cfg := h.cfg
	h.SetProject(&cfg.Projects[1])
	h.drain(cmd) // app's runs land after the switch
	if len(h.table.Rows()) != 0 {
		t.Errorf("stale rows shown: %v", h.table.Rows())
	}
	h.drain(h.Refresh())
	if rows := h.table.Rows(); len(rows) != 1 || rows[0][len(rows[0])-1] != "acme/lib#5" {
		t.Errorf("rows = %v", rows)
	}
}

func TestJobsErrorToastsForSelected(t *testing.T) {
	gh := sampleRuns()
	gh.jobsErr = errors.New("HTTP 500")
	h := newHarness(gh, nil, "app")
	errs := h.init()
	if len(errs) != 1 || !strings.Contains(errs[0], "jobs of ci #3") {
		t.Fatalf("errs = %v", errs)
	}
	mustContain(t, h.view(), "couldn't load jobs")
}

func TestStatusDetailAndEntities(t *testing.T) {
	gh := sampleRuns()
	h := newHarness(gh, nil, "app")
	h.init()
	h.send("m")
	if got := h.StatusDetail(); got != "@daan · run 1/2 · 1 running" {
		t.Errorf("status = %q", got)
	}
	h.send("m")
	ents := h.Entities()
	if len(ents) != 3 || ents[0].Kind != EntityRun || ents[0].ID != "acme/app#3" || !strings.Contains(ents[0].Title, "ci #3") {
		t.Errorf("entities = %+v", ents)
	}
	h.drain(h.setFilters(filters{workflow: "ci"}))
	h.drain(h.Select("acme/app#10"))
	if h.selectedKey() != "acme/app#10" || h.filt.workflow != "" {
		t.Errorf("select: %q, filters %+v", h.selectedKey(), h.filt)
	}
}

func TestCommandsCoverKeys(t *testing.T) {
	h := newHarness(sampleRuns(), nil, "app")
	h.init()
	ids := map[string]bool{}
	for _, c := range h.Commands() {
		ids[c.ID] = true
	}
	for _, id := range []string{"filter", "pipeline_filters", "pipeline_mine", "pipeline_clear_filters", "pipeline_detail_down", "pipeline_detail_up", "pipeline_open", "grow_pane", "shrink_pane"} {
		if !ids[id] {
			t.Errorf("no palette command %s", id)
		}
	}
	h.send("m")
	found := false
	for _, c := range h.Commands() {
		if c.ID == "pipeline_clear_filters" {
			found = true
			h.drain(c.Run())
		}
		if c.ID == "pipeline_mine" && c.Title != "Pipelines: show everyone's" {
			t.Errorf("mine title = %q", c.Title)
		}
	}
	if !found || h.filt.mine {
		t.Error("clear command missing or didn't clear")
	}
}

func TestTextFilter(t *testing.T) {
	h := newHarness(sampleRuns(), nil, "app")
	h.init()
	h.send("/", "b", "o", "b", "enter")
	if n := len(h.table.VisibleRows()); n != 1 {
		t.Errorf("visible = %d", n)
	}
}

func TestCellDuration(t *testing.T) {
	r := run("acme/app", 3, "ci", "x", running)
	if _, got := cellDuration(durationCell(r), now); got != "running 3m00s" {
		t.Errorf("running = %q", got)
	}
	q := run("acme/app", 3, "ci", "x", func(r *github.Run) { r.State = github.CheckPending })
	if _, got := cellDuration(durationCell(q), now); got != "queued" {
		t.Errorf("queued = %q", got)
	}
	if _, got := cellDuration("", now); got != "—" {
		t.Errorf("empty = %q", got)
	}
}

// TestLoadStates: loading → failed (with the retry key) → retry → loaded.
func TestLoadStates(t *testing.T) {
	gh := sampleRuns()
	gh.err = errors.New("network down")
	h := newHarness(gh, nil, "app")
	_ = h.Refresh() // queued, not landed
	mustContain(t, h.view(), "heating up…")

	h = newHarness(gh, nil, "app")
	errs := h.init()
	if len(errs) != 1 || !strings.Contains(errs[0], "· r to retry") {
		t.Fatalf("toasts = %q", errs)
	}
	mustContain(t, h.view(), "couldn't load runs:", "network down", "r to retry")
	if h.Animating() {
		t.Error("a failed list keeps no clock running")
	}
	gh.mu.Lock()
	gh.err = nil
	gh.mu.Unlock()
	h.drain(h.Refresh())
	v := h.view()
	if strings.Contains(v, "couldn't load runs") || strings.Contains(v, "heating up…") || len(h.table.Rows()) == 0 {
		t.Fatalf("after retry:\n%s", v)
	}
}

// TestDetailLoadingHammer (P6.4): a run's jobs and a job's log show the
// hammer while they load, and the clock runs only meanwhile.
func TestDetailLoadingHammer(t *testing.T) {
	h := newHarness(sampleRuns(), nil, "app")
	h.anim = anim.NewEngine(anim.Full())
	tick := h.tick
	h.tick = func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
		if d == detailDebounce {
			return nil // jobs never fetched
		}
		return tick(d, fn)
	}
	h.init()
	mustContain(t, h.view(), "acme/app · ci #3", "heating up…")
	if !h.Animating() {
		t.Error("the jobs hammer must keep the clock running")
	}

	h = newHarness(sampleRuns(), nil, "app")
	h.anim = anim.NewEngine(anim.Full())
	h.init()
	mustLack(t, h.view(), "heating up…")
	if h.Animating() {
		t.Error("jobs loaded: no clock")
	}
}

func TestLogLoadingHammer(t *testing.T) {
	h := atFailedRun(t, failedRuns(t), newKV())
	h.anim = anim.NewEngine(anim.Full())
	h.Update(press("l")) // the log fetch is never run
	if h.log == nil {
		t.Fatal("viewer not open")
	}
	mustContain(t, h.view(), "fetching the log…")
	if !h.Animating() {
		t.Error("the log hammer must keep the clock running")
	}
}
