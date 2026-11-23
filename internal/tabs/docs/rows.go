package docs

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"

	docfs "github.com/daan3011/forge/internal/docs"
	"github.com/daan3011/forge/internal/ui"
)

// row is one visible line of the tree.
type row struct {
	node  *docfs.Node
	root  int // index into Tab.roots
	depth int
	top   bool            // a project's top level (all projects)
	dec   *docfs.Decision // set in the decisions view
	// session marks the read-only "From sessions" rows (P6.5): the group,
	// a session, or one of its docs.
	session bool
}

// rebuild flattens the open folders into rows, keeping the selection.
func (t *Tab) rebuild() {
	t.rows = t.rows[:0]
	if t.decisionsView {
		t.rebuildDecisions()
	} else {
		t.rebuildTree()
	}
	t.restoreCursor()
}

// rebuildTree fills rows with the open folders of every root.
func (t *Tab) rebuildTree() {
	for i, r := range t.roots {
		if r.tree == nil {
			continue
		}
		if t.all() {
			t.rows = append(t.rows, row{node: r.tree, root: i, top: true})
			if t.expanded[r.tree.Path] {
				t.addChildren(r.tree, i, 1)
				t.addSessionDocs(i, 1)
			}
			continue
		}
		t.addChildren(r.tree, i, 0)
		t.addSessionDocs(i, 0)
	}
}

// addSessionDocs adds root i's "From sessions" group, open folders first.
func (t *Tab) addSessionDocs(i, depth int) {
	g := t.roots[i].sessions.group
	if g == nil {
		return
	}
	t.rows = append(t.rows, row{node: g, root: i, depth: depth, session: true})
	if !t.expanded[g.Path] {
		return
	}
	for _, s := range g.Children {
		t.rows = append(t.rows, row{node: s, root: i, depth: depth + 1, session: true})
		if !t.expanded[s.Path] {
			continue
		}
		for _, d := range s.Children {
			t.rows = append(t.rows, row{node: d, root: i, depth: depth + 2, session: true})
		}
	}
}

// readOnlyNote is the toast for edit keys on session docs.
const readOnlyNote = "session docs are read-only here — open the session to change them"

// onSessionRow reports whether the cursor is on a "From sessions" row.
func (t *Tab) onSessionRow() bool {
	r, ok := t.selected()
	return ok && r.session
}

// restoreCursor selects the wanted path, else the previous selection,
// else the same position.
func (t *Tab) restoreCursor() {
	idx := -1
	for _, p := range []string{t.want, t.selPath} {
		if p == "" {
			continue
		}
		if idx = t.indexOf(p); idx >= 0 {
			break
		}
	}
	if idx >= 0 && t.rows[idx].node.Path == t.want {
		t.want = ""
	}
	if idx < 0 {
		idx = min(t.cursor, len(t.rows)-1)
	}
	t.setCursor(max(idx, 0))
}

func (t *Tab) addChildren(n *docfs.Node, rootIdx, depth int) {
	for _, c := range n.Children {
		t.rows = append(t.rows, row{node: c, root: rootIdx, depth: depth})
		if c.IsDir && t.expanded[c.Path] {
			t.addChildren(c, rootIdx, depth+1)
		}
	}
}

func (t *Tab) indexOf(path string) int {
	for i, r := range t.rows {
		if r.node.Path == path {
			return i
		}
	}
	return -1
}

// reveal opens every folder between a root and path.
func (t *Tab) reveal(path string) {
	for _, r := range t.roots {
		rel, err := filepath.Rel(r.path, path)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		t.expanded[r.path] = true
		dir := r.path
		parts := strings.Split(filepath.Dir(rel), string(filepath.Separator))
		for _, p := range parts {
			if p == "." {
				break
			}
			dir = filepath.Join(dir, p)
			t.expanded[dir] = true
		}
	}
}

// selectDoc selects the doc at path, opening its folders, and scrolls the
// detail to heading once it is rendered (the top when heading is ""). A
// path the tree doesn't show yet is selected after the next scan.
func (t *Tab) selectDoc(path, heading string) {
	if t.decisionsView && !t.isDecision(path) {
		t.decisionsView = false // only the tree shows it
	}
	t.reveal(path)
	t.want = path
	t.rebuild()
	t.jump, t.jumpPath = heading, path
	if t.det.path == path {
		t.det.offset = 0
	}
}

func (t *Tab) selected() (row, bool) {
	if t.cursor < 0 || t.cursor >= len(t.rows) {
		return row{}, false
	}
	return t.rows[t.cursor], true
}

func (t *Tab) move(delta int) { t.setCursor(t.cursor + delta) }

func (t *Tab) setCursor(i int) {
	if len(t.rows) == 0 {
		t.cursor, t.top, t.selPath = 0, 0, ""
		return
	}
	t.cursor = min(max(i, 0), len(t.rows)-1)
	t.selPath = t.rows[t.cursor].node.Path
	t.clampTop()
}

func (t *Tab) treeHeight() int {
	_, h := t.split.LeftSize()
	return max(h, 1)
}

// clampTop scrolls the tree so the cursor is on screen.
func (t *Tab) clampTop() {
	h := t.treeHeight()
	if t.cursor < t.top {
		t.top = t.cursor
	}
	if t.cursor >= t.top+h {
		t.top = t.cursor - h + 1
	}
	t.top = max(min(t.top, len(t.rows)-h), 0)
}

// treeView renders the visible rows at w x h.
func (t *Tab) treeView(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	if len(t.rows) == 0 {
		if t.decisionsView && t.scanned && len(t.roots) > 0 {
			return t.decisionsEmptyView(w)
		}
		return t.emptyView(w)
	}
	end := min(t.top+h, len(t.rows))
	lines := make([]string, 0, end-t.top)
	for i := t.top; i < end; i++ {
		lines = append(lines, t.rowView(t.rows[i], i == t.cursor, w))
	}
	return strings.Join(lines, "\n")
}

func (t *Tab) rowView(r row, selected bool, w int) string {
	if r.dec != nil {
		return t.decisionRowView(r, selected, w)
	}
	th := t.theme
	n := r.node
	indent := strings.Repeat("  ", r.depth)
	var label, right string
	style := th.TableRow
	switch {
	case r.session && n.IsDir:
		mark := "▸ "
		if t.expanded[n.Path] {
			mark = "▾ "
		}
		label = mark + n.Name
		right = docCount(n.Docs())
		style = th.DetailLabel
		if n.Name == fromSessions {
			style = th.DetailTitle
		}
	case r.session:
		label = "  " + n.Name
		sd := t.roots[r.root].sessions.info[n.Path]
		right = string(sd.status)
		if sd.agent {
			right = "✎ " + right
		}
		right += " " + ui.ShortAge(t.now().Sub(n.ModTime))
	case r.top:
		mark := "▸ "
		if t.expanded[n.Path] {
			mark = "▾ "
		}
		label = mark + t.roots[r.root].name
		right = docCount(n.Docs())
		style = th.DetailTitle
	case n.IsDir:
		mark := "▸ "
		if t.expanded[n.Path] {
			mark = "▾ "
		}
		label = mark + n.Name + "/"
		right = docCount(n.Docs())
		style = th.DetailLabel
	default:
		label = "  " + n.Name
		if t.isMarked(n.Path) {
			label = "● " + n.Name
			style = th.DocMarked
		}
		right = ui.ShortAge(t.now().Sub(n.ModTime))
	}
	label = indent + label
	space := w - ansi.StringWidth(right) - 1
	if space < 4 {
		right, space = "", w
	}
	label = ansi.Truncate(label, space, "…")
	pad := max(w-ansi.StringWidth(label)-ansi.StringWidth(right), 0)
	line := label + strings.Repeat(" ", pad) + right
	if selected {
		return th.TableSelected.Render(line)
	}
	if right == "" {
		return style.Render(line)
	}
	return style.Render(label+strings.Repeat(" ", pad)) + th.Hint.Render(right)
}

// emptyView explains why there is nothing to show.
func (t *Tab) emptyView(w int) string {
	th := t.theme
	switch {
	case len(t.roots) == 0:
		return th.TableEmpty.Render(ansi.Wrap("No projects configured. Add one under projects: in the config.", w, ""))
	case !t.scanned:
		return th.Hint.Render("Scanning docs…")
	case t.roots[0].err != nil:
		return th.TableEmpty.Render(ansi.Wrap("Can't read "+t.roots[0].path+": "+t.roots[0].err.Error(), w, ""))
	}
	msg := "No docs in " + t.roots[0].path
	return th.TableEmpty.Render(ansi.Wrap(msg, w, "/")) + "\n\n" +
		th.Hint.Render(t.keys.Docs.New.Help().Key+"  create one")
}
