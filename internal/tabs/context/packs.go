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

	"github.com/daan3011/forge/internal/packs"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// The Packs view (P5.2): context packs from ~/.config/forge/packs, each
// expanded against the active project with a rough token total. Packs are
// created with a form and the fuzzy file picker, edited in $EDITOR (globs,
// docs and the preamble are text), and sent to a new or running session
// through the Sessions tab.

// lsTimeout bounds `git ls-files` for the picker.
const lsTimeout = 10 * time.Second

// fileLister lists a checkout's files; *worktree.Exec satisfies it.
type fileLister interface {
	LsFiles(ctx stdctx.Context, dir string) ([]string, error)
}

// packEntry is a pack expanded against the active project.
type packEntry struct {
	pack   packs.Pack
	res    packs.Resolved
	tokens int
}

type packState struct {
	on      bool // the Packs view is showing
	loaded  bool
	entries []packEntry
	errs    []error
	cursor  int
	top     int
	sel     string // selected pack's name, kept across reloads
	offset  int    // detail scroll
	dialog  *packDialog
}

// packDialog is a form or the file picker shown in place of the tab.
type packDialog struct {
	form    *huh.Form
	picker  *ui.FilePicker
	done    func() tea.Cmd
	aborted func() tea.Cmd
}

// --- messages --------------------------------------------------------------------

type packsLoadedMsg struct {
	gen     int
	entries []packEntry
	errs    []error
}

// filesListedMsg carries the checkout's files for the picker; then picks
// up the pack being made or changed.
type filesListedMsg struct {
	pack   packs.Pack
	create bool
	files  []string
	err    error
}

type packSavedMsg struct {
	name    string
	verb    string
	err     error
	deleted bool
}

// TargetTab implements tabs.TargetedMsg.
func (packsLoadedMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (filesListedMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (packSavedMsg) TargetTab() string { return Title }

// --- loading ---------------------------------------------------------------------

// loadPacks reads and expands the packs in a command.
func (t *Tab) loadPacks() tea.Cmd {
	if t.packsDir == "" {
		return nil
	}
	gen, dir, root, docs := t.gen, t.packsDir, t.root(), ""
	if t.project != nil && t.project.Path != "" {
		docs = t.project.DocsPath()
	}
	return func() tea.Msg {
		ps, errs := packs.Load(dir)
		msg := packsLoadedMsg{gen: gen, errs: errs}
		for _, p := range ps {
			res := packs.Expand(p, root, docs)
			msg.entries = append(msg.entries, packEntry{pack: p, res: res, tokens: packs.Tokens(res.Paths)})
		}
		return msg
	}
}

func (t *Tab) packsLoaded(msg packsLoadedMsg) {
	if msg.gen != t.gen {
		return
	}
	p := &t.pk
	p.entries, p.errs, p.loaded = msg.entries, msg.errs, true
	i := slices.IndexFunc(p.entries, func(e packEntry) bool { return e.pack.Name == p.sel })
	if i < 0 {
		i = p.cursor
	}
	t.setPackCursor(i)
}

func (t *Tab) selectedPack() (packEntry, bool) {
	p := &t.pk
	if p.cursor < 0 || p.cursor >= len(p.entries) {
		return packEntry{}, false
	}
	return p.entries[p.cursor], true
}

func (t *Tab) setPackCursor(i int) {
	p := &t.pk
	if len(p.entries) == 0 {
		p.cursor, p.top, p.sel = 0, 0, ""
		return
	}
	i = min(max(i, 0), len(p.entries)-1)
	if i != p.cursor || p.entries[i].pack.Name != p.sel {
		p.offset = 0
	}
	p.cursor, p.sel = i, p.entries[i].pack.Name
	h := t.packListHeight()
	if p.cursor < p.top {
		p.top = p.cursor
	}
	if p.cursor >= p.top+h {
		p.top = p.cursor - h + 1
	}
	p.top = max(min(p.top, len(p.entries)-h), 0)
}

func (t *Tab) packListHeight() int {
	_, h := t.split.LeftSize()
	return max((h-footerLines)/2, 1) // two lines per pack
}

// --- update ----------------------------------------------------------------------

// togglePacks switches between memory files and packs.
func (t *Tab) togglePacks() tea.Cmd {
	t.pk.on = !t.pk.on
	t.focusDetail, t.matrix = false, false
	if t.pk.on && !t.pk.loaded {
		return t.loadPacks()
	}
	return nil
}

func (t *Tab) packKey(kp tea.KeyPressMsg) tea.Cmd {
	ck, l := t.keys.Context, t.keys.List
	switch {
	case key.Matches(kp, ck.NewPack):
		return t.newPack()
	case key.Matches(kp, ck.Edit):
		return t.editPack()
	case key.Matches(kp, ck.AddFiles):
		return t.addFiles()
	case key.Matches(kp, ck.DeletePack):
		return t.deletePack()
	case key.Matches(kp, ck.SendPack):
		return t.sendPack()
	case key.Matches(kp, ck.DetailDown):
		t.scrollPack(1)
	case key.Matches(kp, ck.DetailUp):
		t.scrollPack(-1)
	case key.Matches(kp, l.GrowPane):
		t.split.Grow()
	case key.Matches(kp, l.ShrinkPane):
		t.split.Shrink()
	case key.Matches(kp, l.Up):
		t.setPackCursor(t.pk.cursor - 1)
	case key.Matches(kp, l.Down):
		t.setPackCursor(t.pk.cursor + 1)
	case key.Matches(kp, l.Top):
		t.setPackCursor(0)
	case key.Matches(kp, l.Bottom):
		t.setPackCursor(len(t.pk.entries))
	case key.Matches(kp, l.PageDown):
		t.setPackCursor(t.pk.cursor + max(t.packListHeight()/2, 1))
	case key.Matches(kp, l.PageUp):
		t.setPackCursor(t.pk.cursor - max(t.packListHeight()/2, 1))
	}
	return nil
}

func (t *Tab) scrollPack(delta int) {
	e, ok := t.selectedPack()
	if !ok {
		return
	}
	rw, _ := t.split.RightSize()
	maxOff := max(len(t.packDetailLines(e, rw))-t.detailBodyHeight(), 0)
	t.pk.offset = min(max(t.pk.offset+delta, 0), maxOff)
}

// --- dialogs ---------------------------------------------------------------------

func (t *Tab) styled(g *huh.Group) *huh.Form {
	return huh.NewForm(g).WithTheme(t.theme.Huh()).WithKeyMap(t.keys.HuhKeyMap()).WithShowHelp(true)
}

func (t *Tab) openForm(f *huh.Form, done func() tea.Cmd) tea.Cmd {
	t.pk.dialog = &packDialog{form: f, done: done}
	init := f.Init()
	w, h := t.dialogSize()
	_, _ = f.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return init
}

func (t *Tab) dialogSize() (w, h int) {
	return max(t.width-4, 10), max(t.height-3, 3)
}

func (t *Tab) updatePackDialog(msg tea.Msg) tea.Cmd {
	d := t.pk.dialog
	if d.picker != nil {
		kp, ok := msg.(tea.KeyPressMsg)
		if !ok {
			return nil
		}
		d.picker.Update(kp)
		switch {
		case d.picker.Done():
			t.pk.dialog = nil
			return d.done()
		case d.picker.Aborted():
			t.pk.dialog = nil
			if d.aborted != nil {
				return d.aborted()
			}
		}
		return nil
	}
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		return nil
	}
	_, cmd := d.form.Update(msg)
	switch d.form.State {
	case huh.StateCompleted:
		t.pk.dialog = nil
		return tea.Batch(cmd, d.done())
	case huh.StateAborted:
		t.pk.dialog = nil
		return nil
	}
	return cmd
}

// newPack asks for the name, description and preamble, then the files.
func (t *Tab) newPack() tea.Cmd {
	if t.packsDir == "" {
		return errCmd(errors.New("no packs folder"))
	}
	p := &packs.Pack{}
	taken := func(name string) bool {
		return slices.ContainsFunc(t.pk.entries, func(e packEntry) bool { return e.pack.Name == name })
	}
	f := t.styled(huh.NewGroup(
		huh.NewInput().Key("name").Title("Name").
			Description("letters, digits, - and _; the file is "+t.displayDir()+"/<name>.yaml").
			Placeholder("auth").Value(&p.Name).
			Validate(func(s string) error {
				if err := packs.CheckName(s); err != nil {
					return err
				}
				if taken(s) {
					return fmt.Errorf("a pack called %s already exists", s)
				}
				return nil
			}),
		huh.NewInput().Key("description").Title("Description").Value(&p.Description),
		huh.NewText().Key("preamble").Title("Preamble").
			Description("optional; goes at the top of the first prompt").Lines(4).Value(&p.Preamble),
	).Title("New context pack"))
	return t.openForm(f, func() tea.Cmd {
		p.Description = strings.TrimSpace(p.Description)
		p.Preamble = strings.TrimSpace(p.Preamble)
		if p.Preamble != "" {
			p.Preamble += "\n"
		}
		if t.root() == "" {
			return t.savePack(*p, true, "created")
		}
		return t.listFiles(*p, true)
	})
}

// addFiles opens the picker for the selected pack's literal files.
func (t *Tab) addFiles() tea.Cmd {
	e, ok := t.selectedPack()
	if !ok {
		return nil
	}
	if t.root() == "" {
		return info("pick a project first: pack files are relative to its checkout")
	}
	return t.listFiles(e.pack, false)
}

// listFiles lists the checkout's files in a command, then opens the
// picker for pack.
func (t *Tab) listFiles(pack packs.Pack, create bool) tea.Cmd {
	lister, root := t.lister, t.root()
	return func() tea.Msg {
		if lister == nil {
			return filesListedMsg{pack: pack, create: create, err: errors.New("can't list files")}
		}
		ctx, cancel := stdctx.WithTimeout(stdctx.Background(), lsTimeout)
		defer cancel()
		files, err := lister.LsFiles(ctx, root)
		return filesListedMsg{pack: pack, create: create, files: files, err: err}
	}
}

func (t *Tab) filesListed(msg filesListedMsg) tea.Cmd {
	if msg.err != nil {
		cmds := []tea.Cmd{errCmd(fmt.Errorf("couldn't list files: %w", msg.err))}
		if msg.create {
			cmds = append(cmds, t.savePack(msg.pack, true, "created"))
		}
		return tea.Batch(cmds...)
	}
	title := "Files for " + msg.pack.Name
	pk := ui.NewFilePicker(t.theme, t.keys, title, msg.files, packs.Literals(msg.pack))
	pack, create := msg.pack, msg.create
	verb := "updated"
	if create {
		verb = "created"
	}
	t.pk.dialog = &packDialog{
		picker: pk,
		done:   func() tea.Cmd { return t.savePack(packs.WithFiles(pack, pk.Selected()), create, verb) },
		aborted: func() tea.Cmd {
			if create { // keep what the form asked for
				return t.savePack(pack, true, verb)
			}
			return nil
		},
	}
	return nil
}

// savePack writes pack in a command; packSaved reloads.
func (t *Tab) savePack(pack packs.Pack, create bool, verb string) tea.Cmd {
	dir := t.packsDir
	return func() tea.Msg {
		_, err := packs.Save(dir, pack, create)
		return packSavedMsg{name: pack.Name, verb: verb, err: err}
	}
}

func (t *Tab) packSaved(msg packSavedMsg) tea.Cmd {
	if msg.err != nil {
		return tea.Batch(errCmd(msg.err), t.loadPacks())
	}
	if !msg.deleted {
		t.pk.sel = msg.name
	}
	return tea.Batch(success(fmt.Sprintf("pack %s %s", msg.name, msg.verb)), t.loadPacks())
}

// editPack opens the selected pack's YAML in $EDITOR.
func (t *Tab) editPack() tea.Cmd {
	e, ok := t.selectedPack()
	if !ok {
		return nil
	}
	return t.edit(e.pack.Path)
}

// deletePack asks, then removes the selected pack's file.
func (t *Tab) deletePack() tea.Cmd {
	e, ok := t.selectedPack()
	if !ok {
		return nil
	}
	yes := false
	f := t.styled(huh.NewGroup(
		huh.NewConfirm().Key("confirm").Title("Delete pack " + e.pack.Name + "?").
			Description(t.display(e.pack.Path)).Affirmative("Delete").Negative("Keep").Value(&yes),
	))
	return t.openForm(f, func() tea.Cmd {
		if !yes {
			return nil
		}
		pack := e.pack
		return func() tea.Msg {
			return packSavedMsg{name: pack.Name, verb: "deleted", deleted: true, err: packs.Delete(pack)}
		}
	})
}

// sendPack hands the selected pack to the Sessions tab, which asks for a
// new or a running session.
func (t *Tab) sendPack() tea.Cmd {
	e, ok := t.selectedPack()
	if !ok {
		return nil
	}
	src := tabs.Source{Packs: []string{e.pack.Name}}
	if t.project != nil {
		src.Repo = t.project.Repo
	}
	return tabs.StartSessionFrom(src)
}

// --- view ------------------------------------------------------------------------

// displayDir is the packs folder for display.
func (t *Tab) displayDir() string { return t.display(t.packsDir) }

func (t *Tab) packsListView(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	th, p := t.theme, &t.pk
	if !p.loaded {
		return th.Hint.Render("Reading packs…")
	}
	if len(p.entries) == 0 {
		msg := "No context packs yet. " + t.keys.Context.NewPack.Help().Key + " to make one; they live in " + t.displayDir() + "."
		lines := []string{th.Hint.Render(ansi.Wrap(msg, w, ""))}
		for _, err := range p.errs {
			lines = append(lines, "", th.TableEmpty.Render(ansi.Wrap(err.Error(), w, "/")))
		}
		return strings.Join(lines, "\n")
	}
	body := max(h-footerLines, 1)
	var lines []string
	end := min(p.top+t.packListHeight(), len(p.entries))
	for i := p.top; i < end; i++ {
		lines = append(lines, t.packRow(p.entries[i], i == p.cursor, w)...)
	}
	if len(p.errs) > 0 && len(lines) < body {
		lines = append(lines, th.TableEmpty.Render(ansi.Truncate(fmt.Sprintf("⚠ %d pack(s) couldn't be read", len(p.errs)), w, "…")))
	}
	for len(lines) < body {
		lines = append(lines, "")
	}
	return strings.Join(lines[:body], "\n") + "\n\n" + t.packTotalView(w)
}

// packRow is two lines: "name … ≈1.2k" and the muted description.
func (t *Tab) packRow(e packEntry, selected bool, w int) []string {
	th := t.theme
	right := "≈" + ui.CompactCount(e.tokens)
	label := "  " + e.pack.Name
	if len(e.res.Missing) > 0 {
		label += " ⚠"
	}
	space := max(w-ansi.StringWidth(right)-1, 4)
	label = ansi.Truncate(label, space, "…")
	pad := max(w-ansi.StringWidth(label)-ansi.StringWidth(right), 0)
	desc := e.pack.Description
	if desc == "" {
		desc = fileCount(len(e.res.Paths))
	} else {
		desc += " · " + fileCount(len(e.res.Paths))
	}
	second := ansi.Truncate("    "+desc, w, "…")
	if selected {
		return []string{
			th.TableSelected.Render(label + strings.Repeat(" ", pad) + right),
			th.TableSelected.Render(second + strings.Repeat(" ", max(w-ansi.StringWidth(second), 0))),
		}
	}
	return []string{th.TableRow.Render(label+strings.Repeat(" ", pad)) + th.Hint.Render(right), th.Hint.Render(second)}
}

// packTotalView is the footer: the selected pack's total.
func (t *Tab) packTotalView(w int) string {
	e, ok := t.selectedPack()
	if !ok {
		return ""
	}
	total := e.pack.Name + " ≈ " + ui.CompactCount(e.tokens) + " tokens"
	rest := " · " + fileCount(len(e.res.Paths))
	return t.theme.DetailTitle.Render(ansi.Truncate(total, w, "…")) +
		t.theme.Hint.Render(ansi.Truncate(rest, max(w-ansi.StringWidth(total), 0), ""))
}

// packDetailLines is what the detail pane shows (and scrolls) for e.
func (t *Tab) packDetailLines(e packEntry, w int) []string {
	th := t.theme
	var out []string
	section := func(title string) {
		out = append(out, "", th.DetailLabel.Render(title))
	}
	if e.pack.Description != "" {
		out = append(out, ansi.Wrap(e.pack.Description, w, ""))
	}
	section(fmt.Sprintf("Files (%d)", len(e.res.Paths)))
	if len(e.res.Paths) == 0 {
		out = append(out, th.Hint.Render("none — "+t.keys.Context.AddFiles.Help().Key+" to pick some"))
	}
	for _, p := range e.res.Paths {
		out = append(out, ansi.Truncate("  "+t.display(p), w, "…"))
	}
	if len(e.res.Missing) > 0 {
		section("Matched nothing")
		for _, m := range e.res.Missing {
			out = append(out, th.TableEmpty.Render(ansi.Truncate("  ⚠ "+m, w, "…")))
		}
	}
	if len(e.pack.Files) > 0 {
		section("Patterns")
		for _, f := range e.pack.Files {
			out = append(out, th.Hint.Render(ansi.Truncate("  "+f, w, "…")))
		}
	}
	if len(e.pack.Docs) > 0 {
		section("Docs")
		for _, d := range e.pack.Docs {
			out = append(out, th.Hint.Render(ansi.Truncate("  "+d, w, "…")))
		}
	}
	section("Preamble")
	if strings.TrimSpace(e.pack.Preamble) == "" {
		out = append(out, th.Hint.Render("(none)"))
	} else {
		out = append(out, strings.Split(ansi.Wrap(strings.TrimRight(e.pack.Preamble, "\n"), w, ""), "\n")...)
	}
	return out
}

func (t *Tab) packDetailView(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	th := t.theme
	e, ok := t.selectedPack()
	if !ok {
		return ""
	}
	info := fmt.Sprintf("≈%s tokens · %s · %s", ui.CompactCount(e.tokens), fileCount(len(e.res.Paths)), t.display(e.pack.Path))
	header := th.DetailTitle.Render(ansi.Truncate(e.pack.Name, w, "…")) + "\n" +
		th.Hint.Render(ansi.Truncate(info, w, "…")) + "\n"
	all := t.packDetailLines(e, w)
	body := max(h-detailHeader, 1)
	off := min(t.pk.offset, max(len(all)-body, 0))
	end := min(off+body, len(all))
	return header + strings.Join(all[off:end], "\n")
}

func (t *Tab) packDialogView() string {
	d := t.pk.dialog
	w, h := t.dialogSize()
	body := ""
	if d.picker != nil {
		body = d.picker.View(w, h)
	} else {
		body = d.form.View()
	}
	return t.theme.PaneFocused.Render(ui.Box(body, w, h))
}

func info(text string) tea.Cmd {
	return func() tea.Msg { return ui.InfoMsg{Text: text} }
}

func success(text string) tea.Cmd {
	return func() tea.Msg { return ui.SuccessMsg{Text: text} }
}
