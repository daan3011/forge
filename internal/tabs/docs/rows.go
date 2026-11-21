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
	top   bool // a project's top level (all projects)
}

// rebuild flattens the open folders into rows, keeping the selection.
func (t *Tab) rebuild() {
	t.rows = t.rows[:0]
	for i, r := range t.roots {
		if r.tree == nil {
			continue
		}
		if t.all() {
			t.rows = append(t.rows, row{node: r.tree, root: i, top: true})
			if t.expanded[r.tree.Path] {
				t.addChildren(r.tree, i, 1)
			}
			continue
		}
		t.addChildren(r.tree, i, 0)
	}
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
	th := t.theme
	n := r.node
	indent := strings.Repeat("  ", r.depth)
	var label, right string
	style := th.TableRow
	switch {
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
