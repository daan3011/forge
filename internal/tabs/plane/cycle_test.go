package plane

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/tabs"
)

var (
	stBacklog = plane.State{ID: "s-backlog", Name: "Backlog", Group: plane.GroupBacklog}
	stBlocked = plane.State{ID: "s-blocked", Name: "Blocked", Group: plane.GroupStarted}
	stCancel  = plane.State{ID: "s-cancel", Name: "Cancelled", Group: plane.GroupCancelled}
	lbBlocked = plane.Label{ID: "l-blocked", Name: "Blocked"}
)

func TestCountCycle(t *testing.T) {
	inCycle := func(st plane.State, labels ...plane.Label) plane.WorkItem {
		return item(p1, "FORGE", 9, "x", st, plane.PriorityNone, nil, labels, nil)
	}
	unknown := plane.WorkItem{ID: "w-unknown"}
	tests := []struct {
		name  string
		items []plane.WorkItem
		want  cycleCounts
		pct   float64
	}{
		{"empty", nil, cycleCounts{}, 0},
		{
			"each group",
			[]plane.WorkItem{inCycle(stDone), inCycle(stDoing), inCycle(stTodo), inCycle(stBacklog)},
			cycleCounts{done: 1, started: 1, todo: 2},
			0.25,
		},
		{
			"blocked by label, any case",
			[]plane.WorkItem{inCycle(stTodo, lbBlocked), inCycle(stDoing, lbBug)},
			cycleCounts{blocked: 1, started: 1},
			0,
		},
		{"blocked by state", []plane.WorkItem{inCycle(stBlocked), inCycle(stDone)}, cycleCounts{blocked: 1, done: 1}, 0.5},
		{"done wins over a blocked label", []plane.WorkItem{inCycle(stDone, lbBlocked)}, cycleCounts{done: 1}, 1},
		{
			"cancelled left out of the total",
			[]plane.WorkItem{inCycle(stCancel), inCycle(stCancel, lbBlocked), inCycle(stDone), inCycle(stTodo)},
			cycleCounts{cancelled: 2, done: 1, todo: 1},
			0.5,
		},
		{"only cancelled", []plane.WorkItem{inCycle(stCancel)}, cycleCounts{cancelled: 1}, 0},
		{"unknown state is todo", []plane.WorkItem{unknown}, cycleCounts{todo: 1}, 0},
	}
	stateOf := func(w plane.WorkItem) (string, plane.StateGroup) {
		if w.State == nil {
			return "", ""
		}
		return w.State.Name, w.State.Group
	}
	labelsOf := func(w plane.WorkItem) []string {
		var out []string
		for _, l := range w.Labels {
			out = append(out, l.Name)
		}
		return out
	}
	blocked := config.Default().Plane.IsBlocked
	for _, tt := range tests {
		got := countCycle(tt.items, stateOf, labelsOf, blocked)
		if got != tt.want {
			t.Errorf("%s: counts = %+v, want %+v", tt.name, got, tt.want)
		}
		if p := got.pct(); math.IsNaN(p) || math.Abs(p-tt.pct) > 1e-9 {
			t.Errorf("%s: pct = %v, want %v", tt.name, p, tt.pct)
		}
	}
}

func TestDaysLeft(t *testing.T) {
	ams := time.FixedZone("CET", 3600)
	day := func(y int, m time.Month, d int) plane.Date {
		return plane.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
	}
	tests := []struct {
		name  string
		end   plane.Date
		now   time.Time
		want  int
		label string
	}{
		{"date-only, last day", day(2026, 11, 21), now, 0, "ends today"},
		{"date-only, last day late evening", day(2026, 11, 21), time.Date(2026, 11, 21, 23, 30, 0, 0, ams), 0, "ends today"},
		{"date-only, tomorrow", day(2026, 11, 22), now, 1, "1 day left"},
		{"date-only, across a month", day(2026, 12, 3), now, 12, "12 days left"},
		{"date-time", plane.Date{Time: now.Add(72 * time.Hour)}, now, 3, "3 days left"},
		{"date-time, in now's zone", plane.Date{Time: time.Date(2026, 11, 22, 23, 30, 0, 0, time.UTC)}, time.Date(2026, 11, 21, 12, 0, 0, 0, ams), 2, "2 days left"},
		{"past", day(2026, 11, 20), now, -1, "ended"},
	}
	for _, tt := range tests {
		got := daysLeft(plane.Cycle{EndDate: tt.end}, tt.now)
		if got != tt.want || daysLeftLabel(got) != tt.label {
			t.Errorf("%s: daysLeft = %d (%q), want %d (%q)", tt.name, got, daysLeftLabel(got), tt.want, tt.label)
		}
	}
}

// cyclePlane has Sprint 12 holding my FORGE-1 (in progress) and FORGE-2
// (todo), and bob's done, blocked and cancelled items, which the default
// "mine, open" list doesn't show.
func cyclePlane() *fakePlane {
	api := samplePlane()
	inCycle := func(w *plane.WorkItem) { w.CycleID = cyc12.ID }
	for i := range api.items[p1] {
		if api.items[p1][i].SequenceID == 2 {
			api.items[p1][i].CycleID = cyc12.ID
		}
	}
	api.items[p1] = append(api.items[p1],
		item(p1, "FORGE", 5, "Bob shipped", stDone, plane.PriorityNone, []plane.User{bob}, nil, inCycle),
		item(p1, "FORGE", 6, "Bob waits", stTodo, plane.PriorityNone, []plane.User{bob}, []plane.Label{lbBlocked}, inCycle),
		item(p1, "FORGE", 8, "Dropped", stCancel, plane.PriorityNone, []plane.User{bob}, nil, inCycle),
	)
	return api
}

func TestCycleSummaryHeader(t *testing.T) {
	api := cyclePlane()
	h := newHarness(api, nil, "app")
	if errs := h.init(); len(errs) > 0 {
		t.Fatal(errs)
	}
	if got := api.cycleCalls(); !slices.Equal(got, []string{p1 + " assignee= groups=[] label= cycle=c-12"}) {
		t.Fatalf("cycle calls = %v", got)
	}
	if got := h.keys(); !slices.Equal(got, []string{"FORGE-1", "FORGE-2"}) {
		t.Fatalf("rows = %v (the summary must not change the list)", got)
	}
	// The list pane is narrow: no key hint or cancelled count.
	mustContain(t, h.view(), "◷ Sprint 12 · 3 days left", "25%", "done 1 · in progress 1 · blocked 1 · todo 1")

	// The board has the full width.
	h.send("b")
	mustContain(t, h.view(), "◷ Sprint 12 · 3 days left", "25%",
		"done 1 · in progress 1 · blocked 1 · todo 1 · 1 cancelled", "C cycle")

	// Narrow: short labels.
	h.send("b")
	h.SetSize(90, 30)
	mustContain(t, h.view(), "◷ Sprint 12", "✓1 ▸1 ⊘1 ○1")
}

func TestCycleSummaryHiddenWithoutCurrentCycle(t *testing.T) {
	api := cyclePlane()
	old := cyc12
	old.ID, old.Name = "c-11", "Sprint 11"
	old.StartDate, old.EndDate = plane.Date{Time: now.AddDate(0, 0, -20)}, plane.Date{Time: now.AddDate(0, 0, -6)}
	api.cyclesBy = map[string][]plane.Cycle{p1: {old}}
	h := newHarness(api, nil, "app")
	h.init()
	before := lipgloss.Height(h.table.View())
	if v := h.view(); strings.Contains(v, "◷") {
		t.Fatalf("summary without a current cycle:\n%s", v)
	}
	if got := api.cycleCalls(); len(got) != 0 {
		t.Fatalf("cycle calls = %v", got)
	}
	_, infos := h.drain(h.toggleCycle())
	if len(infos) != 1 || infos[0] != "no current cycle" {
		t.Fatalf("infos = %v", infos)
	}
	// With a current cycle, the line takes a row from the table.
	h2 := newHarness(cyclePlane(), nil, "app")
	h2.init()
	if got := lipgloss.Height(h2.table.View()); got != before-1 {
		t.Fatalf("table height = %d, want %d", got, before-1)
	}
}

func TestCycleSummaryHiddenForSeveralProjects(t *testing.T) {
	api := cyclePlane()
	h := newHarness(api, nil, "")
	h.init()
	if v := h.view(); strings.Contains(v, "◷") {
		t.Fatalf("summary over several projects:\n%s", v)
	}
	_, infos := h.drain(h.toggleCycle())
	if len(infos) != 1 || !strings.Contains(infos[0], "pick one project") {
		t.Fatalf("infos = %v", infos)
	}
}

func TestCycleKeyTogglesFilter(t *testing.T) {
	api := cyclePlane()
	h := newHarness(api, nil, "app")
	h.init()
	h.send("x") // anyone's, every state
	if got := len(h.keys()); got != 7 {
		t.Fatalf("rows = %v", h.keys())
	}
	if errs := h.send("C"); len(errs) > 0 {
		t.Fatal(errs)
	}
	if h.filt.cycle != cyc12.ID {
		t.Fatalf("cycle filter = %q", h.filt.cycle)
	}
	if got := api.listCalls(); got[len(got)-1] != p1+" assignee= groups=[] label= cycle=c-12" {
		t.Fatalf("calls = %v", got)
	}
	if got := h.keys(); !slices.Equal(got, []string{"FORGE-1", "FORGE-2", "FORGE-5", "FORGE-6", "FORGE-8"}) {
		t.Fatalf("rows = %v", got)
	}
	mustContain(t, h.view(), "cycle: Sprint 12")
	if cmd := findCommand(h.Commands(), "plane_cycle"); cmd.Title != "Plane: every cycle" {
		t.Fatalf("palette title = %q", cmd.Title)
	}
	// On the board too, and C again clears it.
	h.send("b", "C")
	if h.filt.cycle != "" {
		t.Fatalf("cycle filter = %q after the second C", h.filt.cycle)
	}
	if v := h.view(); strings.Contains(v, "cycle: Sprint 12") || !strings.Contains(v, "C cycle") {
		t.Fatalf("filter box still names the cycle:\n%s", v)
	}
	if cmd := findCommand(h.Commands(), "plane_cycle"); cmd.Title != "Plane: current cycle only" {
		t.Fatalf("palette title = %q", cmd.Title)
	}
}

func TestCycleSummaryFollowsMoves(t *testing.T) {
	api := cyclePlane()
	h := boardHarness(t, api, nil, "app")
	calls := len(api.cycleCalls())
	mustContain(t, h.view(), "done 1 · in progress 1")
	if errs := h.send("L"); len(errs) > 0 { // FORGE-1: In Progress -> Done
		t.Fatal(errs)
	}
	mustContain(t, h.view(), "50%", "done 2 · in progress 0")
	if got := len(api.cycleCalls()); got != calls {
		t.Fatalf("a move refetched the cycle: %v", api.cycleCalls())
	}
}

func findCommand(cmds []tabs.Command, id string) tabs.Command {
	for _, c := range cmds {
		if c.ID == id {
			return c
		}
	}
	return tabs.Command{}
}
