package notes

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
)

// at is a local time on Mon 23 Nov 2026 or the days before.
func at(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, time.Local) }

func pd(t time.Time) plane.Date { return plane.Date{Time: t} }

func row(name string, st store.Status, archived time.Time, summary string) sessions.Row {
	return sessions.Row{Session: store.Session{Name: name, Status: st, ArchivedAt: archived, Summary: summary}}
}

func TestBuild(t *testing.T) {
	mon := date(2026, 11, 23)
	started := &plane.State{Name: "In Progress", Group: plane.GroupStarted}
	blockedSt := &plane.State{Name: "Blocked", Group: plane.GroupStarted}
	todo := &plane.State{Name: "Todo", Group: plane.GroupUnstarted}
	in := Inputs{
		Day: mon,
		Merged: []github.PR{
			{Repo: "daan/forge", Number: 12, Title: "Notes", MergedAt: at(20, 15)},
			{Repo: "daan/forge", Number: 11, Title: "Too old", MergedAt: at(19, 23)},
			{Repo: "daan/forge", Number: 13, Title: "Saturday fix", MergedAt: at(21, 10)},
			{Repo: "daan/forge", Number: 14, Title: "This morning", MergedAt: at(23, 9)},
		},
		Reviewed: []github.PR{
			{Repo: "acme/ui", Number: 9, Title: "Buttons", UpdatedAt: at(20, 11)},
			{Repo: "daan/forge", Number: 12, Title: "Notes", UpdatedAt: at(20, 16)}, // also merged
		},
		Open: []github.PR{
			{Repo: "daan/forge", Number: 20, Title: "Draft thing", State: github.StateDraft, Draft: true},
			{Repo: "daan/forge", Number: 21, Title: "Red", State: github.StateOpen, CI: github.CIFailure, ReviewDecision: github.ReviewChangesRequested},
			{Repo: "daan/forge", Number: 22, Title: "Closed", State: github.StateClosed},
		},
		Done: []plane.WorkItem{
			{Identifier: "PROJ-7", Name: "Rate limiter", CompletedAt: pd(at(20, 9))},
			{Identifier: "PROJ-6", Name: "Old", CompletedAt: pd(at(18, 9)), UpdatedAt: pd(at(20, 9))},
			{Identifier: "PROJ-5", Name: "No completed date", UpdatedAt: pd(at(20, 8))},
		},
		Active: []plane.WorkItem{
			{Identifier: "PROJ-8", Name: "Webhooks", State: started},
			{Identifier: "PROJ-9", Name: "Waiting on infra", State: blockedSt},
			{Identifier: "PROJ-10", Name: "Labelled", State: todo, Labels: []plane.Label{{Name: "blocked"}}},
			{Identifier: "PROJ-11", Name: "Not started", State: todo},
		},
		Archived: []sessions.Row{
			row("fix-login", store.StatusArchived, at(20, 18),
				"PR daan/forge#12 MERGED — Notes\n12 messages · 3 tool calls\nLast replies:\n- first reply\n- Done: tests pass"),
			row("gone", store.StatusArchived, at(20, 19), ""),
			row("old", store.StatusArchived, at(10, 1), "PR x/y#1 MERGED — z"),
		},
		Live: []sessions.Row{
			row("api", store.StatusWorking, time.Time{}, ""),
			row("crash", store.StatusError, time.Time{}, ""),
			row("dead", store.StatusDead, time.Time{}, ""),
		},
		Blocked: BlockedBy(config.Plane{BlockedStates: []string{"blocked"}, BlockedLabels: []string{"Blocked"}}),
		Errs:    []error{errors.New("plane unavailable: 502\nbody")},
	}
	want := `## Standup — Mon 23 Nov

### Done since Fri 20 Nov
- Merged daan/forge#12 — Notes
- Merged daan/forge#13 — Saturday fix
- Reviewed acme/ui#9 — Buttons
- Closed PROJ-5 — No completed date
- Closed PROJ-7 — Rate limiter
- Agent fix-login — PR daan/forge#12 MERGED — Notes · Done: tests pass
- Agent gone

### Today
- In progress PROJ-8 — Webhooks
- Open PR daan/forge#20 — Draft thing (draft)
- Agent api — working

### Blockers
- PROJ-9 — Waiting on infra (Blocked)
- PROJ-10 — Labelled (Todo)
- daan/forge#21 — Red (CI failing, changes requested)
- Agent crash — error

_(plane unavailable: 502)_
`
	if got := Build(in); got != want {
		t.Errorf("Build =\n%s\nwant\n%s", got, want)
	}
}

func TestBuildEmpty(t *testing.T) {
	got := Build(Inputs{Day: date(2026, 11, 24)})
	want := "## Standup — Tue 24 Nov\n\n### Done yesterday (Mon 23 Nov)\n- nothing recorded\n\n### Today\n- nothing recorded\n"
	if got != want {
		t.Errorf("Build empty = %q", got)
	}
	if strings.Contains(got, "Blockers") {
		t.Error("empty Blockers should be left out")
	}
}

func TestSessionSummary(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"no transcript", ""},
		{"issue PROJ-7 done — Thing\nLast replies:\n- ok", " — issue PROJ-7 done — Thing · ok"},
		{"3 messages\nLast replies:\n- a\n- b", " — b"},
		{"PR a/b#1 OPEN — x\nPR a/b#2 MERGED — y", " — PR a/b#1 OPEN — x"},
	}
	for _, tt := range tests {
		if got := sessionSummary(tt.in); got != tt.want {
			t.Errorf("sessionSummary(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

type fakeSearch struct {
	queries []string
	res     map[string][]github.PR
	extra   map[string]int // matches beyond the page
	err     error
}

func (f *fakeSearch) SearchPRs(_ context.Context, q string, _ int) (github.SearchResult, error) {
	f.queries = append(f.queries, q)
	if f.err != nil {
		return github.SearchResult{}, f.err
	}
	return github.SearchResult{PRs: f.res[q], Total: len(f.res[q]) + f.extra[q]}, nil
}

type fakePlane struct {
	me      plane.User
	meErr   error
	filters map[string][]plane.WorkItemFilter
	fail    string // project id that errors
}

func (f *fakePlane) Me(context.Context) (plane.User, error) { return f.me, f.meErr }

func (f *fakePlane) WorkItems(_ context.Context, id string, flt plane.WorkItemFilter) ([]plane.WorkItem, error) {
	if f.filters == nil {
		f.filters = map[string][]plane.WorkItemFilter{}
	}
	f.filters[id] = append(f.filters[id], flt)
	if id == f.fail {
		return nil, errors.New("boom")
	}
	return []plane.WorkItem{{ID: id + "-" + string(flt.StateGroups[0])}}, nil
}

type fakeSessions struct{ live, archived []sessions.Row }

func (f fakeSessions) List(_ context.Context, project string, archived bool) ([]sessions.Row, error) {
	if project != "" {
		return nil, errors.New("want all projects")
	}
	if archived {
		return f.archived, nil
	}
	return f.live, nil
}

func TestGather(t *testing.T) {
	mon := date(2026, 11, 23)
	mq, rq, oq := Queries(mon)
	since := date(2026, 11, 20).Format(time.RFC3339) // e.g. 2026-11-20T00:00:00+07:00
	if mq != "is:pr is:merged author:@me merged:>="+since+" sort:updated-desc" ||
		rq != "is:pr reviewed-by:@me -author:@me updated:>="+since+" sort:updated-desc" ||
		oq != "is:pr is:open author:@me sort:updated-desc" {
		t.Errorf("queries = %q %q %q", mq, rq, oq)
	}
	gh := &fakeSearch{
		res:   map[string][]github.PR{mq: {{Number: 1}}, rq: {{Number: 2}}, oq: {{Number: 3}}},
		extra: map[string]int{mq: 4},
	}
	pl := &fakePlane{me: plane.User{ID: "u-me"}, fail: "p2"}
	g := Gatherer{
		GitHub: gh, Plane: pl, PlaneProjects: []string{"p1", "p2"},
		Sessions: fakeSessions{
			live:     []sessions.Row{row("a", store.StatusWorking, time.Time{}, "")},
			archived: []sessions.Row{row("b", store.StatusArchived, at(20, 1), "")},
		},
	}
	in := g.Gather(context.Background(), mon)
	if !slices.Equal(gh.queries, []string{mq, rq, oq}) {
		t.Errorf("queries sent = %q", gh.queries)
	}
	if len(in.Merged) != 1 || in.Merged[0].Number != 1 || in.Reviewed[0].Number != 2 || in.Open[0].Number != 3 {
		t.Errorf("PRs = %+v %+v %+v", in.Merged, in.Reviewed, in.Open)
	}
	done := pl.filters["p1"][0]
	if !slices.Equal(done.StateGroups, []plane.StateGroup{plane.GroupCompleted}) || !slices.Equal(done.AssigneeIDs, []string{"u-me"}) ||
		!done.UpdatedAfter.Equal(date(2026, 11, 20)) || done.Archived != plane.ArchivedInclude {
		t.Errorf("done filter = %+v", done)
	}
	open := pl.filters["p1"][1]
	if slices.Contains(open.StateGroups, plane.GroupCompleted) || !slices.Contains(open.StateGroups, plane.GroupStarted) ||
		!slices.Equal(open.Expand, []string{"state", "labels"}) || !slices.Equal(open.AssigneeIDs, []string{"u-me"}) {
		t.Errorf("open filter = %+v", open)
	}
	if len(in.Done) != 1 || in.Done[0].ID != "p1-completed" || len(in.Active) != 1 {
		t.Errorf("plane = %+v / %+v (p2 failing must not hide p1)", in.Done, in.Active)
	}
	if len(in.Live) != 1 || len(in.Archived) != 1 {
		t.Errorf("sessions = %+v / %+v", in.Live, in.Archived)
	}
	if len(in.Errs) != 2 || in.Errs[0].Error() != "github: only the 1 most recently updated of 5 merged PRs were read" ||
		!strings.Contains(in.Errs[1].Error(), "plane unavailable: boom") {
		t.Errorf("errs = %v", in.Errs)
	}
}

// TestQueriesOffset: the window start keeps its zone, so a day ahead of
// UTC isn't cut short.
func TestQueriesOffset(t *testing.T) {
	wib := time.FixedZone("WIB", 7*3600)
	mq, _, _ := Queries(time.Date(2026, 11, 23, 0, 0, 0, 0, wib))
	if !strings.Contains(mq, "merged:>=2026-11-20T00:00:00+07:00 ") {
		t.Errorf("merged query = %q", mq)
	}
}

func TestGatherFailures(t *testing.T) {
	g := Gatherer{
		GitHub: &fakeSearch{err: errors.New("no auth")},
		Plane:  &fakePlane{meErr: errors.New("401")}, PlaneProjects: []string{"p1"},
	}
	in := g.Gather(context.Background(), date(2026, 11, 23))
	if len(in.Errs) != 2 || !strings.Contains(in.Errs[0].Error(), "github unavailable") ||
		!strings.Contains(in.Errs[1].Error(), "plane unavailable: 401") {
		t.Errorf("errs = %v", in.Errs)
	}
	// Nil clients are skipped without errors.
	if in := (Gatherer{}).Gather(context.Background(), date(2026, 11, 23)); len(in.Errs) != 0 {
		t.Errorf("nil clients errs = %v", in.Errs)
	}
}

func TestPlaneProjects(t *testing.T) {
	got := PlaneProjects([]config.Project{{PlaneProjectID: "a"}, {}, {PlaneProjectID: "b"}, {PlaneProjectID: "a"}})
	if !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("PlaneProjects = %v", got)
	}
}
