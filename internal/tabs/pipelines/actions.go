package pipelines

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/ui"
)

// Reruns and cancel (P2.7) act on the run in the log viewer, else the
// selected one. GitHub takes a moment to show the new state, so the runs
// refresh actionRefreshDelay after the call; the fast refresh takes over
// once the run shows as active.

const (
	actionTimeout      = 30 * time.Second
	actionRefreshDelay = 3 * time.Second
)

// run actions, as runActionMsg names them.
const (
	actRerunFailed = "rerun-failed"
	actRerun       = "rerun"
	actCancel      = "cancel"
)

type runActionMsg struct {
	key    string // run key
	action string
	label  string // "CI #42"
	err    error
}

// refreshSoonMsg refreshes the runs after an action.
type refreshSoonMsg struct{}

func (runActionMsg) TargetTab() string   { return Title }
func (refreshSoonMsg) TargetTab() string { return Title }

func info(s string) tea.Cmd { return func() tea.Msg { return ui.InfoMsg{Text: s} } }

func runLabel(r github.Run) string { return fmt.Sprintf("%s #%d", r.Workflow, r.RunNumber) }

// actionTarget is the viewer's run, else the selected one.
func (t *Tab) actionTarget() (github.Run, bool) {
	if t.log != nil {
		return t.log.run, true
	}
	return t.selected()
}

// rerunnable reports whether GitHub will rerun r's failed jobs: it has
// finished without succeeding.
func rerunnable(r github.Run) bool {
	switch r.Conclusion {
	case "failure", "cancelled", "timed_out", "startup_failure":
		return true
	}
	return false
}

// rerunFailed reruns the target run's failed jobs.
func (t *Tab) rerunFailed() tea.Cmd {
	r, ok := t.actionTarget()
	if !ok {
		return nil
	}
	switch {
	case r.Active():
		return info(runLabel(r) + " is still running")
	case !rerunnable(r):
		return info(runLabel(r) + " has no failed jobs")
	}
	return t.runAction(r, actRerunFailed)
}

// rerunAll asks, then reruns every job of the target run.
func (t *Tab) rerunAll() tea.Cmd {
	r, ok := t.actionTarget()
	if !ok {
		return nil
	}
	if r.Active() {
		return info(runLabel(r) + " is still running")
	}
	return t.confirm(fmt.Sprintf("Rerun every job of %s?", runLabel(r)), "Rerun", r, actRerun)
}

// cancelRun asks, then cancels the target run.
func (t *Tab) cancelRun() tea.Cmd {
	r, ok := t.actionTarget()
	if !ok {
		return nil
	}
	if !r.Active() {
		return info(runLabel(r) + " isn't running")
	}
	return t.confirm(fmt.Sprintf("Cancel %s?", runLabel(r)), "Cancel run", r, actCancel)
}

func (t *Tab) confirm(title, affirm string, r github.Run, action string) tea.Cmd {
	yes := false
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Key("ok").Title(title).Description(r.Repo + " · " + r.Branch).
			Affirmative(affirm).Negative("Keep").Value(&yes),
	))
	return t.openDialog(form, func() tea.Cmd {
		if !yes {
			return nil
		}
		return t.runAction(r, action)
	})
}

// runAction calls GitHub, once per run at a time.
func (t *Tab) runAction(r github.Run, action string) tea.Cmd {
	if t.gh == nil {
		return errCmd(errors.New("GitHub isn't available"))
	}
	k := r.Key()
	if t.acting[k] {
		return info(runLabel(r) + ": already on it")
	}
	t.acting[k] = true
	gh, repo, id, label := t.gh, r.Repo, r.ID, runLabel(r)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		var err error
		switch action {
		case actRerunFailed:
			err = gh.RerunFailedJobs(ctx, repo, id)
		case actRerun:
			err = gh.RerunRun(ctx, repo, id)
		case actCancel:
			err = gh.CancelRun(ctx, repo, id)
		}
		return runActionMsg{key: k, action: action, label: label, err: err}
	}
}

var actionWords = map[string]struct{ verb, done string }{
	actRerunFailed: {"rerun the failed jobs of", "rerunning the failed jobs of"},
	actRerun:       {"rerun", "rerunning"},
	actCancel:      {"cancel", "cancelling"},
}

func (t *Tab) actionDone(msg runActionMsg) tea.Cmd {
	delete(t.acting, msg.key)
	w := actionWords[msg.action]
	if msg.err != nil {
		return errCmd(fmt.Errorf("couldn't %s %s: %s", w.verb, msg.label, github.Reason(msg.err)))
	}
	return tea.Batch(
		info(w.done+" "+msg.label),
		t.tick(actionRefreshDelay, func(time.Time) tea.Msg { return refreshSoonMsg{} }),
	)
}

// openURL opens url in the browser.
func (t *Tab) openURL(url, what string) tea.Cmd {
	if t.browser == nil {
		return errCmd(errors.New("can't open a browser here"))
	}
	if url == "" {
		return errCmd(fmt.Errorf("%s: no URL", what))
	}
	b := t.browser
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := b.Open(ctx, url); err != nil {
			return ui.ErrMsg{Err: fmt.Errorf("%s: %w", what, err)}
		}
		return nil
	}
}
