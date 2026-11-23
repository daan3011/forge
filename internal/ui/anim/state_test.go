package anim

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/ui"
)

func TestStateBodies(t *testing.T) {
	th := ui.NewTheme(true, nil)
	off := Settings{Level: config.AnimationsOff}
	tests := []struct {
		name string
		got  func(w, h int) string
		want []string
	}{
		{"loading", func(w, h int) string { return Loading("heating up…", 0, off, th, w, h) }, []string{HammerStatic, "heating up…"}},
		{"failed", func(w, h int) string { return Failed("couldn't load runs: timeout", "r", th, w, h) }, []string{"couldn't load runs: timeout", "r to retry"}},
		{"failed without key", func(w, h int) string { return Failed("boom", "", th, w, h) }, []string{"boom"}},
		{"notice", func(w, h int) string { return Notice("Plane isn't set up", "set plane.workspace_slug", th, w, h) }, []string{"Plane isn't set up", "set plane.workspace_slug"}},
	}
	for _, tt := range tests {
		for _, size := range [][2]int{{80, 24}, {40, 10}, {30, 3}, {12, 2}} {
			w, h := size[0], size[1]
			out := tt.got(w, h)
			lines := strings.Split(out, "\n")
			if len(lines) != h {
				t.Errorf("%s %dx%d: %d lines", tt.name, w, h, len(lines))
			}
			for i, l := range lines {
				if lw := ansi.StringWidth(l); lw != w {
					t.Errorf("%s %dx%d: line %d is %d wide", tt.name, w, h, i, lw)
				}
			}
			if w >= 40 && h >= 10 {
				plain := ansi.Strip(out)
				for _, want := range tt.want {
					if !strings.Contains(plain, want) {
						t.Errorf("%s %dx%d lacks %q:\n%s", tt.name, w, h, want, plain)
					}
				}
			}
		}
		if tt.got(0, 5) != "" || tt.got(5, 0) != "" {
			t.Errorf("%s: zero size must render nothing", tt.name)
		}
	}
}

func TestTicks(t *testing.T) {
	for _, tt := range []struct {
		s    Settings
		want bool
	}{
		{Full(), true},
		{Settings{Level: config.AnimationsReduced, Color: true}, true},
		{Settings{Level: config.AnimationsReduced}, false},
		{Settings{Level: config.AnimationsOff, Color: true}, false},
	} {
		if got := Ticks(tt.s); got != tt.want {
			t.Errorf("Ticks(%+v) = %v", tt.s, got)
		}
	}
}
