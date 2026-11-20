package tabkit

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/refresh"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

func testDeps(s tabs.KVStore) Deps {
	return Deps{Theme: ui.NewTheme(true, nil), Keys: ui.DefaultKeyMap(), Store: s}
}

// run executes cmd and returns the messages it produces, flattening batches.
func run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, run(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// drain runs cmd and feeds every resulting message back into p until no
// commands are left. ErrMsgs fail the test.
func drain(t *testing.T, p *Placeholder, cmd tea.Cmd) {
	t.Helper()
	queue := run(cmd)
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		if e, ok := msg.(ui.ErrMsg); ok {
			t.Fatalf("unexpected error: %v", e.Err)
		}
		_, next := p.Update(msg)
		queue = append(queue, run(next)...)
	}
}

type entry struct {
	project string
	value   []byte
	at      time.Time
}

type fakeStore struct {
	mu      sync.Mutex
	m       map[string]entry
	putErr  error
	putKeys []string
}

func newFakeStore() *fakeStore { return &fakeStore{m: map[string]entry{}} }

func (f *fakeStore) Get(_ context.Context, key string) (store.CacheEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.m[key]
	if !ok {
		return store.CacheEntry{}, store.ErrNotFound
	}
	return store.CacheEntry{Key: key, Project: e.project, Value: e.value, FetchedAt: e.at}, nil
}

func (f *fakeStore) Put(_ context.Context, key, _, project string, value []byte, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putKeys = append(f.putKeys, key)
	if f.putErr != nil {
		return f.putErr
	}
	f.m[key] = entry{project: project, value: value, at: time.Now()}
	return nil
}

func (f *fakeStore) seed(key string, rows [][]string, at time.Time) {
	b, _ := json.Marshal(rows)
	f.m[key] = entry{value: b, at: at}
}

func TestCacheKey(t *testing.T) {
	tests := []struct {
		tab  string
		p    *config.Project
		want string
	}{
		{"PRs", nil, "tab:prs:all"},
		{"PRs", &config.Project{Name: "forge"}, "tab:prs:forge"},
		{"Pipelines", &config.Project{Name: "x"}, "tab:pipelines:x"},
	}
	for _, tt := range tests {
		if got := CacheKey(tt.tab, tt.p); got != tt.want {
			t.Errorf("CacheKey(%q) = %q, want %q", tt.tab, got, tt.want)
		}
	}
}

func TestLoadCached(t *testing.T) {
	fs := newFakeStore()
	at := time.Now().Add(-time.Hour)
	fs.seed("k", [][]string{{"a"}}, at)
	tests := []struct {
		name string
		s    tabs.KVStore
		key  string
		want bool
	}{
		{"hit", fs, "k", true},
		{"miss", fs, "nope", false},
		{"no store", nil, "k", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgs := run(LoadCached(tt.s, "PRs", tt.key))
			if (len(msgs) == 1) != tt.want {
				t.Fatalf("msgs = %v", msgs)
			}
			if tt.want {
				m := msgs[0].(CacheLoadedMsg)
				if m.TargetTab() != "PRs" || m.Key != "k" || !m.FetchedAt.Equal(at) {
					t.Fatalf("msg = %+v", m)
				}
			}
		})
	}
}

func TestFetchAndCache(t *testing.T) {
	ok := func(context.Context) ([]byte, error) { return []byte(`[["x"]]`), nil }
	boom := func(context.Context) ([]byte, error) { return nil, errors.New("boom") }
	tests := []struct {
		name       string
		fetch      FetchFunc
		putErr     error
		wantErr    string
		wantCache  string
		wantCached bool
	}{
		{name: "ok", fetch: ok, wantCached: true},
		{name: "fetch fails", fetch: boom, wantErr: "prs: boom"},
		{name: "cache write fails", fetch: ok, putErr: errors.New("disk"), wantCache: "prs cache: disk"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := newFakeStore()
			fs.putErr = tt.putErr
			at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
			msgs := run(FetchAndCache(fs, Fetch{
				Tab: "PRs", Key: "tab:prs:forge", Project: &config.Project{Name: "forge"},
				Run: tt.fetch, Now: func() time.Time { return at },
			}))
			m := msgs[0].(FetchedMsg)
			if errString(m.Err) != tt.wantErr || errString(m.CacheErr) != tt.wantCache {
				t.Fatalf("err %v, cacheErr %v", m.Err, m.CacheErr)
			}
			if !m.At.Equal(at) || m.TargetTab() != "PRs" {
				t.Fatalf("msg = %+v", m)
			}
			e, err := fs.Get(context.Background(), "tab:prs:forge")
			if (err == nil) != tt.wantCached {
				t.Fatalf("cached = %v", err == nil)
			}
			if tt.wantCached && (e.Project != "forge" || string(e.Value) != `[["x"]]`) {
				t.Fatalf("entry = %+v", e)
			}
		})
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

var sampleSpec = PlaceholderSpec{
	Title:   "PRs",
	Columns: []ui.Column{{Title: "#", Width: 4}, {Title: "Title", Flex: true}},
	Rows:    [][]string{{"#1", "fresh"}},
}

func TestPlaceholderCacheFirst(t *testing.T) {
	fs := newFakeStore()
	cachedAt := time.Now().Add(-3 * time.Minute).Truncate(time.Millisecond)
	fs.seed("tab:prs:all", [][]string{{"#9", "cached"}}, cachedAt)

	p := NewPlaceholder(testDeps(fs), sampleSpec)
	p.SetSize(80, 10)
	msgs := run(p.Init())
	if !p.Fetching() {
		t.Fatal("Init should start a fetch")
	}
	var cached, fetched tea.Msg
	for _, m := range msgs {
		switch m.(type) {
		case CacheLoadedMsg:
			cached = m
		case FetchedMsg:
			fetched = m
		}
	}
	if cached == nil || fetched == nil {
		t.Fatalf("msgs = %v", msgs)
	}

	p.Update(cached)
	if view := ansi.Strip(p.View()); !strings.Contains(view, "cached") {
		t.Fatalf("cached rows not shown:\n%s", view)
	}
	if !p.LastUpdated().Equal(cachedAt) || !p.Fetching() {
		t.Fatalf("after cache: updated %v, fetching %v", p.LastUpdated(), p.Fetching())
	}

	p.Update(fetched)
	if view := ansi.Strip(p.View()); !strings.Contains(view, "fresh") || strings.Contains(view, "cached") {
		t.Fatalf("fresh rows not shown:\n%s", view)
	}
	if p.Fetching() || !p.LastUpdated().After(cachedAt) {
		t.Fatalf("after fetch: updated %v, fetching %v", p.LastUpdated(), p.Fetching())
	}
	var rows [][]string
	e, _ := fs.Get(context.Background(), "tab:prs:all")
	if err := json.Unmarshal(e.Value, &rows); err != nil || !reflect.DeepEqual(rows, sampleSpec.Rows) {
		t.Fatalf("cache not updated: %s", e.Value)
	}

	// A late cache read must not overwrite fresher data.
	p.Update(cached)
	if strings.Contains(ansi.Strip(p.View()), "cached") {
		t.Fatal("stale cache replaced fresh rows")
	}
}

func TestPlaceholderRefreshDedupes(t *testing.T) {
	fs := newFakeStore()
	p := NewPlaceholder(testDeps(fs), sampleSpec)
	first := p.Refresh()
	if p.Refresh() != nil {
		t.Fatal("second Refresh while fetching should do nothing")
	}
	drain(t, p, first)
	if p.Fetching() {
		t.Fatal("still fetching")
	}
	if p.Refresh() == nil {
		t.Fatal("Refresh after the fetch landed should fetch again")
	}
}

func TestPlaceholderProjectSwitchDropsStaleFetch(t *testing.T) {
	fs := newFakeStore()
	p := NewPlaceholder(testDeps(fs), sampleSpec)
	p.SetSize(80, 10)
	old := run(p.Refresh())

	p.SetProject(&config.Project{Name: "forge"})
	if p.Fetching() || !p.LastUpdated().IsZero() || len(p.Entities()) != 0 {
		t.Fatal("project switch should reset the tab")
	}
	for _, m := range old {
		p.Update(m)
	}
	if !p.LastUpdated().IsZero() {
		t.Fatal("fetch for the old project was applied")
	}
	drain(t, p, p.Refresh())
	if p.LastUpdated().IsZero() || p.key != "tab:prs:forge" {
		t.Fatalf("new project not loaded: key %s", p.key)
	}
	if _, err := fs.Get(context.Background(), "tab:prs:forge"); err != nil {
		t.Fatal("new project not cached")
	}
}

func TestPlaceholderFetchError(t *testing.T) {
	p := NewPlaceholder(testDeps(nil), sampleSpec)
	p.SetSize(80, 10)
	drain(t, p, p.Init())
	before := p.LastUpdated()

	p.Refresh()
	_, cmd := p.Update(FetchedMsg{Tab: "PRs", Key: p.key, Err: errors.New("prs: offline")})
	msgs := run(cmd)
	if len(msgs) != 1 {
		t.Fatalf("msgs = %v", msgs)
	}
	if e, ok := msgs[0].(ui.ErrMsg); !ok || e.Err.Error() != "prs: offline" {
		t.Fatalf("msg = %v", msgs[0])
	}
	if p.Fetching() || !p.LastUpdated().Equal(before) || !strings.Contains(ansi.Strip(p.View()), "fresh") {
		t.Fatal("error should keep the old rows and update time")
	}
}

func TestPlaceholderCacheWriteErrorKeepsData(t *testing.T) {
	fs := newFakeStore()
	fs.putErr = errors.New("disk full")
	p := NewPlaceholder(testDeps(fs), sampleSpec)
	var errs []error
	queue := run(p.Init())
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		if e, ok := msg.(ui.ErrMsg); ok {
			errs = append(errs, e.Err)
			continue
		}
		_, next := p.Update(msg)
		queue = append(queue, run(next)...)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "disk full") {
		t.Fatalf("errs = %v", errs)
	}
	if p.LastUpdated().IsZero() {
		t.Fatal("fetched data should be shown even if caching failed")
	}
}

func TestPlaceholderAutoRefresh(t *testing.T) {
	spec := sampleSpec
	spec.Sources = []refresh.Source{refresh.GitHub}
	spec.RefreshWhenHidden = true
	p := NewPlaceholder(testDeps(nil), spec)
	if !reflect.DeepEqual(p.Sources(), spec.Sources) || !p.RefreshWhenHidden() {
		t.Fatalf("sources %v, hidden %v", p.Sources(), p.RefreshWhenHidden())
	}
	if none := NewPlaceholder(testDeps(nil), sampleSpec); len(none.Sources()) != 0 || none.RefreshWhenHidden() {
		t.Fatal("no sources by default")
	}
}
