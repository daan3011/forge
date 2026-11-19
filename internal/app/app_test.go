package app

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

func TestQuitFlow(t *testing.T) {
	tm := teatest.NewTestModel(t, New(config.Default()), teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("forge"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}

func TestSwitchTabFlow(t *testing.T) {
	tm := teatest.NewTestModel(t, New(config.Default()), teatest.WithInitialTermSize(80, 24))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("forge"))
	}, teatest.WithDuration(2*time.Second))
	tm.Send(press("2"))
	tm.Send(press("j"))
	tm.Send(press("q"))

	// The renderer only redraws changed cells, so assert on the final model
	// rather than on raw output.
	m := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second)).(Model)
	if m.active != 1 {
		t.Fatalf("active = %d, want 1", m.active)
	}
	view := ansi.Strip(m.render())
	for _, want := range []string{"2 PRs", "coming in P2.1", "Title: sample: Bump dependencies"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestQuitKeys(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.KeyPressMsg
		quit bool
	}{
		{"q", tea.KeyPressMsg{Code: 'q', Text: "q"}, true},
		{"ctrl+c", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, true},
		{"x", tea.KeyPressMsg{Code: 'x', Text: "x"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, cmd := New(config.Default()).Update(tt.msg)
			gotQuit := cmd != nil && isQuit(cmd())
			if gotQuit != tt.quit {
				t.Fatalf("quit = %v, want %v", gotQuit, tt.quit)
			}
		})
	}
}

func TestTabSwitching(t *testing.T) {
	tests := []struct {
		name  string
		start int
		keys  []tea.KeyPressMsg
		want  int
	}{
		{"digit", 0, []tea.KeyPressMsg{press("3")}, 2},
		{"last digit", 0, []tea.KeyPressMsg{press("8")}, 7},
		{"digit without tab ignored", 4, []tea.KeyPressMsg{press("9")}, 4},
		{"tab", 0, []tea.KeyPressMsg{{Code: tea.KeyTab}}, 1},
		{"tab wraps", 7, []tea.KeyPressMsg{{Code: tea.KeyTab}}, 0},
		{"shift+tab wraps", 0, []tea.KeyPressMsg{{Code: tea.KeyTab, Mod: tea.ModShift}}, 7},
		{"shift+tab", 3, []tea.KeyPressMsg{{Code: tea.KeyTab, Mod: tea.ModShift}}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(config.Default())
			m.active = tt.start
			var model tea.Model = m
			for _, k := range tt.keys {
				model, _ = model.Update(k)
			}
			if got := model.(Model).active; got != tt.want {
				t.Fatalf("active = %d, want %d", got, tt.want)
			}
		})
	}
}

// fakeTab records what the root model sends it.
type fakeTab struct {
	title  string
	w, h   int
	keys   []string
	proj   *config.Project
	update time.Time
}

func (f *fakeTab) Init() tea.Cmd { return nil }
func (f *fakeTab) Update(msg tea.Msg) (tabs.Tab, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		f.keys = append(f.keys, k.String())
	}
	return f, nil
}
func (f *fakeTab) View() string                 { return f.title }
func (f *fakeTab) Title() string                { return f.title }
func (f *fakeTab) ShortHelp() []key.Binding     { return nil }
func (f *fakeTab) Refresh() tea.Cmd             { return nil }
func (f *fakeTab) SetSize(w, h int)             { f.w, f.h = w, h }
func (f *fakeTab) SetProject(p *config.Project) { f.proj = p }
func (f *fakeTab) LastUpdated() time.Time       { return f.update }

func TestRouting(t *testing.T) {
	cfg := config.Default()
	cfg.Projects = []config.Project{{Name: "forge", Repo: "daan3011/forge"}}
	a, b := &fakeTab{title: "A"}, &fakeTab{title: "B"}
	var model tea.Model = newModel(cfg, ui.NewTheme(true, nil), []tabs.Tab{a, b})

	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	for _, ft := range []*fakeTab{a, b} {
		if ft.w != 100 || ft.h != 30-ui.TabBarHeight-1 {
			t.Fatalf("tab %s got size %dx%d", ft.title, ft.w, ft.h)
		}
		if ft.proj == nil || ft.proj.Name != "forge" {
			t.Fatalf("tab %s did not get the project", ft.title)
		}
	}

	model, _ = model.Update(press("j"))
	model, _ = model.Update(press("2"))
	_, _ = model.Update(press("k"))
	if strings.Join(a.keys, ",") != "j" || strings.Join(b.keys, ",") != "k" {
		t.Fatalf("keys went to the wrong tab: a=%v b=%v", a.keys, b.keys)
	}
}

func TestErrorToast(t *testing.T) {
	var model tea.Model = New(config.Default())
	model, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model, cmd := model.Update(ui.ErrMsg{Err: errors.New("gh: rate limited")})
	if cmd == nil {
		t.Fatal("error toast should schedule its expiry")
	}
	view := model.(Model).render()
	if !strings.Contains(view, "gh: rate limited") {
		t.Fatalf("toast not shown:\n%s", view)
	}
	assertScreen(t, view, 80, 24)
}

func TestBackgroundSwitchesTheme(t *testing.T) {
	m := New(config.Default())
	theme := m.theme
	_, _ = m.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})
	if theme.IsDark {
		t.Fatal("light background should switch the shared theme to light")
	}
}

// TestEveryTabFits renders every tab at several sizes, with the split moved to
// both extremes, and checks the screen is exactly the terminal size.
func TestEveryTabFits(t *testing.T) {
	sizes := [][2]int{{80, 24}, {120, 40}, {200, 50}}
	for _, sz := range sizes {
		var model tea.Model = New(config.Default())
		model, _ = model.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		n := len(model.(Model).tabs)
		for i := range n {
			model, _ = model.Update(press(string(rune('1' + i))))
			title := model.(Model).tabs[i].Title()
			for _, k := range []string{"", "<", "<", "<", "<", "<", "<", ">", ">", ">", ">", ">", ">", ">", ">", ">", ">", ">", ">"} {
				if k != "" {
					model, _ = model.Update(press(k))
				}
				view := model.(Model).render()
				if !strings.Contains(view, title) {
					t.Fatalf("%dx%d tab %s: title missing", sz[0], sz[1], title)
				}
				assertScreen(t, view, sz[0], sz[1])
			}
		}
	}
}

func assertScreen(t *testing.T, s string, width, height int) {
	t.Helper()
	lines := strings.Split(s, "\n")
	if len(lines) != height {
		t.Fatalf("%dx%d: got %d lines", width, height, len(lines))
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w != width {
			t.Fatalf("%dx%d: line %d is %d wide: %q", width, height, i, w, l)
		}
	}
}

func press(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func isQuit(msg tea.Msg) bool {
	_, ok := msg.(tea.QuitMsg)
	return ok
}
