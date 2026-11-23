package home

import (
	"strconv"

	"github.com/daan3011/forge/internal/tabs"
)

var _ tabs.Selector = (*Tab)(nil)

// Selection implements tabs.Selector: the selected agent, PR or issue.
func (t *Tab) Selection() tabs.Selection {
	it, ok := t.selected()
	if !ok {
		return tabs.Selection{}
	}
	switch it.kind {
	case kindWaiting, kindErrored:
		return it.agent.Vars()
	case kindReview, kindFailing, kindApproved:
		return tabs.Selection{Repo: it.pr.Repo, PR: strconv.Itoa(it.pr.Number), Branch: it.pr.HeadRef}
	case kindIssue:
		return tabs.Selection{Issue: it.issue.Key()}
	case kindProposed:
		return tabs.Selection{Session: it.proposal.SessionName, Issue: it.proposal.Handle()}
	}
	return tabs.Selection{}
}
