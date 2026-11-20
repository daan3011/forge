package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// goerTab is a fake tab that owns sessions.
type goerTab struct {
	fakeTab
	entities []tabs.Entity
	attached []string
}

func (g *goerTab) Entities() []tabs.Entity { return g.entities }
func (g *goerTab) Select(string) tea.Cmd   { return nil }
func (g *goerTab) AttachSession(id string) tea.Cmd {
	g.attached = append(g.attached, id)
	return nil
}

func ctrlG() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl} }

func sessionModel() (Model, *goerTab) {
	a := &fakeTab{title: "A"}
	s := &goerTab{fakeTab: fakeTab{title: "Sessions"}, entities: []tabs.Entity{
		{Kind: "Session", ID: "fix-ci", Title: "fix-ci", Detail: "dead · alpha · agent/fix-ci · #12", Search: "daan3011/alpha#12 #12 agent/fix-ci"},
		{Kind: "Session", ID: "docs", Title: "docs", Detail: "idle · alpha · agent/docs", Search: "PLANE-7 agent/docs"},
	}}
	m := newModel(twoProjects(), ui.NewTheme(true, nil), ui.DefaultKeyMap(), []tabs.Tab{a, s})
	return m, s
}

func TestSessionPicker(t *testing.T) {
	for _, tt := range []struct{ query, want string }{
		{"fix", "fix-ci"},
		{"#12", "fix-ci"},
		{"plane-7", "docs"},
	} {
		t.Run(tt.query, func(t *testing.T) {
			m, s := sessionModel()
			var model tea.Model = m
			model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			model, _ = model.Update(ctrlG())
			m = model.(Model)
			if m.overlay != overlaySessions {
				t.Fatal("ctrl+g should open the session picker")
			}
			if plain := ansi.Strip(m.render()); !strings.Contains(plain, "fix-ci") || strings.Contains(plain, "Quit forge") {
				t.Fatalf("picker should list only sessions:\n%s", plain)
			}
			for _, k := range typeText(tt.query) {
				model, _ = model.Update(k)
			}
			model, _ = model.Update(enter)
			m = model.(Model)
			if m.overlay != overlayNone || m.active != 1 {
				t.Fatalf("overlay = %v, active = %d", m.overlay, m.active)
			}
			if len(s.attached) != 1 || s.attached[0] != tt.want {
				t.Fatalf("attached = %v, want %s", s.attached, tt.want)
			}
		})
	}
}

func TestGoToSessionPaletteCommand(t *testing.T) {
	m, _ := sessionModel()
	var model tea.Model = m
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model, _ = runPalette(t, model, "go to session")
	if model.(Model).overlay != overlaySessions {
		t.Fatal("\"Go to session…\" should open the session picker")
	}
}

func TestSessionEntitiesMatchLinks(t *testing.T) {
	m, s := sessionModel()
	var model tea.Model = m
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	// The ctrl+k palette finds sessions by linked issue too.
	model, _ = runPalette(t, model, "session plane-7")
	if model.(Model).active != 1 || len(s.attached) != 0 {
		t.Fatalf("active = %d attached = %v (selecting an entity only selects)", model.(Model).active, s.attached)
	}
}

func TestSessionPickerWithoutSessions(t *testing.T) {
	m, s := sessionModel()
	s.entities = nil
	var model tea.Model = m
	model, cmd := model.Update(ctrlG())
	if model.(Model).overlay != overlayNone || cmd == nil {
		t.Fatal("want a hint toast, not an empty picker")
	}
}
