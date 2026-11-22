package app

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/refresh"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// autoTab is a fakeTab that refreshes in the background.
type autoTab struct {
	fakeTab
	sources  []refresh.Source
	hidden   bool
	fetching bool
	got      []tea.Msg
}

func (a *autoTab) Update(msg tea.Msg) (tabs.Tab, tea.Cmd) {
	a.got = append(a.got, msg)
	return a, nil
}
func (a *autoTab) Sources() []refresh.Source { return a.sources }
func (a *autoTab) RefreshWhenHidden() bool   { return a.hidden }
func (a *autoTab) Fetching() bool            { return a.fetching }

type targeted struct{ to string }

func (t targeted) TargetTab() string { return t.to }

// refreshModel has Home (all sources, refreshes hidden), PRs (GitHub),
// Plane (Plane) and Docs (no sources), with PRs active.
func refreshModel() (Model, *autoTab, *autoTab, *autoTab, *autoTab) {
	home := &autoTab{fakeTab: fakeTab{title: "Home"}, sources: refresh.All, hidden: true}
	prs := &autoTab{fakeTab: fakeTab{title: "PRs"}, sources: []refresh.Source{refresh.GitHub}}
	plane := &autoTab{fakeTab: fakeTab{title: "Plane"}, sources: []refresh.Source{refresh.Plane}}
	docs := &autoTab{fakeTab: fakeTab{title: "Docs"}}
	m := newModel(config.Default(), ui.NewTheme(true, nil), ui.DefaultKeyMap(), []tabs.Tab{home, prs, plane, docs})
	m.active = 1
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }
	m.now = now
	for _, t := range []*autoTab{home, prs, plane, docs} {
		t.update = now // all freshly loaded
	}
	return m, home, prs, plane, docs
}

func refreshes(ts ...*autoTab) []int {
	out := make([]int, len(ts))
	for i, t := range ts {
		out[i] = t.refreshes
	}
	return out
}

func TestTickRefreshesVisibleAndBackgroundTabs(t *testing.T) {
	tests := []struct {
		name   string
		active int
		src    refresh.Source
		want   []int // home, prs, plane, docs
	}{
		{"github tick, PRs visible", 1, refresh.GitHub, []int{1, 1, 0, 0}},
		{"github tick, PRs hidden is paused", 3, refresh.GitHub, []int{1, 0, 0, 0}},
		{"plane tick, Plane visible", 2, refresh.Plane, []int{1, 0, 1, 0}},
		{"sessions tick only reaches Home", 1, refresh.Sessions, []int{1, 0, 0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, home, prs, plane, docs := refreshModel()
			m.active = tt.active
			_, cmd := m.Update(m.sched.Msg(tt.src))
			if got := refreshes(home, prs, plane, docs); !slices.Equal(got, tt.want) {
				t.Fatalf("refreshes = %v, want %v", got, tt.want)
			}
			if cmd == nil {
				t.Fatal("tick should re-arm")
			}
		})
	}
}

func TestStaleTickIgnored(t *testing.T) {
	m, home, prs, plane, docs := refreshModel()
	m.sched.SetDelay(refresh.GitHub, 10*time.Minute) // replaces the timer
	_, _ = m.Update(refresh.TickMsg{Source: refresh.GitHub, Gen: 0})
	if got := refreshes(home, prs, plane, docs); !slices.Equal(got, []int{0, 0, 0, 0}) {
		t.Fatalf("stale tick refreshed: %v", got)
	}
}

func TestSwitchCatchesUpStaleTab(t *testing.T) {
	tests := []struct {
		name string
		age  time.Duration
		zero bool
		want int
	}{
		{"fresh", 10 * time.Second, false, 0},
		{"missed a tick", 2 * time.Minute, false, 1},
		{"never loaded", 0, true, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _, prs, _, _ := refreshModel()
			m.active = 3
			prs.update = m.now.Add(-tt.age)
			if tt.zero {
				prs.update = time.Time{}
			}
			var model tea.Model = m
			model, _ = model.Update(press("2"))
			if model.(Model).active != 1 || prs.refreshes != tt.want {
				t.Fatalf("active %d, refreshes %d, want %d", model.(Model).active, prs.refreshes, tt.want)
			}
		})
	}
}

func TestSwitchCatchUpRespectsBackoff(t *testing.T) {
	m, _, prs, _, _ := refreshModel()
	m.active = 3
	prs.update = m.now.Add(-2 * time.Minute)
	m.sched.SetDelay(refresh.GitHub, 10*time.Minute)
	_, _ = m.Update(press("2"))
	if prs.refreshes != 0 {
		t.Fatal("switching should not bypass the rate limit backoff")
	}
}

func TestTargetedMsgReachesHiddenTab(t *testing.T) {
	m, home, prs, _, _ := refreshModel()
	_, _ = m.Update(targeted{to: "Home"})
	if len(home.got) != 1 || len(prs.got) != 0 {
		t.Fatalf("home got %v, prs got %v", home.got, prs.got)
	}
	_, _ = m.Update(targeted{to: "Nobody"}) // dropped, no panic
}

func TestRefreshAll(t *testing.T) {
	for _, via := range []string{"key", "palette"} {
		t.Run(via, func(t *testing.T) {
			m, home, prs, plane, docs := refreshModel()
			var model tea.Model = m
			if via == "key" {
				_, _ = model.Update(press("R"))
			} else {
				_, _ = runPalette(t, model, "refresh all tabs")
			}
			if got := refreshes(home, prs, plane, docs); !slices.Equal(got, []int{1, 1, 1, 1}) {
				t.Fatalf("refreshes = %v", got)
			}
		})
	}
}

func TestRateLimitBackoffAndBadge(t *testing.T) {
	m, _, _, _, _ := refreshModel()
	var model tea.Model = m
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 20})

	low := github.RateLimit{Limit: 5000, Remaining: 10, Reset: m.now.Add(50 * time.Minute)}
	model, cmd := model.Update(github.RateLimitMsg{RateLimit: low})
	got := model.(Model)
	if cmd == nil || got.sched.Interval(refresh.GitHub) != 5*time.Minute {
		t.Fatalf("interval = %s, want 5m backoff", got.sched.Interval(refresh.GitHub))
	}
	if view := ansi.Strip(got.render()); !strings.Contains(view, "gh 10/5000 · resets 50m") {
		t.Fatalf("badge missing:\n%s", view)
	}

	ok := github.RateLimit{Limit: 5000, Remaining: 4000, Reset: m.now.Add(50 * time.Minute)}
	model, _ = model.Update(github.RateLimitMsg{RateLimit: ok})
	got = model.(Model)
	if got.sched.Interval(refresh.GitHub) != time.Minute {
		t.Fatalf("backoff not cleared: %s", got.sched.Interval(refresh.GitHub))
	}
	if view := ansi.Strip(got.render()); strings.Contains(view, "gh 4000") {
		t.Fatal("badge shown while plenty of calls are left")
	}
}

func TestRateBadgeHiddenAfterReset(t *testing.T) {
	m, _, _, _, _ := refreshModel()
	m.ghRate = github.RateLimit{Limit: 5000, Remaining: 0, Reset: m.now.Add(-time.Second)}
	m.hasRate = true
	if b := m.rateBadge(); b != "" {
		t.Fatalf("badge after reset = %q", b)
	}
}

func TestSpinnerFollowsActiveFetch(t *testing.T) {
	m, _, prs, _, _ := refreshModel()
	var model tea.Model = m
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 20})

	prs.fetching = true
	model, cmd := model.Update(targeted{to: "PRs"})
	got := model.(Model)
	if !got.anim.Ticking() || cmd == nil {
		t.Fatal("animation clock should tick while the visible tab fetches")
	}
	if view := ansi.Strip(got.render()); !strings.Contains(view, "refreshing") {
		t.Fatalf("status bar should say refreshing:\n%s", view)
	}

	// Switching away from the fetching tab stops it.
	model, _ = model.Update(press("4"))
	if model.(Model).anim.Ticking() {
		t.Fatal("spinner kept running for a hidden tab")
	}
	prs.fetching = false
	model, _ = model.Update(press("2"))
	if model.(Model).anim.Ticking() {
		t.Fatal("spinner running with nothing to fetch")
	}
}

// TestRestartShowsCacheThenRefreshes is the P0.8 "done when": a restart
// shows the cached rows at once with their age, then the fresh fetch lands.
// Home (P6.1) loads its PR searches this way.
func TestRestartShowsCacheThenRefreshes(t *testing.T) {
	const key = "tab:home:all:reviews"
	cached, _ := json.Marshal(github.SearchResult{PRs: []github.PR{
		{Repo: "daan/forge", Number: 7, Title: "cached: from last run", State: github.StateOpen},
	}})
	fs := newFakeStore(map[string]string{key: string(cached)})
	fs.at[key] = time.Now().Add(-3 * time.Minute)

	m, err := New(config.Default(), WithStore(fs), WithGitHub(slowGitHub{d: 400 * time.Millisecond}))
	if err != nil {
		t.Fatal(err)
	}
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 24))
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("cached: from last run"))
	}, teatest.WithDuration(2*time.Second))
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Add retry to webhook"))
	}, teatest.WithDuration(3*time.Second))
	tm.Send(press("q"))

	final := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second)).(Model)
	view := ansi.Strip(final.render())
	if !strings.Contains(view, "just now") || strings.Contains(view, "cached: from last run") {
		t.Fatalf("fresh data should replace the cache:\n%s", view)
	}
	if fs.putCount(key) == 0 {
		t.Fatal("fresh rows were not written back to the cache")
	}
}

// slowGitHub is fakeGitHub with slow searches, so the cached copy shows
// first.
type slowGitHub struct {
	fakeGitHub
	d time.Duration
}

func (g slowGitHub) SearchPRs(ctx context.Context, q string, n int) (github.SearchResult, error) {
	time.Sleep(g.d)
	return g.fakeGitHub.SearchPRs(ctx, q, n)
}
