package docs

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

type fakeSessions struct {
	sessions.API
	rows []sessions.Row
}

func (f *fakeSessions) List(context.Context, string, bool) ([]sessions.Row, error) {
	return f.rows, nil
}

type fakeLinks struct{ docs map[string][]string } // session id -> doc paths

func (f fakeLinks) For(_ context.Context, ref store.Ref) ([]store.Link, error) {
	var out []store.Link
	for _, p := range f.docs[ref.ID] {
		out = append(out, store.Link{Other: store.DocRef(p)})
	}
	return out, nil
}

func TestFromSessions(t *testing.T) {
	f := newFixture(t)
	base := t.TempDir()
	wt := filepath.Join(base, "wt", "fix")
	dead := filepath.Join(base, "wt", "old")
	wtDocs := filepath.Join(wt, ".forge", "docs")
	// Same as main: not listed. Changed and new: listed.
	writeDoc(t, wtDocs, "runbooks/deploy.md", "# Deploy\n\nRun **make deploy**.\n")
	writeDoc(t, wtDocs, "architecture.md", "# Architecture v2\n")
	retries := writeDoc(t, wtDocs, "notes/retries.md", "# Retries\n\nBack off **exponentially**.\n")
	writeDoc(t, filepath.Join(dead, ".forge", "docs"), "gone.md", "x")

	svc := &fakeSessions{rows: []sessions.Row{
		{Session: store.Session{ID: 7, Name: "fix", Project: "alpha", Worktree: wt, Status: store.StatusWorking}},
		{Session: store.Session{ID: 8, Name: "old", Project: "alpha", Worktree: dead, Status: store.StatusDead}},
		{Session: store.Session{ID: 9, Name: "other", Project: "bravo", Worktree: wt, Status: store.StatusIdle}},
	}}
	d := tabkit.Deps{
		Theme: ui.NewTheme(true, nil), Keys: ui.DefaultKeyMap(), Config: f.cfg,
		Sessions: svc, Links: fakeLinks{docs: map[string][]string{strconv.Itoa(7): {retries}}},
	}
	tab := newTab(d)
	tab.now = func() time.Time { return now }
	edited := 0
	tab.edit = func(string) tea.Cmd { edited++; return nil }
	tab.SetProject(&f.cfg.Projects[0])
	tab.SetSize(120, 30)
	pump(tab, tab.Init())

	names := strings.Join(rowNames(tab), "|")
	for _, want := range []string{"From sessions", " fix", "  notes/retries.md", "  architecture.md"} {
		if !strings.Contains(names, want) {
			t.Errorf("rows lack %q: %s", want, names)
		}
	}
	for _, not := range []string{"old", "gone.md", "other", "  runbooks/deploy.md"} {
		if strings.Contains(names, "|"+not) || strings.Contains(names, " "+not+"|") {
			t.Errorf("rows show %q: %s", not, names)
		}
	}
	v := view(tab)
	if !strings.Contains(v, "✎ new") || !strings.Contains(v, "changed") {
		t.Errorf("view lacks status marks:\n%s", v)
	}

	// Select the agent's doc and open it: rendered with the same renderer,
	// labelled with the session, read-only.
	for i, r := range tab.rows {
		if r.node.Path == retries {
			tab.setCursor(i)
		}
	}
	pump(tab, tab.syncDetail())
	v = view(tab)
	for _, want := range []string{"from session fix", "read-only", "Back off exponentially"} {
		if !strings.Contains(v, want) {
			t.Errorf("detail lacks %q:\n%s", want, v)
		}
	}
	// Edit, rename, trash, new and send are refused.
	for _, k := range []string{"e", "m", "D", "n", "s", "S"} {
		_, cmd := tab.Update(tea.KeyPressMsg{Code: []rune(k)[0], Text: k})
		msgs := drain(cmd)
		if len(msgs) != 1 {
			t.Errorf("%q: %#v", k, msgs)
			continue
		}
		if im, ok := msgs[0].(ui.InfoMsg); !ok || !strings.Contains(im.Text, "read-only") {
			t.Errorf("%q: %#v", k, msgs[0])
		}
	}
	if edited != 0 || tab.dialog != nil {
		t.Error("an edit went through")
	}
	if _, err := os.Stat(retries); err != nil {
		t.Error("doc touched")
	}
}
