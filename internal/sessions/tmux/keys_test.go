package tmux

import (
	"strings"
	"testing"
)

func TestKeyLabel(t *testing.T) {
	tests := map[string]string{"M-d": "Alt+d", "C-M-x": "Ctrl+Alt+x", "F12": "F12", "M-[": "Alt+[", "S-Enter": "Shift+Enter"}
	for in, want := range tests {
		if got := KeyLabel(in); got != want {
			t.Errorf("KeyLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDetachKey(t *testing.T) {
	tests := []struct{ conf, want string }{
		{"", DetachKey},
		{"bind -n M-q detach-client\n", "M-q"},
		{"bind-key -n C-] detach-client\n", "C-]"},
		{"bind -T root F10 detach-client\n", "F10"},
		{"# bind -n M-x detach-client\nbind -n M-d detach-client\n", "M-d"},
		{"bind -n M-d detach-client\nbind -n M-z detach-client\n", "M-z"},
		{"bind d detach-client\n", DetachKey}, // prefix table: not a direct key
	}
	for _, tt := range tests {
		if got := ParseDetachKey([]byte(tt.conf)); got != tt.want {
			t.Errorf("ParseDetachKey(%q) = %q, want %q", tt.conf, got, tt.want)
		}
	}
}

// The default config binds the shared keys and names them in its status line.
func TestDefaultConfigUsesSharedKeys(t *testing.T) {
	conf := DefaultConfig(ConfigColors{Bg: "#000000", Fg: "#ffffff", Accent: "#ff0000", Muted: "#888888"})
	if got := ParseDetachKey(conf); got != DetachKey {
		t.Errorf("default config detaches with %q", got)
	}
	s := string(conf)
	if !strings.Contains(s, CaptureSnippet) || !strings.Contains(s, DetachKey+" back") {
		t.Errorf("default config misses the shared keys:\n%s", s)
	}
}

func TestDiscardRefusesForgeSocket(t *testing.T) {
	if err := New().Discard(t.Context()); err == nil {
		t.Error("Discard on the forge socket")
	}
	if p := SocketPath("x"); !strings.HasSuffix(p, "/x") || !strings.Contains(p, "tmux-") {
		t.Errorf("SocketPath = %q", p)
	}
}
