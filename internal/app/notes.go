package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/editor"
	"github.com/daan3011/forge/internal/notes"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// standupTimeout bounds gathering a standup across GitHub, Plane and the
// sessions.
const standupTimeout = 20 * time.Second

// Note pane size limits.
const (
	noteMaxWidth  = 100
	noteMinWidth  = 30
	noteMinHeight = 8
)

// noteState is the daily note pane's bookkeeping next to its widget.
type noteState struct {
	gen     int  // bumps on every load; older loads are dropped
	loading bool // the shown day's file is being read
	// standup generates the standup once the load lands (the palette
	// command with the pane closed).
	standup bool
	busy    bool // a standup is being gathered
	// edit opens a note in $EDITOR; swapped in tests.
	edit func(path string, done func(error) tea.Msg) tea.Cmd
}

// noteLoadedMsg is a note read from disk.
type noteLoadedMsg struct {
	gen  int
	day  time.Time
	text string
	err  error
}

// noteSavedMsg is a note written to disk.
type noteSavedMsg struct {
	day  time.Time
	text string
	err  error
}

// noteEditedMsg is the end of an $EDITOR session on a note.
type noteEditedMsg struct {
	day time.Time
	err error
}

// standupMsg is a standup appended to a note.
type standupMsg struct {
	day     time.Time
	text    string // the whole note after appending
	section string
	err     error
}

func (m Model) noteStore() notes.Store { return notes.Store{Dir: m.notesDir} }

func (m Model) today() time.Time { return notes.Day(m.clock()) }

// openNote shows the note pane on day and reads its file.
func (m Model) openNote(day time.Time) (Model, tea.Cmd) {
	if m.notesDir == "" {
		return m, m.toast.Show("daily notes are off (no notes directory)", ui.ToastInfo)
	}
	if m.overlay != overlayNote {
		m.helpOpen = false
		m.overlay = overlayNote
		m.startSlideIn()
	}
	m.resizeNote()
	return m.loadNote(day)
}

// loadNote switches the pane to day, saving unsaved edits of the shown day
// first.
func (m Model) loadNote(day time.Time) (Model, tea.Cmd) {
	save := m.saveNote()
	m.notes.gen++
	m.notes.loading = true
	m.note.Load(day, day.Equal(m.today()), "")
	st, gen := m.noteStore(), m.notes.gen
	load := func() tea.Msg {
		text, err := st.Read(day)
		return noteLoadedMsg{gen: gen, day: day, text: text, err: err}
	}
	return m, tea.Sequence(save, load)
}

// saveNote writes the buffer when it differs from the file; nil otherwise.
func (m Model) saveNote() tea.Cmd {
	if m.notes.loading || !m.note.Dirty() {
		return nil
	}
	st, day, text := m.noteStore(), m.note.Day(), m.note.Text()
	return func() tea.Msg {
		return noteSavedMsg{day: day, text: text, err: st.Write(day, text)}
	}
}

// closeNote hides the pane, saving unsaved edits.
func (m Model) closeNote() (Model, tea.Cmd) {
	if m.note.Editing() {
		m.note.StopEditing()
	}
	m.overlay = overlayNone
	return m, m.saveNote()
}

func (m Model) handleNoteLoaded(msg noteLoadedMsg) (Model, tea.Cmd) {
	if msg.gen != m.notes.gen {
		return m, nil
	}
	m.notes.loading = false
	if msg.err != nil {
		m.notes.standup = false
		return m, m.toast.Show(msg.err.Error(), ui.ToastError)
	}
	m.note.Load(msg.day, msg.day.Equal(m.today()), msg.text)
	if m.notes.standup {
		m.notes.standup = false
		return m.generateStandup()
	}
	return m, nil
}

func (m Model) handleNoteSaved(msg noteSavedMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		return m, m.toast.Show("note not saved: "+msg.err.Error(), ui.ToastError)
	}
	if msg.day.Equal(m.note.Day()) {
		m.note.MarkSaved(msg.text)
	}
	return m, m.notesChanged()
}

func (m Model) handleNoteEdited(msg noteEditedMsg) (Model, tea.Cmd) {
	var cmds []tea.Cmd
	if msg.err != nil {
		cmds = append(cmds, m.toast.Show("editor: "+msg.err.Error(), ui.ToastError))
	}
	cmds = append(cmds, m.notesChanged())
	if m.overlay == overlayNote && msg.day.Equal(m.note.Day()) {
		m, load := m.loadNote(msg.day)
		return m, tea.Batch(append(cmds, load)...)
	}
	return m, tea.Batch(cmds...)
}

// notesChanged tells the tabs that show the note (Home) to reread it.
func (m Model) notesChanged() tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.tabs {
		if w, ok := t.(tabs.NoteWatcher); ok {
			cmds = append(cmds, w.NotesChanged())
		}
	}
	return tea.Batch(cmds...)
}

// handleNoteKey drives the open pane; every key goes here.
func (m Model) handleNoteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Global.Note) { // save and close, editing or not
		return m.closeNote()
	}
	if m.notes.loading {
		if key.Matches(msg, m.keys.Note.Close) {
			return m.closeNote()
		}
		return m, nil
	}
	act, cmd := m.note.Update(msg)
	switch act {
	case ui.NoteSave:
		return m, tea.Batch(cmd, m.saveNote())
	case ui.NoteClose:
		return m.closeNote()
	case ui.NoteEditor:
		return m.editNote(m.note.Day())
	case ui.NotePrevDay:
		return m.loadNote(notes.AddDays(m.note.Day(), -1))
	case ui.NoteNextDay:
		return m.loadNote(notes.AddDays(m.note.Day(), 1))
	case ui.NoteToday:
		return m.loadNote(m.today())
	case ui.NoteStandup:
		return m.generateStandup()
	case ui.NoteCopy:
		section := notes.LastStandup(m.note.Text())
		if section == "" {
			return m, m.toast.Show(noStandupText(m.keys.Note.Standup.Help().Key), ui.ToastInfo)
		}
		return m, m.copyText(section, "standup copied")
	}
	return m, cmd
}

// editNote saves the pane's buffer, then opens day's file in $EDITOR.
// The notes folder is created first so the editor can write a new note.
func (m Model) editNote(day time.Time) (Model, tea.Cmd) {
	if m.notesDir == "" {
		return m, m.toast.Show("daily notes are off (no notes directory)", ui.ToastInfo)
	}
	dir := m.notesDir
	edit := m.notes.edit
	if edit == nil {
		edit = editNoteCmd
	}
	path := m.noteStore().Path(day)
	done := func(err error) tea.Msg { return noteEditedMsg{day: day, err: err} }
	open := func() tea.Msg {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return noteEditedMsg{day: day, err: err}
		}
		return edit(path, done)()
	}
	return m, tea.Sequence(m.saveNote(), open)
}

// editNoteCmd runs $EDITOR on path.
func editNoteCmd(path string, done func(error) tea.Msg) tea.Cmd {
	return tea.ExecProcess(editor.Exec{}.Cmd(context.Background(), path), done)
}

// generateStandup saves the buffer, gathers the standup and appends it to
// the shown day's note, all in one command.
func (m Model) generateStandup() (Model, tea.Cmd) {
	if m.notes.busy {
		return m, nil
	}
	if m.note.Editing() {
		m.note.StopEditing()
	}
	m.notes.busy = true
	m.note.SetBusy("gathering standup…")
	st, day := m.noteStore(), m.note.Day()
	dirty, text := m.note.Dirty(), m.note.Text()
	g := m.gatherer()
	return m, func() tea.Msg {
		if dirty {
			if err := st.Write(day, text); err != nil {
				return standupMsg{day: day, err: err}
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), standupTimeout)
		defer cancel()
		section := notes.Build(g.Gather(ctx, day))
		full, err := st.Append(day, section)
		return standupMsg{day: day, text: full, section: section, err: err}
	}
}

// gatherer reads the standup from every project (it ignores the project
// filter: a standup is about you).
func (m Model) gatherer() notes.Gatherer {
	g := notes.Gatherer{PlaneProjects: notes.PlaneProjects(m.cfg.Projects), Blocked: notes.BlockedBy(m.cfg.Plane)}
	if m.github != nil {
		g.GitHub = m.github
	}
	if m.plane != nil {
		g.Plane = m.plane
	}
	if m.sessions != nil {
		g.Sessions = m.sessions
	}
	return g
}

func (m Model) handleStandup(msg standupMsg) (Model, tea.Cmd) {
	m.notes.busy = false
	m.note.SetBusy("")
	if msg.err != nil {
		return m, m.toast.Show("standup: "+msg.err.Error(), ui.ToastError)
	}
	if msg.day.Equal(m.note.Day()) {
		m.note.MarkSaved(msg.text)
		m.note.ScrollToEnd()
	}
	return m, tea.Batch(m.notesChanged(), m.copyText(msg.section, "standup added to the note and copied"))
}

// copyText copies text to the clipboard and reports done; without a
// clipboard tool it falls back to the terminal (OSC 52).
func (m Model) copyText(text, done string) tea.Cmd {
	c := m.clipboard
	osc := tea.Batch(tea.SetClipboard(text), func() tea.Msg { return ui.SuccessMsg{Text: done} })
	if c == nil {
		return osc
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := c.Copy(ctx, text)
		switch {
		case errors.Is(err, clipboard.ErrNoTool):
			return osc()
		case err != nil:
			return ui.ErrMsg{Err: fmt.Errorf("copy: %w", err)}
		}
		return ui.SuccessMsg{Text: done}
	}
}

// copyLastStandup copies the last standup of day's note, read in the
// command.
func (m Model) copyLastStandup(day time.Time) tea.Cmd {
	if m.notesDir == "" {
		return m.toast.Show("daily notes are off (no notes directory)", ui.ToastInfo)
	}
	st, standup := m.noteStore(), m.keys.Note.Standup.Help().Key
	return func() tea.Msg {
		text, err := st.Read(day)
		if err != nil {
			return ui.ErrMsg{Err: err}
		}
		section := notes.LastStandup(text)
		if section == "" {
			return ui.InfoMsg{Text: noStandupText(standup)}
		}
		return copyMsg{text: section, done: "standup copied"}
	}
}

// copyMsg asks the root to copy text (copyText needs the model's
// clipboard).
type copyMsg struct{ text, done string }

func noStandupText(key string) string {
	return fmt.Sprintf("no standup in this note yet · %s in the note pane generates one", key)
}

// resizeNote fits the pane to the screen: most of it, up to noteMaxWidth.
func (m *Model) resizeNote() {
	w := min(noteMaxWidth, m.width-4)
	h := max(m.height*3/4, min(noteMinHeight, m.height-2))
	m.note.SetSize(max(w, 1), max(h, 1))
}

// noteLayer renders the note pane centred over the screen.
func (m Model) noteLayer() *lipgloss.Layer {
	if m.width-4 < noteMinWidth || m.height < noteMinHeight {
		return nil
	}
	n := m.note
	switch {
	case m.notes.busy:
		n.SetBusy(anim.HammerFrame(m.anim.Frame(), m.anim.Settings(), m.theme) + " gathering standup…")
	case m.notes.loading:
		n.SetBusy("loading…")
	}
	view := n.View()
	x := (m.width - lipgloss.Width(view)) / 2
	y := max((m.height-lipgloss.Height(view))/2, 0)
	off, _ := m.slideIn.At(m.anim.Frame())
	y = max(y+int(off), 0)
	return lipgloss.NewLayer(view).X(x).Y(y).Z(1)
}
