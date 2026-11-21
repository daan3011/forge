package tmux

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testColors = ConfigColors{Bg: "#1c1b1a", Fg: "#e8e2d9", Accent: "#ff7a1a", Muted: "#8a8580"}

func TestDefaultConfig(t *testing.T) {
	conf := string(DefaultConfig(testColors))
	for _, want := range []string{
		"set -sg escape-time 10",
		`set -g default-terminal "tmux-256color"`,
		`set -as terminal-features ",xterm-256color:RGB"`,
		"set -s extended-keys on",
		`set -as terminal-features ",xterm*:extkeys"`,
		"set -g mouse on",
		"set -g history-limit 50000",
		"set -g base-index 1",
		"setw -g pane-base-index 1",
		"set -g renumber-windows on",
		"set -g prefix None",
		"set -g prefix2 None",
		"unbind C-b",
		"bind -n M-d detach-client",
		"bind -n M-[ copy-mode",
		"bind -n M-w choose-tree -Zw",
		"bg=#1c1b1a,fg=#e8e2d9",
		"#[fg=#ff7a1a,bold] #W",
		"M-d back · M-[ scroll · M-w windows",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("default config lacks %q", want)
		}
	}
	if strings.Contains(conf, "{{") || strings.Contains(conf, "<no value>") {
		t.Error("unrendered template field")
	}
	// Every setting is explained by a comment line right above it.
	lines := strings.Split(conf, "\n")
	for i, l := range lines {
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if i == 0 || !strings.HasPrefix(lines[i-1], "#") {
			t.Errorf("line %d %q has no comment above it", i+1, l)
		}
	}
	if v, ok := ParseConfigVersion([]byte(conf)); !ok || v != ConfigVersion {
		t.Errorf("version header = %d, %v", v, ok)
	}
}

func TestParseConfigVersion(t *testing.T) {
	tests := []struct {
		in   string
		want int
		ok   bool
	}{
		{"# forge-tmux-config v1\nset -g mouse on\n", 1, true},
		{"# forge-tmux-config v12\n", 12, true},
		{"set -g mouse on\n# forge-tmux-config v1\n", 0, false},
		{"# forge-tmux-config vX\n", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		v, ok := ParseConfigVersion([]byte(tt.in))
		if v != tt.want || ok != tt.ok {
			t.Errorf("ParseConfigVersion(%q) = %d, %v; want %d, %v", tt.in, v, ok, tt.want, tt.ok)
		}
	}
}

func TestEnsureConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "forge", "tmux.conf")
	created, err := EnsureConfig(path, testColors)
	if err != nil || !created {
		t.Fatalf("first run = %v, %v", created, err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, DefaultConfig(testColors)) {
		t.Error("first run did not write the default config")
	}

	custom := []byte("# mine\nset -g mouse off\n")
	if err := os.WriteFile(path, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	created, err = EnsureConfig(path, testColors)
	if err != nil || created {
		t.Fatalf("existing file = %v, %v", created, err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, custom) {
		t.Errorf("existing file changed: %q", got)
	}
}

func TestResetConfig(t *testing.T) {
	now := time.Date(2026, 11, 20, 14, 5, 9, 0, time.UTC)
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux.conf")

	t.Run("no file: just writes", func(t *testing.T) {
		backup, err := ResetConfig(path, testColors, now)
		if err != nil || backup != "" {
			t.Fatalf("= %q, %v", backup, err)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, DefaultConfig(testColors)) {
			t.Error("not the default")
		}
		if m, _ := filepath.Glob(path + ".bak.*"); len(m) != 0 {
			t.Errorf("unexpected backups %v", m)
		}
	})

	t.Run("backs up the old file", func(t *testing.T) {
		old := []byte("# mine\nset -g mouse off\n")
		if err := os.WriteFile(path, old, 0o644); err != nil {
			t.Fatal(err)
		}
		backup, err := ResetConfig(path, testColors, now)
		if err != nil {
			t.Fatal(err)
		}
		if want := path + ".bak.20261120-140509"; backup != want {
			t.Errorf("backup = %q, want %q", backup, want)
		}
		if got, _ := os.ReadFile(backup); !bytes.Equal(got, old) {
			t.Errorf("backup content = %q", got)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, DefaultConfig(testColors)) {
			t.Error("not the default after reset")
		}
	})
}

func TestDefaultConfigBindsCapture(t *testing.T) {
	conf := string(DefaultConfig(ConfigColors{Bg: "#000000", Fg: "#ffffff", Accent: "#ff0000", Muted: "#888888"}))
	if !strings.Contains(conf, `bind -n M-c display-popup -E -w 76 -h 8 "forge capture"`) {
		t.Errorf("no M-c capture binding:\n%s", conf)
	}
}

func TestConfigVersionOf(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.conf")
	if err := os.WriteFile(old, []byte("# forge-tmux-config v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, ok := ConfigVersionOf(old); !ok || v != 1 {
		t.Errorf("old = %d, %v", v, ok)
	}
	if _, ok := ConfigVersionOf(filepath.Join(dir, "missing.conf")); ok {
		t.Error("missing file has a version")
	}
}
