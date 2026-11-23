package home

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/proposals"
	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/testutil"
	"github.com/daan3011/forge/internal/ui"
)

// propFixture is a Home tab with a real proposal store and a fake Plane.
type propFixture struct {
	*fixture
	st    *store.Store
	plane *testutil.FakePlane
	x     store.Session
}

func newPropFixture(t *testing.T) *propFixture {
	t.Helper()
	testutil.IsolateHome(t)
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "forge.db"), store.WithClock(func() time.Time { return now.Add(-5 * time.Minute) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	x, err := st.Sessions().Create(ctx, store.Session{Name: "fix", ClaudeSessionID: "u1", Project: "forge", Branch: "forge/fix"})
	if err != nil {
		t.Fatal(err)
	}
	f := &propFixture{fixture: newFixture(t), st: st, x: x, plane: &testutil.FakePlane{
		ProjectList: []plane.Project{{ID: "pp1", Name: "forge", Identifier: "FORGE"}},
	}}
	return f
}

func (f *propFixture) propose(t *testing.T, title string) store.Proposal {
	t.Helper()
	p, err := f.st.Proposals().Create(context.Background(), store.Proposal{
		SessionID: f.x.ID, SessionName: "fix", Project: "forge", Title: title, Description: "why",
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *propFixture) tab() *Tab {
	d := tabkit.Deps{
		Theme: ui.NewTheme(true, nil), Keys: ui.DefaultKeyMap(), Config: f.cfg,
		Sessions: f.sess, GitHub: f.gh, Plane: f.pl, Artifacts: f.arts, NotesDir: f.notes,
		Proposals: proposals.Queue{Store: f.st, Plane: f.plane, Projects: func() []config.Project { return f.cfg.Projects }},
	}
	t := newTab(d)
	t.now = func() time.Time { return now }
	t.SetProject(&f.cfg.Projects[0])
	t.SetSize(140, 24)
	return t
}

func TestHomeListsProposals(t *testing.T) {
	f := newPropFixture(t)
	f.full()
	f.sess.rows = append(f.sess.rows, sessions.Row{Session: store.Session{
		Name: "fix", Status: store.StatusWorking, Progress: "tests green", ProgressAt: now.Add(-time.Minute),
	}})
	f.propose(t, "Add retries to the Plane client")
	tab := f.tab()
	pump(tab, tab.Init())

	got := strings.Join(ids(tab.list), ",")
	if got != "agent:fix-login,proposal:1,pr:daan/forge#212,pr:daan/forge#198,pr:daan/forge#190,plane:uuid-41" {
		t.Fatalf("list = %s", got)
	}
	v := view(tab)
	for _, want := range []string{"P-1 Add retries to the Plane client · by fix", "proposed 5m", "a accept", "tests green 1m"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
}

func TestHomeAcceptProposal(t *testing.T) {
	ctx := context.Background()
	f := newPropFixture(t)
	f.propose(t, "Add retries")
	tab := f.tab()
	pump(tab, tab.Init())
	if len(tab.list) != 1 || tab.list[0].kind != kindProposed {
		t.Fatalf("list %v", ids(tab.list))
	}
	// Keys that don't fit a proposal say so.
	if msgs := press(tab, "m"); len(msgs) != 1 || !isInfo(msgs[0]) {
		t.Errorf("m on proposal: %#v", msgs)
	}
	_, cmd := tab.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	msgs := pump(tab, cmd)
	var ok bool
	for _, m := range msgs {
		if s, isS := m.(ui.SuccessMsg); isS && strings.Contains(s.Text, "created FORGE-1") {
			ok = true
		}
	}
	if !ok {
		t.Errorf("no success toast: %#v", msgs)
	}
	if f.plane.CreatedCount() != 1 || !strings.Contains(f.plane.Created[0].DescriptionHTML, "Filed by forge session fix") {
		t.Errorf("created %+v", f.plane.Created)
	}
	p, _ := f.st.Proposals().Get(ctx, 1)
	if p.Status != store.ProposalAccepted {
		t.Errorf("row %+v", p)
	}
	if len(tab.list) != 0 {
		t.Errorf("accepted proposal still listed: %v", ids(tab.list))
	}
}

func TestHomeAcceptFailureKeepsProposal(t *testing.T) {
	f := newPropFixture(t)
	f.cfg.Projects[0].PlaneProjectID, f.cfg.Projects[0].PlaneDecided = "", true // no Plane project
	f.propose(t, "Add retries")
	tab := f.tab()
	pump(tab, tab.Init())
	_, cmd := tab.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	msgs := pump(tab, cmd)
	var gotErr bool
	for _, m := range msgs {
		if e, ok := m.(ui.ErrMsg); ok && strings.Contains(e.Err.Error(), "no Plane project") {
			gotErr = true
		}
	}
	if !gotErr || len(tab.list) != 1 {
		t.Errorf("msgs %#v, list %v", msgs, ids(tab.list))
	}
}

func TestHomeProposalCommands(t *testing.T) {
	tab := newPropFixture(t).tab()
	got := map[string]bool{}
	for _, c := range tab.Commands() {
		got[c.ID] = true
	}
	for _, id := range []string{"home_accept", "home_edit", "home_drop"} {
		if !got[id] {
			t.Errorf("no palette command %s", id)
		}
	}
}

// TestHomeProposalFlow drives edit and drop through the real forms.
func TestHomeProposalFlow(t *testing.T) {
	ctx := context.Background()
	f := newPropFixture(t)
	f.propose(t, "Add retries")
	f.propose(t, "Flaky test")
	h := &host{tab: f.tab()}
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(140, 24))
	h.wait(t, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return strings.Contains(h.text, "P-1 Add retries") && strings.Contains(h.text, "P-2 Flaky test")
	})

	// Edit P-1's title: e opens the form; typing appends to the title.
	tm.Send(tea.KeyPressMsg{Code: tea.KeyHome})
	tm.Send(tea.KeyPressMsg{Code: 'e', Text: "e"})
	h.wait(t, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return strings.Contains(h.text, "Edit P-1")
	})
	tm.Type(" with backoff")
	for range 3 {
		tm.Send(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	h.wait(t, func() bool {
		p, _ := f.st.Proposals().Get(ctx, 1)
		return p.Title == "Add retries with backoff"
	})

	// Drop P-2 after confirming.
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	tm.Send(tea.KeyPressMsg{Code: 'x', Text: "x"})
	h.wait(t, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return strings.Contains(h.text, "Drop P-2?")
	})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyLeft}) // to "Drop"
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	h.wait(t, func() bool {
		p, _ := f.st.Proposals().Get(ctx, 2)
		return p.Status == store.ProposalDropped
	})
	h.wait(t, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return !strings.Contains(h.text, "Flaky test")
	})
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
	if f.plane.CreatedCount() != 0 {
		t.Error("edit or drop created a Plane issue")
	}
}
