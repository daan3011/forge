package tabkit

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/ui"
)

func press(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: rune(s[0]), Text: s} }

func TestPlaceholder(t *testing.T) {
	p := NewPlaceholder(ui.NewTheme(true, nil), PlaceholderSpec{
		Title:   "PRs",
		Feature: "P2.1",
		Blurb:   "Pull requests.",
		Columns: []ui.Column{{Title: "#", Width: 4}, {Title: "Title", Flex: true}},
		Rows:    [][]string{{"#1", "first"}, {"#2", "second"}},
	})
	fixed := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return fixed }
	p.Init()
	if !p.LastUpdated().Equal(fixed) {
		t.Fatal("Init should stamp the update time")
	}

	p.SetSize(80, 21)
	p.SetProject(&config.Project{Name: "forge"})
	p.Update(press("j"))
	view := ansi.Strip(p.View())
	for _, want := range []string{"Title: second", "project: forge", "coming in P2.1"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}

	before := p.split.Ratio()
	p.Update(press(">"))
	if p.split.Ratio() <= before {
		t.Fatal("> should widen the list")
	}
	p.Update(press("<"))
	p.Update(press("<"))
	if p.split.Ratio() >= before {
		t.Fatal("< should narrow the list")
	}
}

func TestWrap(t *testing.T) {
	tests := []struct {
		in    string
		width int
		want  string
	}{
		{"one two three", 7, "one two\nthree"},
		{"short", 20, "short"},
		{"", 5, ""},
		{"a b", 0, "a b"},
	}
	for _, tt := range tests {
		if got := wrap(tt.in, tt.width); got != tt.want {
			t.Fatalf("wrap(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
		}
	}
}
