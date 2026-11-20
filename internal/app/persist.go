package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/config"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

const (
	recentKey  = "palette:recent"
	recentKind = "palette"
	// projectKey holds the active project's name; empty means all projects.
	projectKey  = "app:active_project"
	projectKind = "app"
	// recentTimeout bounds a recents read or write.
	recentTimeout = 2 * time.Second
)

// KVStore persists app state (palette recents, the active project) and the
// tabs' cached data. *store.Cache satisfies it.
type KVStore = tabs.KVStore

// Option configures New.
type Option func(*Model)

// WithStore persists palette recents and the active project in s. Without it
// they only last for the session.
func WithStore(s KVStore) Option {
	return func(m *Model) { m.store = s }
}

// WithSessions gives the Sessions tab the service that starts and manages
// agent sessions. Without it the tab explains that sessions are unavailable.
func WithSessions(s agent.API) Option {
	return func(m *Model) { m.sessions = s }
}

// WithActiveProject starts on the project with this name, as returned by
// LoadActiveProject: empty means all projects, and a name that is no longer
// configured falls back to the first project.
func WithActiveProject(name string) Option {
	return func(m *Model) { m.savedProject, m.hasSaved = name, true }
}

// LoadActiveProject reads the saved active project. found is false when
// nothing has been saved yet. It does I/O, so call it before the program
// starts, never from Update.
func LoadActiveProject(ctx context.Context, s KVStore) (name string, found bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, recentTimeout)
	defer cancel()
	e, err := s.Get(ctx, projectKey)
	if errors.Is(err, store.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("active project: %w", err)
	}
	return string(e.Value), true, nil
}

// saveProject persists the active project (nil = all projects).
func saveProject(s KVStore, p *config.Project) tea.Cmd {
	if s == nil {
		return nil
	}
	name := ""
	if p != nil {
		name = p.Name
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), recentTimeout)
		defer cancel()
		if err := s.Put(ctx, projectKey, projectKind, name, []byte(name), 0); err != nil {
			return ui.ErrMsg{Err: fmt.Errorf("active project: %w", err)}
		}
		return nil
	}
}

// recentsMsg carries the recents loaded at startup.
type recentsMsg []string

func loadRecents(s KVStore) tea.Cmd {
	if s == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), recentTimeout)
		defer cancel()
		e, err := s.Get(ctx, recentKey)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return ui.ErrMsg{Err: fmt.Errorf("palette recents: %w", err)}
		}
		var ids []string
		if err := json.Unmarshal(e.Value, &ids); err != nil {
			return ui.ErrMsg{Err: fmt.Errorf("palette recents: %w", err)}
		}
		return recentsMsg(ids)
	}
}

func saveRecents(s KVStore, ids []string) tea.Cmd {
	if s == nil {
		return nil
	}
	return func() tea.Msg {
		b, err := json.Marshal(ids)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), recentTimeout)
			defer cancel()
			err = s.Put(ctx, recentKey, recentKind, "", b, 0)
		}
		if err != nil {
			return ui.ErrMsg{Err: fmt.Errorf("palette recents: %w", err)}
		}
		return nil
	}
}
