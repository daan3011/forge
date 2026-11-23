package pipelines

import "github.com/daan3011/forge/internal/tabs"

var _ tabs.Selector = (*Tab)(nil)

// Selection implements tabs.Selector: the selected run's repo and branch,
// or those of the run the log viewer shows.
func (t *Tab) Selection() tabs.Selection {
	run, ok := t.selected()
	if t.log != nil {
		run, ok = t.log.run, true
	}
	if !ok {
		return tabs.Selection{}
	}
	return tabs.Selection{Repo: run.Repo, Branch: run.Branch}
}
