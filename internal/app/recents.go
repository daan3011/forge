package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/ui"
)

const (
	recentKey  = "palette:recent"
	recentKind = "palette"
	// recentTimeout bounds a recents read or write.
	recentTimeout = 2 * time.Second
)

// RecentStore persists recently used palette items. *store.Cache satisfies it.
type RecentStore interface {
	Get(ctx context.Context, key string) (store.CacheEntry, error)
	Put(ctx context.Context, key, kind, project string, value []byte, ttl time.Duration) error
}

// Option configures New.
type Option func(*Model)

// WithRecents persists palette recents in s. Without it they only last for
// the session.
func WithRecents(s RecentStore) Option {
	return func(m *Model) { m.recents = s }
}

// recentsMsg carries the recents loaded at startup.
type recentsMsg []string

func loadRecents(s RecentStore) tea.Cmd {
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

func saveRecents(s RecentStore, ids []string) tea.Cmd {
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
