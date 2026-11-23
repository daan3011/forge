package testutil

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/daan3011/forge/internal/plane"
)

// FakePlane is an in-memory plane.API for tests. Methods it doesn't
// implement panic through the nil embedded interface, so a test notices an
// unexpected call. ProjectList, StateMap, LabelMap, Items and CommentMap
// are the fixtures; Created, Updated and Commented record writes.
type FakePlane struct {
	plane.API

	mu          sync.Mutex
	ProjectList []plane.Project
	StateMap    map[string][]plane.State // by project id
	LabelMap    map[string][]plane.Label // by project id
	Items       []plane.WorkItem
	CommentMap  map[string][]plane.Comment // by work item id
	MeUser      plane.User

	Created   []plane.WorkItemCreate
	Updated   map[string]plane.WorkItemUpdate // by work item id
	Commented []plane.CommentCreate
	// Err, when set, is returned by every write.
	Err error
}

// Me implements plane.API.
func (f *FakePlane) Me(context.Context) (plane.User, error) { return f.MeUser, nil }

// Projects implements plane.API.
func (f *FakePlane) Projects(context.Context) ([]plane.Project, error) { return f.ProjectList, nil }

// Project implements plane.API.
func (f *FakePlane) Project(_ context.Context, id string) (plane.Project, error) {
	for _, p := range f.ProjectList {
		if p.ID == id {
			return p, nil
		}
	}
	return plane.Project{}, fmt.Errorf("project %s: not found", id)
}

// States implements plane.API.
func (f *FakePlane) States(_ context.Context, projectID string) ([]plane.State, error) {
	return f.StateMap[projectID], nil
}

// Labels implements plane.API.
func (f *FakePlane) Labels(_ context.Context, projectID string) ([]plane.Label, error) {
	return f.LabelMap[projectID], nil
}

// WorkItems implements plane.API: items of the project, filtered by
// state group (through States), assignee and a name search.
func (f *FakePlane) WorkItems(_ context.Context, projectID string, flt plane.WorkItemFilter) ([]plane.WorkItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	group := map[string]plane.StateGroup{}
	for _, s := range f.StateMap[projectID] {
		group[s.ID] = s.Group
	}
	var out []plane.WorkItem
	for _, w := range f.Items {
		if w.ProjectID != projectID {
			continue
		}
		if len(flt.StateGroups) > 0 && !containsGroup(flt.StateGroups, group[w.StateID]) {
			continue
		}
		if len(flt.AssigneeIDs) > 0 && !overlap(flt.AssigneeIDs, w.AssigneeIDs) {
			continue
		}
		if flt.Search != "" && !strings.Contains(strings.ToLower(w.Name), strings.ToLower(flt.Search)) {
			continue
		}
		out = append(out, w)
		if flt.Limit > 0 && len(out) == flt.Limit {
			break
		}
	}
	return out, nil
}

// WorkItemByIdentifier implements plane.API.
func (f *FakePlane) WorkItemByIdentifier(_ context.Context, key string) (plane.WorkItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, w := range f.Items {
		if strings.EqualFold(w.Key(), key) {
			return w, nil
		}
	}
	return plane.WorkItem{}, fmt.Errorf("work item %s: not found", key)
}

// CreateWorkItem implements plane.API; the new item gets the next
// sequence id and no identifier (like Plane's create response).
func (f *FakePlane) CreateWorkItem(_ context.Context, projectID string, in plane.WorkItemCreate) (plane.WorkItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return plane.WorkItem{}, f.Err
	}
	f.Created = append(f.Created, in)
	seq := len(f.Items) + 1
	w := plane.WorkItem{
		ID: "wi-" + strconv.Itoa(seq), Name: in.Name, ProjectID: projectID, SequenceID: seq,
		Priority: in.Priority, LabelIDs: in.LabelIDs, DescriptionHTML: in.DescriptionHTML, StateID: in.StateID,
	}
	f.Items = append(f.Items, w)
	return w, nil
}

// UpdateWorkItem implements plane.API.
func (f *FakePlane) UpdateWorkItem(_ context.Context, _, id string, in plane.WorkItemUpdate) (plane.WorkItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return plane.WorkItem{}, f.Err
	}
	if f.Updated == nil {
		f.Updated = map[string]plane.WorkItemUpdate{}
	}
	f.Updated[id] = in
	for i, w := range f.Items {
		if w.ID != id {
			continue
		}
		if in.Name != nil {
			w.Name = *in.Name
		}
		if in.StateID != nil {
			w.StateID = *in.StateID
		}
		if in.Priority != nil {
			w.Priority = *in.Priority
		}
		if in.DescriptionHTML != nil {
			w.DescriptionHTML = *in.DescriptionHTML
		}
		f.Items[i] = w
		return w, nil
	}
	return plane.WorkItem{}, fmt.Errorf("work item %s: not found", id)
}

// Comments implements plane.API.
func (f *FakePlane) Comments(_ context.Context, _, id string) ([]plane.Comment, error) {
	return f.CommentMap[id], nil
}

// CreateComment implements plane.API.
func (f *FakePlane) CreateComment(_ context.Context, _, id string, in plane.CommentCreate) (plane.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return plane.Comment{}, f.Err
	}
	f.Commented = append(f.Commented, in)
	return plane.Comment{ID: "c-" + strconv.Itoa(len(f.Commented)), WorkItemID: id, CommentHTML: in.CommentHTML}, nil
}

// CreatedCount returns how many items were created.
func (f *FakePlane) CreatedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Created)
}

func containsGroup(gs []plane.StateGroup, g plane.StateGroup) bool {
	for _, x := range gs {
		if x == g {
			return true
		}
	}
	return false
}

func overlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
