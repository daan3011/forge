package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func paletteRows() []PaletteRow {
	return []PaletteRow{
		{Group: "Commands", Title: "Quit forge", Hint: "q"},
		{Group: "Commands", Title: "Refresh tab", Hint: "r"},
		{Group: "Projects", Title: "Switch project: alpha", Detail: "me/alpha"},
		{Group: "PRs", Title: "#1 a very long pull request title that will not fit in a narrow box", Detail: "alice"},
	}
}

func TestCommandPaletteNavigation(t *testing.T) {
	p := NewCommandPalette(NewTheme(true, nil), DefaultKeyMap())
	p.Reset()
	if p.Selected() != -1 {
		t.Fatal("no rows should select nothing")
	}
	if p.Update(keyMsg("down")) {
		t.Fatal("moving with no rows changed the query")
	}
	p.SetRows(paletteRows())
	tests := []struct {
		key  string
		want int
	}{
		{"down", 1}, {"down", 2}, {"down", 3}, {"down", 0}, {"up", 3}, {"up", 2},
	}
	for _, tt := range tests {
		if p.Update(keyMsg(tt.key)) {
			t.Fatalf("%s changed the query", tt.key)
		}
		if got := p.Selected(); got != tt.want {
			t.Fatalf("after %s selected = %d, want %d", tt.key, got, tt.want)
		}
	}
	if !p.Update(keyMsg("q")) || p.Query() != "q" {
		t.Fatalf("typing should edit the query, got %q", p.Query())
	}
	if !p.Update(keyMsg("backspace")) || p.Query() != "" {
		t.Fatal("backspace should edit the query")
	}
	p.SetRows(paletteRows()[:1])
	if p.Selected() != 0 {
		t.Fatal("SetRows should select the first row")
	}
	p.Update(keyMsg("x"))
	p.Reset()
	if p.Query() != "" || p.Selected() != -1 {
		t.Fatal("Reset should clear the query and rows")
	}
}

func TestCommandPaletteView(t *testing.T) {
	p := NewCommandPalette(NewTheme(true, nil), DefaultKeyMap())
	p.Reset()
	p.SetRows(paletteRows())
	sizes := [][2]int{{8, 5}, {30, 6}, {60, 12}, {72, 30}}
	for _, sz := range sizes {
		for range len(paletteRows()) {
			v := p.View(sz[0], sz[1])
			assertSize(t, v, sz[0], sz[1])
			plain := ansi.Strip(v)
			if !strings.Contains(plain, "›") {
				t.Fatalf("%dx%d: prompt missing:\n%s", sz[0], sz[1], plain)
			}
			p.Update(keyMsg("down"))
		}
	}
	if p.View(7, 10) != "" || p.View(40, 4) != "" {
		t.Fatal("too small should render nothing")
	}

	plain := ansi.Strip(p.View(60, 12))
	for _, want := range []string{"⚒ Commands", "Quit forge", "q", "◆ Projects", "me/alpha", "⎇ PRs", "…"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view missing %q:\n%s", want, plain)
		}
	}

	// A short box scrolls to keep the selection visible.
	p.SetRows(paletteRows())
	p.Update(keyMsg("up")) // wrap to the last row
	plain = ansi.Strip(p.View(60, 6))
	if !strings.Contains(plain, "#1 a very long") {
		t.Fatalf("selection scrolled out of view:\n%s", plain)
	}

	p.SetRows(nil)
	if plain := ansi.Strip(p.View(60, 8)); !strings.Contains(plain, EmptyText) {
		t.Fatalf("empty state missing:\n%s", plain)
	}
	p.Update(keyMsg("z"))
	if plain := ansi.Strip(p.View(60, 8)); !strings.Contains(plain, `no matches for "z"`) {
		t.Fatalf("no-match state missing:\n%s", plain)
	}
	if PaletteIcon("Whatever") != "•" {
		t.Fatal("unknown groups get a bullet")
	}
}
