package sessions

import (
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/tabs"
)

var _ tabs.Selector = (*Tab)(nil)

// Selection implements tabs.Selector: the selected session, or the one the
// transcript viewer shows.
func (t *Tab) Selection() tabs.Selection {
	if t.transcript != nil {
		if r, ok := t.rows[t.transcript.name]; ok {
			return r.Vars()
		}
		return agent.Row{Session: t.transcript.session}.Vars()
	}
	if r, ok := t.current(); ok {
		return r.Vars()
	}
	return tabs.Selection{}
}
