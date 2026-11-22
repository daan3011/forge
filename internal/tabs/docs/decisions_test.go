package docs

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	docfs "github.com/daan3011/forge/internal/docs"
	"github.com/daan3011/forge/internal/ui"
)

// logDecisions writes decisions under root, one day apart, oldest first.
func logDecisions(t *testing.T, root string, titles ...string) []docfs.Decision {
	t.Helper()
	var out []docfs.Decision
	for i, title := range titles {
		d, err := docfs.NewDecision(root, docfs.DecisionInput{Title: title, Decision: "we do " + title}, now.AddDate(0, 0, i-len(titles)))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

// feed sends msg and every message its commands produce back to the tab
// (huh's own included), returning the toasts.
func feed(tab *Tab, msg tea.Msg) (toasts []string) {
	queue := []tea.Msg{msg}
	for i := 0; len(queue) > 0 && i < 100; i++ {
		m := queue[0]
		queue = queue[1:]
		switch m := m.(type) {
		case ui.SuccessMsg:
			toasts = append(toasts, "ok: "+m.Text)
			continue
		case ui.ErrMsg:
			toasts = append(toasts, "err: "+m.Err.Error())
			continue
		case nil, tea.BatchMsg:
			continue
		}
		_, cmd := tab.Update(m)
		queue = append(queue, drainAll(cmd)...)
	}
	return toasts
}

// drainAll is drain, but also unpacks sequences and skips blocking
// commands that never return (cursor blink).
func drainAll(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(200 * time.Millisecond):
		return nil
	}
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, drainAll(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func decisionLabels(tab *Tab) []string {
	var out []string
	for _, r := range tab.rows {
		if r.dec == nil {
			out = append(out, "not a decision: "+r.node.Name)
			continue
		}
		out = append(out, r.dec.Label())
	}
	return out
}

func TestDecisionsViewNewestFirst(t *testing.T) {
	f := newFixture(t)
	logDecisions(t, f.alpha, "Use Go", "Use Charm", "Use SQLite")
	tab := f.tab(0, nil)
	pump(tab, tab.Refresh())

	send(tab, "d")
	want := []string{"0003. Use SQLite", "0002. Use Charm", "0001. Use Go"}
	if got := decisionLabels(tab); !slices.Equal(got, want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}
	v := view(tab)
	for _, s := range []string{"0003  Use SQLite", "accepted", "date: 2026-11-21", "we do Use SQLite", "status: accepted"} {
		if !strings.Contains(v, s) {
			t.Errorf("view lacks %q:\n%s", s, v)
		}
	}
	if strings.Contains(v, "---") {
		t.Errorf("front matter rendered as text:\n%s", v)
	}
	if got := tab.StatusDetail(); got != "decisions · 3 logged" {
		t.Errorf("status = %q", got)
	}

	// Back to the tree keeps the decision selected, its folder opened.
	send(tab, "j")
	send(tab, "d")
	if r, ok := tab.selected(); !ok || r.node.Name != "0002-use-charm.md" {
		t.Errorf("tree selection = %+v", r.node)
	}
}

func TestDecisionsViewAllProjectsAndEmpty(t *testing.T) {
	f := newFixture(t)
	logDecisions(t, f.alpha, "Alpha one")
	logDecisions(t, f.bravo, "Bravo one", "Bravo two")
	tab := f.tab(-1, nil)
	pump(tab, tab.Refresh())
	send(tab, "d")
	if got, want := decisionLabels(tab), []string{"0001. Alpha one", "0002. Bravo two", "0001. Bravo one"}; !slices.Equal(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
	if v := view(tab); !strings.Contains(v, "bravo: Bravo two") {
		t.Errorf("no project column:\n%s", v)
	}

	empty := newFixture(t)
	tab = empty.tab(1, nil)
	pump(tab, tab.Refresh())
	send(tab, "d")
	if v := view(tab); !strings.Contains(v, "No decisions logged in this project") || !strings.Contains(v, "a  log one") {
		t.Errorf("empty view:\n%s", v)
	}
}

func TestSupersedeFromDecisionsView(t *testing.T) {
	f := newFixture(t)
	ds := logDecisions(t, f.alpha, "Use Postgres", "Use MySQL", "Use SQLite")
	tab := f.tab(0, nil)
	pump(tab, tab.Refresh())

	// Outside the decisions view: nothing to supersede.
	if toasts := send(tab, "S"); len(toasts) != 1 || !strings.Contains(toasts[0], "decisions view") {
		t.Errorf("toasts = %q", toasts)
	}
	send(tab, "d")
	// The newest has nothing newer.
	if toasts := send(tab, "S"); len(toasts) != 1 || !strings.Contains(toasts[0], "no newer decision") {
		t.Errorf("toasts = %q", toasts)
	}
	send(tab, "G") // 0001
	send(tab, "S")
	if tab.dialog == nil {
		t.Fatal("no dialog")
	}
	if v := view(tab); !strings.Contains(v, "Supersede 0001. Use Postgres with") || !strings.Contains(v, "0003. Use SQLite") {
		t.Fatalf("dialog:\n%s", v)
	}
	toasts := feed(tab, tea.KeyPressMsg{Code: tea.KeyEnter}) // the first option: the newest, 0003
	if !slices.Contains(toasts, "ok: 0001 superseded by 0003") {
		t.Fatalf("toasts = %q", toasts)
	}
	b, _ := os.ReadFile(ds[0].Path)
	if !strings.Contains(string(b), "status: superseded\nsuperseded_by: 0003-use-sqlite.md\n") ||
		!strings.Contains(string(b), "> Superseded by [0003. Use SQLite](0003-use-sqlite.md)") {
		t.Errorf("old decision:\n%s", b)
	}
	if r, ok := tab.selected(); !ok || r.dec == nil || !r.dec.Superseded() {
		t.Fatalf("selection after = %+v", r.dec)
	}
	if v := view(tab); !strings.Contains(v, "→ 0003") {
		t.Errorf("row lacks the link:\n%s", v)
	}
	// Superseded already.
	if toasts := send(tab, "S"); len(toasts) != 1 || !strings.Contains(toasts[0], "already superseded") {
		t.Errorf("toasts = %q", toasts)
	}
}

// TestLogDecisionFlow: a, title, enter through to the decision, submit;
// the decision is written, selected, and CLAUDE.md points at the log.
func TestLogDecisionFlow(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(0, nil)
	sc := newScreen(t, tab)
	sc.wait(t, "architecture.md")

	sc.tm.Send(press("a"))
	sc.wait(t, "Log a decision", "Title", "Decision", "Consequences")
	step := func(text string) {
		if text != "" {
			sc.tm.Type(text)
		}
		sc.tm.Send(press("enter"))
		time.Sleep(150 * time.Millisecond) // huh moves to the next field in a Cmd
	}
	step("Use SQLite for the cache")
	step("Need a local cache")
	step("modernc sqlite, no cgo")
	step("")
	step("")
	sc.toast(t, "ok: logged 0001. Use SQLite for the cache; CLAUDE.md now points agents at the decision log")
	sc.wait(t, "0001-use-sqlite-for-the-cache.md", "Need a local cache")

	b, err := os.ReadFile(filepath.Join(f.alpha, "decisions", "0001-use-sqlite-for-the-cache.md"))
	if err != nil || !strings.Contains(string(b), "## Decision\n\nmodernc sqlite, no cgo\n") || strings.Contains(string(b), "Alternatives") {
		t.Fatalf("decision: %q, %v", b, err)
	}
	claude, _ := os.ReadFile(filepath.Join(f.cfg.Projects[0].Path, "CLAUDE.md"))
	if !strings.Contains(string(claude), ".forge/docs/decisions/") {
		t.Errorf("CLAUDE.md = %q", claude)
	}
	if len(f.editedPaths()) != 0 {
		t.Errorf("editor opened: %q", f.editedPaths())
	}
	sc.quit(t)
}

func TestLogDecisionMsgOpensForm(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(-1, nil)
	pump(tab, tab.Refresh())
	pump(tab, func() tea.Msg { return LogDecisionMsg{} })
	if tab.dialog == nil || !strings.Contains(view(tab), "Project") {
		t.Fatalf("no form with a project choice:\n%s", view(tab))
	}
}
