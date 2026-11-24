package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/sessions/claude"
	"github.com/daan3011/forge/internal/sessions/hooks"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/tmux/tmuxtest"
	"github.com/daan3011/forge/internal/slashcmd"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/testutil"
)

var t0 = time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

func at(d time.Duration) time.Time { return t0.Add(d) }

func obs(st store.Status, ev string) Observation { return Observation{Status: st, Event: ev} }

func TestSmokeStateMachine(t *testing.T) {
	type step struct {
		after time.Duration
		o     Observation
		want  SmokePhase
	}
	tests := []struct {
		name  string
		steps []step
	}{
		{"happy path", []step{
			{1 * time.Second, obs(store.StatusStarting, ""), SmokeWaiting},
			{3 * time.Second, obs(store.StatusIdle, "SessionStart"), SmokeWaiting},
			{4 * time.Second, obs(store.StatusWorking, "UserPromptSubmit"), SmokeWaiting},
			{8 * time.Second, obs(store.StatusIdle, "Stop"), SmokePassed},
			{9 * time.Second, obs(store.StatusDead, "SessionEnd"), SmokePassed}, // done stays done
		}},
		{"stall before the prompt: needs input, then recovers", []step{
			{5 * time.Second, obs(store.StatusStarting, ""), SmokeWaiting},
			{10 * time.Second, obs(store.StatusStarting, ""), SmokeNeedsInput},
			{30 * time.Second, obs(store.StatusIdle, "SessionStart"), SmokeWaiting},
			{31 * time.Second, obs(store.StatusWorking, "UserPromptSubmit"), SmokeWaiting},
			// a long turn while working is not a stall
			{60 * time.Second, obs(store.StatusWorking, "UserPromptSubmit"), SmokeWaiting},
			{61 * time.Second, obs(store.StatusIdle, "Stop"), SmokePassed},
		}},
		{"waiting before working", []step{
			{2 * time.Second, obs(store.StatusWaiting, "Notification"), SmokeNeedsInput},
			{4 * time.Second, obs(store.StatusWorking, "UserPromptSubmit"), SmokeWaiting},
		}},
		{"error", []step{
			{2 * time.Second, obs(store.StatusError, "StopFailure"), SmokeFailed},
		}},
		{"timeout", []step{
			{30 * time.Second, obs(store.StatusIdle, "SessionStart"), SmokeWaiting},
			{50 * time.Second, obs(store.StatusIdle, "SessionStart"), SmokeNeedsInput},
			{90 * time.Second, obs(store.StatusIdle, "SessionStart"), SmokeTimedOut},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSmokeState(t0)
			for i, st := range tt.steps {
				s = s.Advance(st.o, at(st.after))
				if s.Phase != st.want {
					t.Fatalf("step %d (%v): phase %v, want %v", i, st.o, s.Phase, st.want)
				}
			}
		})
	}
}

func TestSmokeResult(t *testing.T) {
	s := NewSmokeState(t0)
	s = s.Advance(obs(store.StatusIdle, "SessionStart"), at(time.Second))
	s = s.Advance(obs(store.StatusIdle, "SessionStart"), at(SmokeTimeout))
	r := s.Result([]string{"✓ hooks"})
	if r.Status != Failed || !strings.Contains(r.Detail, "last event: SessionStart") || r.Lines[0] != "✓ hooks" {
		t.Errorf("timeout result = %+v", r)
	}
	fresh := NewSmokeState(t0).Advance(obs(store.StatusStarting, ""), at(SmokeTimeout))
	if r := fresh.Result(nil); !strings.Contains(r.Detail, "no hook event yet") {
		t.Errorf("no events = %+v", r)
	}
	pass := NewSmokeState(t0).Advance(obs(store.StatusWorking, "UserPromptSubmit"), at(time.Second)).
		Advance(obs(store.StatusIdle, "Stop"), at(2*time.Second))
	if r := pass.Result(nil); r.Status != Done {
		t.Errorf("pass = %+v", r)
	}
}

func TestRegistrationChecks(t *testing.T) {
	dir := t.TempDir()
	wt, sess := filepath.Join(dir, "wt"), filepath.Join(dir, "sess")
	skills := filepath.Join(sess, "forge")
	lines := RegistrationChecks(wt, sess, skills, "sh -c exec claude")
	if RegistrationsOK(lines) || len(lines) != 3 {
		t.Fatalf("nothing registered: %q", lines)
	}
	if err := hooks.Install(wt, "/usr/local/bin/forge"); err != nil {
		t.Fatal(err)
	}
	if err := claude.WriteMCPConfig(filepath.Join(sess, claude.MCPConfigFile), "/usr/local/bin/forge", "s"); err != nil {
		t.Fatal(err)
	}
	// Written, but claude not started with the flags.
	lines = RegistrationChecks(wt, sess, skills, "sh -c exec claude")
	if !strings.Contains(strings.Join(lines, "\n"), "wasn't started with --mcp-config") {
		t.Errorf("flag check: %q", lines)
	}
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skills, slashcmd.ManifestFile), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines = RegistrationChecks(wt, sess, skills, "sh -c exec 'claude' --add-dir x --mcp-config y")
	if !RegistrationsOK(lines) {
		t.Errorf("all registered: %q", lines)
	}
}

// LiveSmoke on a fake tmux server: the session starts in a temp repo with
// forge's registrations, a hook flipping its status is observed, and
// cleanup removes everything.
func TestLiveSmokeOnFakes(t *testing.T) {
	testutil.IsolateHome(t)
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	fake := tmuxtest.NewFake()
	var socket string
	l := LiveSmoke{
		Store: st, ForgeBin: "/usr/local/bin/forge", ClaudeBin: "claude",
		DataDir: filepath.Join(dir, "data"), Skills: slashcmd.NewCatalog(),
		NewTmux: func(s, _ string) tmux.Driver { socket = s; return fake },
		TempDir: dir, PID: 4242, Now: func() time.Time { return t0 },
	}
	run, err := l.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if socket != "forge-smoke-4242" || socket == tmux.DefaultSocket {
		t.Errorf("socket = %q", socket)
	}
	lr := run.(*liveRun)
	if _, err := os.Stat(filepath.Join(lr.repo, ".git")); err != nil {
		t.Errorf("no temp repo: %v", err)
	}
	_, cmd, env, ok := fake.Started(tmux.Target{Session: "forge-smoke", Window: lr.name})
	if !ok || env["FORGE_SESSION"] != lr.name || !strings.Contains(strings.Join(cmd, " "), "--mcp-config") {
		t.Fatalf("window: ok=%v cmd=%q env=%v", ok, cmd, env)
	}

	o, err := run.Observe(ctx)
	if err != nil || o.Status != store.StatusStarting || o.Event != "" {
		t.Errorf("first look = %+v, %v", o, err)
	}
	// What `forge hook SessionStart` would do.
	x, _ := st.Sessions().GetByName(ctx, lr.name)
	if err := st.Sessions().SetStatus(ctx, x.ID, store.StatusIdle, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Events().Append(ctx, store.Event{SessionID: x.ID, ClaudeSessionID: x.ClaudeSessionID, Event: "SessionStart", CreatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if o, _ := run.Observe(ctx); o.Status != store.StatusIdle || o.Event != "SessionStart" {
		t.Errorf("after hook = %+v", o)
	}

	if lines := run.Checks(ctx); !RegistrationsOK(lines) {
		t.Errorf("registrations: %q", lines)
	}
	if _, err := run.Preview(ctx, 10); err != nil {
		t.Errorf("preview: %v", err)
	}
	if a, err := run.Attach(ctx); err != nil || a.Cmd == nil {
		t.Errorf("attach: %+v %v", a, err)
	}

	sessionDir := filepath.Join(l.DataDir, "sessions", lr.name)
	if err := run.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := run.Cleanup(ctx); err != nil {
		t.Errorf("second cleanup: %v", err)
	}
	if _, err := st.Sessions().GetByName(ctx, lr.name); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("session row left: %v", err)
	}
	for _, p := range []string{lr.dir, sessionDir} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left: %v", p, err)
		}
	}
	if ts, _ := fake.ListAll(ctx); len(ts) != 0 {
		t.Errorf("tmux windows left: %v", ts)
	}
}

// A failed start cleans up after itself.
func TestLiveSmokeStartFailureCleansUp(t *testing.T) {
	testutil.IsolateHome(t)
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	l := LiveSmoke{
		Store: st, TempDir: dir,
		Git:     func(context.Context, string, ...string) error { return errors.New("no git") },
		NewTmux: func(string, string) tmux.Driver { return tmuxtest.NewFake() },
	}
	if _, err := l.Start(ctx); err == nil || !strings.Contains(err.Error(), "no git") {
		t.Fatalf("Start = %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "forge-smoke-") {
			t.Errorf("temp dir left: %s", e.Name())
		}
	}
}
