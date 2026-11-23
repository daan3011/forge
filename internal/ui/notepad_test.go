package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func notePad(t *testing.T) NotePad {
	t.Helper()
	keys, err := NewKeyMap(nil)
	if err != nil {
		t.Fatal(err)
	}
	n := NewNotePad(NewTheme(true, nil), keys)
	n.SetSize(60, 14)
	return n
}

func kp(s string) tea.KeyPressMsg {
	switch s {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func TestNotePadViewKeys(t *testing.T) {
	day := time.Date(2026, 11, 23, 0, 0, 0, 0, time.Local)
	tests := []struct {
		key   string
		today bool
		want  NoteAction
	}{
		{"E", true, NoteEditor},
		{"[", true, NotePrevDay},
		{"]", true, NoteNone}, // never past today
		{"]", false, NoteNextDay},
		{"t", true, NoteNone},
		{"t", false, NoteToday},
		{"s", true, NoteStandup},
		{"y", true, NoteCopy},
		{"q", true, NoteClose},
		{"esc", true, NoteClose},
		{"j", true, NoteNone},
	}
	for _, tt := range tests {
		n := notePad(t)
		n.Load(day, tt.today, "hello")
		if got, _ := n.Update(kp(tt.key)); got != tt.want {
			t.Errorf("%q (today=%v) = %v, want %v", tt.key, tt.today, got, tt.want)
		}
		if n.Editing() {
			t.Errorf("%q started editing", tt.key)
		}
	}
}

func TestNotePadEditing(t *testing.T) {
	n := notePad(t)
	n.Load(time.Now(), true, "a")
	if act, _ := n.Update(kp("e")); act != NoteNone || !n.Editing() {
		t.Fatalf("e: act %v editing %v", act, n.Editing())
	}
	// The pane's view keys type while editing.
	for _, k := range []string{"[", "]", "E", "s", "q", "enter", "x"} {
		if act, _ := n.Update(kp(k)); act != NoteNone {
			t.Errorf("%q while editing = %v", k, act)
		}
	}
	if got := n.Text(); got != "a[]Esq\nx" {
		t.Errorf("text = %q", got)
	}
	if !n.Dirty() || n.Saved() != "a" {
		t.Errorf("dirty %v saved %q", n.Dirty(), n.Saved())
	}
	if act, _ := n.Update(kp("esc")); act != NoteSave || n.Editing() {
		t.Errorf("esc: act %v editing %v", act, n.Editing())
	}
	n.MarkSaved(n.Text())
	if n.Dirty() {
		t.Error("dirty after MarkSaved")
	}
}

func TestNotePadView(t *testing.T) {
	n := notePad(t)
	day := time.Date(2026, 11, 23, 0, 0, 0, 0, time.Local)
	n.Load(day, true, "")
	v := ansi.Strip(n.View())
	for _, want := range []string{"Mon 23 Nov 2026", "today", NoteEmptyText, "s standup"} {
		if !strings.Contains(v, want) {
			t.Errorf("empty view lacks %q:\n%s", want, v)
		}
	}
	if w, h := lineSize(n.View()); w != 60 || h != 14 {
		t.Errorf("view size %dx%d, want 60x14", w, h)
	}

	var lines []string
	for i := range 40 {
		lines = append(lines, "line "+string(rune('a'+i%26)))
	}
	n.Load(day.AddDate(0, 0, -1), false, "# Sun\n"+strings.Join(lines, "\n"))
	v = ansi.Strip(n.View())
	if strings.Contains(v, "today") || !strings.Contains(v, "# Sun") {
		t.Errorf("past day view:\n%s", v)
	}
	n.ScrollToEnd()
	if v := ansi.Strip(n.View()); !strings.Contains(v, "line n") || strings.Contains(v, "# Sun") {
		t.Errorf("scrolled view:\n%s", v)
	}
	n.Update(kp("g"))
	if v := ansi.Strip(n.View()); !strings.Contains(v, "# Sun") {
		t.Errorf("top view:\n%s", v)
	}

	n.Update(kp("e"))
	n.Update(kp("!"))
	if v := ansi.Strip(n.View()); !strings.Contains(v, "●") || !strings.Contains(v, "save & close") {
		t.Errorf("editing view lacks dirty mark / edit help:\n%s", v)
	}
}

func lineSize(s string) (w, h int) {
	ls := strings.Split(s, "\n")
	for _, l := range ls {
		w = max(w, ansi.StringWidth(l))
	}
	return w, len(ls)
}
