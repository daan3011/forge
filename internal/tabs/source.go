package tabs

import (
	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/refresh"
	"github.com/daan3011/forge/internal/store"
)

// Source is the issue or PR a new session is started from (P1.9). The PR
// and Plane tabs fill it from the selected item; the Sessions tab turns it
// into a pre-filled new-session form and links the session to it.
type Source struct {
	Kind store.LinkKind // store.KindPlaneIssue or store.KindGitHubPR
	// ID is the link id: the issue key ("PROJ-123") or "owner/repo#12".
	ID string
	// Number is the PR number; 0 for issues.
	Number int
	URL    string
	Title  string
	State  string // e.g. "started", "open", "draft"
	// Repo ("owner/name") picks the project; empty means the active one.
	Repo string
	// Base is the base branch; empty means the default.
	Base string
	// Branch is the branch to work on: a PR's head branch, or the issue's
	// branch name (P3.7). Empty means agent/<name>.
	Branch string
	// Template is the session template to pre-select; empty picks
	// implement-issue / review-pr when present, else the default.
	Template string
	// Vars pre-fill template variables (e.g. P2.8's failing_log).
	Vars map[string]string
}

// StartSessionMsg asks the tab that owns sessions to open the new-session
// form for Source. The root model switches to that tab first.
type StartSessionMsg struct{ Source Source }

// StartSessionFrom opens the new-session form pre-filled from src: name
// from the issue key or PR number, branch, template and variables. The
// started session is linked to src.
func StartSessionFrom(src Source) tea.Cmd {
	return func() tea.Msg { return StartSessionMsg{Source: src} }
}

// SessionStarter is implemented by the tab that owns agent sessions.
type SessionStarter interface {
	StartSession(src Source) tea.Cmd
}

// ShowRunsMsg asks the tab that lists workflow runs to show the runs of
// Branch in Repo (P2.6). The root model switches to that tab.
type ShowRunsMsg struct {
	Repo   string // owner/name
	Branch string
}

// ShowRuns jumps to the workflow runs of repo's branch.
func ShowRuns(repo, branch string) tea.Cmd {
	return func() tea.Msg { return ShowRunsMsg{Repo: repo, Branch: branch} }
}

// RunsShower is implemented by the tab that lists workflow runs.
type RunsShower interface {
	ShowRuns(repo, branch string) tea.Cmd
}

// SourceRefresher is implemented by AutoRefreshers that do different work
// per source (the Sessions tab syncs PR links on GitHub ticks). The root
// model calls RefreshSource instead of Refresh on background ticks.
type SourceRefresher interface {
	RefreshSource(src refresh.Source) tea.Cmd
}
