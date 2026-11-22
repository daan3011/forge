package home

import (
	"context"
	"errors"
	"fmt"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// planeTitle is the Plane tab's title, where enter on an issue jumps.
const planeTitle = "Plane"

func info(s string) tea.Cmd { return func() tea.Msg { return ui.InfoMsg{Text: s} } }

func errCmd(err error) tea.Cmd { return func() tea.Msg { return ui.ErrMsg{Err: err} } }

// open is the row's main action: attach to an agent, open a review's diff,
// or jump to the PR or issue in its tab.
func (t *Tab) open() tea.Cmd {
	it, ok := t.selected()
	if !ok {
		return nil
	}
	switch it.kind {
	case kindWaiting, kindErrored:
		return tabs.AttachSession(it.agent.Name)
	case kindReview, kindFailing:
		return tabs.RunPRAction(it.pr, tabs.PRDiff)
	case kindApproved:
		return tabs.RunPRAction(it.pr, tabs.PRSelect)
	case kindIssue:
		return tabs.SelectEntity(planeTitle, it.issue.ID)
	}
	return nil
}

// fix starts the CI fix flow for a PR with failing checks.
func (t *Tab) fix() tea.Cmd {
	it, ok := t.selected()
	if !ok {
		return nil
	}
	if it.kind != kindFailing {
		return info(fmt.Sprintf("%s fixes PRs with failing checks", t.keys.Home.Fix.Help().Key))
	}
	return tabs.RunPRAction(it.pr, tabs.PRFix)
}

// merge opens the PRs tab's merge dialog for an approved, green PR.
func (t *Tab) merge() tea.Cmd {
	it, ok := t.selected()
	if !ok {
		return nil
	}
	if it.kind != kindApproved {
		return info(fmt.Sprintf("%s merges approved, green PRs", t.keys.Home.Merge.Help().Key))
	}
	return tabs.RunPRAction(it.pr, tabs.PRMerge)
}

// url is the selected PR's or issue's web address.
func (t *Tab) url(it item) (string, error) {
	switch it.kind {
	case kindReview, kindFailing, kindApproved:
		if it.pr.URL == "" {
			return "", fmt.Errorf("#%d has no URL", it.pr.Number)
		}
		return it.pr.URL, nil
	case kindIssue:
		u := t.cfg.Plane.IssueURL(it.issue.Key())
		if u == "" {
			return "", fmt.Errorf("%s: no URL (plane.base_url / workspace_slug)", it.issue.Key())
		}
		return u, nil
	}
	return "", errors.New("agents have no web page; enter attaches")
}

// browse opens the selected PR or issue in the browser.
func (t *Tab) browse() tea.Cmd {
	it, ok := t.selected()
	if !ok {
		return nil
	}
	u, err := t.url(it)
	if err != nil {
		return info(err.Error())
	}
	if t.browser == nil {
		return errCmd(errors.New("can't open a browser here"))
	}
	b := t.browser
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := b.Open(ctx, u); err != nil {
			return ui.ErrMsg{Err: fmt.Errorf("open %s: %w", u, err)}
		}
		return nil
	}
}

// openHelp is the Open binding labelled with what it does on the row.
func (t *Tab) openHelp(it item) key.Binding {
	b := t.keys.Home.Open
	verb := "open"
	switch it.kind {
	case kindWaiting, kindErrored:
		verb = "attach"
	case kindReview, kindFailing:
		verb = "diff"
	case kindApproved:
		verb = "show PR"
	case kindIssue:
		verb = "show issue"
	}
	b.SetHelp(b.Help().Key, verb)
	return b
}

// ShortHelp implements tabs.Tab: the selected row's actions, the most
// important last.
func (t *Tab) ShortHelp() []key.Binding {
	hk := t.keys.Home
	it, ok := t.selected()
	if !ok {
		return []key.Binding{t.keys.Global.Refresh}
	}
	switch it.kind {
	case kindWaiting, kindErrored:
		return []key.Binding{t.openHelp(it)}
	case kindFailing:
		return []key.Binding{hk.Browse, t.openHelp(it), hk.Fix}
	case kindApproved:
		return []key.Binding{hk.Browse, t.openHelp(it), hk.Merge}
	}
	return []key.Binding{hk.Browse, t.openHelp(it)}
}

// FullHelp implements tabs.Tab.
func (t *Tab) FullHelp() [][]key.Binding {
	l, hk := t.keys.List, t.keys.Home
	return [][]key.Binding{
		{l.Up, l.Down, l.Top, l.Bottom, l.PageDown, l.PageUp},
		{hk.Open, hk.Fix, hk.Merge, hk.Browse},
		{l.ShrinkPane, l.GrowPane},
	}
}

// Commands implements tabs.Commander.
func (t *Tab) Commands() []tabs.Command {
	l, hk := t.keys.List, t.keys.Home
	do := func(f func()) func() tea.Cmd { return func() tea.Cmd { f(); t.resize(); return nil } }
	return []tabs.Command{
		{ID: "home_open", Title: "Home: do it (attach / open diff / show item)", Key: hk.Open, Run: t.open},
		{ID: "home_fix", Title: "Home: fix failing CI of the selected PR", Key: hk.Fix, Run: t.fix},
		{ID: "home_merge", Title: "Home: merge the selected PR (asks first)", Key: hk.Merge, Run: t.merge},
		{ID: "home_browse", Title: "Home: open in browser", Key: hk.Browse, Run: t.browse},
		{ID: "grow_pane", Title: "Widen list", Key: l.GrowPane, Run: do(t.split.Grow)},
		{ID: "shrink_pane", Title: "Narrow list", Key: l.ShrinkPane, Run: do(t.split.Shrink)},
	}
}
