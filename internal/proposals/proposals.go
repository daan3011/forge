// Package proposals turns agent-proposed issues (P6.5) into Plane work
// items. The Home tab's accept action and forge mcp's auto-create share it,
// so both attribute the item the same way: a footer naming the session,
// its branch and PRs, and links session → issue, proposal → issue and
// issue → PR.
package proposals

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/projects"
	"github.com/daan3011/forge/internal/store"
)

// ErrNoPlaneProject is returned when a forge project has no Plane project.
var ErrNoPlaneProject = errors.New("no Plane project")

// Deps are what accepting needs.
type Deps struct {
	Store *store.Store
	Plane plane.API
	// Projects is the resolved forge project list (projects.Resolve).
	Projects []config.Project
}

// PlaneProject finds the Plane project of the forge project called name:
// its saved link, else an automatic match, which is saved like the Plane
// tab does.
func PlaneProject(ctx context.Context, d Deps, name string) (plane.Project, error) {
	if d.Plane == nil {
		return plane.Project{}, errors.New("plane is not configured")
	}
	p, ok := projects.ByName(d.Projects, name)
	if !ok {
		return plane.Project{}, fmt.Errorf("unknown forge project %q", name)
	}
	id, src := projects.PlaneLinkOf(p, nil)
	switch src {
	case projects.PlaneLinked:
		return d.Plane.Project(ctx, id)
	case projects.PlaneNone:
		return plane.Project{}, fmt.Errorf("%s: %w (set to none)", name, ErrNoPlaneProject)
	}
	ps, err := d.Plane.Projects(ctx)
	if err != nil {
		return plane.Project{}, err
	}
	id, src = projects.PlaneLinkOf(p, ps)
	if src != projects.PlaneMatched {
		return plane.Project{}, fmt.Errorf("%s: %w linked; link one in the Plane tab", name, ErrNoPlaneProject)
	}
	if p.Repo != "" && d.Store != nil {
		_ = d.Store.Projects().SetPlane(ctx, p.Repo, id) // best effort, like forge plane ls
	}
	for _, pp := range ps {
		if pp.ID == id {
			return pp, nil
		}
	}
	return plane.Project{ID: id}, nil
}

// Attribution is who filed an issue: the session and its PRs.
type Attribution struct {
	Session store.Session // zero when the session is gone
	PRs     []store.Link  // the session's github_pr links
}

// For loads the attribution of session id (0 or deleted: by name only).
func For(ctx context.Context, st *store.Store, id int64, name string) (Attribution, error) {
	a := Attribution{Session: store.Session{Name: name}}
	if id == 0 {
		return a, nil
	}
	x, err := st.Sessions().Get(ctx, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return a, nil
	case err != nil:
		return a, err
	}
	a.Session = x
	links, err := st.Links().For(ctx, store.SessionRef(id))
	if err != nil {
		return a, err
	}
	for _, l := range links {
		if l.Other.Kind == store.KindGitHubPR {
			a.PRs = append(a.PRs, l)
		}
	}
	return a, nil
}

// Footer is the attribution line appended to an agent-filed description:
// "Filed by forge session fix-ci · branch forge/fix-ci · PR o/r#12".
func (a Attribution) Footer() string {
	parts := []string{"Filed by forge session " + a.Session.Name}
	if a.Session.Branch != "" {
		parts = append(parts, "branch "+a.Session.Branch)
	}
	for _, pr := range a.PRs {
		s := "PR " + pr.Other.ID
		if pr.URL != "" {
			s = "[PR " + pr.Other.ID + "](" + pr.URL + ")"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " · ")
}

// WithFooter returns markdown with the attribution footer appended.
func (a Attribution) WithFooter(markdown string) string {
	md := strings.TrimRight(markdown, "\n")
	if md != "" {
		md += "\n\n"
	}
	return md + "---\n\n_" + a.Footer() + "_\n"
}

// Accepted is the outcome of Accept.
type Accepted struct {
	Item plane.WorkItem
	Key  string
	// UnknownLabels are proposal labels the Plane project doesn't have;
	// they were left off.
	UnknownLabels []string
}

// Accept creates proposal id in Plane and marks it accepted. It refuses a
// proposal that isn't proposed any more, so a second accept never creates
// a duplicate from the same row.
func Accept(ctx context.Context, d Deps, id int64) (Accepted, error) {
	p, err := d.Store.Proposals().Get(ctx, id)
	if err != nil {
		return Accepted{}, err
	}
	if p.Status != store.ProposalProposed {
		return Accepted{}, fmt.Errorf("%s is already %s: %w", p.Handle(), p.Status, store.ErrConflict)
	}
	pp, err := PlaneProject(ctx, d, p.Project)
	if err != nil {
		return Accepted{}, err
	}
	if pp.ID != p.PlaneProjectID {
		if err := d.Store.Proposals().SetPlaneProject(ctx, p.ID, pp.ID); err != nil {
			return Accepted{}, err
		}
	}
	a, err := For(ctx, d.Store, p.SessionID, p.SessionName)
	if err != nil {
		return Accepted{}, err
	}
	html, err := plane.MarkdownToHTML(a.WithFooter(p.Description))
	if err != nil {
		return Accepted{}, err
	}
	in := plane.WorkItemCreate{Name: p.Title, DescriptionHTML: html, Priority: plane.Priority(p.Priority)}
	var unknown []string
	if len(p.Labels) > 0 {
		in.LabelIDs, unknown, err = labelIDs(ctx, d.Plane, pp.ID, p.Labels)
		if err != nil {
			return Accepted{}, err
		}
	}
	w, err := d.Plane.CreateWorkItem(ctx, pp.ID, in)
	if err != nil {
		return Accepted{}, err
	}
	if w.Identifier == "" && pp.Identifier != "" && w.SequenceID > 0 {
		w.Identifier = pp.Identifier + "-" + strconv.Itoa(w.SequenceID)
	}
	key := w.Key()
	if err := d.Store.Proposals().Accept(ctx, p.ID, w.ID, key); err != nil {
		return Accepted{}, fmt.Errorf("created %s but couldn't mark %s accepted: %w", key, p.Handle(), err)
	}
	if err := link(ctx, d.Store, p, a, key, w.Name); err != nil {
		return Accepted{Item: w, Key: key, UnknownLabels: unknown}, fmt.Errorf("created %s; link: %w", key, err)
	}
	return Accepted{Item: w, Key: key, UnknownLabels: unknown}, nil
}

// link records session → issue, proposal → issue and issue → PR.
func link(ctx context.Context, st *store.Store, p store.Proposal, a Attribution, key, title string) error {
	issue := store.IssueRef(key)
	meta := store.LinkMeta{Title: title}
	var errs []error
	errs = append(errs, st.Links().Add(ctx, p.Ref(), issue, meta))
	if a.Session.ID != 0 {
		errs = append(errs, st.Links().Add(ctx, store.SessionRef(a.Session.ID), issue, meta))
	}
	for _, pr := range a.PRs {
		errs = append(errs, st.Links().Add(ctx, issue, pr.Other, pr.LinkMeta))
	}
	return errors.Join(errs...)
}

// labelIDs maps label names (case-insensitive) to the project's label ids.
func labelIDs(ctx context.Context, c plane.API, projectID string, names []string) (ids, unknown []string, err error) {
	ls, err := c.Labels(ctx, projectID)
	if err != nil {
		return nil, nil, err
	}
	for _, n := range names {
		found := false
		for _, l := range ls {
			if strings.EqualFold(l.Name, n) {
				ids, found = append(ids, l.ID), true
				break
			}
		}
		if !found {
			unknown = append(unknown, n)
		}
	}
	return ids, unknown, nil
}
