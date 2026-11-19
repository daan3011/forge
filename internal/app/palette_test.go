package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

func ids(items []item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.id
	}
	return out
}

func TestRank(t *testing.T) {
	reg := []item{
		{id: "quit", title: "Quit forge", group: groupCommands},
		{id: "refresh", title: "Refresh tab", group: groupCommands},
		{id: "tab:PRs", title: "Switch tab: PRs", group: groupTabs},
		{id: "tab:Plane", title: "Switch tab: Plane", group: groupTabs},
		{id: "project:alpha", title: "Switch project: alpha", group: groupProjects},
		{id: "project:beta", title: "Switch project: beta", group: groupProjects},
		{id: "entity:PRs:#1", title: "#1 Fix the bellows", group: "PRs", search: "PR #1 Fix the bellows"},
	}
	tests := []struct {
		name   string
		query  string
		recent []string
		want   []string // leading ids, in order
		groups []string // full group sequence, if set
		none   bool
	}{
		{name: "empty keeps registry order", want: ids(reg)},
		{
			name: "empty puts recents first", recent: []string{"project:beta", "gone", "refresh"},
			want:   []string{"project:beta", "refresh", "quit", "tab:PRs"},
			groups: []string{groupRecent, groupRecent, groupCommands, groupTabs, groupTabs, groupProjects, "PRs"},
		},
		{name: "quit", query: "quit", want: []string{"quit"}},
		{name: "switch tab", query: "switch tab plane", want: []string{"tab:Plane"}},
		{name: "switch project", query: "switch project beta", want: []string{"project:beta"}},
		{name: "project shorthand", query: "beta", want: []string{"project:beta"}},
		{name: "entity by kind", query: "pr bellows", want: []string{"entity:PRs:#1"}},
		{
			name: "recent wins a tie", query: "switch tab", recent: []string{"tab:Plane"},
			want: []string{"tab:Plane", "tab:PRs"},
		},
		{name: "tie keeps registry order", query: "switch tab", want: []string{"tab:PRs", "tab:Plane"}},
		{name: "no matches", query: "zzzz", none: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rank(reg, tt.query, tt.recent)
			if tt.none {
				if len(got) != 0 {
					t.Fatalf("want no results, got %v", ids(got))
				}
				return
			}
			gotIDs := ids(got)
			if len(gotIDs) < len(tt.want) || !slices.Equal(gotIDs[:len(tt.want)], tt.want) {
				t.Fatalf("got %v, want prefix %v", gotIDs, tt.want)
			}
			if tt.groups != nil {
				var gs []string
				for _, it := range got {
					gs = append(gs, it.group)
				}
				if !slices.Equal(gs, tt.groups) {
					t.Fatalf("groups = %v, want %v", gs, tt.groups)
				}
			}
			// Groups are contiguous.
			seen := map[string]bool{}
			for i, it := range got {
				if i > 0 && got[i-1].group != it.group && seen[it.group] {
					t.Fatalf("group %s split: %v", it.group, gotIDs)
				}
				seen[it.group] = true
			}
		})
	}
}

func TestPushRecent(t *testing.T) {
	r := pushRecent(nil, "a")
	r = pushRecent(r, "b")
	r = pushRecent(r, "a")
	if !slices.Equal(r, []string{"a", "b"}) {
		t.Fatalf("got %v", r)
	}
	for i := range 30 {
		r = pushRecent(r, string(rune('A'+i)))
	}
	if len(r) != maxRecent {
		t.Fatalf("len = %d, want %d", len(r), maxRecent)
	}
	if got := mergeRecent([]string{"x", "a"}, []string{"a", "b"}); !slices.Equal(got, []string{"x", "a", "b"}) {
		t.Fatalf("merge = %v", got)
	}
}

// notRegistered are keymap actions that are not palette commands: movement
// and text editing only make sense as keys, force_quit duplicates quit, and
// palette opens the palette itself.
var notRegistered = []string{
	"force_quit", "palette",
	"up", "down", "top", "bottom", "page_down", "page_up",
	"cancel", "accept", "delete_back", "clear_input", "next", "prev",
}

// TestEveryActionIsACommand guards the "every user action is a palette
// command" rule: a new keymap action must be registered or listed above.
func TestEveryActionIsACommand(t *testing.T) {
	m := mustNew(t, config.Default())
	m.active = 1 // a placeholder tab, which contributes list commands
	registered := map[string]bool{}
	for _, it := range m.paletteItems() {
		registered[it.id] = true
		if strings.HasPrefix(it.id, "tab:") {
			registered["goto_tab"] = true
		}
	}
	for _, id := range ui.ActionIDs() {
		if !registered[id] && !slices.Contains(notRegistered, id) {
			t.Errorf("keymap action %q is not a palette command", id)
		}
	}
}

func ctrlK() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl} }

var (
	enter = tea.KeyPressMsg{Code: tea.KeyEnter}
	esc   = tea.KeyPressMsg{Code: tea.KeyEscape}
)

// typeText sends each character of s as a key press.
func typeText(s string) []tea.KeyPressMsg {
	out := make([]tea.KeyPressMsg, 0, len(s))
	for _, r := range s {
		out = append(out, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return out
}

// runPalette opens the palette, types query and presses enter.
func runPalette(t *testing.T, model tea.Model, query string) (tea.Model, tea.Cmd) {
	t.Helper()
	model, _ = model.Update(ctrlK())
	if model.(Model).overlay != overlayCommands {
		t.Fatal("ctrl+k should open the palette")
	}
	for _, k := range typeText(query) {
		var cmd tea.Cmd
		model, cmd = model.Update(k)
		if cmd != nil && isQuit(cmd()) {
			t.Fatalf("typing %q quit forge", query)
		}
	}
	return model.Update(enter)
}

func twoProjects() *config.Config {
	cfg := config.Default()
	cfg.Projects = []config.Project{
		{Name: "alpha", Repo: "daan3011/alpha"},
		{Name: "beta", Repo: "daan3011/beta"},
	}
	return cfg
}

// TestPaletteQuitFlow is part of the P0.6 "done when".
func TestPaletteQuitFlow(t *testing.T) {
	tm := teatest.NewTestModel(t, mustNew(t, config.Default()), teatest.WithInitialTermSize(100, 30))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("forge"))
	}, teatest.WithDuration(2*time.Second))
	tm.Send(ctrlK())
	for _, k := range typeText("quit") {
		tm.Send(k)
	}
	tm.Send(enter)
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}

// TestPaletteSwitchTabAndProjectFlow is the rest of the P0.6 "done when".
func TestPaletteSwitchTabAndProjectFlow(t *testing.T) {
	tm := teatest.NewTestModel(t, mustNew(t, twoProjects()), teatest.WithInitialTermSize(100, 30))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("forge"))
	}, teatest.WithDuration(2*time.Second))
	for _, q := range []string{"plane", "switch project beta"} {
		tm.Send(ctrlK())
		for _, k := range typeText(q) {
			tm.Send(k)
		}
		tm.Send(enter)
	}
	tm.Send(press("q"))

	m := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second)).(Model)
	if m.active != 3 {
		t.Fatalf("active = %d, want 3 (Plane)", m.active)
	}
	if m.project == nil || m.project.Name != "beta" {
		t.Fatalf("project = %v, want beta", m.project)
	}
	view := ansi.Strip(m.render())
	for _, want := range []string{"Plane", "beta", "project: beta"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestPaletteAllProjects(t *testing.T) {
	var model tea.Model = mustNew(t, twoProjects())
	model, _ = runPalette(t, model, "all projects")
	if p := model.(Model).project; p != nil {
		t.Fatalf("project = %v, want all (nil)", p.Name)
	}
}

func TestPaletteOverlay(t *testing.T) {
	var model tea.Model = mustNew(t, twoProjects())
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model, _ = model.Update(ctrlK())
	m := model.(Model)
	view := m.render()
	assertScreen(t, view, 100, 30)
	plain := ansi.Strip(view)
	for _, want := range []string{"⚒ Commands", "Quit forge", "▤ Tabs", "esc cancel"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("overlay missing %q:\n%s", want, plain)
		}
	}
	for _, k := range typeText("beta") {
		model, _ = model.Update(k)
	}
	if plain := ansi.Strip(model.(Model).render()); !strings.Contains(plain, "◆ Projects") || !strings.Contains(plain, "daan3011/beta") {
		t.Fatalf("project result missing:\n%s", plain)
	}
	model, _ = model.Update(esc)
	model, _ = model.Update(ctrlK())

	// Keys go to the query, not to the tab or the global handlers.
	for _, k := range typeText("q2") {
		model, _ = model.Update(k)
	}
	m = model.(Model)
	if m.overlay != overlayCommands || m.active != 0 {
		t.Fatalf("keys leaked out of the palette: overlay=%v active=%d", m.overlay, m.active)
	}
	plain = ansi.Strip(m.render())
	if !strings.Contains(plain, "› q2") || !strings.Contains(plain, "no matches") {
		t.Fatalf("query not shown:\n%s", plain)
	}

	// esc closes without running anything.
	model, cmd := model.Update(esc)
	m = model.(Model)
	if m.overlay != overlayNone || cmd != nil || m.active != 0 {
		t.Fatal("esc should only close the palette")
	}
	// ctrl+k toggles.
	model, _ = model.Update(ctrlK())
	model, _ = model.Update(ctrlK())
	if model.(Model).overlay != overlayNone {
		t.Fatal("ctrl+k should close the palette")
	}
	// Enter with no results does nothing.
	model, _ = model.Update(ctrlK())
	for _, k := range typeText("zzzz") {
		model, _ = model.Update(k)
	}
	if _, cmd := model.Update(enter); cmd != nil {
		t.Fatal("enter with no results should do nothing")
	}
}

func TestPaletteFitsEverySize(t *testing.T) {
	for _, sz := range [][2]int{{20, 8}, {40, 12}, {80, 24}, {200, 60}} {
		var model tea.Model = mustNew(t, twoProjects())
		model, _ = model.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		model, _ = model.Update(ctrlK())
		// Walk the selection past the bottom to exercise scrolling.
		for range 40 {
			model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			assertScreen(t, model.(Model).render(), sz[0], sz[1])
		}
	}
}

func TestPaletteNavigation(t *testing.T) {
	var model tea.Model = mustNew(t, config.Default())
	model, _ = model.Update(ctrlK())
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model, _ = model.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	model, _ = model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m := model.(Model)
	if got := m.shown[m.palette.Selected()].id; got != "help" {
		t.Fatalf("selected %q, want help", got)
	}
	model, _ = model.Update(enter)
	if !model.(Model).helpOpen {
		t.Fatal("running help should open the help overlay")
	}
}

func TestPaletteTabCommands(t *testing.T) {
	var model tea.Model = mustNew(t, config.Default())
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model, _ = model.Update(press("2"))
	model, _ = runPalette(t, model, "filter prs")
	view := ansi.Strip(model.(Model).render())
	if !strings.Contains(view, "/ █") {
		t.Fatalf("filter command should start the filter:\n%s", view)
	}
}

func TestPaletteEntityJump(t *testing.T) {
	var model tea.Model = mustNew(t, config.Default())
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model, _ = runPalette(t, model, "pr bump dependencies")
	m := model.(Model)
	if m.active != 1 {
		t.Fatalf("active = %d, want 1 (PRs)", m.active)
	}
	view := ansi.Strip(m.render())
	if !strings.Contains(view, "Title: sample: Bump dependencies") {
		t.Fatalf("entity should be selected:\n%s", view)
	}
	if _, ok := m.tabs[1].(*tabkit.Placeholder); !ok {
		t.Fatal("PRs tab should be a placeholder")
	}
}

// fakeStore is an in-memory KVStore.
type fakeStore struct {
	mu   sync.Mutex
	vals map[string][]byte
	puts map[string]int
	err  error
}

func newFakeStore(vals map[string]string) *fakeStore {
	f := &fakeStore{vals: map[string][]byte{}, puts: map[string]int{}}
	for k, v := range vals {
		f.vals[k] = []byte(v)
	}
	return f
}

func (f *fakeStore) val(key string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.vals[key]
}

func (f *fakeStore) putCount(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.puts[key]
}

func (f *fakeStore) Get(_ context.Context, key string) (store.CacheEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return store.CacheEntry{}, f.err
	}
	v, ok := f.vals[key]
	if !ok {
		return store.CacheEntry{}, store.ErrNotFound
	}
	return store.CacheEntry{Key: key, Value: v}, nil
}

func (f *fakeStore) Put(_ context.Context, key, kind, _ string, value []byte, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	wantKind := map[string]string{recentKey: recentKind, projectKey: projectKind}[key]
	if wantKind == "" || kind != wantKind || ttl != 0 {
		return errors.New("unexpected key, kind or ttl")
	}
	f.vals[key] = value
	f.puts[key]++
	return nil
}

// drain runs cmd and feeds every message it produces back into the model,
// skipping ticks and other slow commands.
func drain(model tea.Model, cmd tea.Cmd) tea.Model {
	if cmd == nil {
		return model
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		for _, c := range b {
			model = drain(model, c)
		}
		return model
	}
	switch msg.(type) {
	case recentsMsg, ui.ErrMsg:
		model, cmd = model.Update(msg)
		return drain(model, cmd)
	}
	return model
}

func TestPaletteRecents(t *testing.T) {
	fr := newFakeStore(map[string]string{recentKey: `["tab:Docs"]`})
	m, err := New(config.Default(), WithStore(fr))
	if err != nil {
		t.Fatal(err)
	}
	var model tea.Model = m
	loaded := loadRecents(fr)()
	model, _ = model.Update(loaded)
	if !slices.Equal(model.(Model).recent, []string{"tab:Docs"}) {
		t.Fatalf("recent = %v", model.(Model).recent)
	}

	model, cmd := runPalette(t, model, "refresh")
	model = drain(model, cmd)
	if n := fr.putCount(recentKey); n != 1 {
		t.Fatalf("puts = %d, want 1", n)
	}
	var saved []string
	if err := json.Unmarshal(fr.val(recentKey), &saved); err != nil || !slices.Equal(saved, []string{"refresh", "tab:Docs"}) {
		t.Fatalf("saved = %s (%v)", fr.val(recentKey), err)
	}

	// Reopening with an empty query lists recents first.
	model, _ = model.Update(ctrlK())
	m = model.(Model)
	if got := ids(m.shown[:2]); !slices.Equal(got, []string{"refresh", "tab:Docs"}) {
		t.Fatalf("first results = %v", got)
	}
	if m.shown[0].group != groupRecent {
		t.Fatalf("group = %q, want Recent", m.shown[0].group)
	}
	if !strings.Contains(ansi.Strip(m.palette.View(60, 20)), "↺ Recent") {
		t.Fatal("recent header missing")
	}
}

func TestLoadRecentsErrors(t *testing.T) {
	if msg := loadRecents(newFakeStore(nil))(); msg != nil {
		t.Fatalf("missing key should be silent, got %v", msg)
	}
	if _, ok := loadRecents(&fakeStore{err: errors.New("disk on fire")})().(ui.ErrMsg); !ok {
		t.Fatal("store error should become an ErrMsg")
	}
	if _, ok := loadRecents(newFakeStore(map[string]string{recentKey: "{"}))().(ui.ErrMsg); !ok {
		t.Fatal("bad JSON should become an ErrMsg")
	}
	if loadRecents(nil) != nil || saveRecents(nil, nil) != nil {
		t.Fatal("no store means no commands")
	}
}
