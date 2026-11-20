package tabkit

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/tabs"
)

// CacheKind is the kv_cache kind of tab list data.
const CacheKind = "tab"

const (
	cacheTimeout = 2 * time.Second
	fetchTimeout = 30 * time.Second
)

// CacheKey is the kv_cache key of a tab's list for a project (nil = all).
func CacheKey(tab string, p *config.Project) string {
	return "tab:" + strings.ToLower(tab) + ":" + projectName(p, "all")
}

func projectName(p *config.Project, all string) string {
	if p == nil {
		return all
	}
	return p.Name
}

// CacheLoadedMsg carries a tab's cached data, read at startup or after a
// project switch so the tab can render before its fetch finishes.
type CacheLoadedMsg struct {
	Tab       string
	Key       string
	Value     []byte
	FetchedAt time.Time
}

// TargetTab implements tabs.TargetedMsg.
func (m CacheLoadedMsg) TargetTab() string { return m.Tab }

// FetchedMsg is the result of a background fetch. On success Value holds the
// fresh data, which has also been written to the cache; CacheErr reports a
// failed cache write without discarding the data.
type FetchedMsg struct {
	Tab      string
	Key      string
	Value    []byte
	At       time.Time
	Err      error
	CacheErr error
}

// TargetTab implements tabs.TargetedMsg.
func (m FetchedMsg) TargetTab() string { return m.Tab }

// LoadCached reads key from s. A missing entry, a read error or no store
// yields no message: a broken cache must not stop the fetch, it only means
// no instant data this time.
func LoadCached(s tabs.KVStore, tab, key string) tea.Cmd {
	if s == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cacheTimeout)
		defer cancel()
		e, err := s.Get(ctx, key)
		if err != nil {
			return nil
		}
		return CacheLoadedMsg{Tab: tab, Key: key, Value: e.Value, FetchedAt: e.FetchedAt}
	}
}

// FetchFunc loads fresh data. It runs in a tea.Cmd, never in Update.
type FetchFunc func(ctx context.Context) ([]byte, error)

// Fetch describes one background fetch.
type Fetch struct {
	Tab     string
	Key     string
	Project *config.Project
	TTL     time.Duration // cache entry TTL; 0 = never stale
	Run     FetchFunc
	Now     func() time.Time
}

// FetchAndCache runs f.Run, writes the result to s (when not nil) and
// returns a FetchedMsg.
func FetchAndCache(s tabs.KVStore, f Fetch) tea.Cmd {
	now := f.Now
	if now == nil {
		now = time.Now
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		msg := FetchedMsg{Tab: f.Tab, Key: f.Key}
		msg.Value, msg.Err = f.Run(ctx)
		msg.At = now()
		if msg.Err != nil {
			msg.Err = fmt.Errorf("%s: %w", strings.ToLower(f.Tab), msg.Err)
			return msg
		}
		if s != nil {
			cctx, ccancel := context.WithTimeout(context.Background(), cacheTimeout)
			defer ccancel()
			if err := s.Put(cctx, f.Key, CacheKind, projectName(f.Project, ""), msg.Value, f.TTL); err != nil {
				msg.CacheErr = fmt.Errorf("%s cache: %w", strings.ToLower(f.Tab), err)
			}
		}
		return msg
	}
}
