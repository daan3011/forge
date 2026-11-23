package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/notes"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/home"
	"github.com/daan3011/forge/internal/tabs/prs"
	"github.com/daan3011/forge/internal/ui"
)

var cmdType = reflect.TypeFor[tea.Cmd]()

// runAll runs cmd and returns its messages, flattening batches and
// sequences. A command still running after a moment (a tick) is dropped.
func runAll(cmd tea.Cmd) []tea.Msg {
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
	if msg == nil {
		return nil
	}
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == cmdType {
		var out []tea.Msg
		for i := range v.Len() {
			out = append(out, runAll(v.Index(i).Interface().(tea.Cmd))...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// pump runs cmd and feeds the note flow's messages (and the tabs' targeted
// ones) back into m until nothing is left. Everything it saw is returned.
func pump(t *testing.T, m Model, cmd tea.Cmd) (Model, []tea.Msg) {
	t.Helper()
	var seen []tea.Msg
	queue := runAll(cmd)
	for i := 0; len(queue) > 0; i++ {
		if i > 200 {
			t.Fatal("pump: too many messages")
		}
		msg := queue[0]
		queue = queue[1:]
		seen = append(seen, msg)
		switch msg.(type) {
		case noteLoadedMsg, noteSavedMsg, noteEditedMsg, standupMsg, copyMsg, tabs.TargetedMsg,
			ui.SuccessMsg, ui.InfoMsg, ui.ErrMsg:
			next, c := m.Update(msg)
			m = next.(Model)
			queue = append(queue, runAll(c)...)
		}
	}
	return m, seen
}

func keyPress(m Model, t *testing.T, k tea.KeyPressMsg) (Model, []tea.Msg) {
	t.Helper()
	next, cmd := m.Update(k)
	return pump(t, next.(Model), cmd)
}

func ctrlS() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl} }

func noteModel(t *testing.T, opts ...Option) (Model, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "notes")
	m := mustNew(t, config.Default(), append([]Option{WithNotes(dir)}, opts...)...)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	return next.(Model), dir
}

func readNote(t *testing.T, dir string, day time.Time) string {
	t.Helper()
	b, err := os.ReadFile(notes.Path(dir, day))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func screen(m Model) string { return ansi.Strip(m.render()) }

// TestNoteFlow: ctrl+s from a focused filter opens today's note; typing
// and esc saves it, and Home's side panel shows it.
func TestNoteFlow(t *testing.T) {
	m, dir := noteModel(t)
	today := notes.Day(time.Now())

	m, _ = keyPress(m, t, tea.KeyPressMsg{Code: '2', Text: "2"}) // PRs
	if m.tabs[m.active].Title() != prs.Title {
		t.Fatalf("active tab = %s", m.tabs[m.active].Title())
	}
	m, _ = keyPress(m, t, press("/"))
	if !m.capturingInput() {
		t.Fatal("/ should focus the PR filter")
	}
	m, _ = keyPress(m, t, ctrlS())
	if m.overlay != overlayNote || m.notes.loading {
		t.Fatalf("overlay %v loading %v", m.overlay, m.notes.loading)
	}
	if s := screen(m); !strings.Contains(s, today.Format("Mon 2 Jan 2006")) || !strings.Contains(s, ui.NoteEmptyText) {
		t.Errorf("note pane:\n%s", s)
	}

	m, _ = keyPress(m, t, press("e"))
	for _, k := range typeText("ship P6.2") {
		m, _ = keyPress(m, t, k)
	}
	if m.overlay != overlayNote || !strings.Contains(screen(m), "ship P6.2") {
		t.Fatalf("typing left the pane:\n%s", screen(m))
	}
	m, _ = keyPress(m, t, esc)
	if got := readNote(t, dir, today); got != "ship P6.2" {
		t.Errorf("saved note = %q", got)
	}
	if m.note.Dirty() || m.note.Editing() {
		t.Errorf("dirty %v editing %v after esc", m.note.Dirty(), m.note.Editing())
	}
	m, _ = keyPress(m, t, esc) // close
	if m.overlay != overlayNone {
		t.Fatalf("esc should close the pane, overlay = %v", m.overlay)
	}
	// Home's side panel picked up the note.
	m, _ = keyPress(m, t, esc) // leave the PR filter
	m, _ = keyPress(m, t, tea.KeyPressMsg{Code: '1', Text: "1"})
	if m.tabs[m.active].Title() != home.Title || !strings.Contains(screen(m), "ship P6.2") {
		t.Errorf("Home should show today's note:\n%s", screen(m))
	}
}

// TestNoteSaveAndClose: ctrl+s while editing saves and closes.
func TestNoteSaveAndClose(t *testing.T) {
	m, dir := noteModel(t)
	today := notes.Day(time.Now())
	m, _ = keyPress(m, t, ctrlS())
	m, _ = keyPress(m, t, press("e"))
	m, _ = keyPress(m, t, press("x"))
	m, _ = keyPress(m, t, ctrlS())
	if m.overlay != overlayNone {
		t.Fatalf("overlay = %v, want closed", m.overlay)
	}
	if got := readNote(t, dir, today); got != "x" {
		t.Errorf("note = %q", got)
	}
}

// TestNoteDays: [ and ] browse days, never past today; t comes back.
func TestNoteDays(t *testing.T) {
	m, dir := noteModel(t)
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.Local)
	m.clock = func() time.Time { return now }
	st := notes.Store{Dir: dir}
	if err := st.Write(notes.Day(now).AddDate(0, 0, -1), "sunday thoughts"); err != nil {
		t.Fatal(err)
	}
	m, _ = keyPress(m, t, ctrlS())
	m, _ = keyPress(m, t, press("]"))
	if !m.note.IsToday() {
		t.Error("] went past today")
	}
	m, _ = keyPress(m, t, press("["))
	if s := screen(m); !strings.Contains(s, "Sun 22 Nov 2026") || !strings.Contains(s, "sunday thoughts") {
		t.Errorf("[ should show Sunday:\n%s", s)
	}
	m, _ = keyPress(m, t, press("["))
	if !strings.Contains(screen(m), "Sat 21 Nov 2026") {
		t.Error("[ twice should show Saturday")
	}
	m, _ = keyPress(m, t, press("]"))
	m, _ = keyPress(m, t, press("t"))
	if !m.note.IsToday() || !strings.Contains(screen(m), "Mon 23 Nov 2026") {
		t.Errorf("t should come back to today:\n%s", screen(m))
	}
}

// TestNoteDayKeepsEdits: switching days saves the shown day first.
func TestNoteDayKeepsEdits(t *testing.T) {
	m, dir := noteModel(t)
	today := notes.Day(time.Now())
	m, _ = keyPress(m, t, ctrlS())
	m, _ = keyPress(m, t, press("e"))
	m, _ = keyPress(m, t, press("z"))
	m.note.StopEditing() // as if esc had been pressed, without its save
	m, _ = keyPress(m, t, press("["))
	if got := readNote(t, dir, today); got != "z" {
		t.Errorf("today's note = %q, want the edit saved before moving", got)
	}
}

// TestNoteEditor: E saves, then opens the file in $EDITOR and reloads
// after.
func TestNoteEditor(t *testing.T) {
	m, dir := noteModel(t)
	today := notes.Day(time.Now())
	var opened string
	m.notes.edit = func(path string, done func(error) tea.Msg) tea.Cmd {
		return func() tea.Msg {
			opened = path
			_ = os.WriteFile(path, []byte("from vim"), 0o644)
			return done(nil)
		}
	}
	m, _ = keyPress(m, t, ctrlS())
	m, _ = keyPress(m, t, press("E"))
	if opened != notes.Path(dir, today) {
		t.Errorf("editor opened %q", opened)
	}
	if !strings.Contains(screen(m), "from vim") {
		t.Errorf("pane should reload after the editor:\n%s", screen(m))
	}
}

// standupGitHub answers the standup's searches: one merged PR yesterday
// and one open PR with failing checks.
type standupGitHub struct {
	fakeGitHub
	merged time.Time
}

func (f standupGitHub) SearchPRs(_ context.Context, q string, _ int) (github.SearchResult, error) {
	switch {
	case strings.Contains(q, "is:merged"):
		return github.SearchResult{PRs: []github.PR{{Repo: "daan/forge", Number: 46, Title: "Home screen", State: github.StateMerged, MergedAt: f.merged}}}, nil
	case strings.Contains(q, "reviewed-by:@me"):
		return github.SearchResult{PRs: []github.PR{{Repo: "acme/ui", Number: 3, Title: "Buttons", UpdatedAt: f.merged}}}, nil
	case strings.Contains(q, "author:@me"):
		return github.SearchResult{PRs: []github.PR{
			{Repo: "daan/forge", Number: 48, Title: "Notes", State: github.StateOpen},
			{Repo: "daan/forge", Number: 49, Title: "Red", State: github.StateOpen, CI: github.CIFailure},
		}}, nil
	}
	return github.SearchResult{}, nil
}

type fakeCopier struct {
	mu   sync.Mutex
	text string
}

func (f *fakeCopier) Copy(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.text = text
	return nil
}

// TestGenerateStandup: one palette command appends the standup to today's
// note and copies it.
func TestGenerateStandup(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.Local) // a Monday
	clip := &fakeCopier{}
	m, dir := noteModel(t,
		WithGitHub(standupGitHub{merged: time.Date(2026, 11, 20, 15, 0, 0, 0, time.Local)}),
		WithClipboard(clip))
	m.clock = func() time.Time { return now }
	if err := (notes.Store{Dir: dir}).Write(notes.Day(now), "scratch"); err != nil {
		t.Fatal(err)
	}

	next, cmd := runPalette(t, m, "generate standup")
	m, seen := pump(t, next.(Model), cmd)
	if m.overlay != overlayNote || m.notes.busy {
		t.Fatalf("overlay %v busy %v", m.overlay, m.notes.busy)
	}
	note := readNote(t, dir, notes.Day(now))
	for _, want := range []string{
		"scratch\n\n## Standup — Mon 23 Nov\n",
		"### Done since Fri 20 Nov\n- Merged daan/forge#46 — Home screen\n- Reviewed acme/ui#3 — Buttons\n",
		"### Today\n- Open PR daan/forge#48 — Notes\n",
		"### Blockers\n- daan/forge#49 — Red (CI failing)\n",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	clip.mu.Lock()
	copied := clip.text
	clip.mu.Unlock()
	if copied != notes.LastStandup(note) || !strings.HasPrefix(copied, notes.Heading) {
		t.Errorf("copied %q", copied)
	}
	if !strings.Contains(screen(m), "Merged daan/forge#46") {
		t.Errorf("pane should show the standup:\n%s", screen(m))
	}
	found := false
	for _, msg := range seen {
		if s, ok := msg.(ui.SuccessMsg); ok && strings.Contains(s.Text, "copied") {
			found = true
		}
	}
	if !found {
		t.Errorf("no success toast in %v", seen)
	}

	// y copies it again; the palette's "Copy last standup" too.
	clip.text = ""
	m, _ = keyPress(m, t, press("y"))
	if clip.text != copied {
		t.Errorf("y copied %q", clip.text)
	}
	m, _ = keyPress(m, t, esc)
	clip.text = ""
	next, cmd = runPalette(t, m, "copy last standup")
	_, _ = pump(t, next.(Model), cmd)
	if clip.text != copied {
		t.Errorf("palette copy = %q", clip.text)
	}
}

// TestNoteCopyWithoutStandup says how to make one.
func TestNoteCopyWithoutStandup(t *testing.T) {
	m, _ := noteModel(t, WithClipboard(&fakeCopier{}))
	m, _ = keyPress(m, t, ctrlS())
	m, _ = keyPress(m, t, press("y"))
	if !strings.Contains(screen(m), "no standup in this note yet") {
		t.Errorf("want a hint toast:\n%s", screen(m))
	}
}
