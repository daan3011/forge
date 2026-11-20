package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/tmux/tmuxtest"
)

// tmuxEnv is a config dir plus a fake tmux server, optionally running one
// agent window.
type tmuxEnv struct {
	d     deps
	fake  *tmuxtest.Fake
	conf  string
	asked []string
}

func newTmuxEnv(t *testing.T, running bool, answer bool) *tmuxEnv {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	e := &tmuxEnv{fake: tmuxtest.NewFake(), conf: filepath.Join(dir, "forge", "tmux.conf")}
	if running {
		s, _ := e.fake.EnsureSession(context.Background(), "forge")
		if _, err := e.fake.NewWindow(context.Background(), s, "alpha", "", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	e.d = deps{
		opener:  &fakeOpener{},
		newTmux: func(string) tmux.Driver { return e.fake },
		ask: func(title, _ string) (bool, error) {
			e.asked = append(e.asked, title)
			return answer, nil
		},
		now: func() time.Time { return time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC) },
	}
	return e
}

func (e *tmuxEnv) windows(t *testing.T) int {
	t.Helper()
	ws, err := e.fake.ListAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(ws)
}

func TestTmuxRestart(t *testing.T) {
	tests := []struct {
		name      string
		running   bool
		answer    bool
		args      []string
		wantAsk   bool
		wantAlive int
		wantErr   string
	}{
		{name: "no agents: no question", args: []string{"tmux", "restart"}, wantAlive: 0},
		{name: "confirmed", running: true, answer: true, args: []string{"tmux", "restart"}, wantAsk: true, wantAlive: 0},
		{name: "declined", running: true, answer: false, args: []string{"tmux", "restart"}, wantAsk: true, wantAlive: 1},
		{name: "--yes", running: true, args: []string{"tmux", "restart", "--yes"}, wantAlive: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTmuxEnv(t, tt.running, tt.answer)
			_, stderr, err := run(t, e.d, tt.args...)
			if err != nil {
				t.Fatal(err)
			}
			if (len(e.asked) > 0) != tt.wantAsk {
				t.Errorf("asked = %q", e.asked)
			}
			if got := e.windows(t); got != tt.wantAlive {
				t.Errorf("windows left = %d, want %d", got, tt.wantAlive)
			}
			if tt.running && !strings.Contains(stderr, "alpha") {
				t.Errorf("running agents not listed:\n%s", stderr)
			}
			if _, err := os.Stat(e.conf); err != nil {
				t.Errorf("config not written on first run: %v", err)
			}
		})
	}
}

func TestTmuxRestartNeedsTerminalOrYes(t *testing.T) {
	e := newTmuxEnv(t, true, false)
	e.d.ask = nil // and no runForm: no terminal
	if _, _, err := run(t, e.d, "tmux", "restart"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("err = %v", err)
	}
	if e.windows(t) != 1 {
		t.Error("killed without confirmation")
	}
}

func TestTmuxConfig(t *testing.T) {
	t.Run("opens the file, then offers a restart", func(t *testing.T) {
		e := newTmuxEnv(t, true, true)
		op := &fakeOpener{}
		e.d.opener = op
		if _, _, err := run(t, e.d, "tmux", "config"); err != nil {
			t.Fatal(err)
		}
		if op.opened != e.conf {
			t.Errorf("opened %q, want %q", op.opened, e.conf)
		}
		if len(e.asked) != 1 || !strings.Contains(e.asked[0], "apply the new config") {
			t.Errorf("asked = %q", e.asked)
		}
		if e.windows(t) != 0 {
			t.Error("not restarted")
		}
	})

	t.Run("declining keeps the server", func(t *testing.T) {
		e := newTmuxEnv(t, true, false)
		_, stderr, err := run(t, e.d, "tmux", "config")
		if err != nil {
			t.Fatal(err)
		}
		if e.windows(t) != 1 || !strings.Contains(stderr, "applies after `forge tmux restart`") {
			t.Errorf("windows = %d, stderr = %q", e.windows(t), stderr)
		}
	})

	t.Run("existing file is left alone", func(t *testing.T) {
		e := newTmuxEnv(t, false, false)
		if err := os.MkdirAll(filepath.Dir(e.conf), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(e.conf, []byte("# mine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := run(t, e.d, "tmux", "config"); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(e.conf); string(got) != "# mine\n" {
			t.Errorf("config overwritten: %q", got)
		}
	})

	t.Run("--reset backs up and writes the default", func(t *testing.T) {
		e := newTmuxEnv(t, false, false)
		if err := os.MkdirAll(filepath.Dir(e.conf), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(e.conf, []byte("# mine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		op := &fakeOpener{}
		e.d.opener = op
		_, stderr, err := run(t, e.d, "tmux", "config", "--reset")
		if err != nil {
			t.Fatal(err)
		}
		if op.opened != "" {
			t.Error("--reset should not open the editor")
		}
		backup := e.conf + ".bak.20261120-093000"
		if got, _ := os.ReadFile(backup); string(got) != "# mine\n" {
			t.Errorf("backup = %q", got)
		}
		if got, _ := os.ReadFile(e.conf); !bytes.Equal(got, tmux.DefaultConfig(tmuxColors())) {
			t.Error("not reset to the default")
		}
		if !strings.Contains(stderr, backup) {
			t.Errorf("stderr = %q", stderr)
		}
	})
}
