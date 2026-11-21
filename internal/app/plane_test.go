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
	mu    sync.Mutex
	items []plane.WorkItem
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
func (f *fakePlane) States(context.Context, string) ([]plane.State, error) { return nil, nil }
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
