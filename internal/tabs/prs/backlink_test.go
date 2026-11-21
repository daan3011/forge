package prs

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/plane"
)

// fakePlane answers what back-linking needs: projects, item lookup and
// comments.
type fakePlane struct {
	plane.API
	mu          sync.Mutex
	projectsErr error
	projects    int               // Projects calls
	comments    map[string]string // item id -> comment HTML
}

func (f *fakePlane) Projects(context.Context) ([]plane.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects++
	if f.projectsErr != nil {
		return nil, f.projectsErr
	}
	return []plane.Project{{ID: "p1", Identifier: "PROJ"}, {ID: "p2", Identifier: "WEB"}}, nil
}

func (f *fakePlane) WorkItemByIdentifier(_ context.Context, key string) (plane.WorkItem, error) {
	return plane.WorkItem{ID: "id-" + key, ProjectID: "p", Identifier: key}, nil
}

func (f *fakePlane) CreateComment(_ context.Context, _, id string, in plane.CommentCreate) (plane.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.comments == nil {
		f.comments = map[string]string{}
	}
	if _, dup := f.comments[id]; dup {
		return plane.Comment{}, errors.New("commented twice on " + id)
	}
	f.comments[id] = in.CommentHTML
	return plane.Comment{}, nil
}

func (f *fakePlane) commentedOn() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for id := range f.comments {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// backlinkGH names issues in branches and titles: #1 by branch (open),
// #2 by branch (merged), #9 by title (draft).
func backlinkGH() *fakeGH {
	gh := detailGH()
	r := gh.results["author:@me"]
	r.PRs[0].HeadRef = "proj-12-fix-login"
	r.PRs[1].HeadRef, r.PRs[1].State = "web-5-old", github.StateMerged
	inv := gh.results["involves:@me"]
	inv.PRs[0].Title = "fix: viewport (web-3)"
	return gh
}

func backlinkTab(t *testing.T, pl *fakePlane, comment bool) (*Tab, *fakeIssueLinks) {
	t.Helper()
	links := &fakeIssueLinks{}
	d := testDeps(backlinkGH(), newKV())
	d.Sessions = fakeSessions{links: links}
	d.Plane = pl
	d.Config.Plane.CommentOnPRLink = comment
	tb := newTab(d)
	tb.now = func() time.Time { return now }
	tb.tick = immediateTick
	tb.SetSize(120, 40)
	return tb, links
}

func TestBacklinkLinksPRsToIssues(t *testing.T) {
	pl := &fakePlane{}
	tb, links := backlinkTab(t, pl, false)
	if errs := drain(tb, tb.Init()); len(errs) > 0 {
		t.Fatal(errs)
	}
	var keys []string
	for k := range links.known {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	want := []string{"PROJ-12>acme/bubbletea#1", "WEB-3>acme/bubbles#9", "WEB-5>acme/bubbles#2"}
	if !slices.Equal(keys, want) {
		t.Errorf("links = %v, want %v", keys, want)
	}
	if got := links.known["PROJ-12>acme/bubbletea#1"]; got.Meta.Title != "feat(examples): tree" || got.Meta.State != github.StateOpen {
		t.Errorf("meta = %+v", got.Meta)
	}
	if len(pl.comments) != 0 {
		t.Errorf("commented with the option off: %v", pl.comments)
	}
	// The detail pane of #1 shows the item.
	mustContain(t, view(tb), "Issues", "◆ PROJ-12")
	send(tb, "j", "j") // #3 names no item
	v := view(tb)
	mustLack(t, v, "◆ PROJ-12")
	mustContain(t, v, "Issues")

	// Identifiers are asked for once.
	drain(tb, tb.Refresh())
	if pl.projects != 1 {
		t.Errorf("Projects called %d times", pl.projects)
	}
}

func TestBacklinkComments(t *testing.T) {
	pl := &fakePlane{}
	tb, _ := backlinkTab(t, pl, true)
	if errs := drain(tb, tb.Init()); len(errs) > 0 {
		t.Fatal(errs)
	}
	// Open and draft PRs get one comment; the merged one none.
	if got, want := pl.commentedOn(), []string{"id-PROJ-12", "id-WEB-3"}; !slices.Equal(got, want) {
		t.Errorf("commented on %v, want %v", got, want)
	}
	c := pl.comments["id-PROJ-12"]
	if !strings.Contains(c, `href="https://github.com/acme/bubbletea/pull/1"`) || !strings.Contains(c, "feat(examples): tree") {
		t.Errorf("comment = %q", c)
	}
	// Later refreshes find nothing new to comment on.
	if errs := drain(tb, tb.Refresh()); len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(pl.comments) != 2 {
		t.Errorf("comments = %v", pl.comments)
	}
}

func TestBacklinkErrorToastedOnce(t *testing.T) {
	pl := &fakePlane{projectsErr: errors.New("401 unauthorized")}
	tb, links := backlinkTab(t, pl, true)
	errs := drain(tb, tb.Init())
	errs = append(errs, drain(tb, tb.Refresh())...)
	if len(errs) != 1 || !strings.Contains(errs[0], "401 unauthorized") {
		t.Errorf("errs = %v", errs)
	}
	if links.calls != 0 {
		t.Errorf("linked without identifiers")
	}
}

func TestBacklinkWithoutPlane(t *testing.T) {
	links := &fakeIssueLinks{}
	d := testDeps(backlinkGH(), newKV())
	d.Sessions = fakeSessions{links: links}
	tb := newTab(d)
	tb.tick = immediateTick
	if errs := drain(tb, tb.Init()); len(errs) > 0 {
		t.Fatal(errs)
	}
	if links.calls != 0 {
		t.Error("linked without a Plane client")
	}
}
