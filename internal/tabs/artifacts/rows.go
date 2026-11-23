package artifacts

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	artfs "github.com/daan3011/forge/internal/artifacts"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// row is one line of the list: a project header (all projects) or an
// artifact.
type row struct {
	header  bool
	project string
	count   int
	item    int // index into Tab.items; -1 for a header
}

// otherProject heads artifacts from absolute globs outside every project.
const otherProject = "other"

// setItems replaces the artifacts, keeping the selection.
func (t *Tab) setItems(items []store.Artifact) {
	t.items = items
	t.rebuild()
}

// visible are the indexes of the items the filters let through, pinned
// first, each part newest first.
func (t *Tab) visible() []int {
	var pinned, rest []int
	for i, a := range t.items {
		switch {
		case !t.filt.match(a):
		case a.Pinned:
			pinned = append(pinned, i)
		default:
			rest = append(rest, i)
		}
	}
	return append(pinned, rest...)
}

// shown counts the artifacts the filters let through.
func (t *Tab) shown() int {
	n := 0
	for _, a := range t.items {
		if t.filt.match(a) {
			n++
		}
	}
	return n
}

// rebuild groups the visible items into rows: with every project shown,
// one header per project, projects ordered by their newest artifact;
// pinned items come first in their group, then newest first.
func (t *Tab) rebuild() {
	t.rows = t.rows[:0]
	vis := t.visible()
	if !t.all() {
		for _, i := range vis {
			t.rows = append(t.rows, row{item: i})
		}
	} else {
		var order []string
		groups := map[string][]int{}
		// Projects keep their place under a filter: each is ranked by its
		// newest artifact, shown or not.
		newest := map[string]time.Time{}
		for _, a := range t.items {
			p := groupOf(a)
			if a.ModifiedAt.After(newest[p]) {
				newest[p] = a.ModifiedAt
			}
		}
		for _, i := range vis {
			p := groupOf(t.items[i])
			if _, ok := groups[p]; !ok {
				order = append(order, p)
			}
			groups[p] = append(groups[p], i)
		}
		sort.SliceStable(order, func(x, y int) bool { return newest[order[x]].After(newest[order[y]]) })
		for _, p := range order {
			t.rows = append(t.rows, row{header: true, project: p, count: len(groups[p]), item: -1})
			for _, i := range groups[p] {
				t.rows = append(t.rows, row{project: p, item: i})
			}
		}
	}
	idx := -1
	for _, p := range []string{t.want, t.selKey} {
		if p == "" {
			continue
		}
		if idx = t.indexOf(p); idx >= 0 {
			break
		}
	}
	if idx >= 0 && t.items[t.rows[idx].item].Path == t.want {
		t.want = ""
	}
	if idx < 0 {
		idx = min(t.cursor, len(t.rows)-1)
	}
	t.setCursor(max(idx, 0), 1)
}

// groupOf is the project header an artifact sits under.
func groupOf(a store.Artifact) string {
	if a.Project == "" {
		return otherProject
	}
	return a.Project
}

func (t *Tab) indexOf(path string) int {
	for i, r := range t.rows {
		if !r.header && t.items[r.item].Path == path {
			return i
		}
	}
	return -1
}

// selected is the artifact under the cursor.
func (t *Tab) selected() (store.Artifact, bool) {
	if t.cursor < 0 || t.cursor >= len(t.rows) || t.rows[t.cursor].header {
		return store.Artifact{}, false
	}
	return t.items[t.rows[t.cursor].item], true
}

// move steps the cursor by delta, over headers.
func (t *Tab) move(delta int) {
	dir := 1
	if delta < 0 {
		dir = -1
	}
	t.setCursor(t.cursor+delta, dir)
}

// setCursor puts the cursor on row i, or the nearest artifact from it in
// direction dir (then the other way).
func (t *Tab) setCursor(i, dir int) {
	if len(t.rows) == 0 {
		t.cursor, t.top, t.selKey = 0, 0, ""
		return
	}
	i = min(max(i, 0), len(t.rows)-1)
	pick := func(step int) int {
		for j := i; j >= 0 && j < len(t.rows); j += step {
			if !t.rows[j].header {
				return j
			}
		}
		return -1
	}
	j := pick(dir)
	if j < 0 {
		j = pick(-dir)
	}
	if j >= 0 {
		i = j
	}
	t.cursor = i
	if a, ok := t.selected(); ok {
		t.selKey = a.Path
	}
	t.clampTop()
}

func (t *Tab) listHeight() int {
	_, h := t.split.LeftSize()
	return max(h, 1)
}

// clampTop scrolls so the cursor is on screen, showing the header above
// the first artifact of a group when it fits.
func (t *Tab) clampTop() {
	h := t.listHeight()
	if t.cursor < t.top {
		t.top = t.cursor
		if t.top > 0 && t.rows[t.top-1].header {
			t.top--
		}
	}
	if t.cursor >= t.top+h {
		t.top = t.cursor - h + 1
	}
	t.top = max(min(t.top, len(t.rows)-h), 0)
}

// --- view ------------------------------------------------------------------------

// View implements tabs.Tab.
func (t *Tab) View() string {
	if t.dialog != nil {
		return t.dialogView()
	}
	lw, lh := t.split.LeftSize()
	rw, rh := t.split.RightSize()
	return t.split.View(t.listView(lw, lh), t.detailView(rw, rh), true)
}

// icon is the glyph shown before an artifact of type typ.
func icon(typ string) string {
	switch typ {
	case "html":
		return "◫"
	case "svg":
		return "◇"
	case "png", "jpg", "gif", "webp":
		return "▣"
	case "md":
		return "¶"
	case "pdf":
		return "▤"
	default:
		return "·"
	}
}

// source names where an artifact came from: its session, "kept" for a
// copy in .forge/artifacts, else "repo".
func (t *Tab) source(a store.Artifact) string {
	switch {
	case artfs.IsKept(a.Path):
		return "kept"
	case a.SessionName != "":
		return a.SessionName
	case a.SessionID != 0:
		return fmt.Sprintf("session %d", a.SessionID)
	case a.Project == "":
		return filepath.Dir(a.Path)
	default:
		return "repo"
	}
}

func (t *Tab) listView(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	if len(t.rows) == 0 {
		if len(t.items) > 0 && t.filt.any() {
			msg := "No artifacts match " + t.filterLabel() + " (" + t.keys.Artifacts.ClearFilters.Help().Key + " clears)."
			return t.theme.TableEmpty.Render(ansi.Wrap(msg, w, " "))
		}
		return t.emptyView(w, h)
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
	if r.header {
		right := count(r.count, "artifact")
		label := ansi.Truncate(r.project, max(w-ansi.StringWidth(right)-1, 1), "…")
		pad := max(w-ansi.StringWidth(label)-ansi.StringWidth(right), 1)
		return th.DetailTitle.Render(label) + strings.Repeat(" ", pad) + th.Hint.Render(right)
	}
	a := t.items[r.item]
	indent := ""
	if t.all() {
		indent = "  "
	}
	glyph := icon(a.Type)
	star := "  "
	if a.Pinned {
		star = "★ "
	}
	name := displayName(a)
	src := t.source(a)
	age := ui.ShortAge(t.now().Sub(a.ModifiedAt))

	// Columns drop from the right of the name as space runs out: the
	// session first, then the type.
	srcW := min(ansi.StringWidth(src), 18)
	typW := 4
	ageW := 4
	fixed := ansi.StringWidth(indent) + 4 // star, glyph and spaces
	showSrc, showTyp := true, true
	if w-fixed-ageW-typW-srcW-3 < 10 {
		showSrc = false
	}
	if w-fixed-ageW-typW-2 < 10 {
		showTyp = false
	}
	nameW := w - fixed - ageW - 1
	if showTyp {
		nameW -= typW + 1
	}
	if showSrc {
		nameW -= srcW + 1
	}
	nameW = max(nameW, 1)
	name = ansi.Truncate(name, nameW, "…")
	namePad := strings.Repeat(" ", max(nameW-ansi.StringWidth(name), 0))
	srcCell := ""
	if showSrc {
		srcCell = " " + padRight(ansi.Truncate(src, srcW, "…"), srcW)
	}
	typCell := ""
	if showTyp {
		typCell = " " + padRight(ansi.Truncate(a.Type, typW, ""), typW)
	}
	ageCell := " " + padLeft(age, ageW)

	if selected {
		line := indent + star + glyph + " " + name + namePad + srcCell + typCell + ageCell
		return th.TableSelected.Render(padRight(line, w))
	}
	srcStyle := th.ArtifactSession
	switch src {
	case "kept":
		srcStyle = th.ArtifactKept
	case "repo":
		srcStyle = th.Hint
	}
	return indent + th.ArtifactPin.Render(star) + th.ArtifactIcon.Render(glyph) + " " + th.TableRow.Render(name+namePad) +
		srcStyle.Render(srcCell) + th.Hint.Render(typCell+ageCell)
}

// truncateLeft keeps the end of s (the informative part of a path).
func truncateLeft(s string, w int) string {
	if ansi.StringWidth(s) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	return "…" + ansi.TruncateLeft(s, ansi.StringWidth(s)-w+1, "")
}

func padRight(s string, w int) string {
	return s + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0))
}

func padLeft(s string, w int) string {
	return strings.Repeat(" ", max(w-ansi.StringWidth(s), 0)) + s
}

// emptyView explains why there is nothing to show, w×h (P6.4).
func (t *Tab) emptyView(w, h int) string {
	th := t.theme
	switch {
	case !t.scanned:
		return anim.Loading("scanning for artifacts…", t.anim.Frame(), t.anim.Settings(), th, w, h)
	case len(t.projects()) == 0 && len(t.spec.Absolute) == 0:
		return anim.Notice("No projects configured", "add one under projects: in the config", th, w, h)
	case t.scanErr != nil:
		return anim.Failed("couldn't scan for artifacts: "+t.scanErr.Error(), ui.RetryKey(t.keys), th, w, h)
	}
	globs := append(append([]string(nil), t.spec.Relative...), t.spec.Absolute...)
	return anim.Notice("No artifacts yet",
		"watching "+strings.Join(globs, ", ")+" in every checkout and session worktree", th, w, h)
}

// detailView shows the selected artifact's facts.
func (t *Tab) detailView(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	th := t.theme
	a, ok := t.selected()
	if !ok {
		return th.Hint.Render("Nothing selected")
	}
	if s, ok := t.previewDetail(a, w, h); ok {
		return s
	}
	field := func(label, value string) string {
		return th.DetailLabel.Render(padRight(label, 9)) + th.Body.Render(ansi.Truncate(value, max(w-9, 1), "…"))
	}
	project := a.Project
	if project == "" {
		project = "—"
	}
	now := t.now()
	heading := th.ArtifactIcon.Render(icon(a.Type)) + " "
	if a.Pinned {
		heading = th.ArtifactPin.Render("★ ") + heading
	}
	headW := max(w-ansi.StringWidth(ansi.Strip(heading)), 1)
	lines := []string{heading + th.DetailTitle.Render(ansi.Truncate(displayName(a), headW, "…"))}
	if a.Title != "" {
		lines = append(lines, th.Body.Render(ansi.Truncate(filepath.Base(a.Path), w, "…")))
	}
	lines = append(lines,
		th.Hint.Render(truncateLeft(filepath.Dir(a.Path), w)),
		"",
		field("Project", project),
		field("Source", t.source(a)),
		field("Type", a.Type),
		field("Size", ui.HumanBytes(a.Size)),
		field("Modified", a.ModifiedAt.Format("2006-01-02 15:04")+" ("+ago(now.Sub(a.ModifiedAt))+")"),
		field("Created", a.CreatedAt.Format("2006-01-02 15:04")),
	)
	if len(a.Tags) > 0 {
		lines = append(lines, th.DetailLabel.Render(padRight("Tags", 9))+
			th.ArtifactTag.Render(ansi.Truncate(hashTags(a.Tags), max(w-9, 1), "…")))
	}
	if a.Pinned {
		lines = append(lines, th.DetailLabel.Render(padRight("Pinned", 9))+th.ArtifactPin.Render("★"))
	}
	if l := a.Link; l != nil {
		from := l.SessionName
		if from == "" {
			from = filepath.Dir(l.SourcePath)
		}
		if l.Active() {
			lines = append(lines, field("Synced", "from "+from+" · "+ago(now.Sub(l.SyncedAt))))
		} else {
			lines = append(lines, field("Copied", "from "+from+" · final ("+l.DetachReason+")"))
		}
	}
	lines = append(lines, "", t.detailHints(a, w))
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

// detailHints is the key hint line at the bottom of the detail pane.
func (t *Tab) detailHints(a store.Artifact, w int) string {
	ak := t.keys.Artifacts
	pin := " pin"
	if a.Pinned {
		pin = " unpin"
	}
	hints := []string{
		ak.Open.Help().Key + " open", ak.Pin.Help().Key + pin,
		ak.Tags.Help().Key + " tags", ak.Rename.Help().Key + " rename", ak.CopyPath.Help().Key + " copy path",
	}
	if !artfs.IsKept(a.Path) {
		hints = append(hints, ak.Keep.Help().Key+" keep a copy")
	}
	return t.theme.Hint.Render(ansi.Truncate(strings.Join(hints, " · "), w, "…"))
}

// ago is "just now" or "5m ago".
func ago(d time.Duration) string {
	if age := ui.ShortAge(d); age != "now" {
		return age + " ago"
	}
	return "just now"
}
