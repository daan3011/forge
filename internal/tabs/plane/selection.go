package plane

import "github.com/daan3011/forge/internal/tabs"

var _ tabs.Selector = (*Tab)(nil)

// Selection implements tabs.Selector: the selected work item's key, in the
// list and on the board.
func (t *Tab) Selection() tabs.Selection {
	it, ok := t.selected()
	if !ok {
		return tabs.Selection{}
	}
	return tabs.Selection{Issue: it.Key()}
}
