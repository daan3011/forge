package app

import (
	"context"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// fakeProjectStore records touches and Plane links.
type fakeProjectStore struct {
	mu      sync.Mutex
	touched []string // "repo@path"
}

func (f *fakeProjectStore) Touch(_ context.Context, repo, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.touched = append(f.touched, repo+"@"+path)
	return nil
}

func (f *fakeProjectStore) SetPlane(context.Context, string, string) error { return nil }

func linkModel() (Model, *fakeTab) {
	cfg := twoProjects()
	cfg.Projects[0].Path = "/code/alpha"
	tab := &fakeTab{title: "A"}
	m := newModel(cfg, ui.NewTheme(true, nil), ui.DefaultKeyMap(), []tabs.Tab{tab})
	m.resolveProject() // alpha
	return m, tab
}

func TestPlaneLinkedUpdatesActiveProject(t *testing.T) {
	m, tab := linkModel()
	tab.proj, tab.refreshes = nil, 0
	var model tea.Model = m
	model, _ = model.Update(tabs.PlaneLinkedMsg{Links: []tabs.PlaneLink{{Repo: "DAAN3011/alpha", PlaneProjectID: "p-a"}}})
	m = model.(Model)
	if p := m.cfg.Projects[0]; p.PlaneProjectID != "p-a" || !p.PlaneDecided {
		t.Fatalf("alpha = %+v", p)
	}
	if tab.proj == nil || tab.proj.PlaneProjectID != "p-a" || tab.refreshes != 1 {
		t.Errorf("tab got %+v, refreshes %d", tab.proj, tab.refreshes)
	}
	// A link for another project is recorded without bothering the tabs.
	tab.proj, tab.refreshes = nil, 0
	model, _ = model.Update(tabs.PlaneLinkedMsg{Links: []tabs.PlaneLink{{Repo: "daan3011/beta"}}})
	m = model.(Model)
	if p := m.cfg.Projects[1]; p.PlaneProjectID != "" || !p.PlaneDecided {
		t.Errorf("beta = %+v", p)
	}
	if tab.proj != nil || tab.refreshes != 0 {
		t.Errorf("tabs told about another project: %+v %d", tab.proj, tab.refreshes)
	}
}

func TestSwitchingRemembersProject(t *testing.T) {
	m, _ := linkModel()
	ps := &fakeProjectStore{}
	m.projStore = ps
	m, cmd := m.setProject(&m.cfg.Projects[0])
	for _, msg := range runCmd(cmd) {
		m.Update(msg)
	}
	if len(ps.touched) != 1 || ps.touched[0] != "daan3011/alpha@/code/alpha" {
		t.Errorf("touched = %v", ps.touched)
	}
	if _, cmd := m.setProject(nil); len(runCmd(cmd)) > 0 && len(ps.touched) != 1 {
		t.Errorf("all projects touched something: %v", ps.touched)
	}
}

func TestSwitcherShowsCheckouts(t *testing.T) {
	m, _ := linkModel()
	var model tea.Model = m
	model, _ = model.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	model, _ = model.Update(ctrlP())
	view := ansi.Strip(model.(Model).render())
	for _, want := range []string{"daan3011/alpha · /code/alpha · current", "daan3011/beta · no checkout"} {
		if !strings.Contains(view, want) {
			t.Errorf("switcher lacks %q:\n%s", want, view)
		}
	}
}

func TestNoReposHint(t *testing.T) {
	var model tea.Model = mustNew(t, config.Default())
	model, _ = model.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	model, _ = model.Update(ctrlP())
	if v := ansi.Strip(model.(Model).render()); !strings.Contains(v, "no git repos found") {
		t.Errorf("hint missing:\n%s", v)
	}
}
