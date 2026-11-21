package plane

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/tabs"
)

// boardHarness is the app project's tab, loaded and switched to the board
// (the cursor on FORGE-1, the list's first row).
func boardHarness(t *testing.T, api *fakePlane, ss *fakeSessions, project string) *harness {
	t.Helper()
	var h *harness
	if ss != nil {
		h = newHarness(api, ss, project)
	} else {
		h = newHarness(api, nil, project)
	}
	if errs := h.init(); len(errs) > 0 {
		t.Fatal(errs)
	}
	if errs := h.send("b"); len(errs) > 0 {
		t.Fatal(errs)
	}
	if h.mode != modeBoard {
		t.Fatal("b should show the board")
	}
	return h
}

// columns is "name: KEY KEY" per column.
func (h *harness) columns() []string {
	var out []string
	for _, c := range h.board.cols {
		var keys []string
		for _, w := range c.cards {
			keys = append(keys, w.Key())
		}
		out = append(out, c.name+": "+strings.Join(keys, " "))
	}
	return out
}

func (h *harness) cursorCol() string { return h.board.cols[h.board.col].name }

func (h *harness) columnOf(id string) string {
	for _, c := range h.board.cols {
		for _, w := range c.cards {
			if w.ID == id {
				return c.name
			}
		}
	}
	return ""
}

func TestBoardColumnsAndFetch(t *testing.T) {
	api := samplePlane()
	h := boardHarness(t, api, nil, "app")

	want := []string{"Todo: FORGE-2", "In Progress: FORGE-1", "Done: FORGE-3"}
	if got := h.columns(); !slices.Equal(got, want) {
		t.Fatalf("columns = %v, want %v", got, want)
	}
	calls := api.listCalls()
	closed := p1 + " assignee=u-me groups=[completed cancelled] label= cycle= after=2026-11-07T00:00:00Z"
	if !slices.Contains(calls, closed) || !slices.Contains(calls, p1+" assignee=u-me "+openKey+" label= cycle=") {
		t.Fatalf("calls = %v", calls)
	}
	if h.selectedID() != "w-FORGE-1" || h.cursorCol() != "In Progress" {
		t.Fatalf("cursor on %s in %s", h.selectedID(), h.cursorCol())
	}
	v := h.view()
	mustContain(t, v, "Todo 1", "In Progress 1", "Done 1", "Project switcher", "FORGE-2", "every state, closed ≤ 14d")
	if got := h.StatusDetail(); !strings.Contains(got, "In Progress 1/1 · column 2/3") {
		t.Fatalf("status = %q", got)
	}
}

func TestBoardMergesProjectsByState(t *testing.T) {
	api := samplePlane()
	h := boardHarness(t, api, nil, "")
	want := []string{"Todo: FORGE-2 LIB-7", "In Progress: FORGE-1", "Done: FORGE-3"}
	if got := h.columns(); !slices.Equal(got, want) {
		t.Fatalf("columns = %v, want %v", got, want)
	}
	mustContain(t, h.view(), "Todo 2", "2 projects")
}

func TestBoardNavigation(t *testing.T) {
	h := boardHarness(t, samplePlane(), nil, "")
	h.send("h")
	if h.cursorCol() != "Todo" || h.selectedID() != "w-FORGE-2" {
		t.Fatalf("h: %s %s", h.cursorCol(), h.selectedID())
	}
	h.send("j")
	if h.selectedID() != "w-LIB-7" {
		t.Fatalf("j: %s", h.selectedID())
	}
	h.send("j", "k")
	if h.selectedID() != "w-FORGE-2" {
		t.Fatalf("j k: %s", h.selectedID())
	}
	h.send("l", "l", "l")
	if h.cursorCol() != "Done" || h.selectedID() != "w-FORGE-3" {
		t.Fatalf("l: %s %s", h.cursorCol(), h.selectedID())
	}
	// The Todo column remembers its card.
	h.send("j")
	h.send("h", "h")
	if h.selectedID() != "w-FORGE-2" {
		t.Fatalf("back to Todo: %s", h.selectedID())
	}
}

func TestBoardScrollsHorizontally(t *testing.T) {
	h := boardHarness(t, samplePlane(), nil, "app")
	h.SetSize(60, 30) // two 28-wide columns fit
	v := h.view()
	mustContain(t, v, "1 ›", "In Progress")
	if strings.Contains(v, "Done") || strings.Contains(v, "‹") {
		t.Fatalf("Done should be off screen:\n%s", v)
	}
	h.send("l")
	v = h.view()
	mustContain(t, v, "‹ 1", "Done", "In Progress")
	if strings.Contains(v, "›") {
		t.Fatalf("nothing is right of Done:\n%s", v)
	}
	for _, line := range strings.Split(v, "\n") {
		if w := ansi.StringWidth(line); w > 60 {
			t.Fatalf("line %d wide: %q", w, line)
		}
	}
}

func TestBoardCollapse(t *testing.T) {
	api := samplePlane()
	backlog := plane.State{ID: "s-backlog", Name: "Backlog", Group: plane.GroupBacklog}
	review := plane.State{ID: "s-review", Name: "Review", Group: plane.GroupStarted, Sequence: 1}
	cancelled := plane.State{ID: "s-cancel", Name: "Cancelled", Group: plane.GroupCancelled}
	api.statesBy = map[string][]plane.State{p1: {stDone, cancelled, review, stDoing, backlog, stTodo}}
	h := boardHarness(t, api, nil, "app")

	want := []string{"Backlog: ", "Todo: FORGE-2", "In Progress: FORGE-1", "Review: ", "Done: FORGE-3", "Cancelled: "}
	if got := h.columns(); !slices.Equal(got, want) {
		t.Fatalf("columns = %v, want %v", got, want)
	}
	collapsed := func() []bool {
		var out []bool
		for i := range h.board.cols {
			out = append(out, h.board.collapsed(i))
		}
		return out
	}
	if got := collapsed(); !slices.Equal(got, []bool{false, false, false, false, false, true}) {
		t.Fatalf("collapsed = %v", got)
	}

	// z folds the cursor's column; it stays a strip under the cursor and
	// nothing is selected in it.
	h.send("z")
	if got := collapsed(); !slices.Equal(got, []bool{false, false, true, false, false, true}) {
		t.Fatalf("after z = %v", got)
	}
	if h.selectedID() != "" || h.cursorCol() != "In Progress" {
		t.Fatalf("selected %q in %s", h.selectedID(), h.cursorCol())
	}
	h.send("L", "j", "enter") // no card: nothing to move or open
	if len(api.updateCalls()) != 0 || h.overlay {
		t.Fatalf("acted on a collapsed column: %v overlay %v", api.updateCalls(), h.overlay)
	}
	h.send("z")
	if h.board.collapsed(2) || h.selectedID() != "w-FORGE-1" {
		t.Fatalf("second z: collapsed %v, selected %q", h.board.collapsed(2), h.selectedID())
	}

	// Z folds every empty column, then opens them again.
	h.send("Z")
	if got := collapsed(); !slices.Equal(got, []bool{true, false, false, true, false, true}) {
		t.Fatalf("after Z = %v", got)
	}
	if w := h.columnWidths(158); w[0] != boardStrip || w[3] != boardStrip || w[1] == boardStrip {
		t.Fatalf("widths = %v", w)
	}
	h.send("Z")
	if got := collapsed(); !slices.Equal(got, []bool{false, false, false, false, false, false}) {
		t.Fatalf("second Z = %v", got)
	}
	mustContain(t, h.view(), "Cancelled 0")

	// Moving a card into a collapsed column opens it.
	h.send("l", "z", "h", "L")
	if h.board.collapsed(3) || h.columnOf("w-FORGE-1") != "Review" || h.selectedID() != "w-FORGE-1" {
		t.Fatalf("move into folded Review: %v, %s", collapsed(), h.columnOf("w-FORGE-1"))
	}
}

func TestBoardTextFilter(t *testing.T) {
	h := boardHarness(t, samplePlane(), nil, "")
	h.send("/", "b", "u", "g", "enter") // LIB-7 "Lib bug" and FORGE-2's "bug" label
	want := []string{"Todo: FORGE-2 LIB-7", "In Progress: ", "Done: "}
	if got := h.columns(); !slices.Equal(got, want) {
		t.Fatalf("columns = %v, want %v", got, want)
	}
	// Same matching as the list.
	if got := h.keys(); !slices.Equal(got, []string{"FORGE-2", "LIB-7"}) {
		t.Fatalf("list rows = %v", got)
	}
	if h.selectedID() != "w-FORGE-2" {
		t.Fatalf("cursor on %q", h.selectedID())
	}
	mustContain(t, h.view(), "/ bug", "Todo 2", "In Progress 0")

	// Picking a hidden issue drops the filter.
	h.Select("w-FORGE-3")
	if h.filter.Visible() || h.columnOf("w-FORGE-3") != "Done" || h.selectedID() != "w-FORGE-3" {
		t.Fatalf("select hidden: filter %q, selected %q", h.filter.Value(), h.selectedID())
	}
	// esc clears it on the board.
	h.send("/", "s", "w", "i", "t", "c", "h", "enter")
	if got := h.columns(); !slices.Equal(got, []string{"Todo: ", "In Progress: FORGE-1", "Done: "}) {
		t.Fatalf("switch filter: %v", got)
	}
	h.send("esc")
	if h.filter.Visible() || len(h.columns()[0]) == len("Todo: ") {
		t.Fatalf("esc: filter %q, columns %v", h.filter.Value(), h.columns())
	}
}

// A filter that hides the cursor's card moves to the nearest column with
// cards; clearing it goes back to the card.
func TestBoardFilterKeepsWantedCard(t *testing.T) {
	h := boardHarness(t, samplePlane(), nil, "app") // on FORGE-1, In Progress
	h.send("/", "m", "i", "g", "r")                 // only FORGE-3 "Store migrations" (Done)
	if h.selectedID() != "w-FORGE-3" || h.cursorCol() != "Done" {
		t.Fatalf("filtered: %q in %s", h.selectedID(), h.cursorCol())
	}
	h.send("esc")
	if h.selectedID() != "w-FORGE-1" {
		t.Fatalf("after esc: %q", h.selectedID())
	}
}

func TestBoardMoveCard(t *testing.T) {
	api, ss := samplePlane(), &fakeSessions{}
	h := boardHarness(t, api, ss, "app")
	if errs := h.send("L"); len(errs) > 0 {
		t.Fatal(errs)
	}
	if got := api.updateCalls(); !slices.Equal(got, []string{p1 + " w-FORGE-1 s-done"}) {
		t.Fatalf("updates = %v", got)
	}
	if h.columnOf("w-FORGE-1") != "Done" || h.cursorCol() != "Done" || h.selectedID() != "w-FORGE-1" {
		t.Fatalf("FORGE-1 in %s, cursor %s on %s", h.columnOf("w-FORGE-1"), h.cursorCol(), h.selectedID())
	}
	if n := len(h.moves); n != 0 {
		t.Fatalf("%d moves pending", n)
	}
	if last := ss.states[len(ss.states)-1]; last["FORGE-1"] != "Done" {
		t.Fatalf("links state = %v", ss.states)
	}
	// It survives a refresh (now from the closed list), and H moves it back.
	h.drain(h.Refresh())
	if h.columnOf("w-FORGE-1") != "Done" {
		t.Fatalf("after refresh: %v", h.columns())
	}
	h.send("H")
	if h.columnOf("w-FORGE-1") != "In Progress" || api.updateCalls()[1] != p1+" w-FORGE-1 s-doing" {
		t.Fatalf("H: %v %v", h.columns(), api.updateCalls())
	}
	// At the last state nothing happens.
	h.send("l", "j", "L")
	if n := len(api.updateCalls()); n != 2 {
		t.Fatalf("moved past the last state: %v", api.updateCalls())
	}
}

func TestBoardMoveRollsBack(t *testing.T) {
	api := samplePlane()
	api.updateErrs = []error{errors.New("boom")}
	h := boardHarness(t, api, nil, "app")
	errs := h.send("L")
	if len(errs) != 1 || !strings.Contains(errs[0], "FORGE-1: couldn't move to Done: boom") {
		t.Fatalf("errs = %v", errs)
	}
	if h.columnOf("w-FORGE-1") != "In Progress" || h.selectedID() != "w-FORGE-1" {
		t.Fatalf("not rolled back: %v", h.columns())
	}
}

func TestBoardDoubleMoveSecondFails(t *testing.T) {
	api := samplePlane()
	api.updateErrs = []error{nil, errors.New("nope")}
	h := boardHarness(t, api, nil, "app")
	h.send("h") // FORGE-2 in Todo

	_, first := h.Update(press("L"))
	_, second := h.Update(press("L"))
	if h.columnOf("w-FORGE-2") != "Done" {
		t.Fatalf("optimistic: %v", h.columns())
	}
	errs, _ := h.drain(second) // nothing sent: the first is on its way
	errs2, _ := h.drain(first)
	errs = append(errs, errs2...)
	if got := api.updateCalls(); !slices.Equal(got, []string{p1 + " w-FORGE-2 s-doing", p1 + " w-FORGE-2 s-done"}) {
		t.Fatalf("updates = %v", got)
	}
	if len(errs) != 1 || !strings.Contains(errs[0], "couldn't move to Done: nope") {
		t.Fatalf("errs = %v", errs)
	}
	if h.columnOf("w-FORGE-2") != "In Progress" {
		t.Fatalf("should end where Plane accepted it: %v", h.columns())
	}
}

func TestBoardRefreshMidMoveKeepsTarget(t *testing.T) {
	api := samplePlane()
	h := boardHarness(t, api, nil, "app")
	_, move := h.Update(press("L"))
	h.drain(h.Refresh()) // Plane still says In Progress
	if h.columnOf("w-FORGE-1") != "Done" {
		t.Fatalf("flicked back: %v", h.columns())
	}
	h.drain(move)
	if h.columnOf("w-FORGE-1") != "Done" || len(h.moves) != 0 {
		t.Fatalf("after the PATCH: %v, %d pending", h.columns(), len(h.moves))
	}
}

func TestBoardMoveNeedsStates(t *testing.T) {
	h := boardHarness(t, samplePlane(), nil, "app")
	delete(h.meta, p1)
	errs := h.send("L")
	if len(errs) != 1 || !strings.Contains(errs[0], "states still loading") {
		t.Fatalf("errs = %v", errs)
	}
}

func TestBoardToggleKeepsSelection(t *testing.T) {
	h := newHarness(samplePlane(), nil, "app")
	h.init()
	h.send("j") // FORGE-2
	if h.selectedID() != "w-FORGE-2" {
		t.Fatalf("list selection = %s", h.selectedID())
	}
	h.send("b")
	if h.selectedID() != "w-FORGE-2" || h.cursorCol() != "Todo" {
		t.Fatalf("board selection = %s in %s", h.selectedID(), h.cursorCol())
	}
	h.send("l")
	h.send("b")
	if h.mode != modeList || h.selectedID() != "w-FORGE-1" {
		t.Fatalf("list selection after b = %s", h.selectedID())
	}
	mustContain(t, h.view(), "state: open")
}

func TestBoardOverlay(t *testing.T) {
	h := boardHarness(t, samplePlane(), nil, "app")
	if strings.Contains(h.view(), "Description") {
		t.Fatal("no detail on the board until enter")
	}
	h.send("enter")
	if !h.overlay {
		t.Fatal("enter should open the card")
	}
	mustContain(t, h.view(), "Description", "Goal", "Switch projects with")

	_, cmd := h.Update(press("y"))
	_, infos := h.drain(cmd)
	if len(infos) != 1 || infos[0] != "copied https://plane.example/acme/browse/FORGE-1/" {
		t.Fatalf("infos = %v", infos)
	}
	h.send("L") // moves work on the overlay too
	if h.columnOf("w-FORGE-1") != "Done" {
		t.Fatalf("L on overlay: %v", h.columns())
	}
	h.send("esc")
	if h.overlay || strings.Contains(h.view(), "Description") {
		t.Fatal("esc should close the card")
	}
}

func TestBoardPaletteCommands(t *testing.T) {
	h := newHarness(samplePlane(), nil, "app")
	h.init()
	byID := map[string]tabs.Command{}
	for _, c := range h.Commands() {
		byID[c.ID] = c
	}
	for _, id := range []string{"plane_board", "plane_col_left", "plane_col_right", "plane_move_prev", "plane_move_next", "plane_collapse_column", "plane_collapse_empty", "plane_open_card"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("no palette command %s", id)
		}
	}
	// Moving works from the list too (palette only).
	h.drain(byID["plane_move_next"].Run())
	if w, _ := h.itemByID("w-FORGE-1"); w.StateID != stDone.ID {
		t.Fatalf("FORGE-1 state = %s", w.StateID)
	}
	// Board-only commands switch to the board first.
	h.drain(byID["plane_open_card"].Run())
	if h.mode != modeBoard || !h.overlay {
		t.Fatalf("mode %v overlay %v", h.mode, h.overlay)
	}
}

func TestTitleLines(t *testing.T) {
	tests := []struct {
		title  string
		w      int
		l1, l2 string
	}{
		{"Short", 20, "Short", ""},
		{"Fix the palette ranking", 16, "Fix the palette", "ranking"},
		{"Fix the palette ranking for real", 12, "Fix the", "palette ran…"},
		{"Supercalifragilistic", 8, "Supercal", "ifragil…"},
		{"", 10, "", ""},
	}
	for _, tt := range tests {
		l1, l2 := titleLines(tt.title, tt.w)
		if l1 != tt.l1 || l2 != tt.l2 {
			t.Errorf("titleLines(%q, %d) = %q, %q; want %q, %q", tt.title, tt.w, l1, l2, tt.l1, tt.l2)
		}
	}
}

func TestInitials(t *testing.T) {
	tests := []struct {
		names []string
		want  string
	}{
		{nil, ""},
		{[]string{"daan"}, "DA"},
		{[]string{"Daan Dorchholz"}, "DD"},
		{[]string{"jo.smith@example.com"}, "JS"},
		{[]string{"b"}, "B"},
		{[]string{"daan", "bob", "eve", "al"}, "DA BO +2"},
	}
	for _, tt := range tests {
		if got := initials(tt.names); got != tt.want {
			t.Errorf("initials(%v) = %q, want %q", tt.names, got, tt.want)
		}
	}
}

var _ tea.Msg = moveDoneMsg{}
