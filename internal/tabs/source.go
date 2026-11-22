package tabs

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/github"
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
	// plane.BranchFor name (P3.7). Empty means agent/<name>.
	Branch string
	// Template is the session template to pre-select; empty picks
	// implement-issue / review-pr when present, else the default.
	Template string
	// Vars pre-fill template variables (e.g. P2.8's failing_log).
	Vars map[string]string
	// Fix makes this a "fix this" request from CI (P2.8); nil opens the
	// pre-filled new-session form as usual.
	Fix *FixRequest
	// Docs are absolute doc paths to send to an agent (P4.3, the Docs
	// tab): the Sessions tab asks for a new or a running session and
	// mentions them there. Kind and ID are empty then.
	Docs []string
	// Packs are context pack names to give an agent (P5.2, the Context
	// tab's Packs view); handled like Docs, with Repo picking the project.
	Packs []string
}

// FixRequest says how a "fix this" source is handled (P2.8).
type FixRequest struct {
	// Reuse sends the rendered prompt into the newest live session linked
	// to the source, when there is one, instead of starting another.
	Reuse bool
	// Immediate starts the new session without the form, unless a
	// template variable is missing or the template is gone.
	Immediate bool
	// StartCommit starts the new branch at this commit (a fix branch for a
	// run without a PR).
	StartCommit string
	// Confirm, when set, is asked (default yes) before anything happens.
	Confirm string
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

// PlaneLink is a repo's Plane project; PlaneProjectID "" means it has none.
type PlaneLink struct {
	Repo           string // owner/name
	PlaneProjectID string
}

// PlaneLinkedMsg says repos' Plane projects are now known (matched or
// picked, and already saved). The root model updates its projects and tells
// the tabs again.
type PlaneLinkedMsg struct{ Links []PlaneLink }

// ProjectLinker saves a repo's Plane project (id "" = none).
// *store.Projects satisfies it.
type ProjectLinker interface {
	SetPlane(ctx context.Context, repo, id string) error
}

// AttachSessionMsg asks the tab that owns sessions to attach to the named
// session (resuming it if needed), as the "Go to session…" picker does. The
// root model switches to that tab first (Home's enter on an agent, P6.1).
type AttachSessionMsg struct{ Name string }

// AttachSession attaches to the named session.
func AttachSession(name string) tea.Cmd {
	return func() tea.Msg { return AttachSessionMsg{Name: name} }
}

// PRAction is what PRActionMsg does with its PR.
type PRAction int

// PR actions another tab can ask the PRs tab for.
const (
	PRSelect PRAction = iota // select it
	PRDiff                   // open its diff
	PRFix                    // start the CI fix flow (P2.8)
	PRMerge                  // open the merge dialog, which asks first
)

func (a PRAction) String() string {
	switch a {
	case PRDiff:
		return "diff"
	case PRFix:
		return "fix"
	case PRMerge:
		return "merge"
	}
	return "select"
}

// PRActionMsg asks the PRs tab to select PR and run Action on it, so other
// tabs reuse its diff viewer, fix flow and merge dialog (Home, P6.1). The
// root model switches to that tab first.
type PRActionMsg struct {
	PR     github.PR
	Action PRAction
}

// RunPRAction asks the PRs tab to run a on p.
func RunPRAction(p github.PR, a PRAction) tea.Cmd {
	return func() tea.Msg { return PRActionMsg{PR: p, Action: a} }
}

// PRActioner is implemented by the tab that lists PRs. When p isn't loaded
// yet it may run the action once its data arrives.
type PRActioner interface {
	PRAction(p github.PR, a PRAction) tea.Cmd
}

// SelectEntityMsg asks the root model to switch to the tab titled Tab and
// select the entity ID there (EntityProvider.Select), as picking a palette
// result does.
type SelectEntityMsg struct {
	Tab string
	ID  string
}

// SelectEntity jumps to an entity of another tab.
func SelectEntity(tab, id string) tea.Cmd {
	return func() tea.Msg { return SelectEntityMsg{Tab: tab, ID: id} }
}

// SessionWatcher is implemented by tabs that show sessions without owning
// them (Home). The root model calls SessionsChanged on every `forge hook`
// ping, whether or not the tab is visible.
type SessionWatcher interface {
	SessionsChanged() tea.Cmd
}
