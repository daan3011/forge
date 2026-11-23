package docs

import "github.com/daan3011/forge/internal/tabs"

var _ tabs.Selector = (*Tab)(nil)

// Selection implements tabs.Selector: the selected doc or folder, or the
// search hit under the cursor.
func (t *Tab) Selection() tabs.Selection {
	if t.searching() {
		if h, ok := t.currentHit(); ok {
			return tabs.Selection{Path: h.Doc.Path}
		}
		return tabs.Selection{}
	}
	if r, ok := t.selected(); ok && r.node != nil {
		return tabs.Selection{Path: r.node.Path}
	}
	return tabs.Selection{}
}
