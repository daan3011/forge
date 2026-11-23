package docs

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	glamouransi "charm.land/glamour/v2/ansi"
	"github.com/charmbracelet/x/ansi"

	docfs "github.com/daan3011/forge/internal/docs"
	"github.com/daan3011/forge/internal/ui"
)

// The detail pane shows the selected doc rendered with Glamour at the
// pane's width. Rendering is slow, so it runs in a tea.Cmd and is cached by
// path, mtime and width: an edit (new mtime) or a resize renders again.

// detailHeader is the title line, the path line and a blank line.
const detailHeader = 3

// renderCacheCap is how many rendered docs stay in memory.
const renderCacheCap = 32

type rendered struct {
	lines []string
	err   error
}

type detailState struct {
	offset    int
	path      string // doc the offset belongs to
	cache     map[string]rendered
	order     []string
	rendering string // key in flight
}

func newDetailState() detailState { return detailState{cache: map[string]rendered{}} }

func (d *detailState) remember(key string, r rendered) {
	if _, ok := d.cache[key]; !ok {
		d.order = append(d.order, key)
	}
	d.cache[key] = r
	for len(d.order) > renderCacheCap {
		delete(d.cache, d.order[0])
		d.order = d.order[1:]
	}
}

type renderedMsg struct {
	key   string
	lines []string
	err   error
}

// TargetTab implements tabs.TargetedMsg.
func (renderedMsg) TargetTab() string { return Title }

func renderKey(path string, mod time.Time, width int) string {
	return fmt.Sprintf("%s@%d@%d", path, mod.UnixNano(), width)
}

// detailWidth is the width docs render at: the right pane, or the whole
// pane when the split is collapsed.
func (t *Tab) detailWidth() int {
	if t.split.Collapsed() {
		w, _ := t.split.LeftSize()
		return w
	}
	w, _ := t.split.RightSize()
	return w
}

func (t *Tab) detailBodyHeight() int {
	_, h := t.split.RightSize()
	if t.split.Collapsed() {
		_, h = t.split.LeftSize()
	}
	return max(h-detailHeader, 1)
}

// syncDetail resets the scroll for a new doc and starts rendering the
// selected doc if it isn't cached.
func (t *Tab) syncDetail() tea.Cmd {
	r, ok := t.selected()
	d := &t.det
	if !ok || r.node.IsDir {
		t.focusDetail = false
		d.path, d.offset = "", 0
		return nil
	}
	if d.path != r.node.Path {
		d.path, d.offset = r.node.Path, 0
	}
	w := t.detailWidth()
	if w <= 0 {
		return nil
	}
	key := renderKey(r.node.Path, r.node.ModTime, w)
	if _, ok := d.cache[key]; ok || d.rendering == key {
		return nil
	}
	d.rendering = key
	path, style := r.node.Path, t.theme.GlamourStyle()
	return func() tea.Msg {
		b, err := os.ReadFile(path)
		if err != nil {
			return renderedMsg{key: key, err: err}
		}
		return renderedMsg{key: key, lines: renderDoc(style, w, string(b))}
	}
}

// renderDoc renders text, falling back to wrapped plain text when Glamour
// fails, and returns its lines (none for a blank doc).
func renderDoc(style glamouransi.StyleConfig, w int, text string) []string {
	if meta, body := docfs.SplitFrontMatter([]byte(text)); meta != nil {
		// Front matter would render as a rule and a heading; show it as
		// one emphasised line instead.
		text = string(body)
		if line := docfs.FormatMeta(meta); line != "" {
			text = "*" + line + "*\n\n" + text
		}
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	out := ui.RenderMarkdown(style, w, []string{text})[0]
	if out == "" {
		out = ansi.Wrap(text, w, "")
	}
	return strings.Split(out, "\n")
}

func (t *Tab) rendered(msg renderedMsg) {
	if t.det.rendering == msg.key {
		t.det.rendering = ""
	}
	t.det.remember(msg.key, rendered{lines: msg.lines, err: msg.err})
}

// current returns the rendered selected doc, if it is ready.
func (t *Tab) current() (rendered, bool) {
	r, ok := t.selected()
	if !ok || r.node.IsDir {
		return rendered{}, false
	}
	got, ok := t.det.cache[renderKey(r.node.Path, r.node.ModTime, t.detailWidth())]
	return got, ok
}

// applyJump scrolls the detail to the pending heading once the doc it
// belongs to is selected and rendered.
func (t *Tab) applyJump() {
	if t.jump == "" {
		return
	}
	r, ok := t.selected()
	if !ok || r.node.Path != t.jumpPath {
		if t.want != t.jumpPath {
			t.jump, t.jumpPath = "", "" // the selection moved on
		}
		return
	}
	got, ok := t.current()
	if !ok {
		return // still rendering
	}
	if i := headingLine(got.lines, t.jump); i >= 0 {
		maxOff := max(len(got.lines)-t.detailBodyHeight(), 0)
		t.det.offset = min(i, maxOff)
	}
	t.jump, t.jumpPath = "", ""
}

// headingLine finds the rendered line showing heading: one that reads as
// the heading (once styling, margins and "#" marks are stripped), else the
// first line containing it; -1 if none does.
func headingLine(lines []string, heading string) int {
	want := plainHeading(heading)
	if want == "" {
		return -1
	}
	contains := -1
	for i, l := range lines {
		got := plainHeading(ansi.Strip(l))
		if strings.EqualFold(got, want) {
			return i
		}
		if contains < 0 && strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
			contains = i
		}
	}
	return contains
}

// plainHeading drops "#" marks, inline markdown markers and outer space.
func plainHeading(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSpace(strings.TrimLeft(s, "#"))
	s = strings.NewReplacer("**", "", "__", "", "`", "", "*", "").Replace(s)
	return strings.TrimSpace(s)
}

func (t *Tab) scrollDetail(delta int) {
	got, ok := t.current()
	if !ok {
		return
	}
	maxOff := max(len(got.lines)-t.detailBodyHeight(), 0)
	t.det.offset = min(max(t.det.offset+delta, 0), maxOff)
}

// detailView renders the detail pane at w x h.
func (t *Tab) detailView(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	th := t.theme
	r, ok := t.selected()
	if !ok {
		return th.Hint.Render("Nothing selected")
	}
	n := r.node
	if r.top || n.IsDir {
		title := n.Name + "/"
		sub := n.Rel
		if r.top {
			title, sub = t.roots[r.root].name, t.roots[r.root].path
		}
		return th.DetailTitle.Render(ansi.Truncate(title, w, "…")) + "\n" +
			th.Hint.Render(ansi.Truncate(sub, w, "…")) + "\n\n" +
			th.Body.Render(docCount(n.Docs()))
	}
	sub := n.Rel + " · updated " + ui.ShortAge(t.now().Sub(n.ModTime))
	if r.session {
		sd := t.roots[r.root].sessions.info[n.Path]
		sub = "from session " + sd.session + " · " + string(sd.status) + " · read-only · " + ui.ShortAge(t.now().Sub(n.ModTime))
	}
	header := th.DetailTitle.Render(ansi.Truncate(n.Name, w, "…")) + "\n" +
		th.Hint.Render(ansi.Truncate(sub, w, "…")) + "\n"
	got, ok := t.current()
	switch {
	case !ok:
		return header + "\n" + th.Hint.Render("Rendering…")
	case got.err != nil:
		return header + "\n" + th.TableEmpty.Render(ansi.Wrap(got.err.Error(), w, ""))
	case len(got.lines) == 0:
		if r.session {
			return header + "\n" + th.Hint.Render("(empty doc)")
		}
		return header + "\n" + th.Hint.Render("(empty doc — "+t.keys.Docs.Edit.Help().Key+" to write it)")
	}
	body := max(h-detailHeader, 1)
	off := min(t.det.offset, max(len(got.lines)-body, 0))
	end := min(off+body, len(got.lines))
	return header + "\n" + strings.Join(got.lines[off:end], "\n")
}
