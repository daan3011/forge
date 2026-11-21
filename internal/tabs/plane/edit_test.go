package plane

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/store"
)

// CreateWorkItem adds the item to the project. Like Plane's response it
// carries ids, not expansions (and here no identifier, only the sequence).
func (f *fakePlane) CreateWorkItem(_ context.Context, project string, in plane.WorkItemCreate) (plane.WorkItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates = append(f.creates, in)
	if f.writeErr != nil {
		return plane.WorkItem{}, f.writeErr
	}
	seq := 0
	for _, w := range f.items[project] {
		seq = max(seq, w.SequenceID)
	}
	seq++
	w := plane.WorkItem{
		ID: fmt.Sprintf("w-new-%d", seq), Name: in.Name, SequenceID: seq, ProjectID: project,
		StateID: in.StateID, Priority: in.Priority, AssigneeIDs: in.AssigneeIDs, LabelIDs: in.LabelIDs,
		CycleID: in.CycleID, DescriptionHTML: in.DescriptionHTML,
		CreatedAt: plane.Date{Time: now}, UpdatedAt: plane.Date{Time: now},
	}
	listed := w
	listed.Identifier = fmt.Sprintf("FORGE-%d", seq)
	for _, st := range f.statesFor(project) {
		if st.ID == in.StateID {
			s := st
			listed.State = &s
		}
	}
	f.items[project] = append(f.items[project], listed)
	return w, nil
}

func (f *fakePlane) CreateComment(_ context.Context, _, id string, in plane.CommentCreate) (plane.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posted = append(f.posted, in)
	if f.writeErr != nil {
		return plane.Comment{}, f.writeErr
	}
	c := plane.Comment{ID: fmt.Sprintf("c-new-%d", len(f.posted)), CommentHTML: in.CommentHTML, CreatedAt: plane.Date{Time: now}}
	f.comments[id] = append(f.comments[id], c)
	return c, nil
}

func (f *fakePlane) writes() ([]plane.WorkItemCreate, []plane.WorkItemUpdate, []plane.CommentCreate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.creates), slices.Clone(f.bodies), slices.Clone(f.posted)
}

// typeText types s into the focused field.
func (h *harness) typeText(s string) {
	for _, r := range s {
		_, cmd := h.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		h.drain(cmd)
	}
}

// sendAll is send, also returning info toasts.
func (h *harness) sendAll(keys ...string) (errs, infos []string) {
	for _, k := range keys {
		_, cmd := h.Update(press(k))
		e, i := h.drain(cmd)
		errs, infos = append(errs, e...), append(infos, i...)
	}
	return errs, infos
}

func TestCreateIssue(t *testing.T) {
	for _, board := range []bool{false, true} {
		t.Run(fmt.Sprintf("board=%v", board), func(t *testing.T) {
			api := samplePlane()
			h := newHarness(api, nil, "app")
			h.init()
			if board {
				h.drain(h.toggleBoard())
			}
			h.send("n")
			if h.dialog == nil || !h.CapturingInput() {
				t.Fatal("no form")
			}
			mustContain(t, h.view(), "New issue", "State", "Priority", "Labels", "bug", "ui", "Assignee", "Cycle")
			h.typeText("Add forms")
			// title → state (Todo) → priority (none) → labels → assignee:
			// Unassigned, daan (me), bob → cycle → description → submit.
			errs := h.send("enter", "enter", "enter", "x", "enter", "down", "enter", "enter")
			mustContain(t, h.view(), "New issue · description", "Markdown · ctrl+e opens $EDITOR")
			h.typeText("Do **it**")
			errs = append(errs, h.send("enter")...)
			if len(errs) > 0 || h.dialog != nil {
				t.Fatalf("errs %v, form open %v:\n%s", errs, h.dialog != nil, h.view())
			}
			creates, _, _ := api.writes()
			if len(creates) != 1 {
				t.Fatalf("creates = %+v", creates)
			}
			c := creates[0]
			if c.Name != "Add forms" || c.StateID != stTodo.ID || c.Priority != plane.PriorityNone ||
				!slices.Equal(c.LabelIDs, []string{lbBug.ID}) || !slices.Equal(c.AssigneeIDs, []string{me.ID}) ||
				c.CycleID != "" || c.DescriptionHTML != "<p>Do <strong>it</strong></p>" {
				t.Errorf("create = %+v", c)
			}
			if !slices.Equal(h.successes, []string{"created FORGE-5"}) {
				t.Errorf("toasts = %v", h.successes)
			}
			if w, ok := h.selected(); !ok || w.Key() != "FORGE-5" {
				t.Errorf("selected %v %v", w.Key(), ok)
			}
		})
	}
}

func TestCreateIssueValidation(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "app")
	h.init()
	h.send("n")
	h.typeText("   ")
	h.send("enter")
	if h.dialog == nil {
		t.Fatal("blank title accepted")
	}
	mustContain(t, h.view(), "a title is required")
	if creates, _, _ := api.writes(); len(creates) > 0 {
		t.Errorf("created %+v", creates)
	}
	h.send("esc")
	if h.dialog != nil {
		t.Error("esc didn't close the form")
	}
}

func TestCreateHiddenByFilters(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "app") // my open items
	h.init()
	h.send("n")
	h.typeText("Nobody's")
	_, infos := h.sendAll("enter", "enter", "enter", "enter", "enter", "enter", "enter")
	if h.dialog != nil {
		t.Fatalf("form open:\n%s", h.view())
	}
	if !slices.Equal(h.successes, []string{"created FORGE-5"}) || !slices.Contains(infos, "FORGE-5 is hidden by the current filters") {
		t.Errorf("toasts %v, infos %v", h.successes, infos)
	}
	if creates, _, _ := api.writes(); len(creates) != 1 || creates[0].AssigneeIDs != nil || creates[0].DescriptionHTML != "" {
		t.Errorf("creates = %+v", creates)
	}
}

func TestCreateAsksProject(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "") // every project
	h.init()
	h.send("n")
	mustContain(t, h.view(), "New issue in", "app", "lib")
	h.send("down", "enter") // lib
	mustContain(t, h.view(), "Title")
	h.typeText("In lib")
	h.send("enter", "enter", "enter", "enter", "enter", "enter", "enter")
	if creates, _, _ := api.writes(); len(creates) != 1 || creates[0].Name != "In lib" || len(api.items[p2]) != 2 {
		t.Errorf("creates = %+v", creates)
	}
}

func TestCreateFails(t *testing.T) {
	api := samplePlane()
	api.writeErr = &plane.APIError{Status: 400, Title: "name is bad"}
	h := newHarness(api, nil, "app")
	h.init()
	h.send("n")
	h.typeText("x")
	errs := h.send("enter", "enter", "enter", "enter", "enter", "enter", "enter")
	if len(errs) != 1 || !strings.Contains(errs[0], "couldn't create") || len(h.successes) > 0 {
		t.Errorf("errs %v, toasts %v", errs, h.successes)
	}
}

// editTo opens the edit form for FORGE-1 and walks to the description.
func openEdit(t *testing.T, h *harness) {
	t.Helper()
	h.send("e")
	if h.dialog == nil {
		t.Fatal("no edit form")
	}
	mustContain(t, h.view(), "Edit FORGE-1")
}

func TestEditOnlySendsChanges(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "app")
	h.init()
	openEdit(t, h)
	mustContain(t, h.view(), "Project switcher", "In Progress", "[•] ui", "daan (me)", "Sprint 12")
	h.typeText("!")
	// title → state → priority → labels → assignee → cycle → description.
	h.send("enter", "enter", "enter", "enter", "enter", "enter")
	mustContain(t, h.view(), "Edit FORGE-1 · description", "## Goal", "- fuzzy")
	h.send("enter")
	if h.dialog != nil {
		t.Fatalf("form open:\n%s", h.view())
	}
	_, bodies, _ := api.writes()
	if len(bodies) != 1 {
		t.Fatalf("bodies = %+v", bodies)
	}
	u := bodies[0]
	if u.Name == nil || *u.Name != "Project switcher!" {
		t.Errorf("name = %v", u.Name)
	}
	if u.DescriptionHTML != nil || u.StateID != nil || u.Priority != nil || u.LabelIDs != nil || u.AssigneeIDs != nil || u.CycleID != nil {
		t.Errorf("unchanged fields sent: %+v", u)
	}
	if !slices.Equal(h.successes, []string{"updated FORGE-1"}) {
		t.Errorf("toasts = %v", h.successes)
	}
}

func TestEditDescription(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "app")
	h.init()
	openEdit(t, h)
	// Priority high → medium; cycle Sprint 12 → none; the description gets
	// a line.
	h.send("enter", "enter", "down", "enter", "enter", "enter", "up", "enter")
	h.typeText(" more")
	h.send("enter")
	_, bodies, _ := api.writes()
	if len(bodies) != 1 {
		t.Fatalf("bodies = %+v", bodies)
	}
	u := bodies[0]
	if u.Priority == nil || *u.Priority != plane.PriorityMedium || u.CycleID == nil || *u.CycleID != "" || u.Name != nil {
		t.Errorf("update = %+v", u)
	}
	if u.DescriptionHTML == nil || !strings.Contains(*u.DescriptionHTML, "<h2>Goal</h2>") || !strings.Contains(*u.DescriptionHTML, "fuzzy more") {
		t.Errorf("description = %v", u.DescriptionHTML)
	}
}

func TestEditNoChanges(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "app")
	h.init()
	openEdit(t, h)
	_, infos := h.sendAll("enter", "enter", "enter", "enter", "enter", "enter", "enter")
	if _, bodies, _ := api.writes(); len(bodies) > 0 {
		t.Errorf("sent %+v", bodies)
	}
	if !slices.Contains(infos, "FORGE-1: no changes") {
		t.Errorf("infos = %v", infos)
	}
}

func TestEditLossyDescription(t *testing.T) {
	api := samplePlane()
	api.desc["w-FORGE-1"] = `<p>ask <mention-component entity_identifier="u-bob" entity_name="user_mention"></mention-component></p><image-component src="a1"></image-component>`
	h := newHarness(api, nil, "app")
	h.init()
	openEdit(t, h)
	mustContain(t, h.view(), "can't round-trip", "mentions, images")
	if !strings.Contains(h.dialog.GetFocusedField().GetKey(), "title") {
		t.Fatalf("focus on %q, want the title", h.dialog.GetFocusedField().GetKey())
	}
	// Change the title only: the description must not be rewritten.
	h.typeText("!")
	h.send("enter", "enter", "enter", "enter", "enter", "enter", "enter")
	_, bodies, _ := api.writes()
	if len(bodies) != 1 || bodies[0].DescriptionHTML != nil {
		t.Fatalf("bodies = %+v", bodies)
	}

	// Editing the description keeps the mention and the image.
	openEdit(t, h)
	h.send("enter", "enter", "enter", "enter", "enter", "enter")
	h.typeText(" ok")
	h.send("enter")
	_, bodies, _ = api.writes()
	if len(bodies) != 2 || bodies[1].DescriptionHTML == nil {
		t.Fatalf("bodies = %+v", bodies)
	}
	d := *bodies[1].DescriptionHTML
	for _, want := range []string{`<mention-component entity_identifier="u-bob" entity_name="user_mention"></mention-component>`, `<image-component src="a1"></image-component>`, "ok"} {
		if !strings.Contains(d, want) {
			t.Errorf("lost %q: %s", want, d)
		}
	}
}

func TestComment(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "app")
	h.init()
	h.send("c")
	if h.dialog == nil {
		t.Fatal("no comment form")
	}
	mustContain(t, h.view(), "Comment on FORGE-1")
	h.send("enter")
	if h.dialog == nil {
		t.Fatal("blank comment accepted")
	}
	h.typeText("looks *good*")
	h.send("enter")
	if h.dialog != nil {
		t.Fatalf("form open:\n%s", h.view())
	}
	_, _, posted := api.writes()
	if len(posted) != 1 || posted[0].CommentHTML != "<p>looks <em>good</em></p>" {
		t.Errorf("posted = %+v", posted)
	}
	if !slices.Equal(h.successes, []string{"commented on FORGE-1"}) {
		t.Errorf("toasts = %v", h.successes)
	}
	mustContain(t, h.view(), "looks good")
}

func TestStartSession(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "app")
	h.init()
	h.send("s")
	if len(h.started) != 1 {
		t.Fatalf("started = %+v", h.started)
	}
	src := h.started[0]
	if src.Kind != store.KindPlaneIssue || src.ID != "FORGE-1" || src.Title != "Project switcher" ||
		src.Repo != "acme/app" || src.State != "In Progress" || src.URL == "" || src.Template != "" {
		t.Errorf("source = %+v", src)
	}
}

func TestItemKeysInBoardOverlay(t *testing.T) {
	api := samplePlane()
	h := newHarness(api, nil, "app")
	h.init()
	h.drain(h.toggleBoard())
	h.send("enter")
	if !h.overlay {
		t.Fatal("no overlay")
	}
	h.send("s")
	if len(h.started) != 1 {
		t.Errorf("started = %+v", h.started)
	}
	h.send("c")
	if h.dialog == nil {
		t.Error("no comment form from the overlay")
	}
}
