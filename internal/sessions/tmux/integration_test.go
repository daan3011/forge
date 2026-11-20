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
	a, err := e.Attach(ctx, tg)
	if err != nil || a.Mode != tmux.AttachExec || a.Cmd == nil {
		t.Fatalf("Attach = %+v, %v", a, err)
	}
	args := strings.Join(a.Cmd.Args, " ")
	if !strings.HasPrefix(args, "tmux -L "+e.Socket()+" -f "+os.DevNull+" ") ||
		!strings.HasSuffix(args, " select-window -t =integration:=bash ; attach-session -t =integration:") {
		t.Errorf("attach args = %q", a.Cmd.Args)
	}

	if err := e.KillWindow(ctx, tg); err != nil {
		t.Fatal(err)
	}
	if ws, _ := e.ListWindows(ctx, s); len(ws) != 0 {
		t.Errorf("after kill = %+v", ws)
	}
}

// TestExecPasteTextArrivesIntact pastes a multi-line prompt full of shell
// metacharacters into a window reading two lines into a file, and checks the
// bytes that arrive.
func TestExecPasteTextArrivesIntact(t *testing.T) {
	ctx := context.Background()
	e := newServer(t)
	s, err := e.EnsureSession(ctx, "paste")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "got.txt")
	tg, err := e.NewWindow(ctx, s, "reader", "", []string{"sh", "-c", `head -n 2 > "$OUT"`},
		map[string]string{"OUT": out})
	if err != nil {
		t.Fatal(err)
	}
	text := "first 'line' \"quoted\"\nsecond $HOME ${X} `whoami` ; | &"
	if err := e.PasteText(ctx, tg, text); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "reader to exit", func() (bool, string) {
		alive, err := e.WindowAlive(ctx, tg)
		return err == nil && !alive, fmt.Sprint(alive, err)
	})
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != text+"\n" {
		t.Errorf("got %q, want %q", got, text+"\n")
	}
}

// TestServerIgnoresUserConfig starts forge's server with the default config
// while HOME holds a ~/.tmux.conf (and an XDG one) setting a marker, and
// checks the server only ever read forge's file.
func TestServerIgnoresUserConfig(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH")
	}
	ctx := context.Background()
	home := t.TempDir()
	marker := "set -g @forge_marker leaked\nset -g prefix C-a\n"
	for _, p := range []string{filepath.Join(home, ".tmux.conf"), filepath.Join(home, ".config", "tmux", "tmux.conf")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(marker), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	conf := filepath.Join(home, ".config", "forge", "tmux.conf")
	if _, err := tmux.EnsureConfig(conf, tmux.ConfigColors{Bg: "#1c1b1a", Fg: "#e8e2d9", Accent: "#ff7a1a", Muted: "#8a8580"}); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("forge-test-cfg-%d", os.Getpid())
	e := tmux.New(tmux.WithSocket(socket), tmux.WithConfig(conf), tmux.WithGetenv(func(string) string { return "" }))
	t.Cleanup(func() {
		_ = e.KillServer(context.Background())
		_ = os.Remove(socketPath(socket))
	})
	if _, err := e.EnsureSession(ctx, "cfg"); err != nil {
		t.Fatal(err)
	}

	query := func(args ...string) (string, error) {
		out, err := exec.Command("tmux", append([]string{"-L", socket}, args...)...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if out, err := query("show-options", "-gv", "@forge_marker"); err == nil && out != "" {
		t.Errorf("~/.tmux.conf leaked into forge's server: @forge_marker = %q", out)
	}
	if out, err := query("show-options", "-gv", "prefix"); err != nil || out != "None" {
		t.Errorf("prefix = %q, %v; want None", out, err)
	}
	if out, err := query("show-options", "-gv", "status-right"); err != nil || !strings.Contains(out, "M-d back") {
		t.Errorf("status-right = %q, %v", out, err)
	}
	if out, err := query("display-message", "-p", "#{config_files}"); err != nil || out != conf {
		t.Errorf("config_files = %q, %v; want %q", out, err, conf)
	}
	if out, err := query("list-keys", "-T", "root", "M-d"); err != nil || !strings.Contains(out, "detach-client") {
		t.Errorf("M-d binding = %q, %v", out, err)
	}
}
