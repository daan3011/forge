package ui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Completion is what the token under the cursor of a CaptureInput can
// become: replace [Start, End) with Sigil + one of Candidates.
type Completion struct {
	Start, End int
	Sigil      string
	Candidates []string
}

// CaptureCompleter offers completions for the capture line; ok is false
// when the cursor isn't in a token. Offsets are bytes.
type CaptureCompleter interface {
	Complete(text string, cursor int) (Completion, bool)
}

// CaptureFix is an edit tab applies: "did you mean …" for an unknown
// token. Offsets are bytes.
type CaptureFix struct {
	Start, End int
	Text       string
}

// CaptureAction is what a key press asks the CaptureInput's owner to do.
type CaptureAction int

// Capture actions.
const (
	CaptureNone     CaptureAction = iota
	CaptureChanged                // the text changed
	CaptureSubmit                 // create the item
	CaptureFullForm               // open the full form instead
	CaptureCancel                 // close
)

// captureSigils start tokens; kept in sync with internal/capture.
const captureSigils = "#!@+"

// CapturePlaceholder is the capture line's hint while it is empty.
const CapturePlaceholder = "title  #project  !priority  @me  +label"

// captureRows is the rows View takes: the line, completions, a message.
const captureRows = 3

// CaptureInput is the quick capture line (P3.5): a one-line editor with
// token highlighting, tab completion and a message row. The owner
// resolves and submits; Update only edits and reports what was asked.
type CaptureInput struct {
	theme     *Theme
	keys      *KeyMap
	completer CaptureCompleter

	text   string
	cursor int // byte offset

	cycle *captureCycle // tab's candidates while tab is pressed in a row

	msg   string
	isErr bool
	fix   *CaptureFix
	busy  string // shown in place of the message while submitting
}

// captureCycle is tab cycling through a token's candidates: the token's
// start, the end of what tab inserted, and the candidate shown.
type captureCycle struct {
	comp Completion
	end  int
	i    int
}

// NewCaptureInput returns an empty capture line.
func NewCaptureInput(theme *Theme, keys *KeyMap, c CaptureCompleter) CaptureInput {
	return CaptureInput{theme: theme, keys: keys, completer: c}
}

// SetCompleter replaces the completer (when the catalog changes).
func (c *CaptureInput) SetCompleter(cc CaptureCompleter) { c.completer = cc }

// Reset empties the line and its message.
func (c *CaptureInput) Reset() {
	c.text, c.cursor, c.cycle = "", 0, nil
	c.clearMessage()
	c.busy = ""
}

// Value returns the text.
func (c *CaptureInput) Value() string { return c.text }

// SetValue replaces the text and puts the cursor at its end.
func (c *CaptureInput) SetValue(s string) {
	c.text, c.cursor, c.cycle = s, len(s), nil
}

// SetError shows err's message; fix, if set, is what tab applies.
func (c *CaptureInput) SetError(msg string, fix *CaptureFix) {
	c.msg, c.isErr, c.fix, c.busy = msg, true, fix, ""
}

// SetInfo shows a muted message.
func (c *CaptureInput) SetInfo(msg string) {
	c.msg, c.isErr, c.fix = msg, false, nil
}

// SetBusy shows text (a spinner and what is happening) in the message
// row; "" clears it.
func (c *CaptureInput) SetBusy(text string) { c.busy = text }

// Busy reports whether SetBusy is showing something.
func (c *CaptureInput) Busy() bool { return c.busy != "" }

func (c *CaptureInput) clearMessage() { c.msg, c.isErr, c.fix = "", false, nil }

// Update applies a key press. While busy only cancel works.
func (c *CaptureInput) Update(msg tea.KeyPressMsg) CaptureAction {
	ck, in := c.keys.Capture, c.keys.Input
	if key.Matches(msg, in.Cancel) {
		return CaptureCancel
	}
	if c.busy != "" {
		return CaptureNone
	}
	if key.Matches(msg, ck.Complete) {
		return c.complete(1)
	}
	if key.Matches(msg, ck.CompletePrev) {
		return c.complete(-1)
	}
	c.cycle = nil
	switch {
	case key.Matches(msg, ck.FullForm):
		return CaptureFullForm
	case key.Matches(msg, ck.Submit):
		return CaptureSubmit
	case key.Matches(msg, ck.Left):
		_, n := utf8.DecodeLastRuneInString(c.text[:c.cursor])
		c.cursor -= n
		return CaptureNone
	case key.Matches(msg, ck.Right):
		_, n := utf8.DecodeRuneInString(c.text[c.cursor:])
		c.cursor += n
		return CaptureNone
	case key.Matches(msg, ck.Home):
		c.cursor = 0
		return CaptureNone
	case key.Matches(msg, ck.End):
		c.cursor = len(c.text)
		return CaptureNone
	case key.Matches(msg, in.DeleteBack):
		_, n := utf8.DecodeLastRuneInString(c.text[:c.cursor])
		return c.edit(c.cursor-n, c.cursor, "")
	case key.Matches(msg, ck.DeleteFwd):
		_, n := utf8.DecodeRuneInString(c.text[c.cursor:])
		return c.edit(c.cursor, c.cursor+n, "")
	case key.Matches(msg, ck.DeleteWord):
		return c.edit(wordStart(c.text, c.cursor), c.cursor, "")
	case key.Matches(msg, in.ClearInput):
		return c.edit(0, len(c.text), "")
	}
	if msg.Text == "" || msg.Mod&^tea.ModShift != 0 {
		return CaptureNone
	}
	// One line: pasted newlines become spaces.
	txt := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, msg.Text)
	return c.edit(c.cursor, c.cursor, txt)
}

// edit replaces [from, to) with s and puts the cursor after it.
func (c *CaptureInput) edit(from, to int, s string) CaptureAction {
	if from == to && s == "" {
		return CaptureNone
	}
	c.text = c.text[:from] + s + c.text[to:]
	c.cursor = from + len(s)
	c.clearMessage()
	return CaptureChanged
}

// wordStart is where ctrl+w deletes back to: over spaces, then a word.
func wordStart(s string, i int) int {
	for i > 0 {
		r, n := utf8.DecodeLastRuneInString(s[:i])
		if !unicode.IsSpace(r) {
			break
		}
		i -= n
	}
	for i > 0 {
		r, n := utf8.DecodeLastRuneInString(s[:i])
		if unicode.IsSpace(r) {
			break
		}
		i -= n
	}
	return i
}

// complete applies the pending fix, else puts in the next (dir 1) or
// previous candidate for the token under the cursor.
func (c *CaptureInput) complete(dir int) CaptureAction {
	if f := c.fix; f != nil && dir > 0 {
		c.fix = nil
		act := c.edit(f.Start, f.End, f.Text)
		c.cursor = f.Start + len(f.Text)
		return act
	}
	if c.cycle == nil {
		if c.completer == nil {
			return CaptureNone
		}
		comp, ok := c.completer.Complete(c.text, c.cursor)
		if !ok || len(comp.Candidates) == 0 {
			return CaptureNone
		}
		c.cycle = &captureCycle{comp: comp, end: comp.End, i: -1}
	}
	cy := c.cycle
	n := len(cy.comp.Candidates)
	cy.i = ((cy.i+dir)%n + n) % n
	put := cy.comp.Sigil + cy.comp.Candidates[cy.i]
	if cy.end == len(c.text) || !strings.HasPrefix(c.text[cy.end:], " ") {
		put += " "
	}
	c.text = c.text[:cy.comp.Start] + put + c.text[cy.end:]
	cy.end = cy.comp.Start + len(put)
	c.cursor = cy.end
	c.clearMessage()
	return CaptureChanged
}

// View renders the line, its completions and the message, each exactly
// width cells wide.
func (c *CaptureInput) View(width int) string {
	t := c.theme
	prompt := t.CapturePrompt.Render("⚒ ")
	line := prompt + c.renderText(max(width-lipgloss.Width(prompt), 1))
	return strings.Join([]string{fit(line, width), fit(c.suggestions(width), width), fit(c.message(), width)}, "\n")
}

// Rows is how many rows View takes.
func (c *CaptureInput) Rows() int { return captureRows }

// renderText draws the text with tokens highlighted and the cursor,
// scrolled so the cursor stays within width.
func (c *CaptureInput) renderText(width int) string {
	t := c.theme
	if c.text == "" {
		return t.CaptureCursor.Render(" ") + t.CapturePlaceholder.Render(CapturePlaceholder)
	}
	type cell struct {
		r     rune
		token bool
		at    bool // under the cursor
	}
	var cells []cell
	inToken, wordStart := false, true
	for i, r := range c.text {
		if unicode.IsSpace(r) {
			inToken, wordStart = false, true
		} else if wordStart {
			wordStart = false
			inToken = strings.ContainsRune(captureSigils, r) && i+1 < len(c.text) && !unicode.IsSpace(rune(c.text[i+1]))
		}
		cells = append(cells, cell{r: r, token: inToken, at: i == c.cursor})
	}
	if c.cursor == len(c.text) {
		cells = append(cells, cell{r: ' ', at: true})
	}
	// Scroll: keep the cursor in view, showing as much before it as fits.
	cur := 0
	for i, cl := range cells {
		if cl.at {
			cur = i
		}
	}
	from := max(0, cur-width+1)
	cells = cells[from:min(len(cells), from+width)]

	var b strings.Builder
	var run []rune
	style := t.CaptureText
	flush := func() {
		if len(run) > 0 {
			b.WriteString(style.Render(string(run)))
			run = run[:0]
		}
	}
	for _, cl := range cells {
		s := t.CaptureText
		switch {
		case cl.at:
			s = t.CaptureCursor
		case cl.token:
			s = t.CaptureToken
		}
		if s.String() != style.String() || cl.at {
			flush()
			style = s
		}
		run = append(run, cl.r)
		if cl.at {
			flush()
			style = t.CaptureText
		}
	}
	flush()
	return b.String()
}

// suggestions is the completion row: the token's candidates, the one tab
// put in highlighted, else a hint.
func (c *CaptureInput) suggestions(width int) string {
	t := c.theme
	var comp Completion
	sel := -1
	switch {
	case c.cycle != nil:
		comp, sel = c.cycle.comp, c.cycle.i
	case c.completer != nil:
		got, ok := c.completer.Complete(c.text, c.cursor)
		if !ok {
			return c.hint()
		}
		comp = got
	default:
		return c.hint()
	}
	if len(comp.Candidates) == 0 {
		return t.CaptureInfo.Render("no matches")
	}
	var parts []string
	used := 0
	for i, cand := range comp.Candidates {
		s := comp.Sigil + cand
		w := lipgloss.Width(s) + 2
		if used+w > width && len(parts) > 0 {
			parts = append(parts, t.CaptureInfo.Render("…"))
			break
		}
		used += w
		if i == sel {
			parts = append(parts, t.CaptureSuggestSel.Render(s))
		} else {
			parts = append(parts, t.CaptureSuggest.Render(s))
		}
	}
	return strings.Join(parts, "  ")
}

func (c *CaptureInput) hint() string {
	k := c.keys.Capture
	return c.theme.CaptureInfo.Render(k.Submit.Help().Key + " create · " + k.FullForm.Help().Key + " full form · " +
		k.Complete.Help().Key + " complete · " + c.keys.Input.Cancel.Help().Key + " cancel")
}

func (c *CaptureInput) message() string {
	t := c.theme
	switch {
	case c.busy != "":
		return c.busy
	case c.msg == "":
		return ""
	case c.isErr:
		m := t.CaptureError.Render(c.msg)
		if c.fix != nil {
			m += t.CaptureInfo.Render("  (" + c.keys.Capture.Complete.Help().Key + " to fix)")
		}
		return m
	}
	return t.CaptureInfo.Render(c.msg)
}
