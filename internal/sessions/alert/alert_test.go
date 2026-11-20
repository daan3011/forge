package alert

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/notify/notifytest"
	"github.com/daan3011/forge/internal/sessions/hooks"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/tmux/tmuxtest"
	"github.com/daan3011/forge/internal/store"
)

var t0 = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

type env struct {
	st   *store.Store
	rec  *notifytest.Recorder
	tmux *tmuxtest.Fake
	now  time.Time
	c    Checker
}

func newEnv(t *testing.T, p Policy) *env {
	t.Helper()
	ctx := context.Background()
	e := &env{rec: &notifytest.Recorder{}, tmux: tmuxtest.NewFake(), now: t0}
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "forge.db"), store.WithClock(func() time.Time { return e.now }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e.st = st
	e.c = Checker{Store: st.Sessions(), Notifier: e.rec, Watcher: e.tmux, Policy: p, Clock: func() time.Time { return e.now }}
	return e
}

// session creates a session with a tmux window and puts it in status.
func (e *env) session(t *testing.T, name string, status store.Status) store.Session {
	t.Helper()
	ctx := context.Background()
	s, err := e.st.Sessions().Create(ctx, store.Session{Name: name, ClaudeSessionID: "uuid-" + name, Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	ts, _ := e.tmux.EnsureSession(ctx, "p")
	tg, err := e.tmux.NewWindow(ctx, ts, name, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.Sessions().SetWindow(ctx, s.ID, tg.Session, tg.Window); err != nil {
		t.Fatal(err)
	}
	e.set(t, s.ID, status)
	return e.get(t, s.ID)
}

func (e *env) set(t *testing.T, id int64, status store.Status) {
	t.Helper()
	if err := e.st.Sessions().SetStatus(context.Background(), id, status, e.now); err != nil {
		t.Fatal(err)
	}
}

func (e *env) get(t *testing.T, id int64) store.Session {
	t.Helper()
	s, err := e.st.Sessions().Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *env) check(t *testing.T) int {
	t.Helper()
	n, err := e.c.CheckWaiting(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestWaitingNotifiesExactlyOnce is P1.8's "done when": leaving an agent
// waiting produces exactly one notification.
func TestWaitingNotifiesExactlyOnce(t *testing.T) {
	e := newEnv(t, Policy{After: 5 * time.Minute})
	s := e.session(t, "falcon", store.StatusWaiting)
	steps := []struct {
		at   time.Duration
		want int
	}{{0, 0}, {time.Minute, 0}, {5 * time.Minute, 1}, {6 * time.Minute, 1}, {time.Hour, 1}}
	for _, st := range steps {
		e.now = t0.Add(st.at)
		if n := e.check(t); n != 1 {
			t.Fatalf("at %s: waiting count = %d, want 1", st.at, n)
		}
		if got := len(e.rec.Sent()); got != st.want {
			t.Fatalf("at %s: %d notifications, want %d", st.at, got, st.want)
		}
	}
	// The hook racing the TUI on the same spell doesn't add one.
	if sent, err := e.c.NotifyWaitingNow(context.Background(), e.get(t, s.ID)); err != nil || sent {
		t.Fatalf("NotifyWaitingNow after the TUI notified = %v, %v", sent, err)
	}
	want := []notifytest.Sent{{Title: "forge", Body: "falcon is waiting for you"}}
	if got := e.rec.Sent(); !reflect.DeepEqual(got, want) {
		t.Fatalf("sent %+v, want %+v", got, want)
	}

	// A status change resets: the next waiting spell notifies again.
	e.set(t, s.ID, store.StatusWorking)
	e.now = e.now.Add(time.Minute)
	e.set(t, s.ID, store.StatusWaiting)
	e.now = e.now.Add(5 * time.Minute)
	e.check(t)
	e.check(t)
	if got := len(e.rec.Sent()); got != 2 {
		t.Fatalf("after a new waiting spell: %d notifications, want 2", got)
	}
}

func TestCheckWaiting(t *testing.T) {
	tests := []struct {
		name    string
		policy  Policy
		status  store.Status
		watched bool
		wait    time.Duration
		want    int // notifications
		count   int
	}{
		{name: "not due yet", policy: Policy{After: 5 * time.Minute}, status: store.StatusWaiting, wait: 4 * time.Minute, count: 1},
		{name: "due", policy: Policy{After: 5 * time.Minute}, status: store.StatusWaiting, wait: 5 * time.Minute, want: 1, count: 1},
		{name: "disabled", policy: Policy{}, status: store.StatusWaiting, wait: time.Hour, count: 1},
		{name: "not waiting", policy: Policy{After: time.Minute}, status: store.StatusWorking, wait: time.Hour},
		{name: "watched", policy: Policy{After: time.Minute}, status: store.StatusWaiting, watched: true, wait: time.Hour, count: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, tt.policy)
			s := e.session(t, "a", tt.status)
			e.tmux.SetWatched(tmux.Target{Session: s.TmuxSession, Window: s.TmuxWindow}, tt.watched)
			e.now = t0.Add(tt.wait)
			if n := e.check(t); n != tt.count {
				t.Errorf("count = %d, want %d", n, tt.count)
			}
			if got := len(e.rec.Sent()); got != tt.want {
				t.Errorf("%d notifications, want %d", got, tt.want)
			}
		})
	}
}

func TestWatchedThenUnwatched(t *testing.T) {
	e := newEnv(t, Policy{After: time.Minute})
	s := e.session(t, "a", store.StatusWaiting)
	tg := tmux.Target{Session: s.TmuxSession, Window: s.TmuxWindow}
	e.tmux.SetWatched(tg, true)
	e.now = t0.Add(2 * time.Minute)
	e.check(t)
	if got := len(e.rec.Sent()); got != 0 {
		t.Fatalf("watched: %d notifications", got)
	}
	e.tmux.SetWatched(tg, false)
	e.check(t)
	e.check(t)
	if got := len(e.rec.Sent()); got != 1 {
		t.Fatalf("after detaching: %d notifications, want 1", got)
	}
}

func TestNotifyWaitingNow(t *testing.T) {
	tests := []struct {
		name    string
		policy  Policy
		watched bool
		want    bool
	}{
		{name: "ignores After", policy: Policy{After: time.Hour}, want: true},
		{name: "disabled", policy: Policy{}},
		{name: "watched", policy: Policy{After: time.Hour}, watched: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, tt.policy)
			s := e.session(t, "a", store.StatusWaiting)
			e.tmux.SetWatched(tmux.Target{Session: s.TmuxSession, Window: s.TmuxWindow}, tt.watched)
			for range 2 { // the second call is always debounced
				_, _ = e.c.NotifyWaitingNow(context.Background(), e.get(t, s.ID))
			}
			if got := len(e.rec.Sent()) == 1; got != tt.want {
				t.Errorf("sent %v, want one: %v", e.rec.Sent(), tt.want)
			}
		})
	}
}

func TestNotifyTransition(t *testing.T) {
	tests := []struct {
		name    string
		policy  Policy
		kind    Kind
		watched bool
		want    []string
	}{
		{name: "error", policy: Policy{OnError: true}, kind: KindError, want: []string{"a hit an error"}},
		{name: "error off", policy: Policy{OnFinish: true}, kind: KindError},
		{name: "finished", policy: Policy{OnFinish: true}, kind: KindFinished, want: []string{"a finished"}},
		{name: "finished off", policy: Policy{OnError: true}, kind: KindFinished},
		{name: "watched", policy: Policy{OnError: true, OnFinish: true}, kind: KindFinished, watched: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, tt.policy)
			s := e.session(t, "a", store.StatusIdle)
			e.tmux.SetWatched(tmux.Target{Session: s.TmuxSession, Window: s.TmuxWindow}, tt.watched)
			if _, err := e.c.NotifyTransition(context.Background(), s, tt.kind); err != nil {
				t.Fatal(err)
			}
			if got := e.rec.Bodies(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("sent %q, want %q", got, tt.want)
			}
		})
	}
}

func TestForEvent(t *testing.T) {
	tests := []struct {
		event   string
		status  store.Status
		changed bool
		want    Kind
		ok      bool
	}{
		{hooks.Notification, store.StatusWaiting, true, KindWaiting, true},
		{hooks.Notification, store.StatusIdle, false, 0, false},
		{hooks.StopFailure, store.StatusError, true, KindError, true},
		{hooks.Stop, store.StatusIdle, true, KindFinished, true},
		{hooks.Stop, store.StatusArchived, false, 0, false},
		{hooks.PreToolUse, store.StatusWorking, true, 0, false},
		{hooks.SessionEnd, store.StatusDead, true, 0, false},
	}
	for _, tt := range tests {
		got, ok := ForEvent(tt.event, tt.status, tt.changed)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ForEvent(%s, %s, %v) = %v, %v; want %v, %v", tt.event, tt.status, tt.changed, got, ok, tt.want, tt.ok)
		}
	}
}

func TestWaitingDue(t *testing.T) {
	base := store.Session{Status: store.StatusWaiting, StatusChangedAt: t0}
	notified := base
	notified.WaitingNotifiedAt = t0.Add(time.Minute)
	idle := base
	idle.Status = store.StatusIdle
	tests := []struct {
		name  string
		s     store.Session
		now   time.Time
		after time.Duration
		want  bool
	}{
		{"due", base, t0.Add(5 * time.Minute), 5 * time.Minute, true},
		{"early", base, t0.Add(4 * time.Minute), 5 * time.Minute, false},
		{"already notified", notified, t0.Add(time.Hour), 5 * time.Minute, false},
		{"not waiting", idle, t0.Add(time.Hour), 5 * time.Minute, false},
		{"disabled", base, t0.Add(time.Hour), 0, false},
	}
	for _, tt := range tests {
		if got := WaitingDue(tt.s, tt.now, tt.after); got != tt.want {
			t.Errorf("%s: WaitingDue = %v, want %v", tt.name, got, tt.want)
		}
	}
}
