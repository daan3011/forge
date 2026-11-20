package app

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/notify/notifytest"
	"github.com/daan3011/forge/internal/refresh"
	"github.com/daan3011/forge/internal/sessions/alert"
	"github.com/daan3011/forge/internal/sessions/live"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

var alertT0 = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

type alertEnv struct {
	st  *store.Store
	rec *notifytest.Recorder
	now time.Time
}

func newAlertEnv(t *testing.T) *alertEnv {
	t.Helper()
	e := &alertEnv{rec: &notifytest.Recorder{}, now: alertT0}
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "forge.db"),
		store.WithClock(func() time.Time { return e.now }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e.st = st
	return e
}

func (e *alertEnv) checker() alert.Checker {
	return alert.Checker{
		Store: e.st.Sessions(), Notifier: e.rec,
		Policy: alert.PolicyFrom(config.Default().Sessions),
		Clock:  func() time.Time { return e.now },
	}
}

func (e *alertEnv) session(t *testing.T, name string, status store.Status) {
	t.Helper()
	ctx := context.Background()
	s, err := e.st.Sessions().Create(ctx, store.Session{Name: name, ClaudeSessionID: "uuid-" + name, Project: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.Sessions().SetStatus(ctx, s.ID, status, e.now); err != nil {
		t.Fatal(err)
	}
}

func (e *alertEnv) model() Model {
	m := newModel(twoProjects(), ui.NewTheme(true, nil), ui.DefaultKeyMap(),
		[]tabs.Tab{&fakeTab{title: "A"}, &fakeTab{title: "Sessions"}})
	WithAlerts(e.checker())(&m)
	return m
}

// feed runs cmd and passes every message it produces back into m.
func feed(m Model, cmd tea.Cmd) Model {
	for _, msg := range runCmd(cmd) {
		if msg == nil {
			continue
		}
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

// TestAlertsWaitingExactlyOnce is P1.8's "done when" through the TUI: the
// sessions refresh tick notifies once the wait passes notify_after_minutes,
// and only once.
func TestAlertsWaitingExactlyOnce(t *testing.T) {
	e := newAlertEnv(t)
	e.session(t, "falcon", store.StatusWaiting)
	m := e.model()
	tick := refresh.TickMsg{Source: refresh.Sessions}
	for _, at := range []time.Duration{0, time.Minute, 4 * time.Minute, 5 * time.Minute, 6 * time.Minute, time.Hour} {
		e.now = alertT0.Add(at)
		m = feed(m, m.alertTick(tick))
		want := 0
		if at >= 5*time.Minute {
			want = 1
		}
		if got := len(e.rec.Sent()); got != want {
			t.Fatalf("at %s: %d notifications, want %d", at, got, want)
		}
	}
	// A ping for the same spell (hook Notification) doesn't add one either,
	// even unfocused.
	m = update(m, tea.BlurMsg{})
	_, cmd := m.handleSessionPing(tabs.SessionPingMsg{Ping: live.Ping{Session: "falcon", Event: "Notification", Status: "waiting", Changed: true}})
	m = feed(m, cmd)
	if got := e.rec.Bodies(); !reflect.DeepEqual(got, []string{"falcon is waiting for you"}) {
		t.Fatalf("sent %q", got)
	}
	if m.waiting != 1 {
		t.Fatalf("waiting = %d, want 1", m.waiting)
	}
}

func TestAlertsOtherTickSourcesDontCheck(t *testing.T) {
	e := newAlertEnv(t)
	m := e.model()
	if cmd := m.alertTick(refresh.TickMsg{Source: refresh.GitHub}); cmd != nil {
		t.Fatal("GitHub tick checked waiting sessions")
	}
	if cmd := newModel(twoProjects(), ui.NewTheme(true, nil), ui.DefaultKeyMap(), nil).checkWaiting(); cmd != nil {
		t.Fatal("check without WithAlerts")
	}
}

func TestAlertsTransitionsOnlyWhenUnfocused(t *testing.T) {
	stop := live.Ping{Session: "falcon", Event: "Stop", Status: "idle", Changed: true}
	fail := live.Ping{Session: "falcon", Event: "StopFailure", Status: "error", Changed: true}
	tests := []struct {
		name  string
		focus tea.Msg // nil: never told (assumed focused)
		ping  live.Ping
		want  []string
	}{
		{name: "focused by default", ping: stop},
		{name: "focused", focus: tea.FocusMsg{}, ping: fail},
		{name: "unfocused finish", focus: tea.BlurMsg{}, ping: stop, want: []string{"falcon finished"}},
		{name: "unfocused error", focus: tea.BlurMsg{}, ping: fail, want: []string{"falcon hit an error"}},
		{name: "unfocused, unchanged", focus: tea.BlurMsg{}, ping: live.Ping{Session: "falcon", Event: "Stop", Status: "idle"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newAlertEnv(t)
			e.session(t, "falcon", store.StatusIdle)
			m := e.model()
			if tt.focus != nil {
				m = update(m, tt.focus)
			}
			_, cmd := m.handleSessionPing(tabs.SessionPingMsg{Ping: tt.ping})
			feed(m, cmd)
			if got := e.rec.Bodies(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("sent %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAlertsFocusAfterBlur(t *testing.T) {
	m := newAlertEnv(t).model()
	m = update(m, tea.BlurMsg{})
	if m.focused {
		t.Fatal("blur kept focus")
	}
	m = update(m, tea.FocusMsg{})
	if !m.focused {
		t.Fatal("focus not restored")
	}
	if !m.View().ReportFocus {
		t.Fatal("view does not ask for focus reports")
	}
}

func TestAlertsErrorToastOnce(t *testing.T) {
	m := newAlertEnv(t).model()
	m, cmd := m.handleWaiting(waitingMsg{err: context.DeadlineExceeded})
	if cmd == nil || m.toast.Empty() {
		t.Fatal("first failure should toast")
	}
	m.toast = ui.NewToast(m.theme)
	m, _ = m.handleWaiting(waitingMsg{err: context.DeadlineExceeded})
	if !m.toast.Empty() {
		t.Fatal("repeated failure toasted again")
	}
	m, _ = m.handleWaiting(waitingMsg{n: 3})
	if m.alertsFailing || m.waiting != 3 {
		t.Fatalf("recovery: failing %v waiting %d", m.alertsFailing, m.waiting)
	}
}

func TestPaletteTestNotification(t *testing.T) {
	e := newAlertEnv(t)
	m := e.model()
	for _, it := range m.paletteItems() {
		if it.id == "notify_test" {
			_, cmd := it.run(m)
			feed(m, cmd)
		}
	}
	if got := e.rec.Bodies(); !reflect.DeepEqual(got, []string{"notifications work"}) {
		t.Fatalf("sent %q", got)
	}
}

// TestWaitingCountOnEveryTab checks the status bar shows the waiting count
// whichever tab is active.
func TestWaitingCountOnEveryTab(t *testing.T) {
	e := newAlertEnv(t)
	e.session(t, "falcon", store.StatusWaiting)
	e.session(t, "owl", store.StatusWaiting)
	e.session(t, "lark", store.StatusWorking)
	tm := teatest.NewTestModel(t, mustNew(t, config.Default(), WithAlerts(e.checker())), teatest.WithInitialTermSize(120, 30))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("2 waiting"))
	}, teatest.WithDuration(3*time.Second))
	tm.Send(press("3"))
	tm.Send(press("q"))
	m := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second)).(Model)
	if m.active != 2 {
		t.Fatalf("active = %d, want 2", m.active)
	}
	if view := ansi.Strip(m.render()); !strings.Contains(view, "● 2 waiting") {
		t.Fatalf("status bar on tab 3 lacks the waiting count:\n%s", view)
	}
}
