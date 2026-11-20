package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/refresh"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// starterTab is a fake Sessions tab that starts sessions from sources and
// refreshes per source.
type starterTab struct {
	autoTab
	started []tabs.Source
	bySrc   []refresh.Source
}

func (s *starterTab) StartSession(src tabs.Source) tea.Cmd {
	s.started = append(s.started, src)
	return nil
}

func (s *starterTab) RefreshSource(src refresh.Source) tea.Cmd {
	s.bySrc = append(s.bySrc, src)
	return nil
}

func TestStartSessionFromSwitchesToSessions(t *testing.T) {
	a := &fakeTab{title: "PRs"}
	s := &starterTab{autoTab: autoTab{fakeTab: fakeTab{title: "Sessions"}}}
	m := newModel(config.Default(), ui.NewTheme(true, nil), ui.DefaultKeyMap(), []tabs.Tab{a, s})
	src := tabs.Source{Kind: store.KindGitHubPR, ID: "daan/forge#12", Number: 12}
	model, _ := m.Update(tabs.StartSessionFrom(src)())
	m = model.(Model)
	if m.active != 1 {
		t.Errorf("active = %d, want the Sessions tab", m.active)
	}
	if len(s.started) != 1 || s.started[0].ID != "daan/forge#12" {
		t.Errorf("started = %+v", s.started)
	}
}

func TestStartSessionFromWithoutSessionsTab(t *testing.T) {
	m := newModel(config.Default(), ui.NewTheme(true, nil), ui.DefaultKeyMap(), []tabs.Tab{&fakeTab{title: "PRs"}})
	_, cmd := m.Update(tabs.StartSessionFrom(tabs.Source{})())
	if cmd == nil {
		t.Fatal("want an error toast")
	}
	if _, ok := cmd().(ui.ErrMsg); !ok {
		t.Error("want ui.ErrMsg")
	}
}

func TestTickUsesRefreshSource(t *testing.T) {
	s := &starterTab{autoTab: autoTab{
		fakeTab: fakeTab{title: "Sessions"}, sources: []refresh.Source{refresh.Sessions, refresh.GitHub}, hidden: true,
	}}
	m := newModel(config.Default(), ui.NewTheme(true, nil), ui.DefaultKeyMap(), []tabs.Tab{&fakeTab{title: "Home"}, s})
	_, _ = m.Update(m.sched.Msg(refresh.GitHub))
	_, _ = m.Update(m.sched.Msg(refresh.Sessions))
	_, _ = m.Update(m.sched.Msg(refresh.Plane))
	if len(s.bySrc) != 2 || s.bySrc[0] != refresh.GitHub || s.bySrc[1] != refresh.Sessions {
		t.Errorf("RefreshSource calls = %v", s.bySrc)
	}
	if s.refreshes != 0 {
		t.Errorf("Refresh called %d times; RefreshSource replaces it", s.refreshes)
	}
}
