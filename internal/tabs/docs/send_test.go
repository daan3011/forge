package docs

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// sendKey presses s and returns the messages its command produced, without
// feeding them back (StartSessionMsg is for the root model).
func sendKey(tab *Tab, s string) []tea.Msg {
	_, cmd := tab.Update(press(s))
	return drain(cmd)
}

func startMsg(t *testing.T, msgs []tea.Msg) tabs.Source {
	t.Helper()
	for _, m := range msgs {
		if sm, ok := m.(tabs.StartSessionMsg); ok {
			return sm.Source
		}
	}
	t.Fatalf("no StartSessionMsg in %v", msgs)
	return tabs.Source{}
}

func TestMarkAndSendDocs(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(0, nil)
	pump(tab, tab.Refresh())
	send(tab, "enter") // expand runbooks: runbooks, deploy.md, architecture.md

	send(tab, " ") // on a folder: nothing
	if len(tab.marks) != 0 {
		t.Fatalf("marked a folder: %q", tab.marks)
	}
	send(tab, "j")
	send(tab, " ") // deploy.md, moves down
	send(tab, " ") // architecture.md
	deploy := filepath.Join(f.alpha, "runbooks", "deploy.md")
	arch := filepath.Join(f.alpha, "architecture.md")
	if want := []string{deploy, arch}; !slices.Equal(tab.marks, want) {
		t.Fatalf("marks = %q, want %q", tab.marks, want)
	}
	if v := view(tab); !strings.Contains(v, "● deploy.md") || !strings.Contains(v, "● architecture.md") {
		t.Errorf("marks not shown:\n%s", v)
	}
	if got := tab.StatusDetail(); got != "2 docs marked · s send to agent" {
		t.Errorf("StatusDetail = %q", got)
	}

	src := startMsg(t, sendKey(tab, "s"))
	if !slices.Equal(src.Docs, []string{deploy, arch}) || src.Repo != "o/alpha" || src.Kind != "" {
		t.Errorf("source = %+v", src)
	}
	if len(tab.marks) != 0 || tab.StatusDetail() != "" {
		t.Errorf("marks kept after sending: %q", tab.marks)
	}
}

func TestSendWithoutMarksUsesSelectedDoc(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(1, nil) // bravo: its own repo and docs folder
	pump(tab, tab.Refresh())
	src := startMsg(t, sendKey(tab, "s"))
	if want := []string{filepath.Join(f.bravo, "readme.md")}; !slices.Equal(src.Docs, want) || src.Repo != "o/bravo" {
		t.Errorf("source = %+v", src)
	}

	tab = f.tab(0, nil)
	pump(tab, tab.Refresh()) // the cursor is on the runbooks folder
	msgs := sendKey(tab, "s")
	if len(msgs) != 1 {
		t.Fatalf("msgs = %v", msgs)
	}
	if m, ok := msgs[0].(ui.InfoMsg); !ok || !strings.Contains(m.Text, "select or mark a doc") {
		t.Errorf("msg = %#v", msgs[0])
	}
}

func TestMarksClearAndPrune(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(0, nil)
	pump(tab, tab.Refresh())
	send(tab, "j") // architecture.md
	send(tab, " ")
	send(tab, "esc")
	if len(tab.marks) != 0 {
		t.Errorf("esc kept marks: %q", tab.marks)
	}

	send(tab, "k")
	send(tab, "j")
	send(tab, " ")
	if len(tab.marks) != 1 {
		t.Fatalf("marks = %q", tab.marks)
	}
	if err := os.Remove(tab.marks[0]); err != nil {
		t.Fatal(err)
	}
	pump(tab, tab.Refresh())
	if len(tab.marks) != 0 {
		t.Errorf("mark on a deleted doc survived a rescan: %q", tab.marks)
	}

	send(tab, "enter") // expand runbooks
	send(tab, "j")
	send(tab, " ")
	tab.SetProject(&f.cfg.Projects[1])
	if len(tab.marks) != 0 {
		t.Errorf("project switch kept marks: %q", tab.marks)
	}
}

func TestMarkFromSearch(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(0, nil)
	pump(tab, tab.Refresh())
	send(tab, "/")
	typeText(tab, "deploy")
	send(tab, "enter") // leave the input, keep the results
	send(tab, " ")
	if want := filepath.Join(f.alpha, "runbooks", "deploy.md"); !slices.Equal(tab.marks, []string{want}) {
		t.Errorf("marks = %q", tab.marks)
	}
	if v := view(tab); !strings.Contains(v, "● ") {
		t.Errorf("search result not marked:\n%s", v)
	}
}
