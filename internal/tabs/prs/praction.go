package prs

import (
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/tabs"
)

// pendingAction is a tabs.PRAction waiting for its PR to be loaded.
type pendingAction struct {
	pr     github.PR
	action tabs.PRAction
}

// PRAction implements tabs.PRActioner: it selects p and runs a on it with
// the tab's own viewer, fix flow or merge dialog. A PR no section has
// loaded yet waits until a section brings it in; once no section is
// fetching any more and none has it, the tab says so.
func (t *Tab) PRAction(p github.PR, a tabs.PRAction) tea.Cmd {
	t.wanted = &pendingAction{pr: p, action: a}
	if t.dialog != nil {
		t.wanted = nil
		return info("finish the open dialog first")
	}
	return t.runPending()
}

// runPending runs the pending action once its PR is in a section, or drops
// it when every section has finished fetching without it.
func (t *Tab) runPending() tea.Cmd {
	pa := t.wanted
	if pa == nil {
		return nil
	}
	id := pa.pr.ID()
	if !t.hasPR(id) {
		if slices.ContainsFunc(t.sections, func(s *section) bool { return s.fetching }) {
			return nil
		}
		t.wanted = nil
		return info(fmt.Sprintf("#%d isn't in any PRs section", pa.pr.Number))
	}
	t.wanted = nil
	var closeCmd tea.Cmd
	if t.diff != nil {
		closeCmd = t.closeDiff()
	}
	sel := t.Select(id)
	var act tea.Cmd
	switch pa.action {
	case tabs.PRDiff:
		act = t.openDiff()
	case tabs.PRFix:
		act = t.fix(false)
	case tabs.PRMerge:
		act = t.openMerge()
	case tabs.PRSelect:
	}
	return tea.Batch(closeCmd, sel, act)
}

// hasPR reports whether any section has loaded the PR with id.
func (t *Tab) hasPR(id string) bool {
	return slices.ContainsFunc(t.sections, func(s *section) bool {
		return slices.ContainsFunc(s.res.PRs, func(p github.PR) bool { return p.ID() == id })
	})
}
