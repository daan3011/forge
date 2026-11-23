package context

import "github.com/daan3011/forge/internal/tabs"

var _ tabs.Selector = (*Tab)(nil)

// Selection implements tabs.Selector: the selected memory file in the
// memory view; the other views have no file selection.
func (t *Tab) Selection() tabs.Selection {
	if t.view != viewMemory {
		return tabs.Selection{}
	}
	if f, ok := t.selected(); ok {
		return tabs.Selection{Path: f.Path}
	}
	return tabs.Selection{}
}
