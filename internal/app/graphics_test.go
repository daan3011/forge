package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/termcaps"
)

// graphicsTab is a fakeTab that records what the root model tells
// graphics tabs (P4.7).
type graphicsTab struct {
	fakeTab
	caps     []termcaps.Caps
	occluded []bool
	resized  int
}

func (g *graphicsTab) SetCaps(c termcaps.Caps) tea.Cmd {
	g.caps = append(g.caps, c)
	return nil
}

func (g *graphicsTab) SetOccluded(covered bool) tea.Cmd {
	g.occluded = append(g.occluded, covered)
	return nil
}

func (g *graphicsTab) Resized() tea.Cmd { g.resized++; return nil }

var _ tabs.GraphicsAware = (*graphicsTab)(nil)

func graphicsModel(t *testing.T) (Model, *graphicsTab, *fakeTab) {
	t.Helper()
	g, other := &graphicsTab{fakeTab: fakeTab{title: "G"}}, &fakeTab{title: "B"}
	m := newModel(config.Default(), ui.NewTheme(true, nil), ui.DefaultKeyMap(), []tabs.Tab{g, other})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return next.(Model), g, other
}

func TestCapsReachGraphicsTabs(t *testing.T) {
	m, g, _ := graphicsModel(t)
	m.capsDet = termcaps.NewDetector(termcaps.ModeText, func(string) string { return "" }, nil)
	start := m.capsDet.Start()
	if start == nil {
		t.Fatal("no start command")
	}
	next, _ := m.Update(start())
	m = next.(Model)
	if len(g.caps) != 1 || g.caps[0].Chafa || g.caps[0].Protocol != termcaps.None {
		t.Fatalf("caps = %+v", g.caps)
	}
	if !m.capsDet.Done() {
		t.Fatal("detector not done")
	}
}

func TestResizeTellsGraphicsTabs(t *testing.T) {
	m, g, _ := graphicsModel(t)
	if g.resized != 1 {
		t.Fatalf("resized = %d after the first size", g.resized)
	}
	_, _ = m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	if g.resized != 2 || g.w != 90 {
		t.Fatalf("resized = %d, w = %d", g.resized, g.w)
	}
}

func TestOcclusion(t *testing.T) {
	m, g, _ := graphicsModel(t)
	if len(g.occluded) != 0 {
		t.Fatalf("occluded calls before any overlay: %v", g.occluded)
	}
	next, _ := m.Update(press("?")) // help covers the tab
	next, _ = next.Update(press("?"))
	m = next.(Model)
	if got := g.occluded; len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("help: occluded = %v", got)
	}

	// A toast covers it too, and stays covered while it shows.
	next, _ = m.Update(ui.InfoMsg{Text: "copied"})
	_, _ = next.Update(press("j"))
	if got := g.occluded; len(got) != 3 || !got[2] {
		t.Fatalf("toast: occluded = %v", got)
	}
}

func TestSwitchToGraphicsTabPassesOcclusion(t *testing.T) {
	m, g, _ := graphicsModel(t)
	next, _ := m.Update(press("2"))
	next, _ = next.Update(ui.InfoMsg{Text: "hi"}) // a toast over tab B
	next, _ = next.Update(press("1"))
	_ = next
	if got := g.occluded; len(got) == 0 || !got[len(got)-1] {
		t.Fatalf("switching under a toast should say covered: %v", got)
	}
}

// TestTabContentOrigin pins what sixel placement assumes: tab content
// starts at column 0, row ui.TabBarHeight.
func TestTabContentOrigin(t *testing.T) {
	m, g, _ := graphicsModel(t)
	g.title = "ORIGIN"
	lines := strings.Split(m.render(), "\n")
	if got := ansi.Strip(lines[ui.TabBarHeight]); !strings.HasPrefix(got, "ORIGIN") {
		t.Fatalf("row %d = %q", ui.TabBarHeight, got)
	}
}
