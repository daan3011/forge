package docs

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	docfs "github.com/daan3011/forge/internal/docs"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// Send to agent (P4.3): space marks docs, s hands the marked docs (or the
// one under the cursor) to the Sessions tab, which asks for a new or a
// running session.

// target is the doc space and s act on: the search result under the cursor
// while searching, else the selected tree row. Folders don't count.
func (t *Tab) target() (string, bool) {
	if t.searching() {
		h, ok := t.currentHit()
		return h.Doc.Path, ok
	}
	r, ok := t.selected()
	if !ok || r.node.IsDir {
		return "", false
	}
	return r.node.Path, true
}

// rootOf is the index of the root path is under; 0 when none is.
func (t *Tab) rootOf(path string) int {
	for i, r := range t.roots {
		rel, err := filepath.Rel(r.path, path)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return i
		}
	}
	return 0
}

func (t *Tab) isMarked(path string) bool { return slices.Contains(t.marks, path) }

// toggleMark marks or unmarks the target doc and moves down the list.
func (t *Tab) toggleMark() {
	path, ok := t.target()
	if !ok {
		return
	}
	if i := slices.Index(t.marks, path); i >= 0 {
		t.marks = slices.Delete(t.marks, i, i+1)
	} else {
		t.marks = append(t.marks, path)
	}
	switch {
	case t.searching():
		t.setHit(t.hitCursor + 1)
	case !t.focusDetail:
		t.move(1)
	}
}

func (t *Tab) clearMarks() { t.marks = nil }

// pruneMarks drops marks on docs the latest scan no longer has.
func (t *Tab) pruneMarks() {
	if len(t.marks) == 0 {
		return
	}
	have := map[string]bool{}
	for _, r := range t.roots {
		collectDocs(r.tree, have)
	}
	t.marks = slices.DeleteFunc(t.marks, func(p string) bool { return !have[p] })
}

func collectDocs(n *docfs.Node, into map[string]bool) {
	if n == nil {
		return
	}
	if !n.IsDir {
		into[n.Path] = true
	}
	for _, c := range n.Children {
		collectDocs(c, into)
	}
}

// sendToAgent asks the Sessions tab to send the marked docs, or the target
// doc when none are marked, to an agent session. The session's project is
// the one the first doc belongs to.
func (t *Tab) sendToAgent() tea.Cmd {
	paths := slices.Clone(t.marks)
	if len(paths) == 0 {
		path, ok := t.target()
		if !ok {
			return func() tea.Msg { return ui.InfoMsg{Text: "select or mark a doc to send"} }
		}
		paths = []string{path}
	}
	src := tabs.Source{Docs: paths, Repo: t.repoOf(t.rootOf(paths[0]))}
	t.clearMarks()
	return tabs.StartSessionFrom(src)
}

// repoOf is the GitHub repo of root i's project; "" lets the Sessions tab
// use the active project.
func (t *Tab) repoOf(i int) string {
	if i < 0 || i >= len(t.roots) {
		return ""
	}
	for _, p := range t.cfg.Projects {
		if p.Name == t.roots[i].name {
			return p.Repo
		}
	}
	return ""
}

// StatusDetail implements tabs.StatusDetailer: the marked docs, if any.
func (t *Tab) StatusDetail() string {
	if len(t.marks) == 0 {
		return ""
	}
	return fmt.Sprintf("%s marked · %s send to agent", docCount(len(t.marks)), t.keys.Docs.Send.Help().Key)
}
