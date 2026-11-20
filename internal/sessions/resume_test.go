package sessions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/sessions/claude"
	"github.com/daan3011/forge/internal/sessions/hooks"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/store"
)

const newID = "11111111-2222-4333-8444-555555555555"

// transcript gives session id a transcript in the fake claude projects dir.
func (e *env) transcript(t *testing.T, id string) {
	t.Helper()
	dir := filepath.Join(e.claude, "-some-worktree")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// dead starts a session and makes its agent exit, as after a reboot the
// list sweep would.
func (e *env) dead(t *testing.T, name string) store.Session {
	t.Helper()
	x := e.start(t, name)
	e.tmux.Exit(tmux.Target{Session: x.TmuxSession, Window: x.TmuxWindow}, 0)
	if err := e.st.Sessions().SetStatus(context.Background(), x.ID, store.StatusDead, t0); err != nil {
		t.Fatal(err)
	}
	x.Status = store.StatusDead
	return x
}

func argvOf(t *testing.T, e *env, name string) []string {
	t.Helper()
	_, argv, _, ok := e.tmux.Started(tmux.Target{Session: "forge", Window: name})
	if !ok {
		t.Fatalf("no window for %s", name)
	}
	return argv
}

func TestInspect(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	x := e.start(t, "fix-ci")

	p, err := e.svc.Inspect(ctx, x)
	if err != nil {
		t.Fatal(err)
	}
	wantPrompt := filepath.Join(e.data, "sessions", "fix-ci", "prompt.md")
	if !p.Alive || p.WorktreeMissing || p.HasTranscript || p.PromptFile != wantPrompt {
		t.Errorf("live plan = %+v", p)
	}

	e.tmux.Exit(tmux.Target{Session: "forge", Window: "fix-ci"}, 0)
	e.transcript(t, testID)
	if err := os.RemoveAll(x.Worktree); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(e.svc.SessionDir("fix-ci")); err != nil {
		t.Fatal(err)
	}
	p, err = e.svc.Inspect(ctx, x)
	if err != nil {
		t.Fatal(err)
	}
	if p.Alive || !p.WorktreeMissing || !p.HasTranscript || p.PromptFile != "" {
		t.Errorf("dead plan = %+v", p)
	}

	x.Status = store.StatusArchived
	if _, err := e.svc.Inspect(ctx, x); !errors.Is(err, ErrArchived) {
		t.Errorf("archived: err = %v", err)
	}
}

func TestResumeAliveDoesNothing(t *testing.T) {
	e := newEnv(t)
	x := e.start(t, "fix-ci")
	before := argvOf(t, e, "fix-ci")
	res, err := e.svc.Resume(context.Background(), x, ResumeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.AlreadyRunning || !slices.Equal(argvOf(t, e, "fix-ci"), before) {
		t.Errorf("res = %+v", res)
	}
}

func TestResumeWithTranscript(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	x := e.dead(t, "fix-ci")
	e.transcript(t, testID)

	res, err := e.svc.Resume(ctx, x, ResumeOpts{ResendPrompt: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.AlreadyRunning || res.Fresh || res.NewID || res.WorktreeRecreated {
		t.Errorf("res = %+v", res)
	}
	want, _ := claude.Launch{Bin: "claude", SessionID: testID, Resume: true}.Argv()
	if got := argvOf(t, e, "fix-ci"); !slices.Equal(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
	dir, _, envv, _ := e.tmux.Started(tmux.Target{Session: "forge", Window: "fix-ci"})
	if dir != x.Worktree || envv[EnvSessionName] != "fix-ci" {
		t.Errorf("window dir = %q env = %v", dir, envv)
	}
	alive, _ := e.tmux.WindowAlive(ctx, tmux.Target{Session: "forge", Window: "fix-ci"})
	if !alive {
		t.Error("window not alive after resume")
	}
	if res.Session.Status != store.StatusStarting || res.Session.ClaudeSessionID != testID {
		t.Errorf("session = %+v", res.Session)
	}
}

func TestResumeAfterServerRestart(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	x := e.dead(t, "fix-ci")
	e.transcript(t, testID)
	if err := e.tmux.KillServer(ctx); err != nil {
		t.Fatal(err)
	}
	if p, err := e.svc.Inspect(ctx, x); err != nil || p.Alive {
		t.Fatalf("plan = %+v, %v", p, err)
	}
	if _, err := e.svc.Resume(ctx, x, ResumeOpts{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(argvOf(t, e, "fix-ci")[2], "--resume "+testID) {
		t.Errorf("argv = %q", argvOf(t, e, "fix-ci"))
	}
}

func TestResumeMovedWindowUpdatesRow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	x := e.dead(t, "fix-ci")
	// The row points at a window that no longer exists elsewhere.
	if err := e.st.Sessions().SetWindow(ctx, x.ID, "old", "gone"); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Resume(ctx, x, ResumeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Session.TmuxSession != "forge" || res.Session.TmuxWindow != "fix-ci" {
		t.Errorf("target = %s:%s", res.Session.TmuxSession, res.Session.TmuxWindow)
	}
}

func TestResumeWithoutTranscript(t *testing.T) {
	tests := []struct {
		name       string
		resend     bool
		dropPrompt bool
		wantPrompt bool
	}{
		{name: "resend prompt", resend: true, wantPrompt: true},
		{name: "don't resend", resend: false},
		{name: "prompt file gone", resend: true, dropPrompt: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			x := e.dead(t, "fix-ci")
			if tt.dropPrompt {
				_ = os.RemoveAll(e.svc.SessionDir("fix-ci"))
			}
			res, err := e.svc.Resume(context.Background(), x, ResumeOpts{ResendPrompt: tt.resend})
			if err != nil {
				t.Fatal(err)
			}
			if !res.Fresh || res.NewID {
				t.Errorf("res = %+v", res)
			}
			l := claude.Launch{Bin: "claude", SessionID: testID}
			if tt.wantPrompt {
				l.PromptFile = filepath.Join(e.data, "sessions", "fix-ci", "prompt.md")
			}
			want, _ := l.Argv()
			if got := argvOf(t, e, "fix-ci"); !slices.Equal(got, want) {
				t.Errorf("argv = %q, want %q", got, want)
			}
		})
	}
}

func TestResumeIDInUseGetsNewID(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	x := e.dead(t, "fix-ci")
	// Link the session, to check the link survives the id change.
	if err := e.st.Links().Add(ctx, store.SessionRef(x.ID), store.Ref{Kind: store.KindGitHubPR, ID: "daan/forge#12"}, store.LinkMeta{}); err != nil {
		t.Fatal(err)
	}
	e.svc.c.NewID = func() (string, error) { return newID, nil }
	e.tmux.OnStart = func(cmd []string) (string, bool) {
		if strings.Contains(strings.Join(cmd, " "), "--session-id "+testID) {
			return "Error: Session ID " + testID + " is already in use.\n", true
		}
		return "", false
	}
	res, err := e.svc.Resume(ctx, x, ResumeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Fresh || !res.NewID || res.Session.ClaudeSessionID != newID {
		t.Errorf("res = %+v", res)
	}
	want, _ := claude.Launch{Bin: "claude", SessionID: newID}.Argv()
	if got := argvOf(t, e, "fix-ci"); !slices.Equal(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
	rows, err := e.svc.List(ctx, "", false)
	if err != nil || len(rows) != 1 || rows[0].Link != "#12" {
		t.Errorf("rows = %+v, %v", rows, err)
	}
}

func TestResumeMissingWorktree(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	x := e.dead(t, "fix-ci")
	e.transcript(t, testID)
	if err := os.RemoveAll(x.Worktree); err != nil {
		t.Fatal(err)
	}

	_, err := e.svc.Resume(ctx, x, ResumeOpts{})
	var missing *WorktreeMissingError
	if !errors.As(err, &missing) || !errors.Is(err, ErrWorktreeMissing) || missing.Branch != "agent/fix-ci" {
		t.Fatalf("err = %v", err)
	}
	if len(e.wt.created) != 1 {
		t.Fatalf("worktree created without asking: %+v", e.wt.created)
	}

	res, err := e.svc.Resume(ctx, x, ResumeOpts{RecreateWorktree: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.WorktreeRecreated || res.BranchRecreated {
		t.Errorf("res = %+v", res)
	}
	o := e.wt.created[len(e.wt.created)-1]
	if !o.ReuseBranch || o.Session != "fix-ci" || o.Base != "main" {
		t.Errorf("create opts = %+v", o)
	}
	if !isDir(x.Worktree) {
		t.Error("worktree not recreated")
	}
}

func TestResumeMissingWorktreeAndBranch(t *testing.T) {
	e := newEnv(t)
	x := e.dead(t, "fix-ci")
	_ = os.RemoveAll(x.Worktree)
	delete(e.wt.branches, "agent/fix-ci")
	res, err := e.svc.Resume(context.Background(), x, ResumeOpts{RecreateWorktree: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.WorktreeRecreated || !res.BranchRecreated {
		t.Errorf("res = %+v", res)
	}
}

func TestResumeInstallsHooks(t *testing.T) {
	e := newEnv(t)
	x := e.dead(t, "fix-ci")
	e.svc.c.ForgeBin = "/opt/bin/forge"
	if _, err := e.svc.Resume(context.Background(), x, ResumeOpts{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(x.Worktree, hooks.SettingsRel))
	if err != nil || !strings.Contains(string(b), "/opt/bin/forge") {
		t.Errorf("settings = %s, %v", b, err)
	}
	if !slices.Contains(e.wt.excluded, filepath.Join(x.Worktree, hooks.SettingsRel)) {
		t.Errorf("excluded = %v", e.wt.excluded)
	}
}

func TestResumeArchived(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	x := e.start(t, "fix-ci")
	if _, err := e.svc.Kill(ctx, x, KillOpts{RemoveWorktree: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Resume(ctx, x, ResumeOpts{RecreateWorktree: true}); !errors.Is(err, ErrArchived) {
		t.Errorf("err = %v", err)
	}
}

// failingStatus makes SetStatus fail after the window is open.
type failingStatus struct{ Store }

func (failingStatus) SetStatus(context.Context, int64, store.Status, time.Time) error {
	return errors.New("disk full")
}

func TestResumeFailureClosesWindow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	x := e.dead(t, "fix-ci")
	e.svc.c.Store = failingStatus{e.svc.c.Store}
	if _, err := e.svc.Resume(ctx, x, ResumeOpts{}); err == nil {
		t.Fatal("want error")
	}
	if _, _, _, ok := e.tmux.Started(tmux.Target{Session: "forge", Window: "fix-ci"}); ok {
		t.Error("window left open after a failed resume")
	}
}

func TestAttach(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	x := e.start(t, "fix-ci")
	a, err := e.svc.Attach(ctx, x)
	if err != nil || a.Mode != tmux.AttachExec || a.Cmd == nil {
		t.Fatalf("attach = %+v, %v", a, err)
	}
	if got := e.tmux.Attached(); len(got) != 1 || got[0] != (tmux.Target{Session: "forge", Window: "fix-ci"}) {
		t.Errorf("attached = %v", got)
	}
}
