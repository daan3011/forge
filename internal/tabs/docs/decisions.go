package docs

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"

	docfs "github.com/daan3011/forge/internal/docs"
)

// The decision log (P4.4): a flat "Decisions" view of every root's
// decisions/NNNN-slug.md, newest first, with logging and superseding.

// LogDecisionMsg opens the "log a decision" form (the app sends it from
// the global palette command after switching to the Docs tab).
type LogDecisionMsg struct{}

// TargetTab implements tabs.TargetedMsg.
func (LogDecisionMsg) TargetTab() string { return Title }

// decisionTitleMin is the title width a row keeps before dropping the
// date.
const decisionTitleMin = 24

// errNotDecision is shown when superseding outside the decisions view.
var errNotDecision = errors.New("select a decision in the decisions view first")

// toggleDecisions switches between the tree and the decisions view,
// keeping the selected doc when the other view shows it.
func (t *Tab) toggleDecisions() {
	t.decisionsView = !t.decisionsView
	if !t.decisionsView && t.selPath != "" {
		t.reveal(t.selPath) // keep the decision selected in the tree
	}
	t.focusDetail = false
	t.top = 0
	t.rebuild()
}

// rebuildDecisions fills rows with every root's decisions: newest date
// first, then by project, then highest number.
func (t *Tab) rebuildDecisions() {
	type item struct {
		root int
		dec  *docfs.Decision
	}
	var items []item
	for i := range t.roots {
		for j := range t.roots[i].decisions {
			items = append(items, item{root: i, dec: &t.roots[i].decisions[j]})
		}
	}
	sort.SliceStable(items, func(a, b int) bool {
		x, y := items[a], items[b]
		switch {
		case x.dec.Date != y.dec.Date:
			return x.dec.Date > y.dec.Date
		case x.root != y.root:
			return x.root < y.root
		}
		return x.dec.Number > y.dec.Number
	})
	for _, it := range items {
		n := findNode(t.roots[it.root].tree, it.dec.Path)
		if n == nil {
			n = &docfs.Node{Name: it.dec.File(), Path: it.dec.Path, Rel: it.dec.Rel}
		}
		t.rows = append(t.rows, row{node: n, root: it.root, dec: it.dec})
	}
}

// findNode returns the doc at path in tree, or nil.
func findNode(n *docfs.Node, path string) *docfs.Node {
	if n == nil {
		return nil
	}
	if !n.IsDir {
		if n.Path == path {
			return n
		}
		return nil
	}
	if rel, err := filepath.Rel(n.Path, path); err != nil || strings.HasPrefix(rel, "..") {
		return nil
	}
	for _, c := range n.Children {
		if f := findNode(c, path); f != nil {
			return f
		}
	}
	return nil
}

// isDecision reports whether path is one of the listed decisions.
func (t *Tab) isDecision(path string) bool {
	for _, r := range t.roots {
		for _, d := range r.decisions {
			if d.Path == path {
				return true
			}
		}
	}
	return false
}

// decisionRowView renders "0007  Use SQLite …  accepted · 2026-11-20".
func (t *Tab) decisionRowView(r row, selected bool, w int) string {
	th := t.theme
	d := r.dec
	num := fmt.Sprintf("%04d", d.Number)
	if t.isMarked(r.node.Path) {
		num = "●" + num
	} else {
		num = " " + num
	}
	right := d.Status
	if d.Superseded() && d.SupersededBy != "" {
		if n, _, ok := strings.Cut(d.SupersededBy, "-"); ok {
			right = "→ " + n
		}
	}
	title := d.Title
	if t.all() {
		title = t.roots[r.root].name + ": " + title
	}
	lead := ansi.StringWidth(num) + 2
	// The date only when the title keeps room (the preview shows it too).
	if d.Date != "" && w-lead-ansi.StringWidth(right+" · "+d.Date)-1 >= min(ansi.StringWidth(title), decisionTitleMin) {
		right += " · " + d.Date
	}
	space := w - lead - ansi.StringWidth(right) - 1
	if space < 4 {
		right = ""
		space = w - ansi.StringWidth(num) - 2
	}
	title = ansi.Truncate(title, max(space, 1), "…")
	pad := max(w-ansi.StringWidth(num)-2-ansi.StringWidth(title)-ansi.StringWidth(right), 0)
	if selected {
		return th.TableSelected.Render(num + "  " + title + strings.Repeat(" ", pad) + right)
	}
	titleStyle := th.TableRow
	if d.Superseded() {
		titleStyle = th.DecisionSuperseded
	}
	numStyle := th.DecisionNumber
	if t.isMarked(r.node.Path) {
		numStyle = th.DocMarked
	}
	return numStyle.Render(num) + "  " + titleStyle.Render(title) + strings.Repeat(" ", pad) + th.Hint.Render(right)
}

// decisionsEmptyView explains an empty decisions view.
func (t *Tab) decisionsEmptyView(w int) string {
	th := t.theme
	where := "this project"
	if t.all() {
		where = "any project"
	}
	return th.TableEmpty.Render(ansi.Wrap("No decisions logged in "+where+" yet.", w, "")) + "\n\n" +
		th.Hint.Render(t.keys.Docs.Decision.Help().Key+"  log one · "+t.keys.Docs.Decisions.Help().Key+"  back to the tree")
}

// decisionRoot is the root a new decision goes to by default: the
// selection's.
func (t *Tab) decisionRoot() int {
	if r, ok := t.selected(); ok {
		return r.root
	}
	return 0
}

// logDecision asks for a decision and writes it as the next numbered one
// in the chosen project's decisions folder.
func (t *Tab) logDecision() tea.Cmd {
	if len(t.roots) == 0 {
		return errCmd(errNoProject)
	}
	names := make([]string, len(t.roots))
	for i, r := range t.roots {
		names[i] = r.name
	}
	v := &DecisionValues{Project: names[t.decisionRoot()]}
	return t.openDialog(DecisionForm(nil, nil, names, v), func() tea.Cmd {
		idx := -1
		for i, r := range t.roots {
			if r.name == v.Project {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil
		}
		r, in, now := t.roots[idx], v.DecisionInput, t.now()
		return func() tea.Msg {
			l, err := docfs.LogDecision(r.path, r.repo, in, now)
			if err != nil {
				return opDoneMsg{err: err}
			}
			text := "logged " + l.Label()
			switch {
			case l.NoteErr != nil:
				text += " (couldn't update CLAUDE.md: " + l.NoteErr.Error() + ")"
			case l.Noted:
				text += "; CLAUDE.md now points agents at the decision log"
			}
			return opDoneMsg{path: l.Path, text: text, reveal: true}
		}
	})
}

// supersedeSelected asks which newer decision replaces the selected one,
// then marks it superseded with a link to that one.
func (t *Tab) supersedeSelected() tea.Cmd {
	r, ok := t.selected()
	if !ok || r.dec == nil {
		return errCmd(errNotDecision)
	}
	old := *r.dec
	if old.Superseded() {
		return errCmd(fmt.Errorf("%s is already superseded by %s", old.Label(), old.SupersededBy))
	}
	var newer []docfs.Decision
	for _, d := range t.roots[r.root].decisions {
		if d.Number > old.Number && !d.Superseded() {
			newer = append(newer, d)
		}
	}
	if len(newer) == 0 {
		return errCmd(fmt.Errorf("no newer decision to supersede %04d with: log one first (%s)",
			old.Number, t.keys.Docs.Decision.Help().Key))
	}
	opts := make([]huh.Option[int], len(newer))
	for i, d := range newer {
		opts[i] = huh.NewOption(d.Label(), i)
	}
	pick := 0
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[int]().Key("newer").
			Title("Supersede " + old.Label() + " with").
			Description("Marks it superseded and links to the newer decision").
			Options(opts...).Value(&pick),
	))
	return t.openDialog(form, func() tea.Cmd {
		if pick < 0 || pick >= len(newer) {
			return nil
		}
		n := newer[pick]
		return func() tea.Msg {
			if err := docfs.Supersede(old, n); err != nil {
				return opDoneMsg{err: err}
			}
			return opDoneMsg{path: old.Path, text: fmt.Sprintf("%04d superseded by %04d", old.Number, n.Number)}
		}
	})
}
