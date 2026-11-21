package app

import (
	"bytes"
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
)

// fakePlane serves one project's work items, filtered by assignee and
// state group like Plane.
type fakePlane struct {
	plane.API
	mu      sync.Mutex
	items   []plane.WorkItem
	states  []plane.State
	updates []string // "id state_id" per UpdateWorkItem
}

func (f *fakePlane) Me(context.Context) (plane.User, error) {
	return plane.User{ID: "u-me", DisplayName: "daan"}, nil
}

func (f *fakePlane) WorkItems(_ context.Context, _ string, fl plane.WorkItemFilter) ([]plane.WorkItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []plane.WorkItem
	for _, w := range f.items {
		if len(fl.AssigneeIDs) > 0 && !slices.Contains(w.AssigneeIDs, fl.AssigneeIDs[0]) {
			continue
		}
		if len(fl.StateGroups) > 0 && !slices.Contains(fl.StateGroups, w.State.Group) {
			continue
		}
		out = append(out, w)
	}
	return out, nil
}

func (f *fakePlane) CachedWorkItems(context.Context, string, plane.WorkItemFilter) (plane.Cached[plane.WorkItem], bool) {
	return plane.Cached[plane.WorkItem]{}, false
}

func (f *fakePlane) WorkItem(_ context.Context, _, id string) (plane.WorkItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, w := range f.items {
		if w.ID == id {
			w.DescriptionHTML = "<p>Details of " + w.Name + "</p>"
			return w, nil
		}
	}
	return plane.WorkItem{}, &plane.APIError{Status: 404}
}

func (f *fakePlane) Comments(context.Context, string, string) ([]plane.Comment, error) {
	return nil, nil
}

func (f *fakePlane) Project(context.Context, string) (plane.Project, error) {
	return plane.Project{ID: "p1", Identifier: "FORGE"}, nil
}

func (f *fakePlane) States(context.Context, string) ([]plane.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.states, nil
}

func (f *fakePlane) UpdateWorkItem(_ context.Context, _, id string, in plane.WorkItemUpdate) (plane.WorkItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, id+" "+*in.StateID)
	for i, w := range f.items {
		if w.ID != id {
			continue
		}
		for _, st := range f.states {
			if st.ID == *in.StateID {
				s := st
				f.items[i].StateID, f.items[i].State = st.ID, &s
			}
		}
		f.items[i].UpdatedAt = plane.Date{Time: time.Now()}
		return f.items[i], nil
	}
	return plane.WorkItem{}, &plane.APIError{Status: 404}
}

func (f *fakePlane) updateCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.updates)
}
func (f *fakePlane) Labels(context.Context, string) ([]plane.Label, error) { return nil, nil }
func (f *fakePlane) Cycles(context.Context, string) ([]plane.Cycle, error) { return nil, nil }
func (f *fakePlane) ProjectMembers(context.Context, string) ([]plane.Member, error) {
	return nil, nil
}

func planeItem(seq int, name string, group plane.StateGroup, assignee string) plane.WorkItem {
	st := plane.State{ID: string(group), Name: string(group), Group: group}
	return plane.WorkItem{
		ID: name, Identifier: "FORGE-" + string(rune('0'+seq)), SequenceID: seq, Name: name,
		ProjectID: "p1", StateID: st.ID, State: &st, AssigneeIDs: []string{assignee},
		Assignees: []plane.User{{ID: assignee, DisplayName: assignee}},
		UpdatedAt: plane.Date{Time: time.Now().Add(-time.Duration(seq) * time.Hour)},
	}
}

// TestBrowsePlaneIssues is the P3.2 "done when": browse and filter my
// Plane issues per project.
func TestBrowsePlaneIssues(t *testing.T) {
	cfg := config.Default()
	cfg.Plane = config.Plane{BaseURL: "https://plane.example", Workspace: "acme"}
	cfg.Projects = []config.Project{{Name: "forge", Repo: "daan/forge", PlaneProjectID: "p1"}}
	api := &fakePlane{items: []plane.WorkItem{
		planeItem(1, "Project switcher", plane.GroupStarted, "u-me"),
		planeItem(2, "Someone elses ticket", plane.GroupUnstarted, "u-bob"),
		planeItem(3, "Finished migration", plane.GroupCompleted, "u-me"),
	}}
	// Not mustNew: its synchronous Init drops follow-up commands (the
	// fetch after Me), so the program's own Init loads the tab here.
	m, err := New(cfg, withFetchDelay(0), WithGitHub(fakeGitHub{}), WithPlane(api, nil))
	if err != nil {
		t.Fatal(err)
	}
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(170, 30))

	var seen []byte
	wait := func(s string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
			if !bytes.Contains(append(slices.Clip(seen), b...), []byte(s)) {
				return false
			}
			seen = append(seen, b...)
			return true
		}, teatest.WithDuration(5*time.Second))
	}
	wait("forge")
	tm.Send(press("4")) // Plane
	wait("Project switcher")
	wait("Details of Project switcher")
	tm.Send(press("m")) // everyone's
	wait("Someone elses ticket")
	tm.Send(press("x")) // every state
	wait("Finished migration")

	tm.Send(ctrlK())
	for _, k := range typeText("FORGE-3") {
		tm.Send(k)
	}
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	wait("Details of Finished migration")

	tm.Send(press("q"))
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// TestPlaneBoardMove is the P3.3 "done when": move work across the board
// with the keyboard and Plane gets the change.
func TestPlaneBoardMove(t *testing.T) {
	cfg := config.Default()
	cfg.Plane = config.Plane{BaseURL: "https://plane.example", Workspace: "acme"}
	cfg.Projects = []config.Project{{Name: "forge", Repo: "daan/forge", PlaneProjectID: "p1"}}
	todo := plane.State{ID: "s-todo", Name: "Todo", Group: plane.GroupUnstarted, Sequence: 1}
	doing := plane.State{ID: "s-doing", Name: "Doing", Group: plane.GroupStarted, Sequence: 2}
	shipped := plane.State{ID: "s-shipped", Name: "Shipped", Group: plane.GroupCompleted, Sequence: 3}
	inState := func(w plane.WorkItem, st plane.State) plane.WorkItem {
		w.StateID, w.State = st.ID, &st
		return w
	}
	api := &fakePlane{
		states: []plane.State{todo, doing, shipped},
		items: []plane.WorkItem{
			inState(planeItem(1, "Project switcher", plane.GroupStarted, "u-me"), doing),
			inState(planeItem(2, "Board columns", plane.GroupUnstarted, "u-me"), todo),
		},
	}
	m, err := New(cfg, withFetchDelay(0), WithGitHub(fakeGitHub{}), WithPlane(api, nil))
	if err != nil {
		t.Fatal(err)
	}
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(170, 30))

	var seen []byte
	wait := func(s string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
			if !bytes.Contains(append(slices.Clip(seen), b...), []byte(s)) {
				return false
			}
			seen = append(seen, b...)
			return true
		}, teatest.WithDuration(5*time.Second))
	}
	wait("forge")
	tm.Send(press("4")) // Plane
	wait("Project switcher")
	tm.Send(press("b"))
	wait("every state, closed")
	// The status bar only redraws changed cells, so wait for short pieces.
	wait("Doing 1/1") // the cursor on Project switcher
	tm.Send(press("L"))
	wait("Shipped 1/1") // it moved, the cursor with it
	updates := func(n int) []string {
		deadline := time.Now().Add(5 * time.Second)
		for len(api.updateCalls()) < n && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		return api.updateCalls()
	}
	if got := updates(1); !slices.Equal(got, []string{"Project switcher s-shipped"}) {
		t.Fatalf("updates = %v", got)
	}
	tm.Send(press("H"))
	if got := updates(2); !slices.Equal(got, []string{"Project switcher s-shipped", "Project switcher s-doing"}) {
		t.Fatalf("updates = %v", got)
	}
	tm.Send(press("b"))
	wait("state: open")

	tm.Send(press("q"))
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}
