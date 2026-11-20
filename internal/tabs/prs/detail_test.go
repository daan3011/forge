package prs

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
)

// heldTicks replaces the tab's debounce timer: ticks wait in the slice
// until fire delivers them, like a cursor resting.
type heldTicks struct{ fns []func(time.Time) tea.Msg }

func (h *heldTicks) tick(_ time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
	h.fns = append(h.fns, fn)
	return nil
}

// fire delivers every held tick in order and returns the error toasts.
func (h *heldTicks) fire(tb *Tab) []string {
	fns := h.fns
	h.fns = nil
	var errs []string
	for _, fn := range fns {
		errs = append(errs, drain(tb, func() tea.Msg { return fn(now) })...)
	}
	return errs
}

func holdTicks(tb *Tab) *heldTicks {
	h := &heldTicks{}
	tb.tick = h.tick
	return h
}

func detailGH() *fakeGH {
	gh := sampleGH()
	gh.details = map[string]github.PRDetail{
		"acme/bubbletea#1": {Body: "Body one", ReviewDecision: github.ReviewApproved, Mergeable: github.Mergeable},
		"acme/bubbles#2":   {Body: "Body two", ReviewDecision: github.ReviewApproved, Mergeable: github.Conflicting},
		"acme/lipgloss#3":  {Body: "Body three", ReviewDecision: github.ReviewApproved, Mergeable: github.Mergeable},
	}
	return gh
}

// TestDetailNoLag is P2.2's done-when: moving through the list updates the
// pane at once; only the PR the cursor rests on is fetched; going back to
// a fetched PR needs no fetch.
func TestDetailNoLag(t *testing.T) {
	gh := detailGH()
	tb := newTestTab(gh, newKV())
	if errs := drain(tb, tb.Init()); len(errs) > 0 {
		t.Fatal(errs)
	}
	mustContain(t, view(tb), "main ← feat-1", "Body one")
	if got := gh.detailCalls(); !slices.Equal(got, []string{"acme/bubbletea#1"}) {
		t.Fatalf("detail calls after init = %v", got)
	}

	held := holdTicks(tb)
	send(tb, "j")
	v := view(tb)
	mustContain(t, v, "main ← feat-2", "heating up…", "No conflicts with base branch")
	mustLack(t, v, "main ← feat-1", "Body one")
	send(tb, "j", "k", "j")
	mustContain(t, view(tb), "main ← feat-3")
	if got := len(gh.detailCalls()); got != 1 {
		t.Fatalf("fetched while moving: %v", gh.detailCalls())
	}
	if errs := held.fire(tb); len(errs) > 0 {
		t.Fatal(errs)
	}
	if got := gh.detailCalls(); !slices.Equal(got, []string{"acme/bubbletea#1", "acme/lipgloss#3"}) {
		t.Fatalf("detail calls = %v, want only the PR the cursor rested on", got)
	}
	mustContain(t, view(tb), "Body three")

	send(tb, "k", "k")
	mustContain(t, view(tb), "main ← feat-1", "Body one") // from memory, before any tick
	held.fire(tb)
	if got := len(gh.detailCalls()); got != 2 {
		t.Fatalf("revisiting fetched again: %v", gh.detailCalls())
	}
}

// TestDetailUpdatedAt: a new updatedAt fetches again; the old details
// show, marked, until the new ones land.
func TestDetailUpdatedAt(t *testing.T) {
	gh := detailGH()
	tb := newTestTab(gh, newKV())
	drain(tb, tb.Init())
	held := holdTicks(tb)

	gh.mu.Lock()
	res := gh.results["author:@me"]
	res.PRs = slices.Clone(res.PRs)
	res.PRs[0].UpdatedAt = now.Add(-time.Minute)
	gh.results["author:@me"] = res
	gh.details["acme/bubbletea#1"] = github.PRDetail{Body: "Body one v2"}
	gh.mu.Unlock()

	drain(tb, tb.Refresh())
	mustContain(t, view(tb), "updating…", "Body one")
	held.fire(tb)
	v := view(tb)
	mustContain(t, v, "Body one v2")
	mustLack(t, v, "updating…")
	if got := len(gh.detailCalls()); got != 2 {
		t.Errorf("detail calls = %v", gh.detailCalls())
	}
}

// TestDetailKVCache: a restart shows cached details without fetching
// while updatedAt matches, and fetches once it doesn't.
func TestDetailKVCache(t *testing.T) {
	kv := newKV()
	first := newTestTab(detailGH(), kv)
	drain(first, first.Init())
	if !slices.Contains(kv.keys(), "pr-detail:acme/bubbletea#1") {
		t.Fatalf("detail not cached: %v", kv.keys())
	}

	gh := detailGH()
	gh.details["acme/bubbletea#1"] = github.PRDetail{Body: "from GitHub"}
	tb := newTestTab(gh, kv)
	drain(tb, tb.Init())
	mustContain(t, view(tb), "Body one")
	if got := gh.detailCalls(); len(got) != 0 {
		t.Fatalf("fetched despite the cache: %v", got)
	}

	gh.mu.Lock()
	res := gh.results["author:@me"]
	res.PRs = slices.Clone(res.PRs)
	res.PRs[0].UpdatedAt = now
	gh.results["author:@me"] = res
	gh.mu.Unlock()
	drain(tb, tb.Refresh())
	mustContain(t, view(tb), "from GitHub")
	if got := len(gh.detailCalls()); got != 1 {
		t.Errorf("detail calls = %v", gh.detailCalls())
	}
}

func TestDetailErrors(t *testing.T) {
	gh := detailGH()
	gh.detailErr = errors.New("boom")
	tb := newTestTab(gh, newKV())
	errs := drain(tb, tb.Init())
	if len(errs) != 1 || !strings.Contains(errs[0], "acme/bubbletea#1") || !strings.Contains(errs[0], "boom") {
		t.Fatalf("toasts = %v, want one naming the PR", errs)
	}
	mustContain(t, view(tb), "couldn't load details: boom", "Changes approved")

	// A late result for a PR nobody looks at any more: no toast, no entry.
	_, cmd := tb.Update(detailLoadedMsg{id: "acme/bubbles#2", key: "acme/bubbles#2@x", err: errors.New("late")})
	if msgs := run(cmd); len(msgs) != 0 {
		t.Errorf("stale error gave %v", msgs)
	}
	_, _ = tb.Update(detailLoadedMsg{id: "acme/bubbles#2", key: "acme/bubbles#2@x", detail: github.PRDetail{Body: "stale"}})
	mustLack(t, view(tb), "stale")
	// So is a render for a width the pane no longer has.
	_, _ = tb.Update(detailRenderedMsg{key: "acme/bubbletea#1@x", width: 3, body: "wrong width"})
	mustLack(t, view(tb), "wrong width")

	// Moving back to the PR retries it.
	gh.mu.Lock()
	gh.detailErr = nil
	gh.mu.Unlock()
	send(tb, "j", "k")
	mustContain(t, view(tb), "Body one")
}

func TestDetailNoGitHub(t *testing.T) {
	tb := newTestTab(nil, newKV())
	_, cmd := tb.Update(detailDueMsg{seq: tb.det.seq})
	if msgs := run(cmd); len(msgs) != 0 {
		t.Errorf("no client still loaded: %v", msgs)
	}
}

func checksAndActivity() github.PRDetail {
	at := func(m int) time.Time { return now.Add(-time.Duration(m) * time.Minute) }
	return github.PRDetail{
		Body: "Body one", ReviewDecision: github.ReviewChangesRequested, Mergeable: github.Mergeable,
		MergeState: github.MergeStateBehind, Approvals: 1,
		Labels: []github.Label{{Name: "bug", Color: "d73a4a"}},
		Reviewers: []github.Reviewer{
			{Login: "bob", State: github.ReviewStateApproved},
			{Login: "carol", State: github.ReviewStateChangesRequested},
			{Login: "acme/core", State: github.ReviewStatePending},
		},
		Checks: []github.Check{
			{Name: "test", Workflow: "CI", State: github.CheckFail, StartedAt: at(10), CompletedAt: at(5)},
			{Name: "e2e", State: github.CheckRunning, StartedAt: at(2)},
			{Name: "lint", Workflow: "CI", State: github.CheckPass, StartedAt: at(10), CompletedAt: at(10).Add(74 * time.Second)},
			{Name: "deploy", State: github.CheckSkipped},
		},
		Activity: []github.Event{
			{Kind: github.EventReview, Author: "carol", State: github.ReviewStateChangesRequested, Body: "Please rename", At: at(30)},
			{Kind: github.EventComment, Author: "", Body: "ghost words", At: at(120)},
		},
	}
}

func TestDetailTabs(t *testing.T) {
	gh := detailGH()
	gh.details["acme/bubbletea#1"] = checksAndActivity()
	tb := newTestTab(gh, newKV())
	tb.SetSize(160, 60) // room for the whole overview
	drain(tb, tb.Init())

	v := view(tb)
	mustContain(t, v,
		"Changes requested", "by @carol",
		"Some checks failed", "1 failing, 1 running", "1 skipped",
		"No conflicts with base branch", "behind main",
		"Labels", "bug", "Reviewers", "@bob approved", "@carol requested changes", "@acme/core review requested",
		"Checks 1✗", "Activity 2", "Body one",
	)

	send(tb, "]")
	v = view(tb)
	mustContain(t, v, "✗ test · CI", "5m00s", "e2e", "running 2m00s", "lint · CI", "1m14s", "deploy", "skipped")
	if strings.Index(v, "test · CI") > strings.Index(v, "lint · CI") {
		t.Error("failing check not listed first")
	}
	mustLack(t, v, "Body one")

	send(tb, "]")
	v = view(tb)
	mustContain(t, v, "@carol ✗ requested changes · 30m ago", "Please rename", "ghost commented · 2h ago", "ghost words")
	if strings.Index(v, "Please rename") > strings.Index(v, "ghost words") {
		t.Error("activity not newest first")
	}
	send(tb, "]") // wraps to Overview
	mustContain(t, view(tb), "Body one")
	send(tb, "[")
	mustContain(t, view(tb), "ghost words")

	for _, c := range []struct{ id, want string }{
		{"pr_detail_checks", "running 2m00s"},
		{"pr_detail_overview", "Body one"},
		{"pr_detail_activity", "Please rename"},
		{"pr_detail_next", "Body one"},
		{"pr_detail_prev", "Please rename"},
	} {
		runCommand(t, tb, c.id)
		mustContain(t, view(tb), c.want)
	}
}

func runCommand(t *testing.T, tb *Tab, id string) {
	t.Helper()
	for _, c := range tb.Commands() {
		if c.ID == id {
			drain(tb, c.Run())
			return
		}
	}
	t.Fatalf("no command %q", id)
}

func TestDetailScroll(t *testing.T) {
	gh := detailGH()
	var long []string
	for i := range 60 {
		long = append(long, "line "+string(rune('a'+i%26))+" "+strings.Repeat("x", i%7))
	}
	gh.details["acme/bubbletea#1"] = github.PRDetail{Body: strings.Join(long, "\n\n"), ReviewDecision: github.ReviewApproved, Mergeable: github.Mergeable}
	tb := newTestTab(gh, newKV())
	drain(tb, tb.Init())
	mustContain(t, view(tb), "Changes approved")
	send(tb, "J", "J", "J")
	v := view(tb)
	mustLack(t, v, "Changes approved")
	if tb.det.offset == 0 {
		t.Fatal("J didn't scroll")
	}
	send(tb, "K", "K", "K", "K")
	mustContain(t, view(tb), "Changes approved")
	if lines := strings.Split(tb.View(), "\n"); len(lines) != 30 {
		t.Errorf("view is %d lines, want 30", len(lines))
	}
	// A new PR starts at the top.
	send(tb, "J", "j")
	if tb.det.offset != 0 {
		t.Errorf("offset %d after moving to another PR", tb.det.offset)
	}
}

// fakeSessions answers LinkedSessions only.
type fakeSessions struct {
	sessions.API
	linked map[string][]store.Session
	err    error
}

func (f fakeSessions) LinkedSessions(_ context.Context, kind store.LinkKind, id string) ([]store.Session, error) {
	if kind != store.KindGitHubPR {
		return nil, errors.New("wrong kind")
	}
	return f.linked[id], f.err
}

func TestDetailLinkedSessions(t *testing.T) {
	gh := detailGH()
	d := testDeps(gh, newKV())
	d.Sessions = fakeSessions{linked: map[string][]store.Session{
		"acme/bubbletea#1": {
			{Name: "retry-fix", Status: store.StatusWaiting, CreatedAt: now.Add(-3 * time.Hour)},
			{Name: "old-try", Status: store.StatusDead, ArchivedAt: now, CreatedAt: now.Add(-48 * time.Hour)},
		},
	}}
	tb := newTab(d)
	tb.now = func() time.Time { return now }
	tb.tick = immediateTick
	tb.SetSize(120, 30)
	drain(tb, tb.Init())
	v := view(tb)
	mustContain(t, v, "Sessions", "▸ retry-fix waiting · 3h ago", "▸ old-try archived · 2d ago")
	if strings.Index(v, "retry-fix") > strings.Index(v, "old-try") {
		t.Error("newest session not first")
	}
	send(tb, "j")
	mustContain(t, view(tb), "Sessions", "none")

	// Without a session service there is no Sessions section.
	plain := newTestTab(detailGH(), newKV())
	drain(plain, plain.Init())
	mustLack(t, view(plain), "Sessions")
}

func TestDetailMemEviction(t *testing.T) {
	d := newDetailState()
	for i := range detailMemCap + 5 {
		id := "a/b#" + string(rune('0'+i%10)) + string(rune('a'+i/10))
		d.remember(&detailEntry{id: id, key: id + "@1"})
	}
	if len(d.mem) != detailMemCap || len(d.order) != detailMemCap || len(d.latest) != detailMemCap {
		t.Errorf("mem %d order %d latest %d, want %d", len(d.mem), len(d.order), len(d.latest), detailMemCap)
	}
}

func TestCheckDuration(t *testing.T) {
	tests := []struct {
		c    github.Check
		want string
	}{
		{github.Check{State: github.CheckPass, StartedAt: now.Add(-14 * time.Second), CompletedAt: now}, "14s"},
		{github.Check{State: github.CheckFail, StartedAt: now.Add(-134 * time.Second), CompletedAt: now}, "2m14s"},
		{github.Check{State: github.CheckPass, StartedAt: now.Add(-62 * time.Minute), CompletedAt: now}, "1h02m"},
		{github.Check{State: github.CheckRunning, StartedAt: now.Add(-time.Minute)}, "running 1m00s"},
		{github.Check{State: github.CheckPending}, "queued"},
		{github.Check{State: github.CheckSkipped}, "skipped"},
		{github.Check{State: github.CheckPass}, "—"},
	}
	for _, tt := range tests {
		if got := checkDuration(tt.c, now); got != tt.want {
			t.Errorf("checkDuration(%+v) = %q, want %q", tt.c, got, tt.want)
		}
	}
}

var _ tabs.TargetedMsg = detailDueMsg{}
