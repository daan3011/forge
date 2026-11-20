package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/sessions/live"
	"github.com/daan3011/forge/internal/store"
)

const hookID = "0b5f3c3e-8a1d-4c2b-9f00-1234567890ab"

type fakeSender struct {
	mu    sync.Mutex
	pings []live.Ping
	err   error
}

func (f *fakeSender) Send(_ context.Context, p live.Ping) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pings = append(f.pings, p)
	return f.err
}

type hookEnv struct {
	d      deps
	db     string
	sender *fakeSender
	env    map[string]string
}

func newHookEnv(t *testing.T) *hookEnv {
	t.Helper()
	e := &hookEnv{db: filepath.Join(t.TempDir(), "data", "forge.db"), sender: &fakeSender{}, env: map[string]string{}}
	e.d = deps{
		db: e.db, sender: e.sender,
		getenv: func(k string) string { return e.env[k] },
		now:    func() time.Time { return time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC) },
	}
	return e
}

func (e *hookEnv) session(t *testing.T, name string, st store.Status) {
	t.Helper()
	s, err := store.Open(context.Background(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Sessions().Create(context.Background(), store.Session{
		Name: name, ClaudeSessionID: hookID, Project: "forge", Status: st,
	}); err != nil {
		t.Fatal(err)
	}
}

func (e *hookEnv) status(t *testing.T, name string) store.Status {
	t.Helper()
	s, err := store.Open(context.Background(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	x, err := s.Sessions().GetByName(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return x.Status
}

func (e *hookEnv) log() string {
	b, _ := os.ReadFile(filepath.Join(filepath.Dir(e.db), "hook.log"))
	return string(b)
}

func runHook(t *testing.T, d deps, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd := newRootCmd(d)
	cmd.SetArgs(append([]string{"hook"}, args...))
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	err = cmd.Execute()
	return out.String(), errb.String(), err
}

func TestHookUpdatesStatusAndPings(t *testing.T) {
	e := newHookEnv(t)
	e.session(t, "fix", store.StatusIdle)
	steps := []struct {
		event, extra string
		want         store.Status
	}{
		{"UserPromptSubmit", `"prompt":"go"`, store.StatusWorking},
		{"Notification", `"notification_type":"permission_prompt"`, store.StatusWaiting},
		{"PostToolUse", `"tool_name":"Bash"`, store.StatusWorking},
		{"Stop", `"stop_hook_active":false`, store.StatusIdle},
	}
	for _, s := range steps {
		in := `{"session_id":"` + hookID + `","hook_event_name":"` + s.event + `",` + s.extra + `}`
		stdout, stderr, err := runHook(t, e.d, in, s.event)
		if err != nil || stdout != "" || stderr != "" {
			t.Fatalf("%s: err %v stdout %q stderr %q", s.event, err, stdout, stderr)
		}
		if got := e.status(t, "fix"); got != s.want {
			t.Errorf("%s: status %s, want %s", s.event, got, s.want)
		}
	}
	if len(e.sender.pings) != len(steps) {
		t.Fatalf("pings %+v", e.sender.pings)
	}
	last := e.sender.pings[len(steps)-1]
	if last != (live.Ping{Session: "fix", ClaudeSessionID: hookID, Event: "Stop", Status: "idle", Changed: true}) {
		t.Errorf("last ping %+v", last)
	}
	if l := e.log(); l != "" {
		t.Errorf("hook log: %s", l)
	}
}

func TestHookEventFallsBackToArg(t *testing.T) {
	e := newHookEnv(t)
	e.session(t, "fix", store.StatusIdle)
	if _, _, err := runHook(t, e.d, `{"session_id":"`+hookID+`"}`, "PreToolUse"); err != nil {
		t.Fatal(err)
	}
	if got := e.status(t, "fix"); got != store.StatusWorking {
		t.Errorf("status %s", got)
	}
}

func TestHookUnknownSessionDoesNotPing(t *testing.T) {
	e := newHookEnv(t)
	if _, _, err := runHook(t, e.d, `{"session_id":"other","hook_event_name":"Stop"}`, "Stop"); err != nil {
		t.Fatal(err)
	}
	if len(e.sender.pings) != 0 {
		t.Errorf("pinged %+v", e.sender.pings)
	}
}

func TestHookNeverFails(t *testing.T) {
	tests := []struct {
		name    string
		stdin   string
		args    []string
		setup   func(t *testing.T, e *hookEnv)
		wantLog string
	}{
		{name: "garbage", stdin: "not json", args: []string{"Stop"}, wantLog: "Stop: hooks: parse payload"},
		{name: "empty", stdin: "", args: []string{"Stop"}, wantLog: "empty payload"},
		{name: "no args", stdin: "{", wantLog: "parse payload"},
		{name: "flags are not parsed", stdin: "{", args: []string{"--bogus", "-x"}, wantLog: "--bogus"},
		{
			name: "send fails", stdin: `{"session_id":"` + hookID + `","hook_event_name":"Stop"}`, args: []string{"Stop"},
			setup: func(t *testing.T, e *hookEnv) {
				e.session(t, "fix", store.StatusWorking)
				e.sender.err = errors.New("boom")
			},
			wantLog: "boom",
		},
		{
			name: "database unusable", stdin: `{"session_id":"x","hook_event_name":"Stop"}`, args: []string{"Stop"},
			setup: func(_ *testing.T, e *hookEnv) {
				// The database path is a directory.
				_ = os.MkdirAll(e.db, 0o700)
			},
			wantLog: "Stop:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newHookEnv(t)
			if tt.setup != nil {
				tt.setup(t, e)
			}
			stdout, stderr, err := runHook(t, e.d, tt.stdin, tt.args...)
			if err != nil || stdout != "" || stderr != "" {
				t.Fatalf("err %v stdout %q stderr %q", err, stdout, stderr)
			}
			if l := e.log(); !strings.Contains(l, tt.wantLog) {
				t.Errorf("log %q, want %q", l, tt.wantLog)
			}
		})
	}
}

func TestHookRecoversFromPanic(t *testing.T) {
	e := newHookEnv(t)
	e.d.getenv = func(string) string { panic("kaboom") }
	e.session(t, "fix", store.StatusIdle)
	_, _, err := runHook(t, e.d, `{"session_id":"`+hookID+`","hook_event_name":"Stop"}`, "Stop")
	if err != nil {
		t.Fatal(err)
	}
	if l := e.log(); !strings.Contains(l, "panic: kaboom") {
		t.Errorf("log %q", l)
	}
}

func TestHookLogRotates(t *testing.T) {
	e := newHookEnv(t)
	path := filepath.Join(filepath.Dir(e.db), "hook.log")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), hookLogMax+1), 0o600); err != nil {
		t.Fatal(err)
	}
	e.d.logHook("Stop", errors.New("fresh"))
	if l := e.log(); !strings.HasSuffix(l, "Stop: fresh\n") || len(l) > 200 {
		t.Errorf("log not rotated: %d bytes", len(l))
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("no rotated log: %v", err)
	}
}
