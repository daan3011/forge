package sessions

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/config"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/live"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

func newLiveTab(svc agent.API, pings <-chan live.Ping, level config.Animations) *Tab {
	return newLiveTabWith(svc, pings, anim.Settings{Level: level, Color: level != config.AnimationsOff})
}

func newLiveTabWith(svc agent.API, pings <-chan live.Ping, s anim.Settings) *Tab {
	tab := New(tabkit.Deps{
		Theme: ui.NewTheme(true, nil), Keys: ui.DefaultKeyMap(), Config: testConfig(),
		Sessions: svc, Live: pings, Anim: anim.NewEngine(s),
	})
	tab.SetProject(&tab.cfg.Projects[0])
	tab.SetSize(120, 30)
	return tab
}

// load runs one list refresh synchronously.
func load(t *testing.T, tab *Tab) {
	t.Helper()
	cmd := tab.Refresh()
	if cmd == nil {
		t.Fatal("no refresh")
	}
	tab.Update(cmd())
}

func names(tab *Tab) []string {
	var out []string
	for _, r := range tab.table.Rows() {
		out = append(out, r[0]+":"+r[statusCol])
	}
	return out
}

// runAll runs cmd and the commands of any batch it returns, one level deep,
// returning the messages.
func runAll(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, runAll(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func TestPingUpdatesRowBeforeRefresh(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusWorking), row("beta", store.StatusWorking))
	tab := newLiveTab(svc, nil, config.AnimationsOff)
	load(t, tab)
	if got := strings.Join(names(tab), " "); got != "alpha:working beta:working" {
		t.Fatalf("rows %s", got)
	}
	_, cmd := tab.Update(pingMsg{ok: true, ping: live.Ping{V: 1, Session: "beta", Event: "Notification", Status: "waiting", Changed: true}})
	// Shown at once, waiting first, before any refresh has landed.
	if got := strings.Join(names(tab), " "); got != "beta:waiting alpha:working" {
		t.Errorf("rows after ping %s", got)
	}
	if !tab.fetching || cmd == nil {
		t.Error("ping did not start a refresh")
	}
}

func TestPingUnknownOrUnchangedKeepsRows(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusIdle))
	tab := newLiveTab(svc, nil, config.AnimationsOff)
	load(t, tab)
	tab.Update(pingMsg{ok: true, ping: live.Ping{V: 1, Session: "ghost", Status: "working", Changed: true}})
	tab.fetching = false
	tab.Update(pingMsg{ok: true, ping: live.Ping{V: 1, Session: "alpha", Status: "idle"}})
	if got := strings.Join(names(tab), " "); got != "alpha:idle" {
		t.Errorf("rows %s", got)
	}
	if _, cmd := tab.Update(pingMsg{ok: false}); cmd != nil {
		t.Error("closed socket re-armed")
	}
}

func TestPingsCoalesceRefreshes(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusIdle))
	tab := newLiveTab(svc, nil, config.AnimationsOff)
	load(t, tab)

	inflight := tab.Refresh() // a refresh that read the db before the hook
	ping := pingMsg{ok: true, ping: live.Ping{V: 1, Session: "alpha", Status: "working", Changed: true}}
	for range 3 {
		if _, cmd := tab.Update(ping); cmd != nil {
			t.Fatal("ping during a refresh started another one")
		}
	}
	if !tab.dirty {
		t.Fatal("not marked dirty")
	}
	svc.setStatus("alpha", store.StatusWorking)
	_, cmd := tab.Update(inflight())
	if tab.dirty || !tab.fetching || cmd == nil {
		t.Fatalf("no follow-up refresh (dirty %v fetching %v)", tab.dirty, tab.fetching)
	}
	for _, m := range runAll(cmd) {
		if lm, ok := m.(listedMsg); ok {
			tab.Update(lm)
		}
	}
	if tab.fetching || strings.Join(names(tab), " ") != "alpha:working" {
		t.Errorf("rows %v fetching %v", names(tab), tab.fetching)
	}
}

func TestLivePingsEndToEnd(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusIdle), row("beta", store.StatusIdle))
	pings := make(chan live.Ping, 4)
	tab := newLiveTab(svc, pings, config.AnimationsOff)
	sc := newScreen(t, &host{tab: tab})
	sc.wait(t, "▬ idle")

	svc.setStatus("beta", store.StatusWaiting)
	pings <- live.Ping{V: 1, Session: "beta", Event: "Notification", Status: "waiting", Changed: true}
	sc.until(t, "beta waiting, listed first", func(frame string, _ []string) bool {
		order := ""
		for l := range strings.SplitSeq(frame, "\n") {
			for _, n := range []string{"alpha", "beta"} {
				if strings.HasPrefix(l, "│"+n+" ") {
					order += n + " "
				}
			}
		}
		return strings.Contains(frame, "● waiting") && order == "beta alpha "
	})

	// The listener is re-armed: a second ping is handled too.
	svc.setStatus("beta", store.StatusWorking)
	pings <- live.Ping{V: 1, Session: "beta", Event: "PostToolUse", Status: "working", Changed: true}
	sc.wait(t, "▲ working")

	sc.tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	sc.tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestStatusFlames(t *testing.T) {
	svc := newFakeSvc(
		row("a-work", store.StatusWorking), row("b-wait", store.StatusWaiting), row("c-idle", store.StatusIdle),
		row("d-err", store.StatusError), row("e-dead", store.StatusDead), row("f-new", store.StatusStarting),
	)
	tab := newLiveTab(svc, nil, config.AnimationsOff)
	load(t, tab)
	v := ansi.Strip(tab.View())
	for _, want := range []string{"▲ working", "● waiting", "▬ idle", "╳ error", "· dead", "▬ starting"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q in\n%s", want, v)
		}
	}
	if !strings.Contains(v, "b-wait  ● waiting") { // detail pane title of the selected (first) row
		t.Errorf("detail title has no flame:\n%s", v)
	}
}

func TestAnimatingOnlyForMovingFlames(t *testing.T) {
	full, reduced := anim.Full(), anim.Settings{Level: config.AnimationsReduced, Color: true}
	noColor := anim.Settings{Level: config.AnimationsReduced}
	tests := []struct {
		name string
		s    anim.Settings
		st   store.Status
		want bool
	}{
		{"working, full", full, store.StatusWorking, true},
		{"waiting, full", full, store.StatusWaiting, true},
		{"waiting, reduced", reduced, store.StatusWaiting, true},
		{"waiting, reduced without colour", noColor, store.StatusWaiting, false},
		{"idle, full", full, store.StatusIdle, false},
		{"dead, full", full, store.StatusDead, false},
		{"error, full", full, store.StatusError, false},
		{"working, off", anim.Settings{Level: config.AnimationsOff}, store.StatusWorking, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tab := newLiveTabWith(newFakeSvc(row("a", tt.st)), nil, tt.s)
			load(t, tab)
			if got := tab.Animating(); got != tt.want {
				t.Errorf("Animating = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPermissionPromptFallback(t *testing.T) {
	const prompt = " Do you want to proceed?\n ❯ 1. Yes\n   2. No\n"
	svc := newFakeSvc(row("w", store.StatusWaiting))
	tab := newLiveTab(svc, nil, config.AnimationsOff)
	tab.tick = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }
	load(t, tab)
	tab.SetVisible(true)
	capture := func(text string) tea.Cmd {
		_, cmd := tab.Update(previewMsg{gen: tab.previewGen, name: "w", text: text})
		tab.previewBusy = false
		return cmd
	}

	if capture(prompt) != nil || capture("working...") != nil {
		t.Fatal("cleared too early")
	}
	if capture(prompt) != nil || capture("working...") != nil { // a visible prompt resets the streak
		t.Fatal("streak not reset by a visible prompt")
	}
	cmd := capture("still working")
	if cmd == nil {
		t.Fatal("two misses in a row did not clear")
	}
	_, refresh := tab.Update(cmd())
	if got := svc.clearCalls(); len(got) != 1 || got[0] != "w" {
		t.Fatalf("ClearWaiting calls %q", got)
	}
	if refresh == nil {
		t.Fatal("no refresh after clearing")
	}
	tab.Update(refresh())
	if r, _ := tab.current(); r.Status != store.StatusWorking {
		t.Errorf("status %s", r.Status)
	}
	// Not waiting any more: no more checks.
	capture("x")
	capture("y")
	if n := len(svc.clearCalls()); n != 1 {
		t.Errorf("ClearWaiting called %d times", n)
	}
}

func TestPermissionPromptFallbackDeclinedOnce(t *testing.T) {
	// ClearWaiting says no (e.g. an elicitation): don't ask again for the
	// same waiting spell.
	svc := newFakeSvc(row("w", store.StatusWaiting))
	tab := newLiveTab(svc, nil, config.AnimationsOff)
	tab.tick = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }
	tab.svc = declining{svc}
	load(t, tab)
	tab.SetVisible(true)
	calls := 0
	for range 6 {
		_, cmd := tab.Update(previewMsg{gen: tab.previewGen, name: "w", text: "no prompt"})
		tab.previewBusy = false
		if cmd != nil {
			calls++
			tab.Update(cmd())
		}
	}
	if calls != 1 {
		t.Errorf("asked %d times, want 1", calls)
	}
}

type declining struct{ *fakeSvc }

func (declining) ClearWaiting(context.Context, store.Session) (bool, error) { return false, nil }
