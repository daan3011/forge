package context

import (
	stdctx "context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/slashcmd"
)

// The Commands view (P6.7): forge's slash commands (Claude Code skills)
// that every session gets with --add-dir. Built-ins, one per session
// template and the user's overrides in ~/.config/forge/commands; e edits an
// override (copying the built-in first), X resets to the built-in and S
// re-syncs every running session, which picks the change up live.

// syncTimeout bounds re-syncing every session's skills.
const syncTimeout = time.Minute

type cmdState struct {
	loaded bool
	items  []slashcmd.Status
	errs   []error
	cursor int
	top    int
	sel    string // selected command's name, kept across reloads
	offset int    // detail scroll
	busy   bool   // a re-sync is running
}

// --- messages --------------------------------------------------------------------

type commandsLoadedMsg struct {
	gen   int
	items []slashcmd.Status
	errs  []error
}

type overrideReadyMsg struct {
	path string
	err  error
}

type commandResetMsg struct {
	name string
	err  error
}

type skillsSyncedMsg struct {
	res []sessions.SyncResult
	err error
}

// TargetTab implements tabs.TargetedMsg.
func (commandsLoadedMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (overrideReadyMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (commandResetMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (skillsSyncedMsg) TargetTab() string { return Title }

// --- loading ---------------------------------------------------------------------

func (t *Tab) loadCommands() tea.Cmd {
	if t.skills == nil {
		return nil
	}
	gen, cat, root := t.gen, t.skills, t.root()
	return func() tea.Msg {
		items, errs := cat.Status(root)
		return commandsLoadedMsg{gen: gen, items: items, errs: errs}
	}
}

func (t *Tab) commandsLoaded(msg commandsLoadedMsg) {
	if msg.gen != t.gen {
		return
	}
	c := &t.cm
	c.items, c.errs, c.loaded = msg.items, msg.errs, true
	i := slices.IndexFunc(c.items, func(s slashcmd.Status) bool { return s.Name == c.sel })
	if i < 0 {
		i = c.cursor
	}
	t.setCommandCursor(i)
}

func (t *Tab) selectedCommand() (slashcmd.Status, bool) {
	c := &t.cm
	if c.cursor < 0 || c.cursor >= len(c.items) {
		return slashcmd.Status{}, false
	}
	return c.items[c.cursor], true
}

func (t *Tab) setCommandCursor(i int) {
	c := &t.cm
	if len(c.items) == 0 {
		c.cursor, c.top, c.sel = 0, 0, ""
		return
	}
	i = min(max(i, 0), len(c.items)-1)
	if i != c.cursor || c.items[i].Name != c.sel {
		c.offset = 0
	}
	c.cursor, c.sel = i, c.items[i].Name
	h := t.packListHeight() // two lines per row, like packs
	if c.cursor < c.top {
		c.top = c.cursor
	}
	if c.cursor >= c.top+h {
		c.top = c.cursor - h + 1
	}
	c.top = max(min(c.top, len(c.items)-h), 0)
}

// --- update ----------------------------------------------------------------------

// toggleCommands switches between memory files and the commands.
func (t *Tab) toggleCommands() tea.Cmd {
	if t.view == viewCommands {
		t.view = viewMemory
	} else {
		t.view = viewCommands
	}
	t.focusDetail, t.matrix = false, false
	if t.view == viewCommands {
		return t.loadCommands()
	}
	return nil
}

// inCommands wraps a Commands view action for the palette.
func (t *Tab) inCommands(f func() tea.Cmd) func() tea.Cmd {
	return func() tea.Cmd {
		var load tea.Cmd
		if t.view != viewCommands {
			load = t.toggleCommands()
		}
		if !t.cm.loaded {
			return load
		}
		return tea.Batch(load, f())
	}
}

func (t *Tab) commandKey(kp tea.KeyPressMsg) tea.Cmd {
	ck, l := t.keys.Context, t.keys.List
	switch {
	case key.Matches(kp, ck.Edit):
		return t.editCommand()
	case key.Matches(kp, ck.ResetCommand):
		return t.resetCommand()
	case key.Matches(kp, ck.SyncCommands):
		return t.syncSkills()
	case key.Matches(kp, ck.DetailDown):
		t.scrollCommand(1)
	case key.Matches(kp, ck.DetailUp):
		t.scrollCommand(-1)
	case key.Matches(kp, l.GrowPane):
		t.split.Grow()
	case key.Matches(kp, l.ShrinkPane):
		t.split.Shrink()
	case key.Matches(kp, l.Up):
		t.setCommandCursor(t.cm.cursor - 1)
	case key.Matches(kp, l.Down):
		t.setCommandCursor(t.cm.cursor + 1)
	case key.Matches(kp, l.Top):
		t.setCommandCursor(0)
	case key.Matches(kp, l.Bottom):
		t.setCommandCursor(len(t.cm.items))
	case key.Matches(kp, l.PageDown):
		t.setCommandCursor(t.cm.cursor + max(t.packListHeight()/2, 1))
	case key.Matches(kp, l.PageUp):
		t.setCommandCursor(t.cm.cursor - max(t.packListHeight()/2, 1))
	}
	return nil
}

func (t *Tab) scrollCommand(delta int) {
	s, ok := t.selectedCommand()
	if !ok {
		return
	}
	rw, _ := t.split.RightSize()
	maxOff := max(len(t.commandDetailLines(s, rw))-t.detailBodyHeight(), 0)
	t.cm.offset = min(max(t.cm.offset+delta, 0), maxOff)
}

// editCommand opens the selected command's override in $EDITOR, copying
// the built-in (or template) into it first.
func (t *Tab) editCommand() tea.Cmd {
	s, ok := t.selectedCommand()
	if !ok || t.skills == nil {
		return nil
	}
	cat, name := t.skills, s.Name
	return func() tea.Msg {
		path, err := cat.EnsureOverride(name)
		return overrideReadyMsg{path: path, err: err}
	}
}

func (t *Tab) overrideReady(msg overrideReadyMsg) tea.Cmd {
	if msg.err != nil {
		return errCmd(fmt.Errorf("override: %w", msg.err))
	}
	return t.edit(msg.path)
}

// resetCommand asks, then deletes the selected command's override.
func (t *Tab) resetCommand() tea.Cmd {
	s, ok := t.selectedCommand()
	if !ok || t.skills == nil {
		return nil
	}
	if s.Source != slashcmd.SourceOverride {
		return info("/" + s.Name + " has no override to reset")
	}
	title := "Reset /" + s.Name + " to the built-in?"
	switch s.Overrides {
	case "":
		title = "Delete your command /" + s.Name + "?"
	case slashcmd.SourceTemplate:
		title = "Reset /" + s.Name + " to its template?"
	}
	yes := false
	f := t.styled(huh.NewGroup(
		huh.NewConfirm().Key("confirm").Title(title).
			Description("deletes " + t.display(s.Path) + "; re-sync to update running sessions").
			Affirmative("Reset").Negative("Keep").Value(&yes),
	))
	cat, name := t.skills, s.Name
	return t.openForm(f, func() tea.Cmd {
		if !yes {
			return nil
		}
		return func() tea.Msg { return commandResetMsg{name: name, err: cat.Reset(name)} }
	})
}

func (t *Tab) commandReset(msg commandResetMsg) tea.Cmd {
	if msg.err != nil {
		return tea.Batch(errCmd(msg.err), t.loadCommands())
	}
	return tea.Batch(success("/"+msg.name+" reset"), t.loadCommands())
}

// syncSkills re-syncs every running session's skills in a command.
func (t *Tab) syncSkills() tea.Cmd {
	if t.syncAll == nil {
		return info("no sessions to sync")
	}
	if t.cm.busy {
		return info("already re-syncing")
	}
	t.cm.busy = true
	sync := t.syncAll
	return func() tea.Msg {
		ctx, cancel := stdctx.WithTimeout(stdctx.Background(), syncTimeout)
		defer cancel()
		res, err := sync(ctx)
		return skillsSyncedMsg{res: res, err: err}
	}
}

func (t *Tab) skillsSynced(msg skillsSyncedMsg) tea.Cmd {
	t.cm.busy = false
	if msg.err != nil {
		return errCmd(fmt.Errorf("re-sync: %w", msg.err))
	}
	return tea.Batch(syncSummary(msg.res)...)
}

// syncSummary is the toast(s) for a re-sync.
func syncSummary(res []sessions.SyncResult) []tea.Cmd {
	if len(res) == 0 {
		return []tea.Cmd{info("no running sessions to sync")}
	}
	written, removed := 0, 0
	var errs []error
	for _, r := range res {
		written += len(r.Written)
		removed += len(r.Removed)
		if r.Err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.Session, r.Err))
		}
	}
	text := fmt.Sprintf("skills re-synced in %s", plural(len(res), "session"))
	if written+removed == 0 {
		text += " (already up to date)"
	} else {
		text += fmt.Sprintf(" (%s written, %d removed)", plural(written, "file"), removed)
	}
	cmds := []tea.Cmd{success(text)}
	if len(errs) > 0 {
		cmds = append(cmds, errCmd(errors.Join(errs...)))
	}
	return cmds
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// --- view ------------------------------------------------------------------------

func (t *Tab) commandsListView(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	th, c := t.theme, &t.cm
	switch {
	case t.skills == nil:
		return th.Hint.Render(ansi.Wrap("forge's slash commands aren't available here.", w, ""))
	case !c.loaded:
		return th.Hint.Render("Reading commands…")
	}
	body := max(h-footerLines, 1)
	var lines []string
	end := min(c.top+t.packListHeight(), len(c.items))
	for i := c.top; i < end; i++ {
		lines = append(lines, t.commandRow(c.items[i], i == c.cursor, w)...)
	}
	if len(c.errs) > 0 && len(lines) < body {
		lines = append(lines, th.TableEmpty.Render(ansi.Truncate(fmt.Sprintf("⚠ %d file(s) couldn't be read", len(c.errs)), w, "…")))
	}
	for len(lines) < body {
		lines = append(lines, "")
	}
	foot := fmt.Sprintf("%d commands · %s re-syncs running sessions", len(c.items), t.keys.Context.SyncCommands.Help().Key)
	if c.busy {
		foot = "re-syncing sessions…"
	}
	return strings.Join(lines[:body], "\n") + "\n\n" + th.Hint.Render(ansi.Truncate(foot, w, "…"))
}

// commandRow is two lines: "/name … source" and the description, or the
// shadowing badge.
func (t *Tab) commandRow(s slashcmd.Status, selected bool, w int) []string {
	th := t.theme
	right := s.Label()
	label := "  /" + s.Name
	if s.Shadow != "" {
		label += " ⚠"
	}
	space := max(w-ansi.StringWidth(right)-1, 4)
	label = ansi.Truncate(label, space, "…")
	pad := max(w-ansi.StringWidth(label)-ansi.StringWidth(right), 0)
	desc := s.Description
	if s.Shadow != "" {
		desc = s.Shadow.Badge(s.Name)
	}
	second := ansi.Truncate("    "+desc, w, "…")
	if selected {
		return []string{
			th.TableSelected.Render(label + strings.Repeat(" ", pad) + right),
			th.TableSelected.Render(second + strings.Repeat(" ", max(w-ansi.StringWidth(second), 0))),
		}
	}
	if s.Shadow != "" {
		return []string{th.TableRow.Render(label+strings.Repeat(" ", pad)) + th.Hint.Render(right), th.TableEmpty.Render(second)}
	}
	return []string{th.TableRow.Render(label+strings.Repeat(" ", pad)) + th.Hint.Render(right), th.Hint.Render(second)}
}

// commandDetailLines is the detail pane: the badge, then the SKILL.md.
func (t *Tab) commandDetailLines(s slashcmd.Status, w int) []string {
	th := t.theme
	var out []string
	if s.Shadow != "" {
		out = append(out, th.TableEmpty.Render(ansi.Wrap("⚠ "+s.Shadow.Badge(s.Name), w, "")), "")
	}
	body := strings.TrimRight(strings.ReplaceAll(string(s.Content), "\r\n", "\n"), "\n")
	out = append(out, strings.Split(ansi.Wrap(body, w, ""), "\n")...)
	return out
}

func (t *Tab) commandDetailView(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	th := t.theme
	s, ok := t.selectedCommand()
	if !ok {
		return ""
	}
	where := s.Label()
	if s.Path != "" {
		where += " · " + t.display(s.Path)
	}
	header := th.DetailTitle.Render(ansi.Truncate("/"+s.Name, w, "…")) + "\n" +
		th.Hint.Render(ansi.Truncate(where, w, "…")) + "\n"
	all := t.commandDetailLines(s, w)
	body := max(h-detailHeader, 1)
	off := min(t.cm.offset, max(len(all)-body, 0))
	end := min(off+body, len(all))
	return header + strings.Join(all[off:end], "\n")
}
