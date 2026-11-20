//go:build tmux

// Integration tests against a real tmux server on a throwaway socket.
// Run with: go test -tags tmux ./internal/sessions/tmux/...

package tmux_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/tmux/tmuxtest"
)

func newServer(t *testing.T) *tmux.Exec {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH")
	}
	socket := fmt.Sprintf("forge-test-%d", os.Getpid())
	e := tmux.New(
		tmux.WithSocket(socket),
		tmux.WithConfig(os.DevNull),
		tmux.WithGetenv(func(string) string { return "" }),
	)
	t.Cleanup(func() {
		if err := e.KillServer(context.Background()); err != nil {
			t.Errorf("kill-server: %v", err)
		}
		// kill-server leaves the socket file behind.
		_ = os.Remove(socketPath(socket))
	})
	return e
}

// socketPath is where tmux puts a -L socket: $TMUX_TMPDIR (or /tmp)/tmux-UID/name.
func socketPath(name string) string {
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), name)
}

func TestExecConformance(t *testing.T) {
	tmuxtest.RunConformance(t, newServer(t), []string{"sleep", "600"})
}

func waitFor(t *testing.T, what string, cond func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		var ok bool
		if ok, last = cond(); ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; last state:\n%s", what, last)
}

func TestExecBashWindow(t *testing.T) {
	ctx := context.Background()
	e := newServer(t)
	s, err := e.EnsureSession(ctx, "integration")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	tg, err := e.NewWindow(ctx, s, "bash", dir, []string{"bash", "--norc", "--noprofile"},
		map[string]string{"FORGE_MARK": "ember", "PS1": "$ "})
	if err != nil {
		t.Fatal(err)
	}

	// Literal text with tmux and shell metacharacters survives; env and dir
	// were applied.
	if err := e.SendKeys(ctx, tg, `echo "forged-$FORGE_MARK; $(basename "$PWD")"`, true); err != nil {
		t.Fatal(err)
	}
	want := "forged-ember; " + filepath.Base(dir)
	waitFor(t, "echo output", func() (bool, string) {
		out, err := e.CapturePane(ctx, tg, 20)
		if err != nil {
			return false, err.Error()
		}
		for line := range strings.SplitSeq(out, "\n") {
			if strings.TrimSpace(line) == want {
				return true, out
			}
		}
		return false, out
	})

	ws, err := e.ListWindows(ctx, s)
	if err != nil || len(ws) != 1 || ws[0].Name != "bash" {
		t.Fatalf("ListWindows = %+v, %v", ws, err)
	}

	// Exit: remain-on-exit keeps the window, reported dead.
	if err := e.SendKeys(ctx, tg, "exit 3", true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "window to die", func() (bool, string) {
		alive, err := e.WindowAlive(ctx, tg)
		return err == nil && !alive, fmt.Sprintf("alive=%v err=%v", alive, err)
	})
	ws, _ = e.ListWindows(ctx, s)
	if len(ws) != 1 || !ws[0].Dead || ws[0].ExitStatus != 3 {
		t.Errorf("dead window = %+v", ws)
	}
	if out, err := e.CapturePane(ctx, tg, 20); err != nil || !strings.Contains(out, want) {
		t.Errorf("dead pane capture = %q, %v; want output kept", out, err)
	}

	// Attach from a plain terminal: a command for tea.ExecProcess.
	cmd, err := e.Attach(ctx, tg)
	if err != nil || cmd == nil {
		t.Fatalf("Attach = %v, %v", cmd, err)
	}
	args := strings.Join(cmd.Args, " ")
	if !strings.HasPrefix(args, "tmux -L "+e.Socket()+" ") ||
		!strings.HasSuffix(args, " select-window -t =integration:=bash ; attach-session -t =integration:") {
		t.Errorf("attach args = %q", cmd.Args)
	}

	if err := e.KillWindow(ctx, tg); err != nil {
		t.Fatal(err)
	}
	if ws, _ := e.ListWindows(ctx, s); len(ws) != 0 {
		t.Errorf("after kill = %+v", ws)
	}
}
