package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// staticCompleter offers fixed candidates for any +token.
type staticCompleter []string

func (s staticCompleter) Complete(text string, cursor int) (Completion, bool) {
	start := strings.LastIndex(text[:cursor], " ") + 1
	if start >= len(text) || text[start] != '+' {
		return Completion{}, false
	}
	end := start + strings.IndexByte(text[start:]+" ", ' ')
	var out []string
	for _, c := range s {
		if strings.HasPrefix(c, text[start+1:cursor]) {
			out = append(out, c)
		}
	}
	return Completion{Start: start, End: end, Sigil: "+", Candidates: out}, true
}

func newTestCapture(c CaptureCompleter) CaptureInput {
	return NewCaptureInput(NewTheme(true, nil), DefaultKeyMap(), c)
}

func typeInto(c *CaptureInput, s string) {
	for _, r := range s {
		c.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestCaptureInputEditing(t *testing.T) {
	c := newTestCapture(nil)
	typeInto(&c, "fix login")
	c.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	typeInto(&c, "> ")
	c.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	c.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	if got := c.Value(); got != "> fix " {
		t.Fatalf("value = %q", got)
	}
	c.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	c.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := c.Value(); got != "> fi " {
		t.Fatalf("value = %q", got)
	}
	if act := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); act != CaptureSubmit {
		t.Errorf("enter = %v", act)
	}
	if act := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}); act != CaptureFullForm {
		t.Errorf("shift+enter = %v", act)
	}
	if act := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}); act != CaptureFullForm {
		t.Errorf("alt+enter = %v", act)
	}
	if act := c.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); act != CaptureCancel {
		t.Errorf("esc = %v", act)
	}
}

func TestCaptureInputCompletion(t *testing.T) {
	c := newTestCapture(staticCompleter{"bug", "build", "ui"})
	typeInto(&c, "fix +b")
	if view := ansi.Strip(c.View(60)); !strings.Contains(view, "+bug  +build") {
		t.Fatalf("suggestions missing:\n%s", view)
	}
	c.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := c.Value(); got != "fix +bug " {
		t.Fatalf("first tab: %q", got)
	}
	c.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := c.Value(); got != "fix +build " {
		t.Fatalf("second tab: %q", got)
	}
	c.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if got := c.Value(); got != "fix +bug " {
		t.Fatalf("shift+tab: %q", got)
	}
}

func TestCaptureInputFix(t *testing.T) {
	c := newTestCapture(nil)
	typeInto(&c, "x +frontnd !high")
	c.SetError(`no label "frontnd" — did you mean frontend?`, &CaptureFix{Start: 2, End: 10, Text: "+frontend"})
	view := ansi.Strip(c.View(80))
	if !strings.Contains(view, "did you mean frontend?") || !strings.Contains(view, "tab to fix") {
		t.Fatalf("message missing:\n%s", view)
	}
	c.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := c.Value(); got != "x +frontend !high" {
		t.Fatalf("fixed = %q", got)
	}
	if strings.Contains(ansi.Strip(c.View(80)), "did you mean") {
		t.Error("message kept after the fix")
	}
}

func TestCaptureInputBusy(t *testing.T) {
	c := newTestCapture(nil)
	typeInto(&c, "x")
	c.SetBusy("creating…")
	typeInto(&c, "y")
	if c.Value() != "x" {
		t.Errorf("typed while busy: %q", c.Value())
	}
	if act := c.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); act != CaptureCancel {
		t.Errorf("esc while busy = %v", act)
	}
}

func TestCaptureInputViewWidth(t *testing.T) {
	c := newTestCapture(nil)
	typeInto(&c, strings.Repeat("long title ", 20)+"#forge")
	for _, line := range strings.Split(c.View(40), "\n") {
		if w := ansi.StringWidth(line); w != 40 {
			t.Errorf("line width %d, want 40: %q", w, ansi.Strip(line))
		}
	}
	if !strings.Contains(ansi.Strip(c.View(40)), "#forge") {
		t.Error("the end of the line (cursor) scrolled out of view")
	}
	empty := newTestCapture(nil)
	if !strings.Contains(ansi.Strip(empty.View(60)), "#project") {
		t.Error("no placeholder")
	}
}
