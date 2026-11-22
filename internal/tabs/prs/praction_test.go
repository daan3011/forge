package prs

import (
	"testing"

	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

func TestPRActionOnLoadedPR(t *testing.T) {
	tests := []struct {
		action tabs.PRAction
		check  func(*Tab) bool
	}{
		{tabs.PRSelect, func(tb *Tab) bool { return tb.diff == nil && tb.dialog == nil }},
		{tabs.PRDiff, func(tb *Tab) bool { return tb.diff != nil && tb.diff.pr.Number == 3 }},
		{tabs.PRMerge, func(tb *Tab) bool { return tb.dialog != nil }},
	}
	for _, tt := range tests {
		t.Run(tt.action.String(), func(t *testing.T) {
			tb := newTestTab(sampleGH(), nil)
			drain(tb, tb.Refresh())
			tb.switchSection(2) // somewhere else
			drain(tb, tb.PRAction(pr("acme/lipgloss", 3, "", nil), tt.action))
			if p, ok := tb.selected(); !ok || p.Number != 3 {
				t.Fatalf("selected = %+v", p)
			}
			if !tt.check(tb) {
				t.Errorf("%v didn't run", tt.action)
			}
		})
	}
}

// PRFix runs the tab's own fix flow on the PR (here its fetched detail has
// no failing checks, which the flow reports).
func TestPRActionFix(t *testing.T) {
	tb := newTestTab(sampleGH(), nil)
	drain(tb, tb.Refresh())
	tb.switchSection(2)
	msgs := run(tb.PRAction(pr("acme/bubbletea", 1, "", nil), tabs.PRFix))
	found := false
	for _, m := range msgs {
		if im, ok := m.(ui.InfoMsg); ok && im.Text == "#1 has no failing checks" {
			found = true
		}
	}
	if !found {
		t.Errorf("fix didn't run on #1: %#v", msgs)
	}
}

// The PRs tab refreshes only while visible: an action asked for while its
// sections are still fetching runs when the PR arrives.
func TestPRActionWaitsForData(t *testing.T) {
	tb := newTestTab(sampleGH(), nil)
	refresh := tb.Refresh() // what switching to the tab starts
	if cmd := tb.PRAction(pr("acme/lipgloss", 3, "", nil), tabs.PRDiff); cmd != nil {
		if msgs := run(cmd); len(msgs) > 0 {
			t.Fatalf("ran before the data arrived: %#v", msgs)
		}
	}
	drain(tb, refresh)
	if tb.diff == nil || tb.diff.pr.Number != 3 {
		t.Error("the diff didn't open once the PR was loaded")
	}
	if tb.wanted != nil {
		t.Error("the action is still pending")
	}
}

func TestPRActionUnknownPR(t *testing.T) {
	tb := newTestTab(sampleGH(), nil)
	drain(tb, tb.Refresh())
	msgs := run(tb.PRAction(github.PR{Repo: "acme/x", Number: 99}, tabs.PRDiff))
	if len(msgs) != 1 {
		t.Fatalf("msgs = %#v", msgs)
	}
	if m, ok := msgs[0].(ui.InfoMsg); !ok || m.Text != "#99 isn't in any PRs section" {
		t.Errorf("msg = %#v", msgs[0])
	}
	if tb.wanted != nil || tb.diff != nil {
		t.Error("an unknown PR left state behind")
	}
}
