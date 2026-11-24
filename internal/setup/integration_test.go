//go:build tmux

// Integration tests against a real tmux server on a throwaway socket.
// Run with: go test -tags tmux ./internal/setup/...

package setup

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
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/testutil"
)

func needTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH")
	}
}

func TestPopupTestOnRealTmux(t *testing.T) {
	needTmux(t)
	ctx := context.Background()
	conf := filepath.Join(t.TempDir(), "tmux.conf")
	if _, err := tmux.EnsureConfig(conf, tmux.ConfigColors{Bg: "#000000", Fg: "#ffffff", Accent: "#ff7a1a", Muted: "#888888"}); err != nil {
		t.Fatal(err)
	}
	d := tmux.New(tmux.WithSocket(PopupSocket(os.Getpid())), tmux.WithConfig(conf), tmux.WithGetenv(func(string) string { return "" }))
	defer func() { _ = d.KillServer(ctx) }()
	a, err := StartPopupTest(ctx, d, tmux.DetachKeyOf(conf))
	if err != nil {
		t.Fatal(err)
	}
	if a.Mode != tmux.AttachExec || !strings.Contains(strings.Join(a.Cmd.Args, " "), "forge-setup-") {
		t.Errorf("attach = %v %q", a.Mode, a.Cmd.Args)
	}
	files, running, err := d.ServerConfigFiles(ctx)
	if err != nil || !running || len(files) != 1 {
		t.Errorf("server config = %v %v %v", files, running, err)
	}
	if err := StopPopupTest(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, running, _ := d.ServerConfigFiles(ctx); running {
		t.Error("throwaway server still running")
	}
	if _, err := os.Stat(tmux.SocketPath(d.Socket())); !os.IsNotExist(err) {
		t.Errorf("socket file left: %v", err)
	}
}

// The smoke test's session on a real throwaway tmux server, with a stand-in
// claude that records its environment: a temp-dir XDG_DATA_HOME in forge's
// environment never reaches it, and cleanup removes the server.
func TestLiveSmokeOnRealTmux(t *testing.T) {
	needTmux(t)
	testutil.IsolateHome(t) // XDG_* now point into a temp dir
	ctx := context.Background()
	dir := t.TempDir()
	envOut := filepath.Join(dir, "claude.env")
	bin := filepath.Join(dir, "claude")
	script := fmt.Sprintf("#!/bin/sh\nenv > %q\nsleep 30\n", envOut)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(dir, "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var drv *tmux.Exec
	l := LiveSmoke{
		Store: st, ClaudeBin: bin, TmuxConf: os.DevNull, DataDir: filepath.Join(dir, "data"), TempDir: dir,
		NewTmux: func(socket, conf string) tmux.Driver {
			drv = tmux.New(tmux.WithSocket(socket), tmux.WithConfig(conf), tmux.WithGetenv(func(string) string { return "" }))
			return drv
		},
	}
	run, err := l.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Cleanup(ctx) }()
	if !strings.HasPrefix(drv.Socket(), "forge-smoke-") {
		t.Errorf("socket = %q", drv.Socket())
	}
	var env []byte
	for range 50 {
		if env, err = os.ReadFile(envOut); err == nil && len(env) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(env) == 0 {
		t.Fatal("the stand-in claude never ran")
	}
	for _, line := range strings.Split(string(env), "\n") {
		if strings.HasPrefix(line, "XDG_") {
			t.Errorf("claude got %s", line)
		}
	}
	if !strings.Contains(string(env), "FORGE_SESSION=setup-smoke-") {
		t.Errorf("no FORGE_SESSION:\n%s", env)
	}
	if err := run.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, running, _ := drv.ServerConfigFiles(ctx); running {
		t.Error("smoke tmux server still running")
	}
	if _, err := os.Stat(tmux.SocketPath(drv.Socket())); !os.IsNotExist(err) {
		t.Errorf("socket file left: %v", err)
	}
}
