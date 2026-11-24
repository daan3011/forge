package prs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/delta"
	"github.com/daan3011/forge/internal/editor"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// diffTimeout bounds fetching a diff, and rendering it.
const diffTimeout = time.Minute

// diffCacheSize is how many fetched diffs are kept in memory.
const diffCacheSize = 8

// deltaWorkers is how many delta processes render a diff's files at once.
const deltaWorkers = 4

// diffHeaderLines are the viewer's title line and the rule under it.
const diffHeaderLines = 2

// diffView is the full-screen diff viewer (P2.4). It takes over the tab
// while open.
type diffView struct {
	id    int // tells this viewer's messages from an earlier one's
	pr    github.PR
	files []github.DiffFile

	loaded bool

	vp     viewport.Model
	starts []int // first viewport line of each file
	file   int   // the file in view

	// Rendered text per file for width; delta reports whether delta
	// rendered it (it draws its own file headers).
	rendered  []string
	delta     bool
	width     int
	rendering bool
}

type diffLoadedMsg struct {
	id    int
	key   string
	files []github.DiffFile
	err   error
}

type diffRenderedMsg struct {
	id       int
	width    int
	out      []string
	delta    bool
	deltaErr error // delta failed for some file; those got Lip Gloss colours
}

func (diffLoadedMsg) TargetTab() string   { return Title }
func (diffRenderedMsg) TargetTab() string { return Title }

// diffCache keeps recently fetched diffs, keyed by PR and updatedAt.
type diffCache struct {
	order []string
	files map[string][]github.DiffFile
}

func (c *diffCache) get(k string) ([]github.DiffFile, bool) {
	f, ok := c.files[k]
	return f, ok
}

func (c *diffCache) put(k string, files []github.DiffFile) {
	if c.files == nil {
		c.files = map[string][]github.DiffFile{}
	}
	if _, ok := c.files[k]; !ok {
		c.order = append(c.order, k)
	}
	c.files[k] = files
	for len(c.order) > diffCacheSize {
		delete(c.files, c.order[0])
		c.order = c.order[1:]
	}
}

// updateDiffMsg handles the viewer's messages.
func (t *Tab) updateDiffMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case diffLoadedMsg:
		return t.diffLoaded(msg), true
	case diffRenderedMsg:
		return t.diffRendered(msg), true
	}
	return nil, false
}

// openDiff opens the viewer on the selected PR.
func (t *Tab) openDiff() tea.Cmd {
	p, ok := t.selected()
	if !ok {
		return nil
	}
	if t.gh == nil {
		return errCmd(errors.New("GitHub isn't available"))
	}
	t.diffID++
	v := &diffView{id: t.diffID, pr: p, vp: viewport.New()}
	t.diff = v
	k := detailKey(p)
	if files, ok := t.diffs.get(k); ok {
		t.showDiff(files)
		return t.ensureDiffRendered()
	}
	gh, id := t.gh, v.id
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), diffTimeout)
		defer cancel()
		raw, err := gh.Diff(ctx, p.Repo, p.Number)
		if err != nil {
			return diffLoadedMsg{id: id, key: k, err: err}
		}
		return diffLoadedMsg{id: id, key: k, files: github.ParseDiff(raw)}
	}
}

func (t *Tab) closeDiff() tea.Cmd {
	t.diff = nil
	return nil
}

func (t *Tab) diffLoaded(msg diffLoadedMsg) tea.Cmd {
	v := t.diff
	if msg.err == nil {
		t.diffs.put(msg.key, msg.files)
	}
	if v == nil || msg.id != v.id {
		return nil
	}
	if msg.err != nil {
		t.diff = nil
		if errors.Is(msg.err, github.ErrDiffTooLarge) {
			return info(fmt.Sprintf("#%d: the diff is too large for the GitHub API; open it in the browser (%s)", v.pr.Number, t.keys.PRs.Open.Help().Key))
		}
		return errCmd(github.ActionError("load the diff of", v.pr.Number, msg.err))
	}
	t.showDiff(msg.files)
	return t.ensureDiffRendered()
}

func (t *Tab) showDiff(files []github.DiffFile) {
	v := t.diff
	v.files, v.loaded = files, true
	v.rendered, v.width = nil, 0
	t.layoutDiff()
}

// diffSize is the viewer's inner size: the tab below the section bar,
// inside the pane border.
func (t *Tab) diffSize() (w, h int) {
	return max(t.width-2, 10), max(t.height-3, 3)
}

// diffListWidth is the file list's width; narrow terminals get no list.
func (t *Tab) diffListWidth() int {
	w, _ := t.diffSize()
	if w < 60 {
		return 0
	}
	return min(max(w*3/10, 24), w/2)
}

// diffTextWidth is the diff pane's width.
func (t *Tab) diffTextWidth() int {
	w, _ := t.diffSize()
	if lw := t.diffListWidth(); lw > 0 {
		return max(w-lw-1, 8)
	}
	return w
}

// ensureDiffRendered renders the diff, off the update loop, when what is
// shown was rendered for another width (or not yet).
func (t *Tab) ensureDiffRendered() tea.Cmd {
	v := t.diff
	if v == nil || !v.loaded || v.rendering || len(v.files) == 0 {
		return nil
	}
	width := t.diffTextWidth()
	if v.rendered != nil && v.width == width {
		return nil
	}
	v.rendering = true
	files, id, th, r := v.files, v.id, t.theme, t.delta
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), diffTimeout)
		defer cancel()
		out, usedDelta, err := renderDiff(ctx, th, r, files, width)
		return diffRenderedMsg{id: id, width: width, out: out, delta: usedDelta, deltaErr: err}
	}
}

func (t *Tab) diffRendered(msg diffRenderedMsg) tea.Cmd {
	v := t.diff
	if v == nil || msg.id != v.id {
		return nil
	}
	v.rendering = false
	var cmd tea.Cmd
	if msg.deltaErr != nil && !t.deltaWarned {
		t.deltaWarned = true
		cmd = info("delta failed, showing plain colours: " + msg.deltaErr.Error())
	}
	if msg.width == t.diffTextWidth() {
		v.rendered, v.delta, v.width = msg.out, msg.delta, msg.width
		t.layoutDiff()
	}
	return tea.Batch(cmd, t.ensureDiffRendered()) // the width changed meanwhile
}

// renderDiff colours every file for width: with delta when it is installed
// (per file, so each file's first line is known), else with Lip Gloss. A
// file delta fails on gets Lip Gloss colours; the first error is returned.
func renderDiff(ctx context.Context, th *ui.Theme, r delta.Renderer, files []github.DiffFile, width int) ([]string, bool, error) {
	out := make([]string, len(files))
	if r == nil || !r.Available() {
		for i, f := range files {
			out[i] = colorizeFile(th, f, width)
		}
		return out, false, nil
	}
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		next     = make(chan int)
	)
	for range min(deltaWorkers, len(files)) {
		wg.Go(func() {
			for i := range next {
				s, err := r.Render(ctx, files[i].Text, width)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					s = colorizeFile(th, files[i], width)
				}
				out[i] = s
			}
		})
	}
	for i := range files {
		next <- i
	}
	close(next)
	wg.Wait()
	return out, true, firstErr
}

// statusGlyph is the one-letter mark of a file's status.
func statusGlyph(th *ui.Theme, status string) string {
	switch status {
	case github.FileAdded:
		return th.DiffAdd.Render("A")
	case github.FileDeleted:
		return th.DiffDel.Render("D")
	case github.FileRenamed:
		return th.DiffHunk.Render("R")
	case github.FileBinary:
		return th.DiffMeta.Render("B")
	}
	return th.DiffHunk.Render("M")
}

// fileTitle is "old → new" for renames, else the path.
func fileTitle(f github.DiffFile) string {
	if f.Status == github.FileRenamed && f.OldPath != f.NewPath {
		return f.OldPath + " → " + f.NewPath
	}
	return f.Path()
}

// fileBand is the header line drawn above each file in Lip Gloss mode.
func fileBand(th *ui.Theme, f github.DiffFile, width int) string {
	text := fmt.Sprintf(" %s  +%d -%d", fileTitle(f), f.Additions, f.Deletions)
	return th.DiffFileHeader.Width(width).Render(ansi.Truncate(text, width, "…"))
}

// colorizeFile colours one file's diff with the theme: a header band, then
// hunks. The git header lines the band repeats are dropped; the rest
// (mode, rename, binary) stay, muted.
func colorizeFile(th *ui.Theme, f github.DiffFile, width int) string {
	lines := []string{fileBand(th, f, width)}
	inHdr := true
	for i, l := range strings.Split(f.Text, "\n") {
		if i == 0 {
			continue // diff --git
		}
		l = ansi.Truncate(strings.ReplaceAll(l, "\t", "    "), width, "…")
		if inHdr && !strings.HasPrefix(l, "@@") {
			if strings.HasPrefix(l, "index ") || strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "+++ ") {
				continue
			}
			lines = append(lines, th.DiffMeta.Render(l))
			continue
		}
		inHdr = false
		switch {
		case strings.HasPrefix(l, "@@"):
			lines = append(lines, th.DiffHunk.Render(l))
		case strings.HasPrefix(l, "+"):
			lines = append(lines, th.DiffAdd.Render(l))
		case strings.HasPrefix(l, "-"):
			lines = append(lines, th.DiffDel.Render(l))
		case strings.HasPrefix(l, `\`):
			lines = append(lines, th.DiffMeta.Render(l))
		default:
			lines = append(lines, th.Body.Render(l))
		}
	}
	return strings.Join(lines, "\n")
}

// plainFile is a file before its colours land: the band and the raw diff.
func plainFile(th *ui.Theme, f github.DiffFile, width int) string {
	lines := []string{fileBand(th, f, width)}
	for i, l := range strings.Split(f.Text, "\n") {
		if i > 0 {
			lines = append(lines, ansi.Truncate(strings.ReplaceAll(l, "\t", "    "), width, "…"))
		}
	}
	return strings.Join(lines, "\n")
}

// layoutDiff fills the viewport with every file, a blank line apart, and
// records where each starts.
func (t *Tab) layoutDiff() {
	v := t.diff
	if v == nil {
		return
	}
	_, h := t.diffSize()
	width := t.diffTextWidth()
	v.vp.SetWidth(width)
	v.vp.SetHeight(max(h-diffHeaderLines, 1))
	var lines []string
	v.starts = v.starts[:0]
	for i, f := range v.files {
		if i > 0 {
			lines = append(lines, "")
		}
		v.starts = append(v.starts, len(lines))
		text := plainFile(t.theme, f, width)
		if i < len(v.rendered) && v.width == width {
			text = v.rendered[i]
		}
		for _, l := range strings.Split(text, "\n") {
			lines = append(lines, ansi.Truncate(l, width, "…"))
		}
	}
	y := v.vp.YOffset()
	v.vp.SetContentLines(lines)
	v.vp.SetYOffset(y)
	v.file = min(v.file, max(len(v.starts)-1, 0))
}

// fileAt is the file shown at viewport line y.
func (v *diffView) fileAt(y int) int {
	i := 0
	for j, s := range v.starts {
		if s > y {
			break
		}
		i = j
	}
	return i
}

func (t *Tab) diffScroll(f func(vp *viewport.Model)) {
	v := t.diff
	f(&v.vp)
	v.file = v.fileAt(v.vp.YOffset())
}

// jumpFile moves to the next (+1) or previous (-1) file.
func (t *Tab) jumpFile(dir int) tea.Cmd {
	v := t.diff
	if v == nil || len(v.starts) == 0 {
		return nil
	}
	i := min(max(v.file+dir, 0), len(v.starts)-1)
	v.file = i
	v.vp.SetYOffset(v.starts[i])
	return nil
}

func (t *Tab) updateDiff(kp tea.KeyPressMsg) tea.Cmd {
	v := t.diff
	k := t.keys.Diff
	if key.Matches(kp, k.Close) {
		return t.closeDiff()
	}
	if !v.loaded {
		return nil
	}
	half := max(v.vp.Height()/2, 1)
	switch {
	case key.Matches(kp, k.Up):
		t.diffScroll(func(vp *viewport.Model) { vp.SetYOffset(vp.YOffset() - 1) })
	case key.Matches(kp, k.Down):
		t.diffScroll(func(vp *viewport.Model) { vp.SetYOffset(vp.YOffset() + 1) })
	case key.Matches(kp, k.PageUp):
		t.diffScroll(func(vp *viewport.Model) { vp.SetYOffset(vp.YOffset() - half) })
	case key.Matches(kp, k.PageDown):
		t.diffScroll(func(vp *viewport.Model) { vp.SetYOffset(vp.YOffset() + half) })
	case key.Matches(kp, k.Top):
		t.diffScroll(func(vp *viewport.Model) { vp.GotoTop() })
	case key.Matches(kp, k.Bottom):
		t.diffScroll(func(vp *viewport.Model) { vp.GotoBottom() })
	case key.Matches(kp, k.NextFile):
		return t.jumpFile(1)
	case key.Matches(kp, k.PrevFile):
		return t.jumpFile(-1)
	case key.Matches(kp, k.Approve):
		return t.openReview(github.ReviewEventApprove)
	case key.Matches(kp, k.RequestChanges):
		return t.openReview(github.ReviewEventRequestChanges)
	case key.Matches(kp, k.Comment):
		return t.openReview(github.ReviewEventComment)
	case key.Matches(kp, k.Open):
		return t.openInBrowser()
	}
	return nil
}

// --- reviews -------------------------------------------------------------------

// reviewKinds describe each review event for its dialog and toasts.
var reviewKinds = map[string]struct {
	title, verb, activity, done, affirm string
	needsBody                           bool
}{
	github.ReviewEventApprove:        {"Approve", "approve", "approving", "approved", "Approve", false},
	github.ReviewEventRequestChanges: {"Request changes on", "request changes on", "requesting changes on", "requested changes on", "Request changes", true},
	github.ReviewEventComment:        {"Comment on", "comment on", "commenting on", "commented on", "Comment", true},
}

// viewer is the logged-in GitHub user, as the searches report it.
func (t *Tab) viewer() string {
	for _, s := range t.sections {
		if s.res.Viewer != "" {
			return s.res.Viewer
		}
	}
	return ""
}

// reviewTarget is the PR in the diff viewer, else the selected one, when
// it can be reviewed by the viewer with event.
func (t *Tab) reviewTarget(event string) (github.PR, tea.Cmd, bool) {
	var p github.PR
	if t.diff != nil {
		p = t.diff.pr
	} else {
		var ok bool
		if p, ok = t.selected(); !ok {
			return github.PR{}, nil, false
		}
	}
	if t.gh == nil {
		return github.PR{}, errCmd(errors.New("GitHub isn't available")), false
	}
	if p.State == github.StateMerged || p.State == github.StateClosed {
		return github.PR{}, info(fmt.Sprintf("#%d is %s", p.Number, p.State)), false
	}
	if me := t.viewer(); event != github.ReviewEventComment && me != "" && strings.EqualFold(p.Author, me) {
		return github.PR{}, info(fmt.Sprintf("#%d is your own PR; you can only comment on it", p.Number)), false
	}
	return p, nil, true
}

// openReview asks for the review's body (ctrl+e opens $EDITOR), then
// submits it.
func (t *Tab) openReview(event string) tea.Cmd {
	p, cmd, ok := t.reviewTarget(event)
	if !ok {
		return cmd
	}
	kind := reviewKinds[event]
	body, confirmed := "", true
	desc := "optional · ctrl+e opens $EDITOR"
	if kind.needsBody {
		desc = "required · ctrl+e opens $EDITOR"
	}
	text := huh.NewText().Key("body").
		Title(fmt.Sprintf("%s #%d %s", kind.title, p.Number, p.Title)).
		Description(desc).
		Editor(editor.Command()...).EditorExtension("md").
		Lines(8).Value(&body)
	if kind.needsBody {
		text = text.Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New("write something first")
			}
			return nil
		})
	}
	form := huh.NewForm(huh.NewGroup(text,
		huh.NewConfirm().Key("confirm").
			Title(fmt.Sprintf("Submit the review of #%d?", p.Number)).
			Affirmative(kind.affirm).Negative("Cancel").Value(&confirmed)))
	return t.openDialog(form, func() tea.Cmd {
		if !confirmed {
			return nil
		}
		return t.submitReview(p, event, body)
	})
}

func (t *Tab) submitReview(p github.PR, event, body string) tea.Cmd {
	kind := reviewKinds[event]
	gh := t.gh
	body = strings.TrimSpace(body)
	return t.runAction(p, fmt.Sprintf("%s #%d", kind.activity, p.Number), kind.verb, true, func(ctx context.Context) (string, error) {
		if err := gh.SubmitReview(ctx, p.Repo, p.Number, event, body); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s #%d", kind.done, p.Number), nil
	})
}

// --- view ----------------------------------------------------------------------

func (t *Tab) diffViewString() string {
	v := t.diff
	th := t.theme
	w, h := t.diffSize()
	head := t.diffHeader(w)
	if !v.loaded {
		body := anim.HammerFrame(t.anim.Frame(), t.anim.Settings(), th) + " " + th.Hint.Render("fetching the diff…")
		return th.PaneFocused.Render(ui.Box(head+"\n\n"+body, w, h))
	}
	var body string
	switch {
	case len(v.files) == 0:
		body = th.TableEmpty.Render("no changes")
	case t.diffListWidth() > 0:
		lw := t.diffListWidth()
		bh := max(h-diffHeaderLines, 1)
		list := ui.Box(t.fileList(lw, bh), lw, bh)
		sep := th.SectionSep.Render(strings.TrimSuffix(strings.Repeat("│\n", bh), "\n"))
		body = joinColumns(list, sep, v.vp.View())
	default:
		body = v.vp.View()
	}
	return th.PaneFocused.Render(ui.Box(head+"\n"+body, w, h))
}

// joinColumns puts blocks side by side, line by line.
func joinColumns(cols ...string) string {
	split := make([][]string, len(cols))
	n := 0
	for i, c := range cols {
		split[i] = strings.Split(c, "\n")
		n = max(n, len(split[i]))
	}
	var b strings.Builder
	for row := range n {
		if row > 0 {
			b.WriteByte('\n')
		}
		for _, lines := range split {
			if row < len(lines) {
				b.WriteString(lines[row])
			}
		}
	}
	return b.String()
}

// diffHeader is "#12 title · base ← head · 3 files +10 -2" over a rule.
func (t *Tab) diffHeader(width int) string {
	v := t.diff
	th := t.theme
	p := v.pr
	line := th.DetailBand.Render(fmt.Sprintf(" #%d %s ", p.Number, p.Title))
	if p.BaseRef != "" || p.HeadRef != "" {
		line += th.SectionSep.Render(" · ") + th.Hint.Render(p.BaseRef+" ← "+p.HeadRef)
	}
	if v.loaded {
		add, del := 0, 0
		for _, f := range v.files {
			add += f.Additions
			del += f.Deletions
		}
		files := "files"
		if len(v.files) == 1 {
			files = "file"
		}
		line += th.SectionSep.Render(" · ") + th.Body.Render(fmt.Sprintf("%d %s ", len(v.files), files)) +
			th.DiffAdd.Render(fmt.Sprintf("+%d", add)) + " " + th.DiffDel.Render(fmt.Sprintf("-%d", del))
		if len(v.files) > 0 {
			line += th.SectionSep.Render(" · ") + th.Hint.Render(fmt.Sprintf("file %d/%d", v.file+1, len(v.files)))
		}
	}
	return ansi.Truncate(line, width, "…") + "\n" + th.PreviewRule.Render(strings.Repeat("─", width))
}

// fileList is the list of files, scrolled to keep the file in view on
// screen.
func (t *Tab) fileList(width, height int) string {
	v := t.diff
	th := t.theme
	top := 0
	if v.file >= height {
		top = v.file - height + 1
	}
	var lines []string
	for i := top; i < len(v.files) && len(lines) < height; i++ {
		f := v.files[i]
		stat := fmt.Sprintf(" +%d -%d", f.Additions, f.Deletions)
		room := max(width-3-len(stat), 4)
		path := shortenLeft(f.Path(), room)
		if i == v.file {
			plain := fmt.Sprintf("%s %s", fileStatusLetter(f.Status), path)
			pad := max(width-ansi.StringWidth(plain)-len(stat), 1)
			lines = append(lines, th.DiffFileActive.Render(plain+strings.Repeat(" ", pad)+stat))
			continue
		}
		left := statusGlyph(th, f.Status) + " " + th.DiffFile.Render(path)
		pad := max(width-ansi.StringWidth(left)-len(stat), 1)
		lines = append(lines, left+strings.Repeat(" ", pad)+th.DiffAdd.Render(fmt.Sprintf(" +%d", f.Additions))+th.DiffDel.Render(fmt.Sprintf(" -%d", f.Deletions)))
	}
	return strings.Join(lines, "\n")
}

func fileStatusLetter(status string) string {
	switch status {
	case github.FileAdded:
		return "A"
	case github.FileDeleted:
		return "D"
	case github.FileRenamed:
		return "R"
	case github.FileBinary:
		return "B"
	}
	return "M"
}

// shortenLeft keeps the end of path, the part that names the file.
func shortenLeft(path string, width int) string {
	if ansi.StringWidth(path) <= width {
		return path
	}
	r := []rune(path)
	for len(r) > 0 && ansi.StringWidth(string(r))+1 > width {
		r = r[1:]
	}
	return "…" + string(r)
}

// --- help & palette --------------------------------------------------------------

func (t *Tab) diffHelp() []key.Binding {
	k := t.keys.Diff
	return []key.Binding{k.NextFile, k.PrevFile, k.Down, k.Up, k.Approve, k.RequestChanges, k.Comment, k.Close}
}

// ViewerCommands are the diff viewer's palette commands, for the key
// inventory (`forge keys --dump`): the palette offers them only while the
// viewer is open.
func (t *Tab) ViewerCommands() []tabs.Command { return t.diffCommands() }

func (t *Tab) diffCommands() []tabs.Command {
	k := t.keys.Diff
	return []tabs.Command{
		{ID: "diff_next_file", Title: "Diff: next file", Key: k.NextFile, Run: func() tea.Cmd { return t.jumpFile(1) }},
		{ID: "diff_prev_file", Title: "Diff: previous file", Key: k.PrevFile, Run: func() tea.Cmd { return t.jumpFile(-1) }},
		{ID: "diff_close", Title: "Diff: close", Key: k.Close, Run: t.closeDiff},
	}
}
