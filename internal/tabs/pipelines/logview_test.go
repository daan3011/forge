package pipelines

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/cilog"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/ui"
)

// failedRuns is sampleRuns with jobs and logs for the failed run 10: two
// failed jobs and a passing one.
func failedRuns(t *testing.T) *fakeRuns {
	t.Helper()
	raw, err := os.ReadFile("../../cilog/testdata/job.log")
	if err != nil {
		t.Fatal(err)
	}
	gh := sampleRuns()
	gh.jobs[10] = []github.Job{
		{ID: 101, Name: "test", State: github.CheckFail, URL: "https://github.com/acme/app/actions/runs/10/job/101"},
		{ID: 102, Name: "lint", State: github.CheckFail},
		{ID: 103, Name: "build", State: github.CheckPass},
	}
	gh.logs = map[int64][]byte{
		101: raw,
		102: []byte("2026-11-20T10:00:00Z setup\n2026-11-20T10:00:01Z panic: lint exploded\n"),
		103: []byte("all good\n"),
	}
	return gh
}

// atFailedRun is a harness with run 10 (the second row) selected.
func atFailedRun(t *testing.T, gh *fakeRuns, kv *memKV) *harness {
	t.Helper()
	var h *harness
	if kv == nil {
		h = newHarness(gh, nil, "app")
	} else {
		h = newHarness(gh, kv, "app")
	}
	if errs := h.init(); len(errs) > 0 {
		t.Fatal(errs)
	}
	h.send("j")
	if r, _ := h.selected(); r.ID != 10 {
		t.Fatalf("selected run %d", r.ID)
	}
	return h
}

func TestLogsOpenCollapsedToErrors(t *testing.T) {
	gh := failedRuns(t)
	kv := newKV()
	h := atFailedRun(t, gh, kv)
	if errs := h.send("l"); len(errs) > 0 {
		t.Fatal(errs)
	}
	if h.log == nil || !h.CapturingInput() {
		t.Fatal("viewer not open")
	}
	if got := gh.logCalls(); !slices.Equal(got, []int64{101}) {
		t.Fatalf("log calls = %v", got)
	}
	v := h.view()
	mustContain(t, v, "acme/app · release #10", "test  (1/2 failed)", "errors · 1 hunk",
		"⋯ 7 lines hidden ⋯", "--- FAIL: TestPipelineLiveProgress", "##[error]Process completed with exit code 2.")
	// Timestamps and ANSI are gone; the checkout group is collapsed away.
	mustLack(t, v, "2026-11-20T", "\x1b[31m", "repository: daan3011/forge")

	// The error summary is saved for P2.8.
	e, ok := kv.m[cilog.SummaryKey("acme/app", 10, 101)]
	if !ok {
		t.Fatalf("no summary saved; keys %v", keys(kv))
	}
	var s cilog.Summary
	if err := json.Unmarshal(e.Value, &s); err != nil {
		t.Fatal(err)
	}
	if e.Kind != cilog.Kind || s.Job != "test" || s.Workflow != "release" || s.RunID != 10 ||
		!slices.ContainsFunc(s.Lines, func(l string) bool { return strings.Contains(l, "--- FAIL") }) {
		t.Errorf("summary = %+v (kind %s)", s, e.Kind)
	}
}

func keys(kv *memKV) []string {
	var out []string
	for k := range kv.m {
		out = append(out, k)
	}
	return out
}

func TestLogsToggleFullAndSearch(t *testing.T) {
	gh := failedRuns(t)
	h := atFailedRun(t, gh, nil)
	h.send("l")
	h.send("e")
	// The first error context line stays at the top.
	if top := h.log.rows[h.log.offset].orig; top > 7 {
		t.Errorf("top line after toggle = %d", top)
	}
	h.send("g")
	v := h.view()
	mustContain(t, v, "full log · 27 lines", "repository: daan3011/forge", "▸ Run actions/checkout@v4")
	h.send("e")
	mustContain(t, h.view(), "errors · 1 hunk")

	// A query only in the hidden lines switches to the full log on enter.
	h.send("/", "S", "y", "n", "c", "i", "n", "g", "enter")
	if !h.log.full || len(h.log.matches) != 1 {
		t.Fatalf("full=%v matches=%v", h.log.full, h.log.matches)
	}
	mustContain(t, h.view(), `"Syncing": match 1/1`, "matches only in the full log")

	// esc drops the search, then closes the viewer.
	h.send("esc")
	if h.log == nil || h.log.search.Value() != "" {
		t.Fatal("esc should clear the search first")
	}
	h.send("esc")
	if h.log != nil || h.CapturingInput() {
		t.Fatal("viewer still open")
	}
	if r, _ := h.selected(); r.ID != 10 {
		t.Errorf("selection moved to %d", r.ID)
	}
}

func TestLogsSearchNextMatch(t *testing.T) {
	h := atFailedRun(t, failedRuns(t), nil)
	h.send("l", "e", "/", "o", "k", "enter")
	if n := len(h.log.matches); n != 14 {
		t.Fatalf("matches = %d", n)
	}
	h.send("n", "n")
	if h.log.cur != 2 {
		t.Errorf("cur = %d", h.log.cur)
	}
	h.send("N", "N", "N")
	if h.log.cur != 13 {
		t.Errorf("cur after wrapping back = %d", h.log.cur)
	}
}

func TestLogsSwitchFailedJobs(t *testing.T) {
	gh := failedRuns(t)
	kv := newKV()
	h := atFailedRun(t, gh, kv)
	h.send("l", "]")
	mustContain(t, h.view(), "lint  (2/2 failed)", "panic: lint exploded")
	h.send("]") // wraps, from memory
	mustContain(t, h.view(), "test  (1/2 failed)")
	if got := gh.logCalls(); !slices.Equal(got, []int64{101, 102}) {
		t.Errorf("log calls = %v (the passing job should not load)", got)
	}
	if _, ok := kv.m[cilog.SummaryKey("acme/app", 10, 102)]; !ok {
		t.Error("no summary for the second job")
	}
}

func TestLogsWaitForJobs(t *testing.T) {
	gh := failedRuns(t)
	h := atFailedRun(t, gh, nil)
	delete(h.det.jobs, "acme/app#10")
	h.send("l")
	if h.log == nil || h.log.waitJobs || len(h.log.rows) == 0 {
		t.Fatalf("viewer didn't open after the jobs landed: %+v", h.log)
	}
}

func TestLogsOnGreenRunShowFinishedJobs(t *testing.T) {
	gh := failedRuns(t)
	gh.jobs[20] = []github.Job{{ID: 103, Name: "build", State: github.CheckPass}}
	kv := newKV()
	h := atFailedRun(t, gh, kv)
	h.send("j", "l")
	mustContain(t, h.view(), "build", "no error lines found · showing the tail", "all good")
	if len(kv.m) != 1 { // only the runs cache; green jobs get no summary
		t.Errorf("cache keys = %v", keys(kv))
	}
}

func TestLogsNoFinishedJobs(t *testing.T) {
	h := newHarness(sampleRuns(), nil, "app")
	h.init() // run 3 (running) is selected
	_, cmd := h.Update(press("l"))
	_, other := h.drain(cmd)
	if h.log != nil || len(other) != 1 || !strings.Contains(other[0].(ui.InfoMsg).Text, "no finished jobs yet") {
		t.Fatalf("log=%v other=%v", h.log, other)
	}
}

func TestLogsLoadError(t *testing.T) {
	gh := failedRuns(t)
	gh.logErr = github.ErrLogsUnavailable
	h := atFailedRun(t, gh, nil)
	h.send("l")
	mustContain(t, h.view(), "couldn't load the log: logs not available", "o opens the job in the browser")
}

func TestRerunFailed(t *testing.T) {
	gh := failedRuns(t)
	h := atFailedRun(t, gh, nil)
	_, cmd := h.Update(press("t"))
	_, other := h.drain(cmd)
	if got := gh.actionCalls(); !slices.Equal(got, []string{"rerun-failed 10"}) {
		t.Fatalf("actions = %v", got)
	}
	if len(other) != 1 || other[0].(ui.InfoMsg).Text != "rerunning the failed jobs of release #10" {
		t.Errorf("toasts = %v", other)
	}
	// The runs refresh shortly after.
	before := len(gh.listCalls())
	h.fireFast()
	if len(gh.listCalls()) == before {
		t.Error("no refresh after the rerun")
	}
}

func TestRerunFromLogViewer(t *testing.T) {
	gh := failedRuns(t)
	h := atFailedRun(t, gh, nil)
	h.send("l", "R")
	if got := gh.actionCalls(); !slices.Equal(got, []string{"rerun-failed 10"}) {
		t.Fatalf("actions = %v", got)
	}
	if h.log == nil {
		t.Error("rerun closed the viewer")
	}
}

func TestRerunGuards(t *testing.T) {
	gh := failedRuns(t)
	h := newHarness(gh, nil, "app")
	h.init() // run 3 is running
	for _, k := range []string{"t", "ctrl+r"} {
		_, cmd := h.Update(press(k))
		_, other := h.drain(cmd)
		if len(other) != 1 || !strings.Contains(other[0].(ui.InfoMsg).Text, "still running") {
			t.Errorf("%s on a running run: %v", k, other)
		}
	}
	h.send("j", "j") // run 20 succeeded
	for k, want := range map[string]string{"t": "has no failed jobs", "X": "isn't running"} {
		_, cmd := h.Update(press(k))
		_, other := h.drain(cmd)
		if len(other) != 1 || !strings.Contains(other[0].(ui.InfoMsg).Text, want) {
			t.Errorf("%s on a green run: %v", k, other)
		}
	}
	if got := gh.actionCalls(); len(got) != 0 {
		t.Errorf("actions = %v", got)
	}
}

func TestRerunAllAndCancelConfirm(t *testing.T) {
	gh := failedRuns(t)
	h := atFailedRun(t, gh, nil)
	h.send("ctrl+r")
	if h.dialog == nil {
		t.Fatal("no confirm dialog")
	}
	mustContain(t, h.view(), "Rerun every job of release #10?")
	h.send("y")
	if got := gh.actionCalls(); !slices.Equal(got, []string{"rerun 10"}) {
		t.Fatalf("actions = %v", got)
	}

	h.send("k") // run 3, running
	h.send("X")
	mustContain(t, h.view(), "Cancel ci #3?")
	h.send("n") // keep it
	if got := gh.actionCalls(); len(got) != 1 {
		t.Fatalf("cancel ran without yes: %v", got)
	}
	h.send("X", "y")
	if got := gh.actionCalls(); !slices.Equal(got, []string{"rerun 10", "cancel 3"}) {
		t.Fatalf("actions = %v", got)
	}
}

func TestRunActionError(t *testing.T) {
	gh := failedRuns(t)
	gh.actErr = errorString("boom")
	h := atFailedRun(t, gh, nil)
	errs := h.send("t")
	if len(errs) != 1 || errs[0] != "couldn't rerun the failed jobs of release #10: boom" {
		t.Errorf("errs = %v", errs)
	}
	if len(h.acting) != 0 {
		t.Error("the run stays marked busy")
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

func TestLogCommands(t *testing.T) {
	h := atFailedRun(t, failedRuns(t), nil)
	ids := map[string]bool{}
	for _, c := range h.Commands() {
		ids[c.ID] = true
	}
	for _, id := range []string{"pipeline_logs", "pipeline_rerun_failed", "pipeline_rerun", "pipeline_cancel"} {
		if !ids[id] {
			t.Errorf("no palette command %s", id)
		}
	}
	h.send("l")
	ids = map[string]bool{}
	for _, c := range h.Commands() {
		ids[c.ID] = true
		if c.ID == "log_toggle_full" {
			h.drain(c.Run())
		}
	}
	for _, id := range []string{"log_toggle_full", "log_search", "log_next_match", "log_prev_match", "log_next_job", "log_prev_job", "log_open", "log_close"} {
		if !ids[id] {
			t.Errorf("no palette command %s", id)
		}
	}
	if !h.log.full {
		t.Error("log_toggle_full didn't toggle")
	}
	help := h.ShortHelp()
	if len(help) == 0 || help[0].Help().Desc != "full log / errors" {
		t.Errorf("viewer help = %v", help)
	}
}

var _ tea.Msg = logLoadedMsg{}
