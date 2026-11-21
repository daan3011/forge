package plane

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/ui"
)

// The kanban board (P3.3): one column per state, in group order, merged
// across projects by group and name. H / L move the selected card to the
// previous / next state of its own project; the move shows at once and is
// rolled back with a toast if Plane refuses it.

// viewMode is the list or the board.
type viewMode int

const (
	modeList viewMode = iota
	modeBoard
)

const (
	boardMinCol   = 24 // narrowest column when they all fit
	boardColWidth = 28 // column width when they don't (they scroll)
	boardMaxCol   = 40
	boardStrip    = 3 // a collapsed column
	boardGap      = 1
	// cardHeight is a card's lines: border, key line, two title lines.
	cardHeight = 5
)

// boardColumn is one state (or the same-named state of several projects).
type boardColumn struct {
	key   string // group|name
	name  string
	group plane.StateGroup
	seq   float64
	cards []plane.WorkItem
}

// boardState is the board's columns and cursor. The card picked last
// (want) keeps the cursor on it across refreshes and moves, also when it
// is missing for a moment (between its open and closed lists landing).
type boardState struct {
	cols   []boardColumn
	col    int
	colKey string
	selID  string         // the card under the cursor
	want   string         // the card picked last
	rows   map[string]int // selected card per column key
	offs   map[string]int // first card shown per column key
	first  int            // first column shown
	// fold is a column's collapsed state set with z / Z (or opened to show
	// the wanted card); unset columns follow the default: cancelled
	// collapsed, the rest open.
	fold map[string]bool
}

func newBoardState() boardState {
	return boardState{rows: map[string]int{}, offs: map[string]int{}, fold: map[string]bool{}}
}

// pendingMove is a state change of one item on its way to Plane. Moves
// of one item go out one at a time: when a PATCH lands and want differs
// from what was sent, the next one goes.
type pendingMove struct {
	project   string
	key       string      // PROJ-12, for toasts
	confirmed plane.State // the state Plane last accepted
	want      plane.State // the state shown
	inflight  bool
}

// moveDoneMsg is the answer to a state PATCH.
type moveDoneMsg struct {
	id     string
	target plane.State
	item   plane.WorkItem
	err    error
}

// TargetTab implements tabs.TargetedMsg.
func (moveDoneMsg) TargetTab() string { return Title }

func columnKey(g plane.StateGroup, name string) string {
	return string(g) + "|" + strings.ToLower(name)
}

func groupRank(g plane.StateGroup) int {
	if i := slices.Index(allGroups, g); i >= 0 {
		return i
	}
	return len(allGroups)
}

func priorityRank(p plane.Priority) int {
	switch p {
	case plane.PriorityUrgent:
		return 0
	case plane.PriorityHigh:
		return 1
	case plane.PriorityMedium:
		return 2
	case plane.PriorityLow:
		return 3
	}
	return 4
}

func compareStates(a, b plane.State) int {
	return cmp.Or(
		cmp.Compare(groupRank(a.Group), groupRank(b.Group)),
		cmp.Compare(a.Sequence, b.Sequence),
		cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
	)
}

// stateFull is the item's state: expanded, else from the project's states.
func (t *Tab) stateFull(w plane.WorkItem) (plane.State, bool) {
	if w.State != nil {
		return *w.State, true
	}
	if m := t.meta[w.ProjectID]; m != nil {
		for _, s := range m.states {
			if s.ID == w.StateID {
				return s, true
			}
		}
	}
	return plane.State{}, false
}

// orderedStates are a project's states in board order.
func (t *Tab) orderedStates(project string) []plane.State {
	m := t.meta[project]
	if m == nil {
		return nil
	}
	out := slices.Clone(m.states)
	slices.SortStableFunc(out, compareStates)
	return out
}

// --- columns & cursor ------------------------------------------------------------

// rebuildBoard lays out the columns from the projects' states and the
// loaded items, then puts the cursor back on the selected card (else on
// the same column).
func (t *Tab) rebuildBoard() {
	b := &t.board
	if t.mode != modeBoard {
		b.cols = nil
		return
	}
	cols := map[string]*boardColumn{}
	add := func(st plane.State) *boardColumn {
		k := columnKey(st.Group, st.Name)
		c := cols[k]
		if c == nil {
			c = &boardColumn{key: k, name: st.Name, group: st.Group, seq: st.Sequence}
			cols[k] = c
		}
		return c
	}
	for _, id := range t.projectIDs() {
		if m := t.meta[id]; m != nil {
			for _, st := range m.states {
				c := add(st)
				c.seq = min(c.seq, st.Sequence)
			}
		}
	}
	q := t.filter.Value()
	for _, w := range t.items() {
		if !ui.RowMatches(t.itemRow(w), q) {
			continue // the / filter, matched like the list's rows
		}
		if st, ok := t.stateFull(w); ok {
			c := add(st)
			c.cards = append(c.cards, w)
		}
	}
	b.cols = b.cols[:0]
	for _, c := range cols {
		slices.SortStableFunc(c.cards, func(x, y plane.WorkItem) int {
			return cmp.Or(
				cmp.Compare(priorityRank(x.Priority), priorityRank(y.Priority)),
				y.UpdatedAt.Compare(x.UpdatedAt.Time),
				cmp.Compare(y.SequenceID, x.SequenceID),
			)
		})
		b.cols = append(b.cols, *c)
	}
	slices.SortStableFunc(b.cols, func(x, y boardColumn) int {
		return compareStates(plane.State{Name: x.name, Group: x.group, Sequence: x.seq},
			plane.State{Name: y.name, Group: y.group, Sequence: y.seq})
	})
	if len(b.cols) == 0 {
		b.col, b.selID = 0, ""
		return
	}
	if b.want != "" {
		for ci, c := range b.cols {
			if ri := slices.IndexFunc(c.cards, func(w plane.WorkItem) bool { return w.ID == b.want }); ri >= 0 {
				b.col, b.colKey, b.selID = ci, c.key, b.want
				b.rows[c.key] = ri
				if b.collapsed(ci) {
					b.fold[c.key] = false // open it to show the card
				}
				return
			}
		}
	}
	col := b.col
	if i := slices.IndexFunc(b.cols, func(c boardColumn) bool { return c.key == b.colKey }); i >= 0 {
		col = i
	}
	if b.want != "" {
		// The wanted card is hidden (filtered, or moved away by someone
		// else): go to the nearest column with cards, like the list
		// clamping to a visible row. Keep wanting it while it may come
		// back (a fetch on its way, or the / filter cleared).
		col = b.nearestWithCards(col)
		if !t.Fetching() && t.filter.Value() == "" {
			b.want = ""
		}
	}
	b.show(col)
}

// nearestWithCards is the open column with cards closest to col (col
// itself first, then left before right), or col if there is none.
func (b *boardState) nearestWithCards(col int) int {
	col = min(max(col, 0), len(b.cols)-1)
	for d := 0; d < len(b.cols); d++ {
		for _, i := range []int{col - d, col + d} {
			if i >= 0 && i < len(b.cols) && len(b.cols[i].cards) > 0 && !b.collapsed(i) {
				return i
			}
		}
	}
	return col
}

// pick makes id the wanted card (call rebuildBoard to move there).
func (b *boardState) pick(id string) { b.want, b.selID = id, id }

// setCol moves the cursor to column i (clamped) and its remembered card.
func (b *boardState) setCol(i int) {
	b.show(i)
	b.want = b.selID
}

// show puts the cursor on column i (clamped) and its remembered card,
// without changing the wanted card. On a collapsed column no card is
// selected.
func (b *boardState) show(i int) {
	if len(b.cols) == 0 {
		return
	}
	b.col = min(max(i, 0), len(b.cols)-1)
	c := b.cols[b.col]
	b.colKey = c.key
	b.selID = ""
	if len(c.cards) > 0 && !b.collapsed(b.col) {
		r := min(max(b.rows[c.key], 0), len(c.cards)-1)
		b.rows[c.key] = r
		b.selID = c.cards[r].ID
	}
}

// setRow moves the cursor to card r (clamped) of the current column.
func (b *boardState) setRow(r int) {
	if len(b.cols) == 0 {
		return
	}
	c := b.cols[b.col]
	if len(c.cards) == 0 || b.collapsed(b.col) {
		return
	}
	r = min(max(r, 0), len(c.cards)-1)
	b.rows[c.key] = r
	b.pick(c.cards[r].ID)
}

func (b *boardState) row() int {
	if len(b.cols) == 0 {
		return 0
	}
	return b.rows[b.cols[b.col].key]
}

// collapsed reports whether column i shows as a strip: as set with z / Z,
// else cancelled ones.
func (b *boardState) collapsed(i int) bool {
	c := b.cols[i]
	if f, ok := b.fold[c.key]; ok {
		return f
	}
	return c.group == plane.GroupCancelled
}

// toggleColumn collapses or opens the cursor's column (z).
func (b *boardState) toggleColumn() {
	if len(b.cols) == 0 {
		return
	}
	b.fold[b.cols[b.col].key] = !b.collapsed(b.col)
	b.setCol(b.col)
}

// toggleEmpty collapses every empty column, or opens them all when they
// are all collapsed already (Z).
func (b *boardState) toggleEmpty() {
	var empty []int
	open := false
	for i, c := range b.cols {
		if len(c.cards) == 0 {
			empty = append(empty, i)
			open = open || !b.collapsed(i)
		}
	}
	for _, i := range empty {
		b.fold[b.cols[i].key] = open
	}
	if len(b.cols) > 0 {
		b.show(b.col)
	}
}

// --- actions -----------------------------------------------------------------------

// updateBoard handles keys on the board, and on its card overlay.
func (t *Tab) updateBoard(kp tea.KeyPressMsg) tea.Cmd {
	l, pk, b := t.keys.List, t.keys.Plane, &t.board
	if cmd, ok := t.itemAction(kp); ok {
		return cmd
	}
	if t.overlay {
		switch {
		case key.Matches(kp, t.keys.Input.Cancel):
			t.overlay = false
		case key.Matches(kp, pk.Board):
			return t.toggleBoard()
		case key.Matches(kp, pk.DetailDown):
			t.scrollDetail(1)
		case key.Matches(kp, pk.DetailUp):
			t.scrollDetail(-1)
		case key.Matches(kp, pk.MovePrev):
			return t.moveCard(-1)
		case key.Matches(kp, pk.MoveNext):
			return t.moveCard(1)
		case key.Matches(kp, pk.Open):
			return t.openInBrowser()
		case key.Matches(kp, pk.CopyURL):
			return t.copyURL()
		}
		return nil
	}
	switch {
	case key.Matches(kp, pk.Board):
		return t.toggleBoard()
	case key.Matches(kp, pk.ColLeft):
		return t.moveColumn(-1)
	case key.Matches(kp, pk.ColRight):
		return t.moveColumn(1)
	case key.Matches(kp, l.Up):
		b.setRow(b.row() - 1)
	case key.Matches(kp, l.Down):
		b.setRow(b.row() + 1)
	case key.Matches(kp, l.Top):
		b.setRow(0)
	case key.Matches(kp, l.Bottom):
		b.setRow(1 << 30)
	case key.Matches(kp, pk.MovePrev):
		return t.moveCard(-1)
	case key.Matches(kp, pk.MoveNext):
		return t.moveCard(1)
	case key.Matches(kp, pk.CollapseColumn):
		return t.toggleColumn()
	case key.Matches(kp, pk.CollapseEmpty):
		return t.toggleCollapseEmpty()
	case key.Matches(kp, l.Filter):
		t.startFilter()
	case key.Matches(kp, t.keys.Input.Cancel) && t.filter.Value() != "":
		t.clearFilter()
	case key.Matches(kp, pk.OpenCard):
		return t.openCard()
	case key.Matches(kp, pk.Filters):
		return t.openFilters()
	case key.Matches(kp, pk.Mine):
		return t.toggleMine()
	case key.Matches(kp, pk.ClearFilters):
		return t.clearFilters()
	case key.Matches(kp, pk.Open):
		return t.openInBrowser()
	case key.Matches(kp, pk.CopyURL):
		return t.copyURL()
	}
	return nil
}

// toggleBoard switches between list and board, keeping the selected item
// selected when the other view has it.
func (t *Tab) toggleBoard() tea.Cmd {
	sel := t.selectedID()
	t.overlay = false
	if t.mode == modeBoard {
		t.mode = modeList
	} else {
		t.mode = modeBoard
	}
	t.rebuildLists()
	if t.mode == modeBoard {
		t.board.pick(sel)
	}
	t.setRows()
	if t.mode == modeList && sel != "" {
		t.selectID(sel)
	}
	return t.Refresh()
}

func (t *Tab) moveColumn(dir int) tea.Cmd {
	t.board.setCol(t.board.col + dir)
	return nil
}

func (t *Tab) toggleColumn() tea.Cmd {
	t.board.toggleColumn()
	return nil
}

func (t *Tab) toggleCollapseEmpty() tea.Cmd {
	t.board.toggleEmpty()
	return nil
}

// openCard shows the selected card's detail over the board.
func (t *Tab) openCard() tea.Cmd {
	if t.board.selID == "" {
		return nil
	}
	t.overlay = true
	t.det.want, t.det.offset = "", 0
	return t.syncDetail()
}

// moveCard moves the selected item to the previous (-1) or next (1) state
// of its project: at once on screen, then in Plane.
func (t *Tab) moveCard(dir int) tea.Cmd {
	w, ok := t.selected()
	if !ok || t.api == nil {
		return nil
	}
	states := t.orderedStates(w.ProjectID)
	if len(states) == 0 {
		return errCmd(fmt.Errorf("plane: %s: states still loading", w.Key()))
	}
	i := slices.IndexFunc(states, func(s plane.State) bool { return s.ID == w.StateID })
	if i < 0 {
		return errCmd(fmt.Errorf("plane: %s: its state isn't one of the project's", w.Key()))
	}
	j := i + dir
	if j < 0 || j >= len(states) {
		return nil
	}
	pm := t.moves[w.ID]
	if pm == nil {
		cur, _ := t.stateFull(w)
		pm = &pendingMove{project: w.ProjectID, key: w.Key(), confirmed: cur}
		t.moves[w.ID] = pm
	}
	pm.want = states[j]
	t.applyState(w.ID, pm.want)
	if t.mode == modeBoard {
		t.board.pick(w.ID) // the cursor follows the card
	}
	t.setRows()
	if pm.inflight {
		return nil // sent when the one on its way lands
	}
	return t.sendMove(w.ID, pm)
}

func (t *Tab) sendMove(id string, pm *pendingMove) tea.Cmd {
	pm.inflight = true
	api, project, target := t.api, pm.project, pm.want
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), shortTimeout)
		defer cancel()
		sid := target.ID
		w, err := api.UpdateWorkItem(ctx, project, id, plane.WorkItemUpdate{StateID: &sid})
		return moveDoneMsg{id: id, target: target, item: w, err: err}
	}
}

// moveDone settles a PATCH: roll back to the last accepted state on
// failure, send the next move if the card moved on meanwhile, else keep
// the new state and tell the links table.
func (t *Tab) moveDone(msg moveDoneMsg) tea.Cmd {
	pm := t.moves[msg.id]
	if pm == nil {
		return nil
	}
	pm.inflight = false
	if msg.err != nil {
		delete(t.moves, msg.id)
		t.applyState(msg.id, pm.confirmed)
		t.setRows()
		return errCmd(fmt.Errorf("plane: %s: couldn't move to %s: %s", pm.key, msg.target.Name, reason(msg.err)))
	}
	pm.confirmed = msg.target
	if pm.want.ID != msg.target.ID {
		return t.sendMove(msg.id, pm)
	}
	delete(t.moves, msg.id)
	if !msg.item.UpdatedAt.IsZero() {
		t.editItem(msg.id, func(w *plane.WorkItem) { w.UpdatedAt = msg.item.UpdatedAt })
	}
	t.setRows()
	if w, ok := t.itemByID(msg.id); ok {
		return t.syncStates([]plane.WorkItem{w})
	}
	return nil
}

// applyState shows item id in state st in every list.
func (t *Tab) applyState(id string, st plane.State) {
	t.editItem(id, func(w *plane.WorkItem) {
		s := st
		w.StateID, w.State = st.ID, &s
	})
}

// editItem changes item id in every pooled list. Lists are copied first:
// their arrays may be shared with the cache.
func (t *Tab) editItem(id string, f func(*plane.WorkItem)) {
	for _, l := range t.pool {
		cloned := false
		for i := range l.items {
			if l.items[i].ID != id {
				continue
			}
			if !cloned {
				l.items, cloned = slices.Clone(l.items), true
			}
			f(&l.items[i])
		}
	}
}

// reapplyMoves puts pending moves back on fresh data for l, so a refresh
// landing mid-move doesn't flick the card back.
func (t *Tab) reapplyMoves(l *projectItems) {
	if len(t.moves) == 0 {
		return
	}
	cloned := false
	for i, w := range l.items {
		pm := t.moves[w.ID]
		if pm == nil || w.StateID == pm.want.ID {
			continue
		}
		if !cloned {
			l.items, cloned = slices.Clone(l.items), true
		}
		s := pm.want
		l.items[i].StateID, l.items[i].State = s.ID, &s
	}
}

// boardStatus is "Todo 2/5 · column 4/7".
func (t *Tab) boardStatus() string {
	b := &t.board
	if len(b.cols) == 0 {
		return "no columns"
	}
	c := b.cols[b.col]
	pos := 0
	if len(c.cards) > 0 {
		pos = b.row() + 1
	}
	return fmt.Sprintf("%s %d/%d · column %d/%d", c.name, pos, len(c.cards), b.col+1, len(b.cols))
}

// --- view ----------------------------------------------------------------------------

// overlaySize is the inner size of the card detail over the board.
func (t *Tab) overlaySize() (w, h int) {
	ow := min(max(t.width*4/5, 24), t.width)
	oh := min(max(t.height*85/100, 6), t.height)
	return max(ow-4, 0), max(oh-2, 0) // border and padding
}

// boardScreen is the board in a pane under the filter box, with the card
// detail on top when open.
func (t *Tab) boardScreen() string {
	w, h := t.width-2, t.height-2
	if w <= 0 || h <= 0 {
		return ""
	}
	body, bh := t.filterBox(w), h-filterBoxHeight
	if t.filter.Visible() {
		body += "\n" + t.filter.View(w)
		bh--
	}
	body += "\n" + t.boardView(w, max(bh, 0))
	screen := t.theme.PaneFocused.Render(ui.Box(body, w, h))
	if !t.overlay {
		return screen
	}
	iw, ih := t.overlaySize()
	box := t.theme.BoardOverlay.Render(ui.Box(t.detailView(iw, ih), iw, ih))
	x := max((t.width-lipgloss.Width(box))/2, 0)
	y := max((t.height-lipgloss.Height(box))/2, 0)
	canvas := lipgloss.NewCanvas(t.width, t.height)
	canvas.Compose(lipgloss.NewCompositor(lipgloss.NewLayer(screen), lipgloss.NewLayer(box).X(x).Y(y).Z(1)))
	return ui.Box(canvas.Render(), t.width, t.height)
}

// boardView is the scroll hints line and the columns that fit in w x h.
func (t *Tab) boardView(w, h int) string {
	b := &t.board
	if len(b.cols) == 0 || !t.loaded() || t.asking {
		return t.emptyView(w, h)
	}
	if h < 2 {
		return ""
	}
	widths := t.columnWidths(w)
	first, last := b.visible(widths, w)

	th := t.theme
	left, right := "", ""
	if first > 0 {
		left = th.BoardScrollHint.Render("‹ " + strconv.Itoa(first))
	}
	if n := len(b.cols) - 1 - last; n > 0 {
		right = th.BoardScrollHint.Render(strconv.Itoa(n) + " ›")
	}
	hints := left + strings.Repeat(" ", max(w-ansi.StringWidth(left)-ansi.StringWidth(right), 0)) + right

	ch := h - 1
	gap := strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", boardGap)+"\n", ch), "\n")
	var parts []string
	for i := first; i <= last; i++ {
		if i > first {
			parts = append(parts, gap)
		}
		parts = append(parts, t.columnView(i, widths[i], ch))
	}
	return hints + "\n" + lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

// columnWidths gives every column its width: strips for collapsed ones;
// the others share w when they all fit, else boardColWidth (and scroll).
func (t *Tab) columnWidths(w int) []int {
	b := &t.board
	n, strips := len(b.cols), 0
	for i := range b.cols {
		if b.collapsed(i) {
			strips++
		}
	}
	exp := n - strips
	// They don't all fit: as many boardColWidth columns as fit, widened to
	// fill w.
	k := max((w+boardGap)/(boardColWidth+boardGap), 1)
	cw := (w - (k-1)*boardGap) / k
	if avail := w - strips*boardStrip - (n-1)*boardGap; exp > 0 && avail/exp >= boardMinCol {
		cw = min(avail/exp, boardMaxCol)
	}
	cw = max(min(cw, w), 1)
	out := make([]int, n)
	for i := range out {
		out[i] = cw
		if b.collapsed(i) {
			out[i] = boardStrip
		}
	}
	return out
}

// visible is the range of columns shown, moving the first one so the
// cursor's column fits.
func (b *boardState) visible(widths []int, w int) (first, last int) {
	lastFrom := func(first int) int {
		used, last := 0, first
		for i := first; i < len(widths); i++ {
			need := widths[i]
			if i > first {
				need += boardGap
			}
			if i > first && used+need > w {
				break
			}
			used += need
			last = i
		}
		return last
	}
	b.first = min(max(b.first, 0), b.col)
	for b.first < b.col && lastFrom(b.first) < b.col {
		b.first++
	}
	// Show as many columns as fit when scrolled to the end.
	for b.first > 0 && lastFrom(b.first-1) == len(widths)-1 && lastFrom(b.first-1) >= b.col {
		b.first--
	}
	return b.first, lastFrom(b.first)
}

// columnView is column i at cw x ch: the header, a rule and the cards
// that fit, scrolled so the column's selected card shows.
func (t *Tab) columnView(i, cw, ch int) string {
	th, b := t.theme, &t.board
	c := b.cols[i]
	if b.collapsed(i) {
		return t.stripView(c, ch, i == b.col)
	}
	per := max((ch-2)/cardHeight, 1)
	r := min(b.rows[c.key], max(len(c.cards)-1, 0))
	off := b.offs[c.key]
	if r < off {
		off = r
	}
	if r >= off+per {
		off = r - per + 1
	}
	off = min(max(off, 0), max(len(c.cards)-per, 0))
	b.offs[c.key] = off

	more := ""
	if off > 0 {
		more += " ↑" + strconv.Itoa(off)
	}
	if n := len(c.cards) - off - per; n > 0 {
		more += " ↓" + strconv.Itoa(n)
	}
	count := th.BoardHeader.Render(" " + strconv.Itoa(len(c.cards)) + more)
	name := th.StateGroupStyle(string(c.group)).Render(ansi.Truncate(c.name, max(cw-ansi.StringWidth(count), 1), "…"))
	rule := th.StateGroupStyle(string(c.group)).Render(strings.Repeat("─", cw))
	if i == b.col {
		rule = th.BoardScrollHint.Render(strings.Repeat("━", cw))
	}
	lines := []string{name + count, rule}
	if len(c.cards) == 0 {
		lines = append(lines, th.TableEmpty.Render("empty"))
	}
	for j := off; j < min(off+per, len(c.cards)); j++ {
		lines = append(lines, strings.Split(t.cardView(c.cards[j], cw, i == b.col && j == r), "\n")...)
	}
	return ui.Box(strings.Join(lines, "\n"), cw, ch)
}

// stripView is a collapsed column: its count, then its name downwards,
// in the accent when the cursor is on it.
func (t *Tab) stripView(c boardColumn, ch int, cursor bool) string {
	th := t.theme
	count, st := th.BoardCollapsed, th.StateGroupStyle(string(c.group))
	if cursor {
		count, st = th.BoardScrollHint, th.BoardScrollHint
	}
	lines := []string{count.Render(centre(strconv.Itoa(len(c.cards)), boardStrip))}
	for _, r := range c.name {
		if len(lines) >= ch {
			break
		}
		lines = append(lines, st.Render(centre(string(r), boardStrip)))
	}
	return ui.Box(strings.Join(lines, "\n"), boardStrip, ch)
}

func centre(s string, w int) string {
	s = ansi.Truncate(s, w, "")
	pad := w - ansi.StringWidth(s)
	return strings.Repeat(" ", pad/2) + s + strings.Repeat(" ", pad-pad/2)
}

// cardView is a card cw wide: the key, priority and assignee initials,
// then the title on two lines.
func (t *Tab) cardView(w plane.WorkItem, cw int, selected bool) string {
	th := t.theme
	iw := max(cw-4, 4) // border and padding
	glyph, pst := th.PriorityIcon(string(w.Priority))
	right := pst.Render(glyph)
	if ini := initials(t.assigneeNames(w)); ini != "" {
		right += " " + th.Body.Render(ini)
	}
	rw := ansi.StringWidth(right)
	k := th.PRRepo.Render(ansi.Truncate(w.Key(), max(iw-rw-1, 1), "…"))
	line1 := k + strings.Repeat(" ", max(iw-ansi.StringWidth(k)-rw, 1)) + right
	t1, t2 := titleLines(w.Name, iw)
	fill := func(s string) string {
		return th.Body.Render(s + strings.Repeat(" ", max(iw-ansi.StringWidth(s), 0)))
	}
	style := th.BoardCard
	if selected {
		style = th.BoardCardSelected
	}
	return style.Render(line1 + "\n" + fill(t1) + "\n" + fill(t2))
}

// titleLines wraps a title to two lines of w cells, ending the second in
// "…" when it doesn't fit.
func titleLines(title string, w int) (string, string) {
	lines := strings.Split(ansi.Wrap(strings.TrimSpace(title), max(w, 1), ""), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	switch len(lines) {
	case 1:
		return lines[0], ""
	case 2:
		return lines[0], lines[1]
	}
	rest := strings.Join(lines[1:], " ")
	return lines[0], ansi.Truncate(rest, w, "…")
}

// initials are the first two assignees' initials and how many more:
// "DD BO +1".
func initials(names []string) string {
	var parts []string
	for i, n := range names {
		if i == 2 {
			parts = append(parts, "+"+strconv.Itoa(len(names)-2))
			break
		}
		parts = append(parts, initial(n))
	}
	return strings.Join(parts, " ")
}

// initial is a name's initials: "Daan Dorchholz" → DD, "bob" → BO, an
// email by its local part.
func initial(name string) string {
	name, _, _ = strings.Cut(name, "@")
	f := strings.FieldsFunc(name, func(r rune) bool { return r == ' ' || r == '.' || r == '_' || r == '-' })
	switch {
	case len(f) == 0:
		return "?"
	case len(f) >= 2:
		return strings.ToUpper(string([]rune(f[0])[:1]) + string([]rune(f[1])[:1]))
	}
	r := []rune(f[0])
	return strings.ToUpper(string(r[:min(2, len(r))]))
}
