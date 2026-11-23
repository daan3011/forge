package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestProposalsLifecycle(t *testing.T) {
	ctx := context.Background()
	s, c := newTestStore(t)
	x := mustSession(t, s, "fix")
	ps := s.Proposals()

	p, err := ps.Create(ctx, Proposal{
		SessionID: x.ID, SessionName: x.Name, Project: "forge", Title: "Add retries",
		Description: "to the **Plane** client", Priority: "high", Labels: []string{"bug", "plane"},
	})
	noErr(t, err)
	if p.ID == 0 || p.Status != ProposalProposed || p.Handle() != "P-1" {
		t.Fatalf("created %+v", p)
	}
	got, err := ps.Get(ctx, p.ID)
	noErr(t, err)
	if !reflect.DeepEqual(got, p) {
		t.Errorf("Get = %+v\nwant %+v", got, p)
	}

	c.Advance(time.Minute)
	title, labels := "Add retries with backoff", []string(nil)
	up, err := ps.Update(ctx, p.ID, ProposalEdit{Title: &title, Labels: &labels})
	noErr(t, err)
	if up.Title != title || up.Labels != nil || up.Priority != "high" || !up.UpdatedAt.After(p.UpdatedAt) {
		t.Errorf("updated %+v", up)
	}

	noErr(t, ps.SetPlaneProject(ctx, p.ID, "pp-1"))
	noErr(t, ps.Accept(ctx, p.ID, "wi-1", "FORGE-12"))
	got, err = ps.Get(ctx, p.ID)
	noErr(t, err)
	if got.Status != ProposalAccepted || got.PlaneKey != "FORGE-12" || got.PlaneIssueID != "wi-1" || got.PlaneProjectID != "pp-1" {
		t.Errorf("accepted %+v", got)
	}
	// Accepted proposals can't be accepted, dropped or edited again.
	wantErr(t, ps.Accept(ctx, p.ID, "wi-2", "FORGE-13"), ErrConflict)
	wantErr(t, ps.Drop(ctx, p.ID), ErrConflict)
	_, err = ps.Update(ctx, p.ID, ProposalEdit{Title: &title})
	wantErr(t, err, ErrConflict)
	_, err = ps.Update(ctx, 99, ProposalEdit{Title: &title})
	wantErr(t, err, ErrNotFound)
}

func TestProposalsListAndDrop(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	a, b := mustSession(t, s, "a"), mustSession(t, s, "b")
	ps := s.Proposals()
	mk := func(x Session, project, title string) Proposal {
		p, err := ps.Create(ctx, Proposal{SessionID: x.ID, SessionName: x.Name, Project: project, Title: title})
		noErr(t, err)
		return p
	}
	p1, p2, _ := mk(a, "forge", "one"), mk(b, "forge", "two"), mk(a, "other", "three")
	noErr(t, ps.Drop(ctx, p2.ID))

	titles := func(f ProposalFilter) []string {
		got, err := ps.List(ctx, f)
		noErr(t, err)
		var out []string
		for _, p := range got {
			out = append(out, p.Title)
		}
		return out
	}
	tests := []struct {
		name string
		f    ProposalFilter
		want []string
	}{
		{"all", ProposalFilter{}, []string{"one", "two", "three"}},
		{"proposed", ProposalFilter{Status: ProposalProposed}, []string{"one", "three"}},
		{"project", ProposalFilter{Status: ProposalProposed, Project: "forge"}, []string{"one"}},
		{"session", ProposalFilter{SessionID: a.ID}, []string{"one", "three"}},
		{"dropped", ProposalFilter{Status: ProposalDropped}, []string{"two"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := titles(tt.f); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}

	// Deleting the session keeps the proposal, by name.
	noErr(t, s.Sessions().Delete(ctx, a.ID))
	got, err := ps.Get(ctx, p1.ID)
	noErr(t, err)
	if got.SessionID != 0 || got.SessionName != "a" {
		t.Errorf("after session delete: %+v", got)
	}
}

func TestParseProposalHandle(t *testing.T) {
	tests := []struct {
		in   string
		id   int64
		want bool
	}{
		{"P-7", 7, true},
		{"p-12", 12, true},
		{"P-0", 0, false},
		{"P-", 0, false},
		{"FORGE-12", 0, false},
		{"7", 0, false},
		{"P-x", 0, false},
	}
	for _, tt := range tests {
		id, ok := ParseProposalHandle(tt.in)
		if ok != tt.want || (ok && id != tt.id) {
			t.Errorf("ParseProposalHandle(%q) = %d, %v", tt.in, id, ok)
		}
	}
}

func TestSessionProgressAndAgentSummary(t *testing.T) {
	ctx := context.Background()
	s, c := newTestStore(t)
	x := mustSession(t, s, "fix")
	r := s.Sessions()

	noErr(t, r.SetProgress(ctx, x.ID, "tests green", c.Now()))
	noErr(t, r.SetAgentSummary(ctx, x.ID, "Added retries"))
	got, err := r.Get(ctx, x.ID)
	noErr(t, err)
	if got.Progress != "tests green" || !got.ProgressAt.Equal(c.Now()) || got.AgentSummary != "Added retries" || got.Summary != "Added retries" {
		t.Fatalf("got %+v", got)
	}
	// Update (with a stale copy) must not clobber what the agent wrote.
	noErr(t, r.Update(ctx, x))
	got, err = r.Get(ctx, x.ID)
	noErr(t, err)
	if got.Progress != "tests green" || got.AgentSummary != "Added retries" {
		t.Fatalf("after Update: %+v", got)
	}
	// Archive keeps the agent's summary over the generated one.
	noErr(t, r.Archive(ctx, x.ID, "generated", c.Now()))
	got, err = r.Get(ctx, x.ID)
	noErr(t, err)
	if got.Summary != "Added retries" {
		t.Errorf("archived summary = %q", got.Summary)
	}
	y := mustSession(t, s, "other")
	noErr(t, r.Archive(ctx, y.ID, "generated", c.Now()))
	got, err = r.Get(ctx, y.ID)
	noErr(t, err)
	if got.Summary != "generated" {
		t.Errorf("summary without agent summary = %q", got.Summary)
	}
}

func TestNewLinkKinds(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	x := mustSession(t, s, "fix")
	for _, to := range []Ref{ProposalRef(1), DocRef("/wt/.forge/docs/a.md"), ArtifactRef("/a.png")} {
		noErr(t, s.Links().Add(ctx, SessionRef(x.ID), to, LinkMeta{Title: "t"}))
	}
	got, err := s.Links().For(ctx, SessionRef(x.ID))
	noErr(t, err)
	if len(got) != 3 {
		t.Errorf("links = %+v", got)
	}
}
