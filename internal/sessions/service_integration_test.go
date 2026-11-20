//go:build tmux

// End-to-end: the real tmux driver (throwaway socket), real git worktrees, the
// real store and the real claude argv, with a stand-in claude script.
// Run with: go test -tags tmux ./internal/sessions/

package sessions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/store"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestServiceEndToEnd(t *testing.T) {
	for _, bin := range []string{"tmux", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " not on PATH")
		}
	}
	ctx := context.Background()
	dir := t.TempDir()

	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init", "-q", "-b", "main")
	gitRun(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	// Stand-in claude: prints its working dir, env and args, then waits.
	fake := filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		`printf 'PWD=%s\n' "$PWD"; printf 'FORGE_SESSION=%s\n' "$FORGE_SESSION"` + "\n" +
		`for a in "$@"; do printf 'ARG<%s>\n' "$a"; done` + "\n" +
		"exec sleep 600\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	socket := fmt.Sprintf("forge-e2e-%d", os.Getpid())
	drv := tmux.New(tmux.WithSocket(socket), tmux.WithConfig(os.DevNull), tmux.WithGetenv(func(string) string { return "" }))
	t.Cleanup(func() { _ = drv.KillServer(context.Background()) })

	st, err := store.Open(ctx, filepath.Join(dir, "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	proj := config.Project{Name: "demo", Repo: "daan/demo", Path: repo}
	svc := New(Config{
		Tmux:      drv,
		Worktrees: worktree.New(filepath.Join(dir, "wt"), worktree.NewExec()),
		Store:     StoreFrom(st),
		Projects:  []config.Project{proj},
		DataDir:   filepath.Join(dir, "data"),
		ClaudeBin: fake,
	})

	prompt := "Fix \"the\" 'build'\n$HOME ${USER} `whoami` $(id) ; | &"
	res, err := svc.Start(ctx, StartOpts{Name: "e2e", Project: "demo", Prompt: prompt})
	if err != nil {
		t.Fatal(err)
	}
	x := res.Session

	var out string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if out, err = svc.Preview(ctx, x, 50); err == nil && strings.Contains(out, "ARG<--session-id>") && strings.Count(out, "ARG<") >= 4 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	wantPath, _ := filepath.EvalSymlinks(x.Worktree)
	for _, want := range []string{
		"FORGE_SESSION=e2e",
		"ARG<--session-id>", "ARG<" + x.ClaudeSessionID + ">", "ARG<-->",
		"ARG<Fix \"the\" 'build'", "$HOME ${USER} `whoami` $(id) ; | &>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("pane missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "PWD="+x.Worktree) && !strings.Contains(out, "PWD="+wantPath) {
		t.Errorf("claude not started in the worktree %s:\n%s", x.Worktree, out)
	}

	rows, err := svc.List(ctx, "demo", false)
	if err != nil || len(rows) != 1 || rows[0].Status != store.StatusStarting {
		t.Fatalf("List = %+v, %v", rows, err)
	}

	// Kill + remove: worktree gone, branch kept, archived.
	if _, err := svc.Kill(ctx, x, KillOpts{RemoveWorktree: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(x.Worktree); !os.IsNotExist(err) {
		t.Errorf("worktree still there: %v", err)
	}
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "refs/heads/agent/e2e")
	cmd.Dir = repo
	if err := cmd.Run(); err != nil {
		t.Errorf("branch deleted: %v", err)
	}
	archived, _ := svc.List(ctx, "demo", true)
	if len(archived) != 1 || archived[0].Status != store.StatusArchived {
		t.Errorf("archived = %+v", archived)
	}
}

// TestResumeAfterRebootEndToEnd is P1.5's "done when", short of a real
// reboot: the tmux server goes away, the session reads dead, and Resume
// brings back `claude --resume <uuid>` in the same worktree. Then the
// worktree goes too and is recreated from the branch.
func TestResumeAfterRebootEndToEnd(t *testing.T) {
	for _, bin := range []string{"tmux", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " not on PATH")
		}
	}
	ctx := context.Background()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init", "-q", "-b", "main")
	gitRun(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	fake := filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		`printf 'PWD=%s\n' "$PWD"` + "\n" +
		`for a in "$@"; do printf 'ARG<%s>\n' "$a"; done` + "\n" +
		"exec sleep 600\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("forge-e2e-resume-%d", os.Getpid())
	drv := tmux.New(tmux.WithSocket(socket), tmux.WithConfig(os.DevNull), tmux.WithGetenv(func(string) string { return "" }))
	t.Cleanup(func() { _ = drv.KillServer(context.Background()) })
	st, err := store.Open(ctx, filepath.Join(dir, "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	projects := filepath.Join(dir, "claude-projects")
	svc := New(Config{
		Tmux:      drv,
		Worktrees: worktree.New(filepath.Join(dir, "wt"), worktree.NewExec()),
		Store:     StoreFrom(st),
		Projects:  []config.Project{{Name: "demo", Repo: "daan/demo", Path: repo}},
		DataDir:   filepath.Join(dir, "data"),
		ClaudeBin: fake, ProjectsDir: projects, StartupCheck: 200 * time.Millisecond,
	})
	res, err := svc.Start(ctx, StartOpts{Name: "e2e", Project: "demo", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	x := res.Session
	// Claude Code wrote a transcript for the conversation.
	tdir := filepath.Join(projects, "-wt-demo-e2e")
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tdir, x.ClaudeSessionID+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	waitPane := func(want ...string) string {
		t.Helper()
		var out string
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			cur, _ := st.Sessions().GetByName(ctx, "e2e")
			out, _ = svc.Preview(ctx, cur, 50)
			ok := true
			for _, w := range want {
				ok = ok && strings.Contains(out, w)
			}
			if ok {
				return out
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("pane missing %q:\n%s", want, out)
		return ""
	}
	resumeAndCheck := func(o ResumeOpts) {
		t.Helper()
		rows, err := svc.List(ctx, "demo", false)
		if err != nil || len(rows) != 1 || rows[0].Status != store.StatusDead {
			t.Fatalf("after reboot: rows = %+v, %v", rows, err)
		}
		p, err := svc.Inspect(ctx, rows[0].Session)
		if err != nil || p.Alive || !p.HasTranscript {
			t.Fatalf("plan = %+v, %v", p, err)
		}
		r, err := svc.Resume(ctx, rows[0].Session, o)
		if err != nil {
			t.Fatal(err)
		}
		if r.Fresh || r.Session.ClaudeSessionID != x.ClaudeSessionID {
			t.Errorf("resumed = %+v", r)
		}
		out := waitPane("ARG<--resume>", "ARG<"+x.ClaudeSessionID+">")
		real, _ := filepath.EvalSymlinks(x.Worktree)
		if !strings.Contains(out, "PWD="+x.Worktree) && !strings.Contains(out, "PWD="+real) {
			t.Errorf("not resumed in the worktree:\n%s", out)
		}
		if a, err := svc.Attach(ctx, r.Session); err != nil || a.Mode != tmux.AttachExec {
			t.Errorf("attach = %+v, %v", a, err)
		}
	}

	waitPane("ARG<--session-id>")
	if err := drv.KillServer(ctx); err != nil { // the "reboot"
		t.Fatal(err)
	}
	resumeAndCheck(ResumeOpts{})

	// The worktree vanishes too: Resume asks, then recreates it from the branch.
	if err := drv.KillServer(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(x.Worktree); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Resume(ctx, x, ResumeOpts{}); !errors.Is(err, ErrWorktreeMissing) {
		t.Fatalf("err = %v, want ErrWorktreeMissing", err)
	}
	resumeAndCheck(ResumeOpts{RecreateWorktree: true})
}
