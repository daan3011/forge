package artifacts

import "github.com/daan3011/forge/internal/tabs"

var _ tabs.Selector = (*Tab)(nil)

// Selection implements tabs.Selector: the selected artifact's path and the
// session that made it.
func (t *Tab) Selection() tabs.Selection {
	a, ok := t.selected()
	if !ok {
		return tabs.Selection{}
	}
	return tabs.Selection{Path: a.Path, Session: a.SessionName}
}
