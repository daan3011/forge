package demo

import (
	"context"
	"fmt"
	"time"

	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/testutil"
)

// Plane ids of the demo workspace.
const (
	planeWorkspace = "ember-labs"
	planeProject   = "p-anvil"
	userYou        = "u-you"
	userSmith      = "u-smith"
)

// planeFake is the in-memory Plane: testutil.FakePlane plus the cycle,
// member and single-item reads it lacks.
type planeFake struct {
	*testutil.FakePlane
	cycle   plane.Cycle
	members []plane.Member
}

var _ plane.API = (*planeFake)(nil)

func newPlane(now time.Time) *planeFake {
	date := func(d time.Duration) plane.Date { return plane.Date{Time: now.Add(d)} }
	states := []plane.State{
		{ID: "s-backlog", Name: "Backlog", Color: "#8a8580", Group: plane.GroupBacklog, Sequence: 1},
		{ID: "s-todo", Name: "Todo", Color: "#c9c2b8", Group: plane.GroupUnstarted, Sequence: 2, IsDefault: true},
		{ID: "s-doing", Name: "In Progress", Color: "#ff7a1a", Group: plane.GroupStarted, Sequence: 3},
		{ID: "s-review", Name: "In Review", Color: "#f5b041", Group: plane.GroupStarted, Sequence: 4},
		{ID: "s-done", Name: "Done", Color: "#7fb069", Group: plane.GroupCompleted, Sequence: 5},
	}
	labels := []plane.Label{
		{ID: "l-bug", Name: "bug", Color: "#e5484d"},
		{ID: "l-reliab", Name: "reliability", Color: "#7fb069"},
		{ID: "l-docs", Name: "docs", Color: "#5b9bd5"},
	}
	you := plane.User{ID: userYou, DisplayName: "you", FirstName: "You"}
	smith := plane.User{ID: userSmith, DisplayName: "smith", FirstName: "Smith"}
	cycle := plane.Cycle{ID: "c-12", Name: "Cycle 12 · Hot iron", StartDate: date(-6 * 24 * time.Hour), EndDate: date(8 * 24 * time.Hour)}

	stateByID := map[string]plane.State{}
	for _, s := range states {
		stateByID[s.ID] = s
	}
	labelByID := map[string]plane.Label{}
	for _, l := range labels {
		labelByID[l.ID] = l
	}
	n := 0
	item := func(name, state string, pri plane.Priority, assignee plane.User, lbls ...string) plane.WorkItem {
		n++
		st := stateByID[state]
		w := plane.WorkItem{
			ID: fmt.Sprintf("w-%d", n), Name: name, Identifier: fmt.Sprintf("ANV-%d", 40+n), SequenceID: 40 + n,
			ProjectID: planeProject, StateID: state, Priority: pri, AssigneeIDs: []string{assignee.ID},
			LabelIDs: lbls, CycleID: cycle.ID, State: &st, Assignees: []plane.User{assignee}, Cycle: &cycle,
			DescriptionHTML: "<p>" + name + ".</p><ul><li>acceptance: covered by a test</li><li>runbook updated</li></ul>",
			CreatedAt:       date(-time.Duration(n) * 20 * time.Hour), UpdatedAt: date(-time.Duration(n) * 37 * time.Minute),
			CreatedByID: userSmith,
		}
		for _, id := range lbls {
			w.Labels = append(w.Labels, labelByID[id])
		}
		return w
	}
	items := []plane.WorkItem{
		item("Quench step times out on cold mornings", "s-review", plane.PriorityHigh, you, "l-bug", "l-reliab"),
		item("Stream heat readings over the bellows API", "s-doing", plane.PriorityMedium, you, "l-reliab"),
		item("Airflow presets per alloy", "s-doing", plane.PriorityMedium, you),
		item("Crucible overflows on batches over 40 kg", "s-todo", plane.PriorityUrgent, you, "l-bug"),
		item("Write the forge-weld runbook", "s-todo", plane.PriorityLow, you, "l-docs"),
		item("Configurable hammer cadence", "s-backlog", plane.PriorityLow, you),
		item("Cooler cache for anvil temperatures", "s-done", plane.PriorityMedium, you),
		item("Spark arrestor alarms", "s-todo", plane.PriorityHigh, smith, "l-reliab"),
	}
	f := &testutil.FakePlane{
		ProjectList: []plane.Project{{ID: planeProject, Name: "Anvil", Identifier: "ANV"}},
		StateMap:    map[string][]plane.State{planeProject: states},
		LabelMap:    map[string][]plane.Label{planeProject: labels},
		Items:       items,
		CommentMap: map[string][]plane.Comment{
			"w-1": {{ID: "cm-1", WorkItemID: "w-1", CommentHTML: "<p>Repro'd at 4°C ambient.</p>", CommentStripped: "Repro'd at 4°C ambient.", ActorID: userSmith, CreatedAt: date(-3 * time.Hour)}},
		},
		MeUser: you,
	}
	return &planeFake{
		FakePlane: f, cycle: cycle,
		members: []plane.Member{{ID: "m-1", MemberID: userYou, Member: &you}, {ID: "m-2", MemberID: userSmith, Member: &smith}},
	}
}

func (p *planeFake) Cycles(context.Context, string) ([]plane.Cycle, error) {
	return []plane.Cycle{p.cycle}, nil
}

func (p *planeFake) CurrentCycle(context.Context, string) (plane.Cycle, bool, error) {
	return p.cycle, true, nil
}

func (p *planeFake) ProjectMembers(context.Context, string) ([]plane.Member, error) {
	return p.members, nil
}

func (p *planeFake) WorkspaceMembers(context.Context) ([]plane.Member, error) {
	return p.members, nil
}

func (p *planeFake) CachedWorkItems(context.Context, string, plane.WorkItemFilter) (plane.Cached[plane.WorkItem], bool) {
	return plane.Cached[plane.WorkItem]{}, false
}

func (p *planeFake) WorkItem(_ context.Context, _, id string) (plane.WorkItem, error) {
	for _, w := range p.Items {
		if w.ID == id {
			return w, nil
		}
	}
	return plane.WorkItem{}, fmt.Errorf("work item %s: not found", id)
}
