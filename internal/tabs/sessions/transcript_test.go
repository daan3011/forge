package sessions

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/sessions/transcript"
	"github.com/daan3011/forge/internal/store"
)

// fixtureTranscript parses the transcript parser's own fixture.
func fixtureTranscript(t *testing.T) transcript.Transcript {
	t.Helper()
	tr, err := transcript.NewCache().Load("../../sessions/transcript/testdata/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func quit(t *testing.T, sc *screen) {
	t.Helper()
	sc.tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	sc.tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestTranscriptViewer(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusWorking))
	svc.setTranscript("alpha", fixtureTranscript(t))
	tab := newTab(svc)
	sc := newScreen(t, &host{tab: tab})
	sc.wait(t, "alpha")

	sc.tm.Send(press("t"))
	// Header: counts, files touched relative to nothing (fixture paths are
	// outside /wt/alpha), tokens from usage.
	sc.wait(t, "messages 3", "tools 3", "files 2: /repo/README.md, /repo/main.go", "in 34 · out 16 · cache 100")
	// Entries: prompt, rendered markdown, collapsed tool calls, notices.
	sc.wait(t, "you", "Add a README", "claude", "▸ ⚒ Write  /repo/README.md ✓", "▸ ⚒ Bash  Run tests ✗", "── /clear ──", "Done.")
	if !tab.CapturingInput() {
		t.Error("viewer should capture input")
	}
	if strings.Contains(sc.h.frame, "File created") {
		t.Error("tool result shown while collapsed")
	}

	// The cursor starts on the last entry; walk up to the Write call and
	// expand it.
	for range 5 {
		sc.tm.Send(press("k"))
	}
	sc.tm.Send(press("enter"))
	sc.wait(t, "▾ ⚒ Write", "│ # hi", "├─ result", "│ File created")

	// e expands every call: the Bash result and Edit diff appear.
	sc.tm.Send(press("e"))
	sc.wait(t, "│ FAIL foo", "│ - a", "│ + b")

	// esc closes, and enter attaches again rather than toggling.
	sc.tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	sc.wait(t, "agent/alpha (from main)")
	if tab.CapturingInput() {
		t.Error("still capturing input after close")
	}
	sc.tm.Send(press("enter"))
	sc.until(t, "attach", func(string, []string) bool {
		_, _, attaches := svc.attachCalls()
		return slices.Equal(attaches, []string{"alpha"})
	})
	quit(t, sc)
}

func TestTranscriptViewerLiveRefresh(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusWorking))
	tr := fixtureTranscript(t)
	svc.setTranscript("alpha", tr)
	sc := newScreen(t, &host{tab: newTab(svc)})
	sc.wait(t, "alpha")
	sc.tm.Send(press("t"))
	sc.wait(t, "Done.", "following")

	// New entries are picked up by the poll and, while following, shown.
	tr.Entries = append(slices.Clone(tr.Entries), transcript.Entry{Kind: transcript.User, Text: "now ship it"})
	tr.Summary.Messages++
	svc.setTranscript("alpha", tr)
	sc.wait(t, "now ship it", "messages 4")
	quit(t, sc)
}

func TestTranscriptMissing(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusStarting))
	tab := newTab(svc)
	sc := newScreen(t, &host{tab: tab})
	sc.wait(t, "alpha")
	sc.tm.Send(press("t"))
	sc.toast(t, "info: alpha: no transcript yet")
	sc.wait(t, "agent/alpha (from main)")
	if tab.CapturingInput() {
		t.Error("viewer left open without a transcript")
	}
	quit(t, sc)
}

func TestTranscriptPaletteCommand(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusWorking))
	svc.setTranscript("alpha", fixtureTranscript(t))
	tab := newTab(svc)
	var run func() tea.Cmd
	for _, c := range tab.Commands() {
		if c.ID == "session_transcript" {
			run = c.Run
		}
	}
	if run == nil {
		t.Fatal("no session_transcript command")
	}
	sc := newScreen(t, &host{tab: tab})
	sc.wait(t, "alpha")
	sc.tm.Send(runMsg(run))
	sc.wait(t, "messages 3")

	// While open, every viewer action except movement is a command.
	byID := map[string]func() tea.Cmd{}
	for _, c := range tab.Commands() {
		byID[c.ID] = c.Run
	}
	for _, id := range []string{"transcript_toggle", "transcript_toggle_all", "transcript_follow", "transcript_close"} {
		if byID[id] == nil {
			t.Errorf("no %s command while the viewer is open", id)
		}
	}
	sc.tm.Send(runMsg(byID["transcript_toggle_all"]))
	sc.wait(t, "│ FAIL foo")
	sc.tm.Send(runMsg(byID["transcript_close"]))
	sc.wait(t, "agent/alpha (from main)")
	quit(t, sc)
}

func TestCapLines(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want []string
	}{
		{"a\nb\n", 5, []string{"a", "b"}},
		{"a\nb\nc\nd", 2, []string{"a", "b", "… 2 more lines"}},
		{"\tx", 5, []string{"    x"}},
	}
	for _, tt := range tests {
		if got := capLines(tt.in, tt.n); !slices.Equal(got, tt.want) {
			t.Errorf("capLines(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
		}
	}
}

func TestShortFiles(t *testing.T) {
	files := []string{"/wt/a/x.go", "/wt/a/y.go", "/wt/a/z.go", "/wt/a/q.go", "/wt/a/r.go", "/other/s.go"}
	if got, want := shortFiles(files, "/wt/a"), "x.go, y.go, z.go, q.go, +2"; got != want {
		t.Errorf("shortFiles = %q, want %q", got, want)
	}
}
