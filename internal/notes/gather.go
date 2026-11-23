package notes

import (
	"context"
	"fmt"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/sessions"
)

// searchLimit caps each standup PR search: GitHub's most per request.
const searchLimit = 100

// PlaneSource is the slice of plane.API a standup needs.
type PlaneSource interface {
	Me(ctx context.Context) (plane.User, error)
	WorkItems(ctx context.Context, projectID string, f plane.WorkItemFilter) ([]plane.WorkItem, error)
}

// SessionLister lists sessions (sessions.API.List).
type SessionLister interface {
	List(ctx context.Context, project string, archived bool) ([]sessions.Row, error)
}

// Gatherer collects a standup's inputs across all projects. Any client may
// be nil; that source is then skipped silently.
type Gatherer struct {
	GitHub   github.PRSearcher
	Plane    PlaneSource
	Sessions SessionLister
	// PlaneProjects are the linked Plane project ids.
	PlaneProjects []string
	// Blocked marks open items as blocked (config.Plane.IsBlocked).
	Blocked func(plane.WorkItem) bool
}

// Queries are the PR searches for day: merged, reviewed and open. The
// window starts at local midnight of the previous workday, sent with its
// offset (GitHub reads a bare date as UTC).
func Queries(day time.Time) (merged, reviewed, open string) {
	since := PrevWorkday(day).Format(time.RFC3339)
	return github.BuildQuery("is:merged author:@me merged:>="+since, ""),
		github.BuildQuery("reviewed-by:@me -author:@me updated:>="+since, ""),
		github.BuildQuery("is:open author:@me", "")
}

// Gather loads the inputs for day. It never fails as a whole: a source
// that errors is left out and recorded in Inputs.Errs.
func (g Gatherer) Gather(ctx context.Context, day time.Time) Inputs {
	in := Inputs{Day: day, Blocked: g.Blocked}
	fail := func(src string, err error) { in.Errs = append(in.Errs, fmt.Errorf("%s unavailable: %w", src, err)) }

	if g.GitHub != nil {
		mq, rq, oq := Queries(day)
		for _, s := range []struct {
			what string
			q    string
			out  *[]github.PR
		}{{"merged", mq, &in.Merged}, {"reviewed", rq, &in.Reviewed}, {"open", oq, &in.Open}} {
			res, err := g.GitHub.SearchPRs(ctx, s.q, searchLimit)
			if err != nil {
				fail("github", err)
				break
			}
			*s.out = res.PRs
			if res.Total > len(res.PRs) {
				in.Errs = append(in.Errs, fmt.Errorf("github: only the %d most recently updated of %d %s PRs were read",
					len(res.PRs), res.Total, s.what))
			}
		}
	}

	if g.Plane != nil && len(g.PlaneProjects) > 0 {
		if err := g.gatherPlane(ctx, day, &in); err != nil {
			fail("plane", err)
		}
	}

	if g.Sessions != nil {
		var err error
		if in.Archived, err = g.Sessions.List(ctx, "", true); err != nil {
			fail("sessions", err)
		} else if in.Live, err = g.Sessions.List(ctx, "", false); err != nil {
			fail("sessions", err)
		}
	}
	return in
}

// gatherPlane lists my completed items since the previous workday and my
// open items, in every linked project. A failing project doesn't hide the
// others; the first error is returned.
func (g Gatherer) gatherPlane(ctx context.Context, day time.Time, in *Inputs) error {
	var me plane.User
	if cr, ok := g.Plane.(plane.CacheReader); ok {
		me, _ = cr.CachedMe(ctx)
	}
	if me.ID == "" {
		u, err := g.Plane.Me(ctx)
		if err != nil {
			return err
		}
		me = u
	}
	var first error
	for _, id := range g.PlaneProjects {
		done, err := g.Plane.WorkItems(ctx, id, plane.WorkItemFilter{
			StateGroups:  []plane.StateGroup{plane.GroupCompleted},
			AssigneeIDs:  []string{me.ID},
			UpdatedAfter: PrevWorkday(day),
			Archived:     plane.ArchivedInclude,
		})
		if err == nil {
			in.Done = append(in.Done, done...)
			var open []plane.WorkItem
			open, err = g.Plane.WorkItems(ctx, id, plane.WorkItemFilter{
				StateGroups: []plane.StateGroup{plane.GroupTriage, plane.GroupBacklog, plane.GroupUnstarted, plane.GroupStarted},
				AssigneeIDs: []string{me.ID},
				Expand:      []string{"state", "labels"},
			})
			in.Active = append(in.Active, open...)
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

// BlockedBy marks an item blocked by its state or label names, per the
// Plane config (blocked_states / blocked_labels).
func BlockedBy(p config.Plane) func(plane.WorkItem) bool {
	return func(w plane.WorkItem) bool {
		state := ""
		if w.State != nil {
			state = w.State.Name
		}
		labels := make([]string, 0, len(w.Labels))
		for _, l := range w.Labels {
			labels = append(labels, l.Name)
		}
		return p.IsBlocked(labels, state)
	}
}

// PlaneProjects are the distinct linked Plane project ids of projects.
func PlaneProjects(projects []config.Project) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range projects {
		if p.PlaneProjectID != "" && !seen[p.PlaneProjectID] {
			seen[p.PlaneProjectID] = true
			out = append(out, p.PlaneProjectID)
		}
	}
	return out
}
