package capture

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in     string
		title  string
		tokens []Token
	}{
		{"", "", nil},
		{"fix login", "fix login", nil},
		{"fix  login #forge !high", "fix login", []Token{
			{Project, "forge", 11, 17}, {Priority, "high", 18, 23},
		}},
		{"@me +bug write tests", "write tests", []Token{{Person, "me", 0, 3}, {Label, "bug", 4, 8}}},
		{"C# a+b # ! lone", "C# a+b # ! lone", nil},
		{`ship \#1 now`, "ship #1 now", nil},
		{"tabs\tand\nnewlines +x", "tabs and newlines", []Token{{Label, "x", 18, 20}}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			d := Parse(tt.in)
			if d.Title != tt.title {
				t.Errorf("title = %q, want %q", d.Title, tt.title)
			}
			if !slices.Equal(d.Tokens, tt.tokens) {
				t.Errorf("tokens = %+v, want %+v", d.Tokens, tt.tokens)
			}
		})
	}
}

const (
	forgeID = "p-forge"
	anvilID = "p-anvil"
)

func testCatalog() Catalog {
	return Catalog{
		Projects: []plane.Project{
			{ID: forgeID, Identifier: "FORGE", Name: "Forge"},
			{ID: anvilID, Identifier: "ANV", Name: "Anvil Tools"},
		},
		Aliases: map[string][]string{forgeID: {"forge-cli"}},
		Details: map[string]Details{
			forgeID: {
				Labels: []plane.Label{{ID: "l-bug", Name: "bug"}, {ID: "l-fe", Name: "frontend"}, {ID: "l-nd", Name: "Needs Design"}},
				Members: []plane.Member{
					{MemberID: "u-me", Member: &plane.User{ID: "u-me", DisplayName: "daan"}},
					{MemberID: "u-ann", Member: &plane.User{ID: "u-ann", DisplayName: "annie", FirstName: "Ann", LastName: "Lee", Email: "ann.lee@example.com"}},
					{MemberID: "u-bot", Member: &plane.User{ID: "u-bot", DisplayName: "7777a17b-c5ef-46d5-9af0-c07e6922a803-intake"}},
				},
			},
		},
		Me: plane.User{ID: "u-me", DisplayName: "daan"},
	}
}

func TestResolve(t *testing.T) {
	cat := testCatalog()
	tests := []struct {
		name    string
		in      string
		def     string
		project string
		want    plane.WorkItemCreate
		err     string
	}{
		{name: "plain title", in: "fix login", def: forgeID, project: forgeID, want: plane.WorkItemCreate{Name: "fix login"}},
		{
			name: "every token", in: "fix login #FORGE !high @me @annie +bug +Needs-Design", def: anvilID, project: forgeID,
			want: plane.WorkItemCreate{
				Name: "fix login", Priority: plane.PriorityHigh,
				AssigneeIDs: []string{"u-me", "u-ann"}, LabelIDs: []string{"l-bug", "l-nd"},
			},
		},
		{name: "priority prefix", in: "x !u", def: forgeID, project: forgeID, want: plane.WorkItemCreate{Name: "x", Priority: plane.PriorityUrgent}},
		{name: "project by name", in: "x #anvil-tools", project: anvilID, want: plane.WorkItemCreate{Name: "x"}},
		{name: "project by forge alias", in: "x #forge-cli", project: forgeID, want: plane.WorkItemCreate{Name: "x"}},
		{name: "person by first name and email", in: "x @ann @ann.lee", def: forgeID, project: forgeID, want: plane.WorkItemCreate{Name: "x", AssigneeIDs: []string{"u-ann"}}},
		{name: "duplicates once", in: "x +bug +BUG", def: forgeID, project: forgeID, want: plane.WorkItemCreate{Name: "x", LabelIDs: []string{"l-bug"}}},
		{name: "empty title", in: "#forge !high", def: forgeID, err: "a title is required"},
		{name: "long title", in: strings.Repeat("é", MaxTitle+1), def: forgeID, err: "at most 255 characters"},
		{name: "no project", in: "x", err: ErrNoProject.Error()},
		{name: "unknown label", in: "x +frontnd", def: forgeID, err: `no label "frontnd" — did you mean frontend?`},
		{name: "unknown label prefix", in: "x +front", def: forgeID, err: `no label "front" — did you mean frontend?`},
		{name: "unknown label far", in: "x +zzzzzz", def: forgeID, err: `no label "zzzzzz"`},
		{name: "unknown project", in: "x #frge", def: anvilID, err: `no project "frge" — did you mean FORGE?`},
		{name: "unknown person", in: "x @anny", def: forgeID, err: `no person "anny" — did you mean annie?`},
		{name: "unknown priority", in: "x !hgih", def: forgeID, err: `no priority "hgih" — did you mean high?`},
		{name: "details not loaded", in: "x +bug #ANV", err: "still loading labels and members…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := cat.Resolve(Parse(tt.in), tt.def)
			if tt.err != "" {
				if err == nil || err.Error() != tt.err {
					t.Fatalf("err = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.ProjectID != tt.project {
				t.Errorf("project = %q, want %q", r.ProjectID, tt.project)
			}
			if r.Create.Name != tt.want.Name || r.Create.Priority != tt.want.Priority ||
				!slices.Equal(r.Create.AssigneeIDs, tt.want.AssigneeIDs) || !slices.Equal(r.Create.LabelIDs, tt.want.LabelIDs) {
				t.Errorf("create = %+v, want %+v", r.Create, tt.want)
			}
		})
	}
}

func TestUnknownErrorFix(t *testing.T) {
	in := "x +frontnd !high"
	_, err := testCatalog().Resolve(Parse(in), forgeID)
	var ue *UnknownError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *UnknownError", err)
	}
	start, end, text, ok := ue.Fix()
	if !ok {
		t.Fatal("no fix")
	}
	if got := in[:start] + text + in[end:]; got != "x +frontend !high" {
		t.Errorf("fixed = %q", got)
	}
}

func TestComplete(t *testing.T) {
	cat := testCatalog()
	tests := []struct {
		name   string
		in     string
		cursor int // -1: end
		def    string
		want   []string
		start  int
		ok     bool
	}{
		{name: "label prefix", in: "x +f", cursor: -1, def: forgeID, want: []string{"frontend"}, start: 2, ok: true},
		{name: "label all, by name", in: "x +", cursor: -1, def: forgeID, want: []string{"bug", "frontend", "Needs-Design"}, start: 2, ok: true},
		{name: "label contains", in: "x +design", cursor: -1, def: forgeID, want: []string{"Needs-Design"}, start: 2, ok: true},
		{name: "me first", in: "@", cursor: -1, def: forgeID, want: []string{"me", "annie"}, ok: true},
		{name: "person mid-line", in: "x @ y", cursor: 3, def: forgeID, want: []string{"me", "annie"}, start: 2, ok: true},
		{name: "person prefix", in: "x @a", cursor: -1, def: forgeID, want: []string{"annie"}, start: 2, ok: true},
		{name: "me", in: "x @m", cursor: -1, def: forgeID, want: []string{"me"}, start: 2, ok: true},
		{name: "project default first", in: "x #", cursor: -1, def: anvilID, want: []string{"ANV", "FORGE", "forge-cli"}, start: 2, ok: true},
		{name: "project", in: "x #a", cursor: -1, def: forgeID, want: []string{"ANV"}, start: 2, ok: true},
		{name: "project alias", in: "x #forge-", cursor: -1, def: anvilID, want: []string{"forge-cli"}, start: 2, ok: true},
		{name: "priority", in: "!h x", cursor: 2, want: []string{"high"}, start: 0, ok: true},
		{name: "label in the line's project", in: "#ANV x +b", cursor: -1, def: forgeID, want: nil, start: 7, ok: true},
		{name: "not a token", in: "fix", cursor: -1, ok: false},
		{name: "before the sigil", in: "x +bug", cursor: 2, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cur := tt.cursor
			if cur < 0 {
				cur = len(tt.in)
			}
			c, ok := cat.Complete(tt.in, cur, tt.def)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v (%+v)", ok, tt.ok, c)
			}
			if !ok {
				return
			}
			if c.Start != tt.start {
				t.Errorf("start = %d, want %d", c.Start, tt.start)
			}
			if !slices.Equal(c.Candidates, tt.want) {
				t.Errorf("candidates = %q, want %q", c.Candidates, tt.want)
			}
		})
	}
}

func TestCandidates(t *testing.T) {
	cat := testCatalog()
	if got, want := cat.Candidates(Person, forgeID), []string{"me", "annie"}; !slices.Equal(got, want) {
		t.Errorf("people = %q, want %q", got, want)
	}
	if got, want := cat.Candidates(Label, forgeID), []string{"bug", "frontend", "Needs-Design"}; !slices.Equal(got, want) {
		t.Errorf("labels = %q, want %q", got, want)
	}
	if got, want := cat.Candidates(Project, anvilID), []string{"ANV", "FORGE", "forge-cli"}; !slices.Equal(got, want) {
		t.Errorf("projects = %q, want %q", got, want)
	}
}

type fakeSource struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeSource) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeSource) Me(context.Context) (plane.User, error) {
	f.record("me")
	return plane.User{ID: "u-me"}, nil
}

func (f *fakeSource) Projects(context.Context) ([]plane.Project, error) {
	f.record("projects")
	return []plane.Project{{ID: forgeID, Identifier: "FORGE"}, {ID: anvilID, Identifier: "ANV"}}, nil
}

func (f *fakeSource) Labels(_ context.Context, id string) ([]plane.Label, error) {
	f.record("labels:" + id)
	return []plane.Label{{ID: "l-1", Name: "bug"}}, nil
}

func (f *fakeSource) ProjectMembers(_ context.Context, id string) ([]plane.Member, error) {
	f.record("members:" + id)
	return nil, nil
}

func TestResolveLoading(t *testing.T) {
	t.Run("loads only what the tokens need", func(t *testing.T) {
		src := &fakeSource{}
		r, cat, err := ResolveLoading(t.Context(), src, nil, Catalog{}, Parse("x #ANV +bug @me"), "")
		if err != nil {
			t.Fatal(err)
		}
		if r.ProjectID != anvilID || !slices.Equal(r.Create.LabelIDs, []string{"l-1"}) || !slices.Equal(r.Create.AssigneeIDs, []string{"u-me"}) {
			t.Errorf("result = %+v", r)
		}
		if !cat.HasDetails(anvilID) || cat.HasDetails(forgeID) {
			t.Errorf("details = %v", cat.Details)
		}
		slices.Sort(src.calls)
		if want := []string{"labels:p-anvil", "me", "members:p-anvil", "projects"}; !slices.Equal(src.calls, want) {
			t.Errorf("calls = %q, want %q", src.calls, want)
		}
	})
	t.Run("plain title fetches nothing", func(t *testing.T) {
		src := &fakeSource{}
		if _, _, err := ResolveLoading(t.Context(), src, nil, Catalog{}, Parse("x"), forgeID); err != nil {
			t.Fatal(err)
		}
		if len(src.calls) != 0 {
			t.Errorf("calls = %q", src.calls)
		}
	})
}

func TestDefaultProject(t *testing.T) {
	linked := config.Project{Name: "forge", Repo: "forge", PlaneProjectID: forgeID}
	other := config.Project{Name: "other", Repo: "other", PlaneProjectID: anvilID}
	none := config.Project{Name: "none", Repo: "none", PlaneDecided: true}
	tests := []struct {
		name   string
		active *config.Project
		forge  []config.Project
		want   string
	}{
		{"active linked", &linked, []config.Project{linked, other}, forgeID},
		{"active unlinked", &none, []config.Project{linked}, ""},
		{"all projects, one linked", nil, []config.Project{linked, none}, forgeID},
		{"all projects, several linked", nil, []config.Project{linked, other}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DefaultProject(tt.active, tt.forge, nil); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMerge(t *testing.T) {
	a := Catalog{Details: map[string]Details{forgeID: {}}}
	b := Catalog{Projects: []plane.Project{{ID: anvilID}}, Details: map[string]Details{anvilID: {}}}
	m := a.Merge(b)
	if !m.HasDetails(forgeID) || !m.HasDetails(anvilID) || !m.HasProjects() {
		t.Errorf("merged = %+v", m)
	}
	if a.HasDetails(anvilID) {
		t.Error("merge changed its receiver")
	}
}
