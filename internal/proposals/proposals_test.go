package proposals

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/testutil"
)

type env struct {
	st    *store.Store
	plane *testutil.FakePlane
	d     Deps
	x     store.Session
}

func newEnv(t *testing.T, proj config.Project) *env {
	t.Helper()
	testutil.IsolateHome(t)
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fp := &testutil.FakePlane{
		ProjectList: []plane.Project{{ID: "pp-1", Name: "forge", Identifier: "FORGE"}},
		LabelMap:    map[string][]plane.Label{"pp-1": {{ID: "l-bug", Name: "bug"}}},
	}
	x, err := st.Sessions().Create(ctx, store.Session{
		Name: "fix", ClaudeSessionID: "uuid-fix", Project: proj.Name, Branch: "forge/fix",
	})
	if err != nil {
		t.Fatal(err)
	}
	return &env{st: st, plane: fp, x: x, d: Deps{Store: st, Plane: fp, Projects: []config.Project{proj}}}
}

func (e *env) propose(t *testing.T, p store.Proposal) store.Proposal {
	t.Helper()
	p.SessionID, p.SessionName = e.x.ID, e.x.Name
	if p.Project == "" {
		p.Project = "forge"
	}
	got, err := e.st.Proposals().Create(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

var linked = config.Project{Name: "forge", Repo: "daan3011/forge", PlaneProjectID: "pp-1"}

func TestAcceptCreatesAndLinks(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, linked)
	pr := store.PRRef("daan3011/forge", 12)
	if err := e.st.Links().Add(ctx, store.SessionRef(e.x.ID), pr, store.LinkMeta{URL: "https://gh/pr/12", Title: "Fix", State: "open"}); err != nil {
		t.Fatal(err)
	}
	p := e.propose(t, store.Proposal{Title: "Add retries", Description: "to the Plane client", Priority: "high", Labels: []string{"Bug", "nope"}})

	got, err := Accept(ctx, e.d, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "FORGE-1" || len(got.UnknownLabels) != 1 || got.UnknownLabels[0] != "nope" {
		t.Errorf("accepted %+v", got)
	}
	if e.plane.CreatedCount() != 1 {
		t.Fatalf("created %d", e.plane.CreatedCount())
	}
	in := e.plane.Created[0]
	if in.Name != "Add retries" || in.Priority != plane.PriorityHigh || len(in.LabelIDs) != 1 || in.LabelIDs[0] != "l-bug" {
		t.Errorf("create input %+v", in)
	}
	for _, want := range []string{"to the Plane client", "Filed by forge session fix", "branch forge/fix", "daan3011/forge#12"} {
		if !strings.Contains(in.DescriptionHTML, want) {
			t.Errorf("description lacks %q:\n%s", want, in.DescriptionHTML)
		}
	}
	row, _ := e.st.Proposals().Get(ctx, p.ID)
	if row.Status != store.ProposalAccepted || row.PlaneKey != "FORGE-1" || row.PlaneProjectID != "pp-1" {
		t.Errorf("row %+v", row)
	}
	issueLinks, err := e.st.Links().For(ctx, store.IssueRef("FORGE-1"))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[store.LinkKind]bool{}
	for _, l := range issueLinks {
		kinds[l.Other.Kind] = true
	}
	for _, k := range []store.LinkKind{store.KindSession, store.KindProposal, store.KindGitHubPR} {
		if !kinds[k] {
			t.Errorf("issue not linked to %s: %+v", k, issueLinks)
		}
	}

	// A second accept refuses and creates nothing.
	if _, err := Accept(ctx, e.d, p.ID); !errors.Is(err, store.ErrConflict) {
		t.Errorf("second accept err = %v", err)
	}
	if e.plane.CreatedCount() != 1 {
		t.Errorf("duplicate create")
	}
}

func TestAcceptMatchesAndSavesPlaneProject(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, config.Project{Name: "forge", Repo: "daan3011/forge"})
	p := e.propose(t, store.Proposal{Title: "x"})
	if _, err := Accept(ctx, e.d, p.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := e.st.Projects().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].PlaneProjectID != "pp-1" {
		t.Errorf("saved link %+v", rows)
	}
}

func TestAcceptErrors(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		proj config.Project
		want error
	}{
		{"decided none", config.Project{Name: "forge", Repo: "daan3011/forge", PlaneDecided: true}, ErrNoPlaneProject},
		{"no match", config.Project{Name: "forge", Repo: "someone/else"}, ErrNoPlaneProject},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, tt.proj)
			p := e.propose(t, store.Proposal{Title: "x"})
			if _, err := Accept(ctx, e.d, p.ID); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			row, _ := e.st.Proposals().Get(ctx, p.ID)
			if row.Status != store.ProposalProposed || e.plane.CreatedCount() != 0 {
				t.Errorf("row %+v, created %d", row, e.plane.CreatedCount())
			}
		})
	}
	t.Run("plane create fails", func(t *testing.T) {
		e := newEnv(t, linked)
		e.plane.Err = errors.New("boom")
		p := e.propose(t, store.Proposal{Title: "x"})
		if _, err := Accept(ctx, e.d, p.ID); err == nil {
			t.Fatal("want an error")
		}
		row, _ := e.st.Proposals().Get(ctx, p.ID)
		if row.Status != store.ProposalProposed {
			t.Errorf("row %+v", row)
		}
	})
}

func TestFooter(t *testing.T) {
	tests := []struct {
		name string
		a    Attribution
		want string
	}{
		{"name only", Attribution{Session: store.Session{Name: "fix"}}, "Filed by forge session fix"},
		{"branch and PR", Attribution{
			Session: store.Session{Name: "fix", Branch: "forge/fix"},
			PRs:     []store.Link{{Other: store.PRRef("o/r", 3), LinkMeta: store.LinkMeta{URL: "https://x/3"}}},
		}, "Filed by forge session fix · branch forge/fix · [PR o/r#3](https://x/3)"},
	}
	for _, tt := range tests {
		if got := tt.a.Footer(); got != tt.want {
			t.Errorf("%s: %q", tt.name, got)
		}
	}
}
