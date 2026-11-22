package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/sessions/live"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// The fakes below record the cross-tab calls Home's actions end in (P6.1).
// Each has its own Update so the model keeps the wrapper, not the embedded
// fakeTab.

type watchTab struct {
	fakeTab
	pings int
}

func (w *watchTab) Update(tea.Msg) (tabs.Tab, tea.Cmd) { return w, nil }
func (w *watchTab) SessionsChanged() tea.Cmd           { w.pings++; return nil }

type prActTab struct {
	fakeTab
	actions []tabs.PRAction
}

func (p *prActTab) Update(tea.Msg) (tabs.Tab, tea.Cmd) { return p, nil }
func (p *prActTab) PRAction(_ github.PR, a tabs.PRAction) tea.Cmd {
	p.actions = append(p.actions, a)
	return nil
}

type selectTab struct {
	fakeTab
	selected []string
}

func (s *selectTab) Update(tea.Msg) (tabs.Tab, tea.Cmd) { return s, nil }
func (*selectTab) Entities() []tabs.Entity              { return nil }
func (s *selectTab) Select(id string) tea.Cmd           { s.selected = append(s.selected, id); return nil }

type goTab struct {
	selectTab
	attached []string
}

func (g *goTab) Update(tea.Msg) (tabs.Tab, tea.Cmd) { return g, nil }
func (g *goTab) AttachSession(name string) tea.Cmd  { g.attached = append(g.attached, name); return nil }

func TestHomeActionsRoute(t *testing.T) {
	home := &watchTab{fakeTab: fakeTab{title: "Home"}}
	prs := &prActTab{fakeTab: fakeTab{title: "PRs"}}
	plane := &selectTab{fakeTab: fakeTab{title: "Plane"}}
	sess := &goTab{selectTab: selectTab{fakeTab: fakeTab{title: "Sessions"}}}
	m := newModel(config.Default(), ui.NewTheme(true, nil), ui.DefaultKeyMap(), []tabs.Tab{home, prs, plane, sess})
	active := func() string { return m.tabs[m.active].Title() }
	send := func(msg tea.Msg) {
		got, _ := m.Update(msg)
		m = got.(Model)
	}

	send(tabs.AttachSessionMsg{Name: "fix-login"})
	if active() != "Sessions" || len(sess.attached) != 1 || sess.attached[0] != "fix-login" {
		t.Errorf("attach: active %q, attached %v", active(), sess.attached)
	}

	send(tabs.PRActionMsg{PR: github.PR{Repo: "o/r", Number: 1}, Action: tabs.PRMerge})
	if active() != "PRs" || len(prs.actions) != 1 || prs.actions[0] != tabs.PRMerge {
		t.Errorf("PR action: active %q, actions %v", active(), prs.actions)
	}

	send(tabs.SelectEntityMsg{Tab: "Plane", ID: "uuid-41"})
	if active() != "Plane" || len(plane.selected) != 1 || plane.selected[0] != "uuid-41" {
		t.Errorf("select: active %q, selected %v", active(), plane.selected)
	}

	// A hook ping reaches Home even while it is hidden.
	send(tabs.SessionPingMsg{Ping: live.Ping{Session: "fix-login"}})
	if home.pings != 1 {
		t.Errorf("Home saw %d pings, want 1", home.pings)
	}
}
