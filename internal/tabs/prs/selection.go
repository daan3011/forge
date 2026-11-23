package prs

import (
	"strconv"

	"github.com/daan3011/forge/internal/tabs"
)

var _ tabs.Selector = (*Tab)(nil)

// Selection implements tabs.Selector: the selected PR, or the one the diff
// viewer shows.
func (t *Tab) Selection() tabs.Selection {
	pr, ok := t.selected()
	if t.diff != nil {
		pr, ok = t.diff.pr, true
	}
	if !ok {
		return tabs.Selection{}
	}
	return tabs.Selection{Repo: pr.Repo, PR: strconv.Itoa(pr.Number), Branch: pr.HeadRef}
}
