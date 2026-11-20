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
	p := NewPlaceholder(testDeps(nil), PlaceholderSpec{
		Title:   "PRs",
		Feature: "P2.1",
		Blurb:   "Pull requests.",
		Columns: []ui.Column{{Title: "#", Width: 4}, {Title: "Title", Flex: true}},
		Rows:    [][]string{{"#1", "first"}, {"#2", "second"}},
	})
	fixed := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return fixed }
	drain(t, p, p.Init())
	if !p.LastUpdated().Equal(fixed) {
		t.Fatal("the first fetch should stamp the update time")
	}

	p.SetSize(80, 21)
	p.SetProject(&config.Project{Name: "forge"})
	drain(t, p, p.Refresh())
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

func TestPlaceholderFilter(t *testing.T) {
	p := NewPlaceholder(testDeps(nil), PlaceholderSpec{
		Title:   "PRs",
		Columns: []ui.Column{{Title: "#", Width: 4}, {Title: "Title", Flex: true}},
		Rows:    [][]string{{"#1", "first"}, {"#2", "second"}},
	})
	drain(t, p, p.Init())
	p.SetSize(80, 12)
	p.Update(press("/"))
	if !p.CapturingInput() {
		t.Fatal("/ should start the filter")
	}
	for _, r := range "sec" {
		p.Update(press(string(r)))
	}
	p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if p.CapturingInput() {
		t.Fatal("enter should stop typing")
	}
	view := ansi.Strip(p.View())
	if !strings.Contains(view, "/ sec") || !strings.Contains(view, "Title: second") || strings.Contains(view, "#1") {
		t.Fatalf("filter not applied:\n%s", view)
	}
	if got := len(strings.Split(p.View(), "\n")); got != 12 {
		t.Fatalf("view is %d lines with the filter line, want 12", got)
	}
	p.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if view := ansi.Strip(p.View()); strings.Contains(view, "/ sec") || !strings.Contains(view, "#1") {
		t.Fatalf("esc should clear the filter:\n%s", view)
	}
	if len(p.FullHelp()) == 0 || len(p.ShortHelp()) > 4 {
		t.Fatal("help bindings out of range")
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

func TestPlaceholderPalette(t *testing.T) {
	p := NewPlaceholder(testDeps(nil), PlaceholderSpec{
		Title:            "PRs",
		EntityKind:       "PR",
		EntityTitleCells: 2,
		Columns:          []ui.Column{{Title: "#", Width: 4}, {Title: "Title", Flex: true}, {Title: "By", Width: 6}},
		Rows:             [][]string{{"#1", "first", "ann"}, {"#2", "second", "bob"}, {}},
	})
	drain(t, p, p.Init())
	p.SetSize(80, 12)

	es := p.Entities()
	if len(es) != 2 || es[1].Kind != "PR" || es[1].ID != "#2" || es[1].Title != "#2 second" || es[1].Detail != "bob" {
		t.Fatalf("entities = %+v", es)
	}

	// Select clears a filter that hides the row, then moves to it.
	p.Update(press("/"))
	p.Update(press("f"))
	p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	p.Select("#2")
	if view := ansi.Strip(p.View()); !strings.Contains(view, "Title: second") || strings.Contains(view, "/ f") {
		t.Fatalf("select did not jump to #2:\n%s", view)
	}

	cmds := map[string]func() tea.Cmd{}
	for _, c := range p.Commands() {
		cmds[c.ID] = c.Run
		if c.Title == "" || c.Key.Help().Key == "" {
			t.Fatalf("command %s needs a title and key hint", c.ID)
		}
	}
	ratio := p.split.Ratio()
	cmds["grow_pane"]()
	if p.split.Ratio() <= ratio {
		t.Fatal("grow_pane should widen the list")
	}
	cmds["shrink_pane"]()
	cmds["filter"]()
	if !p.CapturingInput() {
		t.Fatal("filter command should start the filter")
	}

	none := NewPlaceholder(testDeps(nil), PlaceholderSpec{Rows: [][]string{{"x"}}})
	drain(t, none, none.Init())
	if none.Entities() != nil {
		t.Fatal("no EntityKind means no entities")
	}
}
