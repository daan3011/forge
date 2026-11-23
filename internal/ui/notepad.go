package ui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// NoteAction is what a key press asks the NotePad's owner to do.
type NoteAction int

// Note actions.
const (
	NoteNone    NoteAction = iota
	NoteSave               // left the editor: save the text
	NoteClose              // close the pane (save first when dirty)
	NoteEditor             // open the note in $EDITOR
	NotePrevDay            // show the day before
	NoteNextDay            // show the day after (never past today)
	NoteToday              // back to today
	NoteStandup            // generate the standup into this note
	NoteCopy               // copy the last standup section
)

// NoteEmptyText is shown for a day without a note.
const NoteEmptyText = "nothing written yet"

// NotePad is the daily note pane (P6.2): the note read-only, scrollable,
// and an inline editor (a textarea) entered with the edit key. The owner
// does the file I/O; Update only edits and reports what was asked.
type NotePad struct {
	theme *Theme
	keys  *KeyMap

	day   time.Time
	today bool
	saved string // the text on disk

	ta      textarea.Model
	editing bool
	offset  int // first shown line in view mode

	width, height int
	busy          string // e.g. "gathering standup…", shown in the header
}

// NewNotePad returns an empty pane.
func NewNotePad(theme *Theme, keys *KeyMap) NotePad {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 0
	ta.MaxHeight = 0
	st := textarea.DefaultDarkStyles()
	st.Focused.Base, st.Blurred.Base = lipgloss.NewStyle(), lipgloss.NewStyle()
	st.Focused.Text, st.Blurred.Text = theme.NoteText, theme.NoteText
	st.Focused.CursorLine = theme.NoteText
	st.Cursor.Blink = false // no ticking: animations go through ui/anim
	st.Cursor.Color = theme.NoteCursorColor
	ta.SetStyles(st)
	return NotePad{theme: theme, keys: keys, ta: ta}
}

// Load shows day's note in view mode.
func (n *NotePad) Load(day time.Time, today bool, text string) {
	n.day, n.today, n.saved = day, today, text
	n.editing, n.offset, n.busy = false, 0, ""
	n.ta.Blur()
	n.ta.SetValue(text)
}

// MarkSaved records text as what is on disk. In view mode the pane shows
// it; while editing the buffer is kept.
func (n *NotePad) MarkSaved(text string) {
	n.saved = text
	if !n.editing {
		n.ta.SetValue(text)
		n.clampOffset()
	}
}

// ScrollToEnd shows the end of the note (after a standup was appended).
func (n *NotePad) ScrollToEnd() {
	n.offset = len(n.lines())
	n.clampOffset()
}

// SetBusy sets the header's progress text; "" clears it.
func (n *NotePad) SetBusy(s string) { n.busy = s }

// Busy is the header's progress text.
func (n NotePad) Busy() string { return n.busy }

// Day is the shown day.
func (n NotePad) Day() time.Time { return n.day }

// IsToday reports whether the shown day is today.
func (n NotePad) IsToday() bool { return n.today }

// Editing reports whether the inline editor has focus.
func (n NotePad) Editing() bool { return n.editing }

// Text is the current text: the editor's buffer.
func (n NotePad) Text() string { return n.ta.Value() }

// Saved is the text on disk.
func (n NotePad) Saved() string { return n.saved }

// Dirty reports whether the buffer differs from the file.
func (n NotePad) Dirty() bool { return n.ta.Value() != n.saved }

// StopEditing leaves the editor (the owner saves).
func (n *NotePad) StopEditing() {
	n.editing = false
	n.ta.Blur()
	n.clampOffset()
}

// SetSize sets the pane's outer size.
func (n *NotePad) SetSize(w, h int) {
	n.width, n.height = w, h
	n.ta.SetWidth(n.innerWidth())
	n.ta.SetHeight(n.bodyHeight())
	n.clampOffset()
}

func (n NotePad) innerWidth() int {
	return max(n.width-n.theme.NoteBox.GetHorizontalFrameSize(), 1)
}

// bodyHeight is the text rows: the box minus its frame, the header and
// the key hint line.
func (n NotePad) bodyHeight() int {
	return max(n.height-n.theme.NoteBox.GetVerticalFrameSize()-2, 1)
}

// Update handles a key press.
func (n *NotePad) Update(msg tea.Msg) (NoteAction, tea.Cmd) {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return NoteNone, nil
	}
	if n.editing {
		if key.Matches(kp, n.keys.Input.Cancel) {
			n.StopEditing()
			return NoteSave, nil
		}
		var cmd tea.Cmd
		n.ta, cmd = n.ta.Update(kp)
		return NoteNone, cmd
	}
	nk, l := n.keys.Note, n.keys.List
	switch {
	case key.Matches(kp, nk.Edit):
		n.editing = true
		n.ta.MoveToEnd()
		return NoteNone, n.ta.Focus()
	case key.Matches(kp, nk.External):
		return NoteEditor, nil
	case key.Matches(kp, nk.PrevDay):
		return NotePrevDay, nil
	case key.Matches(kp, nk.NextDay):
		if n.today {
			return NoteNone, nil
		}
		return NoteNextDay, nil
	case key.Matches(kp, nk.Today):
		if n.today {
			return NoteNone, nil
		}
		return NoteToday, nil
	case key.Matches(kp, nk.Standup):
		return NoteStandup, nil
	case key.Matches(kp, nk.Copy):
		return NoteCopy, nil
	case key.Matches(kp, nk.Close):
		return NoteClose, nil
	case key.Matches(kp, l.Up):
		n.offset--
	case key.Matches(kp, l.Down):
		n.offset++
	case key.Matches(kp, l.PageUp):
		n.offset -= max(n.bodyHeight()/2, 1)
	case key.Matches(kp, l.PageDown):
		n.offset += max(n.bodyHeight()/2, 1)
	case key.Matches(kp, l.Top):
		n.offset = 0
	case key.Matches(kp, l.Bottom):
		n.offset = len(n.lines())
	}
	n.clampOffset()
	return NoteNone, nil
}

// lines are the view-mode lines, wrapped to the inner width.
func (n NotePad) lines() []string {
	text := strings.TrimRight(n.saved, "\n")
	if text == "" {
		return nil
	}
	w := n.innerWidth()
	var out []string
	for l := range strings.SplitSeq(text, "\n") {
		out = append(out, strings.Split(ansi.Wrap(l, w, ""), "\n")...)
	}
	return out
}

func (n *NotePad) clampOffset() {
	n.offset = min(n.offset, max(len(n.lines())-n.bodyHeight(), 0))
	n.offset = max(n.offset, 0)
}

// View renders the pane at its size.
func (n NotePad) View() string {
	w := n.innerWidth()
	header := n.theme.NoteTitle.Render("¶ " + n.day.Format("Mon 2 Jan 2006"))
	if n.today {
		header += n.theme.NoteMuted.Render(" · today")
	}
	if n.Dirty() {
		header += n.theme.NoteDirty.Render(" ●")
	}
	if n.busy != "" {
		header += n.theme.NoteMuted.Render("  " + n.busy)
	}
	body := n.body()
	hint := n.theme.NoteMuted.Render(ansi.Truncate(n.hint(), w, "…"))
	inner := lipgloss.JoinVertical(lipgloss.Left, ansi.Truncate(header, w, "…"), body, hint)
	return n.theme.NoteBox.Width(n.width).Height(n.height).Render(inner)
}

func (n NotePad) body() string {
	h, w := n.bodyHeight(), n.innerWidth()
	if n.editing {
		return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(n.ta.View())
	}
	lines := n.lines()
	if len(lines) == 0 {
		lines = []string{n.theme.NoteMuted.Render(NoteEmptyText)}
	} else {
		lines = lines[n.offset:min(n.offset+h, len(lines))]
		for i, l := range lines {
			if strings.HasPrefix(l, "#") {
				lines[i] = n.theme.NoteHeading.Render(l)
			} else {
				lines[i] = n.theme.NoteText.Render(l)
			}
		}
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (n NotePad) hint() string {
	help := n.Help()
	parts := make([]string, 0, len(help))
	for _, b := range help {
		parts = append(parts, b.Help().Key+" "+b.Help().Desc)
	}
	return strings.Join(parts, " · ")
}

// Help is the pane's keys for the current mode.
func (n NotePad) Help() []key.Binding {
	if n.editing {
		return n.keys.NoteEditHelp()
	}
	return n.keys.NoteHelp()
}
