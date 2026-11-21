package app

import (
	"bytes"
	"context"
	"slices"
	"strings"
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
	lists   int      // WorkItems calls
	creates []plane.WorkItemCreate
}

// CreateWorkItem adds the item; the response has its sequence, not its
// identifier, like Plane's.
func (f *fakePlane) CreateWorkItem(_ context.Context, project string, in plane.WorkItemCreate) (plane.WorkItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates = append(f.creates, in)
	seq := len(f.items) + 1
	w := plane.WorkItem{
		ID: in.Name, Name: in.Name, SequenceID: seq, ProjectID: project, StateID: in.StateID,
		Priority: in.Priority, UpdatedAt: plane.Date{Time: time.Now()},
	}
	listed := w
	listed.Identifier = "FORGE-" + string(rune('0'+seq))
	for _, st := range f.states {
		if st.ID == in.StateID {
			s := st
			listed.State = &s
		}
	}
	f.items = append(f.items, listed)
	return w, nil
}

func (f *fakePlane) createCalls() []plane.WorkItemCreate {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.creates)
}

func (f *fakePlane) Me(context.Context) (plane.User, error) {
	return plane.User{ID: "u-me", DisplayName: "daan"}, nil
}

func (f *fakePlane) WorkItems(_ context.Context, _ string, fl plane.WorkItemFilter) ([]plane.WorkItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists++
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

// planeConfig is the default config with one forge project linked to
// Plane project p1. Intervals are set explicitly: a zero interval would be
// clamped (see TestZeroRefreshIntervalDoesNotSpin), and these tests must
// not depend on that.
func planeConfig() *config.Config {
	cfg := config.Default()
	cfg.Plane = config.Plane{
		BaseURL: "https://plane.example", Workspace: "acme",
		RefreshInterval: config.Duration{Duration: 2 * time.Minute},
		BoardDoneDays:   config.DefaultBoardDoneDays,
	}
	cfg.Projects = []config.Project{{Name: "forge", Repo: "daan/forge", PlaneProjectID: "p1"}}
	return cfg
}

// TestBrowsePlaneIssues is the P3.2 "done when": browse and filter my
// Plane issues per project.
func TestBrowsePlaneIssues(t *testing.T) {
	cfg := planeConfig()
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
	cfg := planeConfig()
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

// TestPlaneCreateIssue is the P3.4 "done when" for creating: a new item
// from the keyboard, its key in a toast, the cursor on it.
func TestPlaneCreateIssue(t *testing.T) {
	cfg := planeConfig()
	todo := plane.State{ID: "s-todo", Name: "Todo", Group: plane.GroupUnstarted, Sequence: 1, IsDefault: true}
	doing := plane.State{ID: "s-doing", Name: "Doing", Group: plane.GroupStarted, Sequence: 2}
	api := &fakePlane{
		states: []plane.State{todo, doing},
		items: []plane.WorkItem{
			planeItem(1, "Project switcher", plane.GroupStarted, "u-me"),
			planeItem(2, "Board columns", plane.GroupUnstarted, "u-bob"),
		},
	}
	m, err := New(cfg, withFetchDelay(0), WithGitHub(fakeGitHub{}), WithPlane(api, nil))
	if err != nil {
		t.Fatal(err)
	}
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(170, 40))

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
	tm.Send(press("m")) // everyone's: the new item is unassigned
	wait("Board columns")
	tm.Send(press("n"))
	wait("New issue")
	for _, r := range "Palette" {
		tm.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	// title → state → priority → assignee → cycle → description → submit
	for range 6 {
		tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	wait("created FORGE-3")
	wait("issue 1/3") // the cursor on the new (newest) item
	if got := api.createCalls(); len(got) != 1 || got[0].Name != "Palette" || got[0].StateID != "s-todo" {
		t.Fatalf("creates = %+v", got)
	}

	tm.Send(press("q"))
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// TestZeroRefreshIntervalDoesNotSpin: a config that skipped validation
// with plane.refresh_interval 0 used to refetch in a tight loop (hundreds
// of list calls a second). The scheduler clamps it to 5s and warns.
func TestZeroRefreshIntervalDoesNotSpin(t *testing.T) {
	cfg := planeConfig()
	cfg.Plane.RefreshInterval = config.Duration{}
	api := &fakePlane{items: []plane.WorkItem{planeItem(1, "Project switcher", plane.GroupStarted, "u-me")}}
	m, err := New(cfg, withFetchDelay(0), WithGitHub(fakeGitHub{}), WithPlane(api, nil))
	if err != nil {
		t.Fatal(err)
	}
	if w := m.Warnings(); len(w) != 1 || !strings.Contains(w[0], "plane.refresh_interval is 0s") {
		t.Fatalf("warnings = %q", w)
	}
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(170, 30))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool { return bytes.Contains(b, []byte("warning: plane.refresh")) },
		teatest.WithDuration(5*time.Second))
	tm.Send(press("4")) // Plane
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool { return bytes.Contains(b, []byte("Project switcher")) },
		teatest.WithDuration(5*time.Second))
	time.Sleep(time.Second)

	api.mu.Lock()
	n := api.lists
	api.mu.Unlock()
	if n > 3 {
		t.Fatalf("%d list fetches in about a second; the refresh loop spins", n)
	}
	tm.Send(press("q"))
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}
