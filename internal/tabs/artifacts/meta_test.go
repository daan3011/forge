package artifacts

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, drain(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// pump runs cmd, feeding the tab's own messages back until none are left,
// and returns the toasts.
func pump(tab *Tab, cmd tea.Cmd) (toasts []string) {
	queue := drain(cmd)
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		switch m := msg.(type) {
		case ui.SuccessMsg:
			toasts = append(toasts, "ok: "+m.Text)
		case ui.InfoMsg:
			toasts = append(toasts, "info: "+m.Text)
		case ui.ErrMsg:
			toasts = append(toasts, "err: "+m.Err.Error())
		case tabs.TargetedMsg:
			_, next := tab.Update(m)
			queue = append(queue, drain(next)...)
		}
	}
	return toasts
}

func (f *fixture) copyPath() string {
	return filepath.Join(f.alpha, ".forge", "artifacts", "s1", "mock.html")
}

func TestPinWorktreeFileMovesToCopy(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(-1, nil)
	refresh(t, tab)
	ctx := context.Background()
	orig := filepath.Join(f.wt, "design", "mock.html")
	if err := f.st.Artifacts().SetTags(ctx, orig, []string{"design"}); err != nil {
		t.Fatal(err)
	}
	refresh(t, tab)

	// mock.html (worktree) is selected: pinning keeps a copy and pins it.
	toasts := pump(tab, press(tab, "p"))
	if len(toasts) != 1 || !strings.Contains(toasts[0], "pinned · copied to .forge/artifacts/s1/mock.html") {
		t.Fatalf("toasts = %q", toasts)
	}
	cp := f.copyPath()
	a, ok := tab.selected()
	if !ok || a.Path != cp || !a.Pinned || !slices.Equal(a.Tags, []string{"design"}) || a.Link == nil || !a.Link.Active() {
		t.Fatalf("selected after pin = %+v", a)
	}
	// The copy sorts first in its group; the original is left plain.
	if got := names(tab); !slices.Equal(got[:2], []string{"# alpha", "mock.html"}) || tab.items[tab.rows[1].item].Path != cp {
		t.Fatalf("rows = %v", got)
	}
	for _, x := range tab.items {
		if x.Path == orig && (x.Pinned || len(x.Tags) > 0) {
			t.Errorf("original kept meta: %+v", x)
		}
	}
	v := view(tab)
	for _, s := range []string{"★ ◫ mock.html", "Tags     #design", "Pinned   ★", "Synced   from s1"} {
		if !strings.Contains(v, s) {
			t.Errorf("view lacks %q:\n%s", s, v)
		}
	}

	// An edit to the original reaches the copy on the next refresh.
	writeFile(t, orig, "<h1>mock v2</h1>", now.Add(-time.Minute))
	refresh(t, tab)
	if b, _ := os.ReadFile(cp); string(b) != "<h1>mock v2</h1>" {
		t.Errorf("copy = %q", b)
	}

	// Unpin: no star, sync stops.
	pump(tab, press(tab, "p"))
	if a, _ := tab.selected(); a.Pinned || a.Link == nil || a.Link.Active() || a.Link.DetachReason != reasonManual {
		t.Errorf("after unpin = %+v / %+v", a, a.Link)
	}
}

func TestPinRepoFileInPlace(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(1, nil) // bravo: shot.png
	refresh(t, tab)
	toasts := pump(tab, press(tab, "p"))
	if !slices.Equal(toasts, []string{"ok: pinned shot.png"}) {
		t.Fatalf("toasts = %q", toasts)
	}
	if a, _ := tab.selected(); a.Path != filepath.Join(f.bravo, "shot.png") || !a.Pinned || a.Link != nil {
		t.Errorf("selected = %+v", a)
	}
	if _, err := os.Stat(filepath.Join(f.bravo, ".forge")); !os.IsNotExist(err) {
		t.Errorf("repo file was copied: %v", err)
	}
}

func TestSyncDetaches(t *testing.T) {
	tests := []struct {
		name   string
		change func(f *fixture)
		reason string
	}{
		{"worktree removed", func(f *fixture) { _ = os.RemoveAll(f.wt) }, reasonRemoved},
		{"session archived", func(f *fixture) {
			s := f.sess.live[0]
			s.Status = store.StatusArchived
			f.sess.live, f.sess.archived = nil, []store.Session{s}
		}, reasonArchived},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			tab := f.tab(-1, nil)
			refresh(t, tab)
			pump(tab, press(tab, "p"))
			tt.change(f)
			orig := filepath.Join(f.wt, "design", "mock.html")
			if _, err := os.Stat(f.wt); err == nil {
				writeFile(t, orig, "changed after archive", now)
			}
			refresh(t, tab)
			tab.Select(f.copyPath())
			a, _ := tab.selected()
			if a.Path != f.copyPath() || a.Link == nil || a.Link.Active() || a.Link.DetachReason != tt.reason {
				t.Fatalf("link = %+v", a.Link)
			}
			if b, _ := os.ReadFile(f.copyPath()); string(b) != "<h1>mock</h1>" {
				t.Errorf("final copy = %q", b)
			}
			if !strings.Contains(view(tab), "final ("+tt.reason+")") {
				t.Errorf("detail lacks final:\n%s", view(tab))
			}
		})
	}
}

func TestDetachCommand(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(-1, nil)
	refresh(t, tab)
	run := func(id string) []string {
		for _, c := range tab.Commands() {
			if c.ID == id {
				return pump(tab, c.Run())
			}
		}
		t.Fatalf("no command %s", id)
		return nil
	}
	if got := run("artifacts_detach"); len(got) != 1 || !strings.Contains(got[0], "isn't synced") {
		t.Errorf("detach unlinked = %q", got)
	}
	run("artifacts_pin")
	if got := run("artifacts_detach"); !slices.Equal(got, []string{"ok: detached mock.html from s1"}) {
		t.Errorf("detach = %q", got)
	}
	writeFile(t, filepath.Join(f.wt, "design", "mock.html"), "later", now)
	refresh(t, tab)
	if b, _ := os.ReadFile(f.copyPath()); string(b) != "<h1>mock</h1>" {
		t.Errorf("detached copy synced: %q", b)
	}
	if a, _ := tab.selected(); !a.Pinned || a.Link.DetachReason != reasonManual {
		t.Errorf("after detach = %+v", a)
	}
}

func TestFilters(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	idx := f.st.Artifacts()
	tab := f.tab(-1, nil)
	refresh(t, tab)
	flow := filepath.Join(f.alpha, "docs", "flow.svg")
	shot := filepath.Join(f.bravo, "shot.png")
	for _, err := range []error{
		idx.SetTags(ctx, flow, []string{"design", "diagram"}),
		idx.SetTags(ctx, shot, []string{"design"}),
		idx.SetPinned(ctx, shot, "bravo", true),
		idx.SetTitle(ctx, shot, "Checkout screenshot"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	refresh(t, tab)
	if !slices.Equal(tab.allTags, []string{"design", "diagram"}) {
		t.Errorf("allTags = %v", tab.allTags)
	}
	if !strings.Contains(view(tab), "★ ▣ Checkout screenshot") {
		t.Errorf("pinned title not shown:\n%s", view(tab))
	}

	tests := []struct {
		name   string
		filt   filters
		want   []string
		status string
	}{
		{"none", filters{}, []string{"# alpha", "mock.html", "flow.svg", "# bravo", "shot.png"}, "3 artifacts"},
		{"tag", filters{tag: "design"}, []string{"# alpha", "flow.svg", "# bravo", "shot.png"}, "2 of 3 artifacts · #design"},
		{"tag diagram", filters{tag: "diagram"}, []string{"# alpha", "flow.svg"}, "1 of 3 artifacts · #diagram"},
		{"pinned", filters{pinned: true}, []string{"# bravo", "shot.png"}, "1 of 3 artifacts · pinned only"},
		{"both, none match", filters{tag: "diagram", pinned: true}, nil, "0 of 3 artifacts · #diagram · pinned only"},
	}
	for _, tt := range tests {
		tab.setFilters(tt.filt)
		if got := names(tab); !slices.Equal(got, tt.want) {
			t.Errorf("%s: rows = %v, want %v", tt.name, got, tt.want)
		}
		if got := tab.StatusDetail(); got != tt.status {
			t.Errorf("%s: status = %q, want %q", tt.name, got, tt.status)
		}
	}
	if v := view(tab); !strings.Contains(v, "No artifacts match #diagram · pinned only (x clears)") {
		t.Errorf("empty filter view:\n%s", v)
	}

	// P toggles pinned only, x clears everything.
	tab.setFilters(filters{})
	press(tab, "P")
	if got := names(tab); !slices.Equal(got, []string{"# bravo", "shot.png"}) {
		t.Errorf("after P = %v", got)
	}
	press(tab, "x")
	if tab.filt.any() || len(tab.rows) != 5 {
		t.Errorf("after x = %+v %v", tab.filt, names(tab))
	}

	// Pins float to the top of a project's list.
	if err := idx.SetPinned(ctx, flow, "alpha", true); err != nil {
		t.Fatal(err)
	}
	refresh(t, tab)
	if got := names(tab); !slices.Equal(got, []string{"# alpha", "flow.svg", "mock.html", "# bravo", "shot.png"}) {
		t.Errorf("pinned first = %v", got)
	}

	// Palette entities use titles and tags; selecting a filtered-out one
	// clears the filter.
	var shotEnt tabs.Entity
	for _, e := range tab.Entities() {
		if e.ID == shot {
			shotEnt = e
		}
	}
	if shotEnt.Title != "Checkout screenshot" || !strings.Contains(shotEnt.Search, "#design") {
		t.Errorf("entity = %+v", shotEnt)
	}
	tab.setFilters(filters{tag: "diagram"})
	tab.Select(shot)
	if a, _ := tab.selected(); a.Path != shot || tab.filt.any() {
		t.Errorf("Select through filter = %s, %+v", a.Path, tab.filt)
	}
}

func TestPaletteCommandsP46(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(-1, nil)
	ids := map[string]bool{}
	for _, c := range tab.Commands() {
		ids[c.ID] = true
	}
	for _, id := range []string{
		"artifacts_pin", "artifacts_tags", "artifacts_rename", "artifacts_filters",
		"artifacts_pinned_only", "artifacts_clear_filters", "artifacts_detach",
	} {
		if !ids[id] {
			t.Errorf("no palette command %s", id)
		}
	}
}

// TestPinTagRenameFlow is the P4.6 "done when", driven like a user: pin,
// tag with completion, rename and filter; then a fresh tab on the same
// database (a restart) shows it all again.
func TestPinTagRenameFlow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.st.Artifacts().SetTags(ctx, filepath.Join(f.bravo, "shot.png"), []string{"review"}); err != nil {
		t.Fatal(err)
	}
	tab := f.tab(-1, nil)
	h := &host{tab: tab}
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(120, 30))
	h.wait(t, "mock.html")
	key := func(s string) {
		switch s {
		case "enter":
			tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
		case "ctrl+e":
			tm.Send(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
		case "ctrl+u":
			tm.Send(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
		default:
			tm.Send(tea.KeyPressMsg{Code: []rune(s)[0], Text: s})
		}
	}

	key("p")
	h.wait(t, "Synced   from s1")
	key("t")
	h.wait(t, "ctrl+e completes")
	tm.Type("UI, r")
	h.wait(t, "> UI, review") // the completion, shown once it loads
	key("ctrl+e")             // accepts it
	key("enter")
	h.wait(t, "#review #ui")
	key("m")
	h.wait(t, "Rename mock.html")
	key("ctrl+u")
	tm.Type("Checkout mock")
	key("enter")
	h.wait(t, "★ ◫ Checkout mock")
	key("P")
	h.waitGone(t, "flow.svg")
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	if got := tab.StatusDetail(); got != "1 of 4 artifacts · pinned only" {
		t.Errorf("status = %q", got)
	}

	// Restart: a new tab over the same database.
	again := f.tab(-1, nil)
	refresh(t, again)
	a, ok := again.selected()
	if !ok || a.Path != f.copyPath() || !a.Pinned || a.Title != "Checkout mock" ||
		!slices.Equal(a.Tags, []string{"review", "ui"}) {
		t.Fatalf("after restart = %+v", a)
	}
	again.setFilters(filters{tag: "ui"})
	if got := names(again); !slices.Equal(got, []string{"# alpha", "mock.html"}) {
		t.Errorf("tag filter after restart = %v", got)
	}
}

// waitGone waits until the frame no longer shows s.
func (h *host) waitGone(t *testing.T, s string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		frame := h.frame
		h.mu.Unlock()
		if !strings.Contains(frame, s) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q to go\nframe:\n%s", s, frame)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
