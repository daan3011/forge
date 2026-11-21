package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

func ctrlP() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl} }

// fakeModel is a root model over two fake tabs with two projects.
func fakeModel(fs *fakeStore) (Model, *fakeTab, *fakeTab) {
	a, b := &fakeTab{title: "A"}, &fakeTab{title: "B"}
	m := newModel(twoProjects(), ui.NewTheme(true, nil), ui.DefaultKeyMap(), []tabs.Tab{a, b})
	if fs != nil {
		m.store = fs
	}
	return m, a, b
}

func projectName(p *config.Project) string {
	if p == nil {
		return "<all>"
	}
	return p.Name
}

// TestProjectSwitcherFlow is the P0.7 "done when": switching project changes
// the status bar and every tab receives the change.
func TestProjectSwitcherFlow(t *testing.T) {
	fs := newFakeStore(nil)
	m, a, b := fakeModel(fs)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 30))
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("alpha"))
	}, teatest.WithDuration(2*time.Second))
	tm.Send(ctrlP())
	for _, k := range typeText("beta") {
		tm.Send(k)
	}
	tm.Send(enter)
	tm.Send(press("q"))

	final := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second)).(Model)
	if projectName(final.project) != "beta" {
		t.Fatalf("project = %s, want beta", projectName(final.project))
	}
	// Every tab gets the project; only the visible one reloads now; hidden
	// B is paused and catches up when shown.
	for _, ft := range []*fakeTab{a, b} {
		wantRefresh := ft == a
		if projectName(ft.proj) != "beta" || (ft.refreshes > 0) != wantRefresh {
			t.Fatalf("tab %s: project = %s, refreshes = %d", ft.title, projectName(ft.proj), ft.refreshes)
		}
		if len(ft.keys) != 0 {
			t.Fatalf("picker keys leaked into tab %s: %v", ft.title, ft.keys)
		}
	}
	if view := ansi.Strip(final.render()); !strings.Contains(view, "A · beta") {
		t.Fatalf("status bar should show beta:\n%s", view)
	}
	// The save runs as a command; give it a moment to land.
	deadline := time.Now().Add(time.Second)
	for string(fs.val(projectKey)) != "beta" {
		if time.Now().After(deadline) {
			t.Fatalf("saved project = %q, want beta", fs.val(projectKey))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestProjectPicker(t *testing.T) {
	m, a, _ := fakeModel(nil)
	var model tea.Model = m
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// Opens with the current project selected.
	model, _ = model.Update(ctrlP())
	m = model.(Model)
	if m.overlay != overlayProjects {
		t.Fatal("ctrl+p should open the project switcher")
	}
	if got := ids(m.shown); strings.Join(got, ",") != "project:*,project:alpha,project:beta" {
		t.Fatalf("rows = %v", got)
	}
	if i := m.palette.Selected(); i != 1 {
		t.Fatalf("selected = %d, want 1 (alpha, current)", i)
	}
	view := m.render()
	assertScreen(t, view, 100, 30)
	plain := ansi.Strip(view)
	for _, want := range []string{"◆ Projects", "All projects", "daan3011/alpha · no checkout · current"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("picker missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "Quit forge") {
		t.Fatal("picker should only list projects")
	}

	// esc closes without switching.
	model, cmd := model.Update(esc)
	m = model.(Model)
	if m.overlay != overlayNone || cmd != nil || projectName(m.project) != "alpha" || a.refreshes != 0 {
		t.Fatal("esc should only close the picker")
	}

	// ctrl+p inside the picker moves up (Input.Prev): alpha -> All projects.
	model, _ = model.Update(ctrlP())
	model, _ = model.Update(ctrlP())
	if model.(Model).overlay != overlayProjects {
		t.Fatal("ctrl+p inside the picker should not close it")
	}
	model, _ = model.Update(enter)
	m = model.(Model)
	if m.project != nil || a.proj != nil || a.refreshes != 1 {
		t.Fatalf("want all projects: project = %s, tab = %s, refreshes = %d",
			projectName(m.project), projectName(a.proj), a.refreshes)
	}
	if !strings.Contains(ansi.Strip(m.render()), "A · all projects") {
		t.Fatal("status bar should show all projects")
	}

	// ctrl+k switches from the picker to the command palette.
	model, _ = model.Update(ctrlP())
	model, _ = model.Update(ctrlK())
	if model.(Model).overlay != overlayCommands {
		t.Fatal("ctrl+k from the picker should open the command palette")
	}
}

func TestCtrlPInPaletteMovesSelection(t *testing.T) {
	var model tea.Model = mustNew(t, twoProjects())
	model, _ = model.Update(ctrlK())
	model, _ = model.Update(ctrlP())
	m := model.(Model)
	if m.overlay != overlayCommands {
		t.Fatalf("overlay = %v, want the command palette", m.overlay)
	}
	if got, want := m.palette.Selected(), len(m.shown)-1; got != want {
		t.Fatalf("selected = %d, want %d (wrapped to last)", got, want)
	}
}

func TestCtrlPWhileFiltering(t *testing.T) {
	var model tea.Model = mustNew(t, twoProjects())
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model, _ = model.Update(press("/"))
	if !model.(Model).capturingInput() {
		t.Fatal("/ should start the filter")
	}
	model, _ = model.Update(ctrlP())
	if model.(Model).overlay != overlayProjects {
		t.Fatal("ctrl+p should open the switcher while filtering")
	}
}

func TestPaletteOpensProjectSwitcher(t *testing.T) {
	var model tea.Model = mustNew(t, twoProjects())
	model, _ = runPalette(t, model, "Switch project…")
	if model.(Model).overlay != overlayProjects {
		t.Fatal("the palette command should open the project switcher")
	}
}

func TestProjectSwitcherWithoutProjects(t *testing.T) {
	var model tea.Model = mustNew(t, config.Default())
	model, cmd := model.Update(ctrlP())
	m := model.(Model)
	if m.overlay != overlayNone || cmd == nil || m.toast.Empty() {
		t.Fatal("ctrl+p without projects should only show a hint")
	}
}

func TestStartupProject(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
		want string
	}{
		{name: "nothing saved", want: "alpha"},
		{name: "saved project", opts: []Option{WithActiveProject("beta")}, want: "beta"},
		{name: "saved all projects", opts: []Option{WithActiveProject("")}, want: "<all>"},
		{name: "saved project removed", opts: []Option{WithActiveProject("gone")}, want: "alpha"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := New(twoProjects(), tt.opts...)
			if err != nil {
				t.Fatal(err)
			}
			if got := projectName(m.project); got != tt.want {
				t.Fatalf("project = %s, want %s", got, tt.want)
			}
			// Placeholder tabs render the project they were given.
			want := strings.ReplaceAll(tt.want, "<all>", "all projects")
			m.tabs[6].SetSize(100, 30) // Artifacts (PRs, Pipelines, Plane and Docs are no longer placeholders)
			if view := ansi.Strip(m.tabs[6].View()); !strings.Contains(view, "project: "+want) {
				t.Fatalf("tab did not get %s:\n%s", want, view)
			}
		})
	}
}

func TestLoadActiveProject(t *testing.T) {
	tests := []struct {
		name      string
		store     *fakeStore
		wantName  string
		wantFound bool
		wantErr   bool
	}{
		{name: "saved", store: newFakeStore(map[string]string{projectKey: "beta"}), wantName: "beta", wantFound: true},
		{name: "saved all", store: newFakeStore(map[string]string{projectKey: ""}), wantFound: true},
		{name: "nothing saved", store: newFakeStore(nil)},
		{name: "store error", store: &fakeStore{err: errors.New("disk on fire")}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, found, err := LoadActiveProject(context.Background(), tt.store)
			if (err != nil) != tt.wantErr || name != tt.wantName || found != tt.wantFound {
				t.Fatalf("got (%q, %v, %v), want (%q, %v, err=%v)", name, found, err, tt.wantName, tt.wantFound, tt.wantErr)
			}
		})
	}
}

func TestSaveProject(t *testing.T) {
	fs := newFakeStore(nil)
	p := &config.Project{Name: "beta"}
	if msg := saveProject(fs, p)(); msg != nil {
		t.Fatalf("save: %v", msg)
	}
	if string(fs.val(projectKey)) != "beta" {
		t.Fatalf("saved %q", fs.val(projectKey))
	}
	if msg := saveProject(fs, nil)(); msg != nil || string(fs.val(projectKey)) != "" || fs.putCount(projectKey) != 2 {
		t.Fatal("all projects should save an empty name")
	}
	if _, ok := saveProject(&fakeStore{err: errors.New("disk on fire")}, p)().(ui.ErrMsg); !ok {
		t.Fatal("store error should become an ErrMsg")
	}
	if saveProject(nil, p) != nil {
		t.Fatal("no store means no command")
	}
}
