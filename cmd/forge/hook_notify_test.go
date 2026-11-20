package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/store"
)

// window gives the session "fix" a tmux window on the fake server.
func (e *hookEnv) window(t *testing.T) tmux.Target {
	t.Helper()
	ctx := context.Background()
	ts, _ := e.tmux.EnsureSession(ctx, "forge")
	tg, err := e.tmux.NewWindow(ctx, ts, "fix", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, e.db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	x, err := s.Sessions().GetByName(ctx, "fix")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Sessions().SetWindow(ctx, x.ID, tg.Session, tg.Window); err != nil {
		t.Fatal(err)
	}
	return tg
}

func hookEvent(event, extra string) string {
	return `{"session_id":"` + hookID + `","hook_event_name":"` + event + `"` + extra + `}`
}

const (
	permission = `,"notification_type":"permission_prompt"`
	needsInput = `,"notification_type":"agent_needs_input"`
)

func TestHookNotifications(t *testing.T) {
	type step struct{ event, extra string }
	tests := []struct {
		name    string
		from    store.Status
		steps   []step
		noTUI   bool
		watched bool
		cfg     func(*hookEnv)
		want    []string
	}{
		{
			name: "waiting without a TUI notifies exactly once", noTUI: true, from: store.StatusWorking,
			steps: []step{{"Notification", permission}, {"Notification", needsInput}, {"Notification", permission}},
			want:  []string{"fix is waiting for you"},
		},
		{
			name: "a new waiting spell notifies again", noTUI: true, from: store.StatusWorking,
			steps: []step{{"Notification", permission}, {"PostToolUse", `,"tool_name":"Bash"`}, {"Notification", permission}},
			want:  []string{"fix is waiting for you", "fix is waiting for you"},
		},
		{
			name: "a running TUI handles it", from: store.StatusWorking,
			steps: []step{{"Notification", permission}, {"Stop", ""}, {"StopFailure", ""}},
		},
		{
			name: "watched window is skipped", noTUI: true, watched: true, from: store.StatusWorking,
			steps: []step{{"Notification", permission}, {"Stop", ""}, {"StopFailure", ""}},
		},
		{
			name: "notify_after_minutes 0 disables waiting", noTUI: true, from: store.StatusWorking,
			cfg:   func(e *hookEnv) { e.cfg.Sessions.NotifyAfterMinutes = 0 },
			steps: []step{{"Notification", permission}},
		},
		{
			name: "finished and error", noTUI: true, from: store.StatusWorking,
			steps: []step{{"Stop", ""}, {"UserPromptSubmit", `,"prompt":"go"`}, {"StopFailure", ""}},
			want:  []string{"fix finished", "fix hit an error"},
		},
		{
			name: "toggles off", noTUI: true, from: store.StatusWorking,
			cfg: func(e *hookEnv) {
				e.cfg.Sessions.NotifyOnFinish, e.cfg.Sessions.NotifyOnError = false, false
			},
			steps: []step{{"Stop", ""}, {"UserPromptSubmit", `,"prompt":"go"`}, {"StopFailure", ""}},
		},
		{
			name: "other events stay quiet", noTUI: true, from: store.StatusIdle,
			steps: []step{{"UserPromptSubmit", `,"prompt":"go"`}, {"PreToolUse", `,"tool_name":"Bash"`}, {"Notification", `,"notification_type":"idle_prompt"`}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newHookEnv(t)
			e.sender.noTUI = tt.noTUI
			if tt.cfg != nil {
				tt.cfg(e)
			}
			e.session(t, "fix", tt.from)
			e.tmux.SetWatched(e.window(t), tt.watched)
			for _, s := range tt.steps {
				if _, _, err := runHook(t, e.d, hookEvent(s.event, s.extra), s.event); err != nil {
					t.Fatal(err)
				}
			}
			if got := e.notes.Bodies(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("notifications %q, want %q", got, tt.want)
			}
			if log := e.log(); log != "" {
				t.Errorf("hook.log: %s", log)
			}
		})
	}
}

// A ping that fails to send reached no TUI, so the hook notifies itself.
func TestHookNotifiesWhenPingFails(t *testing.T) {
	e := newHookEnv(t)
	e.sender.err = errors.New("connect: invalid argument")
	e.session(t, "fix", store.StatusWorking)
	for range 2 {
		if _, _, err := runHook(t, e.d, hookEvent("Notification", permission), "Notification"); err != nil {
			t.Fatal(err)
		}
	}
	if got := e.notes.Bodies(); !reflect.DeepEqual(got, []string{"fix is waiting for you"}) {
		t.Errorf("notifications %q", got)
	}
	if !strings.Contains(e.log(), "invalid argument") {
		t.Errorf("send error not logged: %q", e.log())
	}
}
