package ui

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

// KeyMap holds every key binding in forge. Define bindings only here so the
// help overlay stays accurate. Build one with NewKeyMap; the root model owns
// it and hands a pointer to every tab and widget.
type KeyMap struct {
	Global   GlobalKeys
	List     ListKeys
	Input    InputKeys
	Sessions SessionKeys
	PRs      PRKeys
	// Pipelines are the Pipelines tab's actions (P2.6).
	Pipelines PipelineKeys
	// Plane are the Plane tab's actions (P3.2).
	Plane PlaneKeys
	// Docs are the Docs tab's actions (P4.1).
	Docs DocKeys
	// Artifacts are the Artifacts tab's actions (P4.5).
	Artifacts ArtifactKeys
	// Transcript drives the transcript viewer (P1.6), which takes over
	// the Sessions tab while open.
	Transcript TranscriptKeys
	// Diff drives the PR diff viewer (P2.4), which takes over the PRs tab
	// while open.
	Diff DiffKeys
	// Log drives the job log viewer (P2.7), which takes over the Pipelines
	// tab while open.
	Log LogKeys
	// Capture drives the quick capture line (P3.5): the ctrl+n overlay
	// and `forge capture`.
	Capture CaptureKeys
	Demo    DemoKeys
	Tmux    TmuxDemoKeys
}

// GlobalKeys work on every tab.
type GlobalKeys struct {
	Quit       key.Binding
	ForceQuit  key.Binding // also works while a text input has focus
	Help       key.Binding
	NextTab    key.Binding
	PrevTab    key.Binding
	GotoTab    key.Binding
	Refresh    key.Binding
	RefreshAll key.Binding // every tab, visible or not
	Palette    key.Binding // also works while a text input has focus
	Projects   key.Binding // project switcher; also works while a text input has focus
	// GotoSession opens the session picker; picking one attaches to it.
	GotoSession key.Binding
	// Capture opens the quick capture line (P3.5).
	Capture key.Binding
}

// ListKeys drive the list/detail layout every tab uses. Tab-specific groups
// (PR actions, session actions, ...) get their own struct here.
type ListKeys struct {
	Up         key.Binding
	Down       key.Binding
	Top        key.Binding
	Bottom     key.Binding
	PageDown   key.Binding
	PageUp     key.Binding
	Filter     key.Binding
	GrowPane   key.Binding
	ShrinkPane key.Binding
}

// InputKeys apply while a text input or overlay has focus.
type InputKeys struct {
	Cancel     key.Binding
	Accept     key.Binding
	DeleteBack key.Binding
	ClearInput key.Binding
	Next       key.Binding // next result in a picker (palette)
	Prev       key.Binding // previous result in a picker (palette)
}

// SessionKeys are the Sessions tab's actions.
type SessionKeys struct {
	New         key.Binding
	Kill        key.Binding
	Delete      key.Binding // archived view only
	ArchiveView key.Binding // toggle between active and archived sessions
	Archive     key.Binding // archive the session; restore it in the archived view
	Attach      key.Binding // attach, resuming a dead session first
	Resume      key.Binding // resume a dead session without attaching
	Transcript  key.Binding // open the transcript viewer
	OpenPR      key.Binding // open the linked PR (else the issue) in the browser
	OpenIssue   key.Binding // open the linked issue in the browser
}

// PRKeys are the PRs tab's actions (P2.1).
type PRKeys struct {
	NextSection key.Binding
	PrevSection key.Binding
	RepoFilter  key.Binding // pick a repo to narrow every section to
	StaleOnly   key.Binding // show only stale PRs (P2.5)
	// The detail pane (P2.2): its Overview / Checks / Activity tabs and
	// scrolling, while the list keeps j / k.
	DetailNext key.Binding
	DetailPrev key.Binding
	DetailDown key.Binding
	DetailUp   key.Binding
	// Quick actions on the selected PR (P2.3).
	Open         key.Binding
	CopyURL      key.Binding
	Checkout     key.Binding
	UpdateBranch key.Binding
	Merge        key.Binding
	AutoMerge    key.Binding
	ToggleDraft  key.Binding
	Session      key.Binding
	// Diff opens the full-screen diff viewer to review the PR (P2.4).
	Diff key.Binding
	// Runs jumps to the PR branch's workflow runs on the Pipelines tab
	// (P2.6).
	Runs key.Binding
	// Fix hands the PR's failing checks to an agent (P2.8); FixEdit opens
	// the prompt for editing first.
	Fix     key.Binding
	FixEdit key.Binding
}

// PipelineKeys are the Pipelines tab's actions (P2.6).
type PipelineKeys struct {
	Filters      key.Binding // branch / workflow / status / mine form
	Mine         key.Binding // toggle runs started by me
	ClearFilters key.Binding
	DetailDown   key.Binding
	DetailUp     key.Binding
	Open         key.Binding // the run in the browser
	// Logs and reruns (P2.7).
	Logs        key.Binding // the failed jobs' logs, collapsed to errors
	RerunFailed key.Binding
	RerunAll    key.Binding
	CancelRun   key.Binding
	// "Fix this" (P2.8): the failed run to an agent; FixEdit edits first.
	Fix     key.Binding
	FixEdit key.Binding
}

// PlaneKeys are the Plane tab's actions (P3.2).
type PlaneKeys struct {
	Filters      key.Binding // project / assignee / state / label / cycle form
	Mine         key.Binding // toggle assigned to me / anyone
	ClearFilters key.Binding
	DetailDown   key.Binding
	DetailUp     key.Binding
	Open         key.Binding // the work item in the browser
	CopyURL      key.Binding

	// Kanban board (P3.3).
	Board          key.Binding // list <-> board
	ColLeft        key.Binding
	ColRight       key.Binding
	MovePrev       key.Binding // the card to the previous state
	MoveNext       key.Binding // the card to the next state
	CollapseColumn key.Binding // fold / unfold the selected column
	CollapseEmpty  key.Binding // fold / unfold every empty column
	OpenCard       key.Binding // the card's detail as an overlay (Input.Cancel closes it)

	// Create & edit (P3.4).
	New     key.Binding // new work item form
	Edit    key.Binding // edit the selected item
	Comment key.Binding // comment on the selected item
	Session key.Binding // start a forge session for the selected item

	// Cycle summary (P3.6).
	Cycle key.Binding // filter to the current cycle / back to every cycle
}

// DocKeys are the Docs tab's actions (P4.1). While the detail pane has
// focus, the List keys scroll it.
type DocKeys struct {
	New        key.Binding // new doc form (name + template)
	Edit       key.Binding // the doc in $EDITOR
	Rename     key.Binding // rename / move the doc
	Delete     key.Binding // move the doc to .trash, after confirming
	Open       key.Binding // expand a folder; focus the detail pane on a doc
	Back       key.Binding // collapse a folder (or go to its parent); back to the tree
	DetailDown key.Binding
	DetailUp   key.Binding
	Mark       key.Binding // mark / unmark the doc for sending to an agent (P4.3)
	Send       key.Binding // send the marked docs (or the selected one) to an agent session
	Decision   key.Binding // log a decision (P4.4)
	Decisions  key.Binding // toggle the decisions view
	Supersede  key.Binding // mark the selected decision superseded by a newer one
}

// ArtifactKeys are the Artifacts tab's actions (P4.5, P4.6).
type ArtifactKeys struct {
	Open         key.Binding // open in the default app (browser for HTML)
	CopyPath     key.Binding // copy the artifact's path
	Keep         key.Binding // copy it into the project's .forge/artifacts
	Pin          key.Binding // pin / unpin (pinned float to the top)
	Tags         key.Binding // edit the comma separated tags
	Rename       key.Binding // set the display title
	Filters      key.Binding // filter by tag / pinned only
	PinnedOnly   key.Binding // toggle showing pinned artifacts only
	ClearFilters key.Binding // drop the tag and pinned-only filters
}

// LogKeys are active while the job log viewer is open; nothing else (not
// even the global keys) is.
type LogKeys struct {
	Up          key.Binding
	Down        key.Binding
	PageUp      key.Binding
	PageDown    key.Binding
	Top         key.Binding
	Bottom      key.Binding
	ToggleFull  key.Binding // error context <-> the full log
	Search      key.Binding
	NextMatch   key.Binding
	PrevMatch   key.Binding
	NextJob     key.Binding // the run's next failed job
	PrevJob     key.Binding
	RerunFailed key.Binding
	RerunAll    key.Binding
	CancelRun   key.Binding
	Fix         key.Binding // the job on screen to an agent (P2.8)
	FixEdit     key.Binding
	Open        key.Binding // the job in the browser
	Close       key.Binding
}

// TranscriptKeys are active while the transcript viewer is open; nothing
// else (not even the global keys) is.
type TranscriptKeys struct {
	Up        key.Binding // previous entry
	Down      key.Binding // next entry
	PageUp    key.Binding
	PageDown  key.Binding
	Top       key.Binding
	Bottom    key.Binding
	Toggle    key.Binding // expand / collapse the tool call under the cursor
	ToggleAll key.Binding // expand / collapse every tool call
	Follow    key.Binding // keep the newest entry in view as it arrives
	Close     key.Binding
}

// DiffKeys are active while the PR diff viewer is open; nothing else (not
// even the global keys) is.
type DiffKeys struct {
	Up             key.Binding
	Down           key.Binding
	PageUp         key.Binding
	PageDown       key.Binding
	Top            key.Binding
	Bottom         key.Binding
	NextFile       key.Binding
	PrevFile       key.Binding
	Approve        key.Binding
	RequestChanges key.Binding
	Comment        key.Binding
	Open           key.Binding // the PR in the browser
	Close          key.Binding
}

// CaptureKeys are active while the quick capture line has focus, together
// with the input Cancel, DeleteBack and ClearInput keys.
type CaptureKeys struct {
	Submit       key.Binding // create the item in the background
	FullForm     key.Binding // open the full create form instead
	Complete     key.Binding // complete the token, or apply the suggestion
	CompletePrev key.Binding // previous completion
	Left         key.Binding
	Right        key.Binding
	Home         key.Binding
	End          key.Binding
	DeleteWord   key.Binding
	DeleteFwd    key.Binding
}

// DemoKeys drive the hidden `forge demo anim` showcase. They are not
// configurable and not palette commands: the demo is its own program.
type DemoKeys struct {
	Replay  key.Binding
	Burst   key.Binding
	Full    key.Binding
	Reduced key.Binding
	Off     key.Binding
	Quit    key.Binding
}

// TmuxDemoKeys drive the hidden `forge demo tmux` program, which exercises
// the tmux driver. Same rules as DemoKeys.
type TmuxDemoKeys struct {
	Attach  key.Binding
	Restart key.Binding
	Kill    key.Binding
	Quit    key.Binding
}

func bind(desc string, keys ...string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(helpKey(keys), desc))
}

// DefaultKeyMap returns the built-in bindings.
func DefaultKeyMap() *KeyMap {
	return &KeyMap{
		Global: GlobalKeys{
			Quit:       bind("quit", "q"),
			ForceQuit:  bind("force quit", "ctrl+c"),
			Help:       bind("help", "?"),
			NextTab:    bind("next tab", "tab"),
			PrevTab:    bind("prev tab", "shift+tab"),
			GotoTab:    bind("go to tab", "1", "2", "3", "4", "5", "6", "7", "8", "9"),
			Refresh:    bind("refresh", "r"),
			RefreshAll: bind("refresh all", "R"),
			Palette:    bind("commands", "ctrl+k"),
			Projects:   bind("switch project", "ctrl+p"),

			GotoSession: bind("go to session", "ctrl+g"),
			Capture:     bind("quick capture", "ctrl+n"),
		},
		List: ListKeys{
			Up:         bind("up", "k", "up"),
			Down:       bind("down", "j", "down"),
			Top:        bind("top", "g", "home"),
			Bottom:     bind("bottom", "G", "end"),
			PageDown:   bind("half page down", "ctrl+d"),
			PageUp:     bind("half page up", "ctrl+u"),
			Filter:     bind("filter", "/"),
			GrowPane:   bind("widen list", ">"),
			ShrinkPane: bind("narrow list", "<"),
		},
		Input: InputKeys{
			Cancel:     bind("cancel", "esc"),
			Accept:     bind("accept", "enter"),
			DeleteBack: bind("delete char", "backspace"),
			ClearInput: bind("clear input", "ctrl+u"),
			Next:       bind("next", "down", "ctrl+n"),
			Prev:       bind("prev", "up", "ctrl+p"),
		},
		Sessions: SessionKeys{
			New:         bind("new session", "n"),
			Kill:        bind("kill session", "x"),
			Delete:      bind("delete session", "D"),
			ArchiveView: bind("archived view", "a"),
			Archive:     bind("archive / restore", "A"),
			Attach:      bind("attach", "enter"),
			Resume:      bind("background resume", "b"),
			Transcript:  bind("transcript", "t"),
			OpenPR:      bind("open linked PR", "o"),
			OpenIssue:   bind("open linked issue", "O"),
		},
		PRs: PRKeys{
			NextSection: bind("next section", "l", "right"),
			PrevSection: bind("prev section", "h", "left"),
			RepoFilter:  bind("filter by repo", "f"),
			StaleOnly:   bind("stale only", "S"),
			DetailNext:  bind("next detail tab", "]"),
			DetailPrev:  bind("prev detail tab", "["),
			DetailDown:  bind("scroll detail down", "J"),
			DetailUp:    bind("scroll detail up", "K"),

			Open:         bind("open in browser", "o"),
			CopyURL:      bind("copy URL", "y"),
			Checkout:     bind("check out", "c"),
			UpdateBranch: bind("update branch", "u"),
			Merge:        bind("merge", "m"),
			AutoMerge:    bind("auto-merge", "M"),
			ToggleDraft:  bind("toggle draft", "d"),
			Session:      bind("start session", "s"),
			Diff:         bind("review diff", "v"),
			Runs:         bind("pipeline runs", "p"),
			Fix:          bind("fix failing checks", "F"),
			FixEdit:      bind("fix (edit prompt)", "alt+f"),
		},
		Pipelines: PipelineKeys{
			Filters:      bind("filters", "f"),
			Mine:         bind("mine only", "m"),
			ClearFilters: bind("clear filters", "x"),
			DetailDown:   bind("scroll detail down", "J"),
			DetailUp:     bind("scroll detail up", "K"),
			Open:         bind("open in browser", "o"),
			Logs:         bind("job logs", "l"),
			RerunFailed:  bind("rerun failed jobs", "t"),
			RerunAll:     bind("rerun whole run", "ctrl+r"),
			CancelRun:    bind("cancel run", "X"),
			Fix:          bind("fix this", "F"),
			FixEdit:      bind("fix (edit prompt)", "alt+f"),
		},
		Plane: PlaneKeys{
			Filters:      bind("filters", "f"),
			Mine:         bind("mine only", "m"),
			ClearFilters: bind("clear filters", "x"),
			DetailDown:   bind("scroll detail down", "J"),
			DetailUp:     bind("scroll detail up", "K"),
			Open:         bind("open in browser", "o"),
			CopyURL:      bind("copy URL", "y"),

			Board:          bind("board / list", "b"),
			ColLeft:        bind("prev column", "h", "left"),
			ColRight:       bind("next column", "l", "right"),
			MovePrev:       bind("move to prev state", "H"),
			MoveNext:       bind("move to next state", "L"),
			CollapseColumn: bind("collapse column", "z"),
			CollapseEmpty:  bind("collapse empty columns", "Z"),
			OpenCard:       bind("open card", "enter"),

			New:     bind("new issue", "n"),
			Edit:    bind("edit", "e"),
			Comment: bind("comment", "c"),
			Session: bind("start session", "s"),

			Cycle: bind("current cycle", "C"),
		},
		Docs: DocKeys{
			New:        bind("new doc", "n"),
			Edit:       bind("edit in $EDITOR", "e"),
			Rename:     bind("rename / move", "m"),
			Delete:     bind("delete (to .trash)", "D"),
			Open:       bind("open / expand", "enter", "l", "right"),
			Back:       bind("back / collapse", "h", "left"),
			DetailDown: bind("scroll detail down", "J"),
			DetailUp:   bind("scroll detail up", "K"),
			Mark:       bind("mark for agent", "space"),
			Send:       bind("send to agent", "s"),
			Decision:   bind("log decision", "a"),
			Decisions:  bind("decisions view", "d"),
			Supersede:  bind("mark superseded", "S"),
		},
		Artifacts: ArtifactKeys{
			Open:         bind("open", "o"),
			CopyPath:     bind("copy path", "y"),
			Keep:         bind("keep a copy", "c"),
			Pin:          bind("pin / unpin", "p"),
			Tags:         bind("edit tags", "t"),
			Rename:       bind("rename", "m"),
			Filters:      bind("filters", "f"),
			PinnedOnly:   bind("pinned only", "P"),
			ClearFilters: bind("clear filters", "x"),
		},
		Transcript: TranscriptKeys{
			Up:        bind("prev entry", "k", "up"),
			Down:      bind("next entry", "j", "down"),
			PageUp:    bind("page up", "ctrl+u", "pgup"),
			PageDown:  bind("page down", "ctrl+d", "pgdown"),
			Top:       bind("first entry", "g", "home"),
			Bottom:    bind("last entry", "G", "end"),
			Toggle:    bind("expand tool call", "enter"),
			ToggleAll: bind("expand all", "e"),
			Follow:    bind("follow", "f"),
			Close:     bind("close", "esc", "q"),
		},
		Diff: DiffKeys{
			Up:             bind("scroll up", "k", "up"),
			Down:           bind("scroll down", "j", "down"),
			PageUp:         bind("page up", "ctrl+u", "pgup"),
			PageDown:       bind("page down", "ctrl+d", "pgdown", "space"),
			Top:            bind("top", "g", "home"),
			Bottom:         bind("bottom", "G", "end"),
			NextFile:       bind("next file", "n"),
			PrevFile:       bind("prev file", "p"),
			Approve:        bind("approve", "a"),
			RequestChanges: bind("request changes", "x"),
			Comment:        bind("comment", "C"),
			Open:           bind("open in browser", "o"),
			Close:          bind("close", "esc", "q"),
		},
		Log: LogKeys{
			Up:          bind("scroll up", "k", "up"),
			Down:        bind("scroll down", "j", "down"),
			PageUp:      bind("page up", "ctrl+u", "pgup"),
			PageDown:    bind("page down", "ctrl+d", "pgdown", "space"),
			Top:         bind("top", "g", "home"),
			Bottom:      bind("bottom", "G", "end"),
			ToggleFull:  bind("full log / errors", "e"),
			Search:      bind("search", "/"),
			NextMatch:   bind("next match", "n"),
			PrevMatch:   bind("prev match", "N"),
			NextJob:     bind("next failed job", "]"),
			PrevJob:     bind("prev failed job", "["),
			RerunFailed: bind("rerun failed jobs", "t", "R"),
			RerunAll:    bind("rerun whole run", "ctrl+r"),
			CancelRun:   bind("cancel run", "X"),
			Fix:         bind("fix this", "F"),
			FixEdit:     bind("fix (edit prompt)", "alt+f"),
			Open:        bind("open job in browser", "o"),
			Close:       bind("close", "esc", "q"),
		},
		Capture: CaptureKeys{
			Submit:       bind("create", "enter"),
			FullForm:     bind("full form", "shift+enter", "alt+enter"),
			Complete:     bind("complete", "tab"),
			CompletePrev: bind("prev completion", "shift+tab"),
			Left:         bind("left", "left", "ctrl+b"),
			Right:        bind("right", "right", "ctrl+f"),
			Home:         bind("start of line", "home", "ctrl+a"),
			End:          bind("end of line", "end", "ctrl+e"),
			DeleteWord:   bind("delete word", "ctrl+w", "alt+backspace"),
			DeleteFwd:    bind("delete char under cursor", "delete"),
		},
		Demo: DemoKeys{
			Replay:  bind("replay splash", "r"),
			Burst:   bind("spark burst", "space"),
			Full:    bind("full", "1"),
			Reduced: bind("reduced", "2"),
			Off:     bind("off", "3"),
			Quit:    bind("quit", "q", "esc", "ctrl+c"),
		},
		Tmux: TmuxDemoKeys{
			Attach:  bind("attach", "enter"),
			Restart: bind("new window", "n"),
			Kill:    bind("kill window", "x"),
			Quit:    bind("quit", "q", "ctrl+c"),
		},
	}
}

// action names a binding for config overrides and palette commands.
type action struct {
	id    string
	scope string // bindings in the same scope must not share keys
	b     *key.Binding
}

// Scopes group actions that are active at the same time; a key may be bound
// once per scope. Tab scopes are active together with scopeMain, so their
// keys must not clash with it either, but two tabs may reuse a key.
const (
	scopeMain     = "main" // global + list keys, active together
	scopeInput    = "input"
	scopeSessions = "tab:sessions"
	scopePRs      = "tab:prs"
	scopePipes    = "tab:pipelines"
	scopePlane    = "tab:plane"
	scopeDocs     = "tab:docs"
	scopeArts     = "tab:artifacts"
	// scopeTranscript is modal: while the viewer is open only its keys
	// (and force quit) are live, so it is checked on its own.
	scopeTranscript = "transcript"
	// scopeDiff is modal too: the PR diff viewer (P2.4).
	scopeDiff = "diff"
	// scopeLog is modal too: the job log viewer (P2.7).
	scopeLog = "log"
	// scopeCapture is modal too: the quick capture line (P3.5), live
	// with the input keys.
	scopeCapture = "capture"
)

// isTabScope reports whether scope belongs to one tab (and is active
// together with scopeMain).
func isTabScope(scope string) bool { return strings.HasPrefix(scope, "tab:") }

func (k *KeyMap) actions() []action {
	g, l, in, ss, pr, tr, df := &k.Global, &k.List, &k.Input, &k.Sessions, &k.PRs, &k.Transcript, &k.Diff
	pp, lg, pl, cp, dc, ar := &k.Pipelines, &k.Log, &k.Plane, &k.Capture, &k.Docs, &k.Artifacts
	return []action{
		{"quit", scopeMain, &g.Quit},
		{"force_quit", scopeMain, &g.ForceQuit},
		{"help", scopeMain, &g.Help},
		{"next_tab", scopeMain, &g.NextTab},
		{"prev_tab", scopeMain, &g.PrevTab},
		{"goto_tab", scopeMain, &g.GotoTab},
		{"refresh", scopeMain, &g.Refresh},
		{"refresh_all", scopeMain, &g.RefreshAll},
		{"palette", scopeMain, &g.Palette},
		{"projects", scopeMain, &g.Projects},
		{"goto_session", scopeMain, &g.GotoSession},
		{"capture", scopeMain, &g.Capture},
		{"up", scopeMain, &l.Up},
		{"down", scopeMain, &l.Down},
		{"top", scopeMain, &l.Top},
		{"bottom", scopeMain, &l.Bottom},
		{"page_down", scopeMain, &l.PageDown},
		{"page_up", scopeMain, &l.PageUp},
		{"filter", scopeMain, &l.Filter},
		{"grow_pane", scopeMain, &l.GrowPane},
		{"shrink_pane", scopeMain, &l.ShrinkPane},
		{"session_new", scopeSessions, &ss.New},
		{"session_kill", scopeSessions, &ss.Kill},
		{"session_delete", scopeSessions, &ss.Delete},
		{"session_archive_view", scopeSessions, &ss.ArchiveView},
		{"session_archive", scopeSessions, &ss.Archive},
		{"session_attach", scopeSessions, &ss.Attach},
		{"session_resume", scopeSessions, &ss.Resume},
		{"session_transcript", scopeSessions, &ss.Transcript},
		{"session_open_pr", scopeSessions, &ss.OpenPR},
		{"session_open_issue", scopeSessions, &ss.OpenIssue},
		{"pr_next_section", scopePRs, &pr.NextSection},
		{"pr_prev_section", scopePRs, &pr.PrevSection},
		{"pr_repo_filter", scopePRs, &pr.RepoFilter},
		{"pr_stale_only", scopePRs, &pr.StaleOnly},
		{"pr_detail_next", scopePRs, &pr.DetailNext},
		{"pr_detail_prev", scopePRs, &pr.DetailPrev},
		{"pr_detail_down", scopePRs, &pr.DetailDown},
		{"pr_detail_up", scopePRs, &pr.DetailUp},
		{"pr_open", scopePRs, &pr.Open},
		{"pr_copy_url", scopePRs, &pr.CopyURL},
		{"pr_checkout", scopePRs, &pr.Checkout},
		{"pr_update_branch", scopePRs, &pr.UpdateBranch},
		{"pr_merge", scopePRs, &pr.Merge},
		{"pr_auto_merge", scopePRs, &pr.AutoMerge},
		{"pr_toggle_draft", scopePRs, &pr.ToggleDraft},
		{"pr_session", scopePRs, &pr.Session},
		{"pr_diff", scopePRs, &pr.Diff},
		{"pr_runs", scopePRs, &pr.Runs},
		{"pr_fix", scopePRs, &pr.Fix},
		{"pr_fix_edit", scopePRs, &pr.FixEdit},
		{"pipeline_filters", scopePipes, &pp.Filters},
		{"pipeline_mine", scopePipes, &pp.Mine},
		{"pipeline_clear_filters", scopePipes, &pp.ClearFilters},
		{"pipeline_detail_down", scopePipes, &pp.DetailDown},
		{"pipeline_detail_up", scopePipes, &pp.DetailUp},
		{"pipeline_open", scopePipes, &pp.Open},
		{"pipeline_logs", scopePipes, &pp.Logs},
		{"pipeline_rerun_failed", scopePipes, &pp.RerunFailed},
		{"pipeline_rerun", scopePipes, &pp.RerunAll},
		{"pipeline_cancel", scopePipes, &pp.CancelRun},
		{"pipeline_fix", scopePipes, &pp.Fix},
		{"pipeline_fix_edit", scopePipes, &pp.FixEdit},
		{"plane_filters", scopePlane, &pl.Filters},
		{"plane_mine", scopePlane, &pl.Mine},
		{"plane_clear_filters", scopePlane, &pl.ClearFilters},
		{"plane_detail_down", scopePlane, &pl.DetailDown},
		{"plane_detail_up", scopePlane, &pl.DetailUp},
		{"plane_open", scopePlane, &pl.Open},
		{"plane_copy_url", scopePlane, &pl.CopyURL},
		{"plane_board", scopePlane, &pl.Board},
		{"plane_col_left", scopePlane, &pl.ColLeft},
		{"plane_col_right", scopePlane, &pl.ColRight},
		{"plane_move_prev", scopePlane, &pl.MovePrev},
		{"plane_move_next", scopePlane, &pl.MoveNext},
		{"plane_collapse_column", scopePlane, &pl.CollapseColumn},
		{"plane_collapse_empty", scopePlane, &pl.CollapseEmpty},
		{"plane_open_card", scopePlane, &pl.OpenCard},
		{"plane_new", scopePlane, &pl.New},
		{"plane_edit", scopePlane, &pl.Edit},
		{"plane_comment", scopePlane, &pl.Comment},
		{"plane_session", scopePlane, &pl.Session},
		{"plane_cycle", scopePlane, &pl.Cycle},
		{"docs_new", scopeDocs, &dc.New},
		{"docs_edit", scopeDocs, &dc.Edit},
		{"docs_rename", scopeDocs, &dc.Rename},
		{"docs_delete", scopeDocs, &dc.Delete},
		{"docs_open", scopeDocs, &dc.Open},
		{"docs_back", scopeDocs, &dc.Back},
		{"docs_detail_down", scopeDocs, &dc.DetailDown},
		{"docs_detail_up", scopeDocs, &dc.DetailUp},
		{"docs_mark", scopeDocs, &dc.Mark},
		{"docs_send", scopeDocs, &dc.Send},
		{"docs_decision", scopeDocs, &dc.Decision},
		{"docs_decisions", scopeDocs, &dc.Decisions},
		{"docs_supersede", scopeDocs, &dc.Supersede},
		{"artifacts_open", scopeArts, &ar.Open},
		{"artifacts_copy_path", scopeArts, &ar.CopyPath},
		{"artifacts_keep", scopeArts, &ar.Keep},
		{"artifacts_pin", scopeArts, &ar.Pin},
		{"artifacts_tags", scopeArts, &ar.Tags},
		{"artifacts_rename", scopeArts, &ar.Rename},
		{"artifacts_filters", scopeArts, &ar.Filters},
		{"artifacts_pinned_only", scopeArts, &ar.PinnedOnly},
		{"artifacts_clear_filters", scopeArts, &ar.ClearFilters},
		{"transcript_up", scopeTranscript, &tr.Up},
		{"transcript_down", scopeTranscript, &tr.Down},
		{"transcript_page_up", scopeTranscript, &tr.PageUp},
		{"transcript_page_down", scopeTranscript, &tr.PageDown},
		{"transcript_top", scopeTranscript, &tr.Top},
		{"transcript_bottom", scopeTranscript, &tr.Bottom},
		{"transcript_toggle", scopeTranscript, &tr.Toggle},
		{"transcript_toggle_all", scopeTranscript, &tr.ToggleAll},
		{"transcript_follow", scopeTranscript, &tr.Follow},
		{"transcript_close", scopeTranscript, &tr.Close},
		{"diff_up", scopeDiff, &df.Up},
		{"diff_down", scopeDiff, &df.Down},
		{"diff_page_up", scopeDiff, &df.PageUp},
		{"diff_page_down", scopeDiff, &df.PageDown},
		{"diff_top", scopeDiff, &df.Top},
		{"diff_bottom", scopeDiff, &df.Bottom},
		{"diff_next_file", scopeDiff, &df.NextFile},
		{"diff_prev_file", scopeDiff, &df.PrevFile},
		{"diff_approve", scopeDiff, &df.Approve},
		{"diff_request_changes", scopeDiff, &df.RequestChanges},
		{"diff_comment", scopeDiff, &df.Comment},
		{"diff_open", scopeDiff, &df.Open},
		{"diff_close", scopeDiff, &df.Close},
		{"log_up", scopeLog, &lg.Up},
		{"log_down", scopeLog, &lg.Down},
		{"log_page_up", scopeLog, &lg.PageUp},
		{"log_page_down", scopeLog, &lg.PageDown},
		{"log_top", scopeLog, &lg.Top},
		{"log_bottom", scopeLog, &lg.Bottom},
		{"log_toggle_full", scopeLog, &lg.ToggleFull},
		{"log_search", scopeLog, &lg.Search},
		{"log_next_match", scopeLog, &lg.NextMatch},
		{"log_prev_match", scopeLog, &lg.PrevMatch},
		{"log_next_job", scopeLog, &lg.NextJob},
		{"log_prev_job", scopeLog, &lg.PrevJob},
		{"log_rerun_failed", scopeLog, &lg.RerunFailed},
		{"log_rerun", scopeLog, &lg.RerunAll},
		{"log_cancel", scopeLog, &lg.CancelRun},
		{"log_fix", scopeLog, &lg.Fix},
		{"log_fix_edit", scopeLog, &lg.FixEdit},
		{"log_open", scopeLog, &lg.Open},
		{"log_close", scopeLog, &lg.Close},
		{"capture_submit", scopeCapture, &cp.Submit},
		{"capture_full_form", scopeCapture, &cp.FullForm},
		{"capture_complete", scopeCapture, &cp.Complete},
		{"capture_complete_prev", scopeCapture, &cp.CompletePrev},
		{"capture_left", scopeCapture, &cp.Left},
		{"capture_right", scopeCapture, &cp.Right},
		{"capture_home", scopeCapture, &cp.Home},
		{"capture_end", scopeCapture, &cp.End},
		{"capture_delete_word", scopeCapture, &cp.DeleteWord},
		{"capture_delete_forward", scopeCapture, &cp.DeleteFwd},
		{"cancel", scopeInput, &in.Cancel},
		{"accept", scopeInput, &in.Accept},
		{"delete_back", scopeInput, &in.DeleteBack},
		{"clear_input", scopeInput, &in.ClearInput},
		{"next", scopeInput, &in.Next},
		{"prev", scopeInput, &in.Prev},
	}
}

// ActionIDs returns the names accepted in the keybinds config section, in
// keymap order.
func ActionIDs() []string {
	acts := DefaultKeyMap().actions()
	ids := make([]string, len(acts))
	for i, a := range acts {
		ids[i] = a.id
	}
	return ids
}

// NewKeyMap returns the default keymap with config overrides (action id ->
// keys) applied. Overriding an action replaces its keys and its help text.
// Unknown actions and keys bound to two actions in the same scope are errors.
func NewKeyMap(overrides map[string][]string) (*KeyMap, error) {
	k := DefaultKeyMap()
	acts := k.actions()
	byID := make(map[string]*action, len(acts))
	for i := range acts {
		byID[acts[i].id] = &acts[i]
	}

	var errs []error
	ids := make([]string, 0, len(overrides))
	for id := range overrides {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a, ok := byID[id]
		if !ok {
			errs = append(errs, fmt.Errorf("keybinds.%s: unknown action; known: %s", id, strings.Join(ActionIDs(), ", ")))
			continue
		}
		keys := overrides[id]
		a.b.SetKeys(keys...)
		a.b.SetHelp(helpKey(keys), a.b.Help().Desc)
	}

	errs = append(errs, conflicts(acts, overrides)...)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return k, nil
}

// conflicts reports keys bound twice in one scope, or by a tab action and a
// main one. Each is reported against the overridden action, so the error
// points at the line the user changed.
func conflicts(acts []action, overrides map[string][]string) []error {
	var errs []error
	conflict := func(a, b, kk string) {
		id, other := a, b
		if _, ok := overrides[id]; !ok {
			id, other = b, a
		}
		errs = append(errs, fmt.Errorf("keybinds.%s: %q is already bound to %s", id, kk, other))
	}
	owner := map[string]map[string]string{} // scope -> key -> action id
	for _, a := range acts {
		if owner[a.scope] == nil {
			owner[a.scope] = map[string]string{}
		}
		for _, kk := range a.b.Keys() {
			prev, taken := owner[a.scope][kk]
			if !taken || prev == a.id {
				owner[a.scope][kk] = a.id
				continue
			}
			conflict(a.id, prev, kk)
		}
	}
	// A tab's keys are live together with the main ones.
	for _, a := range acts {
		if !isTabScope(a.scope) {
			continue
		}
		for _, kk := range a.b.Keys() {
			if prev, taken := owner[scopeMain][kk]; taken {
				conflict(a.id, prev, kk)
			}
		}
	}
	return errs
}

// keyNames are the help-text spellings of special keys.
var keyNames = map[string]string{
	"up":    "↑",
	"down":  "↓",
	"left":  "←",
	"right": "→",
}

// helpKey renders keys for help text: "k/↑", or "1-9" for long runs.
func helpKey(keys []string) string {
	names := make([]string, len(keys))
	for i, k := range keys {
		if n, ok := keyNames[k]; ok {
			k = n
		}
		names[i] = k
	}
	if len(names) > 3 {
		return names[0] + "-" + names[len(names)-1]
	}
	return strings.Join(names, "/")
}

// GlobalHelp returns the bindings that work on every tab, for the help overlay.
func (k *KeyMap) GlobalHelp() []key.Binding {
	g := k.Global
	return []key.Binding{g.Palette, g.Projects, g.GotoSession, g.Capture, g.GotoTab, g.NextTab, g.PrevTab, g.Refresh, g.RefreshAll, g.Help, g.Quit, g.ForceQuit}
}

// CaptureHelp returns the quick capture line's keys, for the status bar
// and `forge capture`.
func (k *KeyMap) CaptureHelp() []key.Binding {
	c := k.Capture
	return []key.Binding{c.Submit, c.FullForm, c.Complete, k.Input.Cancel}
}

// HuhKeyMap is the key map for Huh forms embedded in forge: Huh's defaults,
// except that the input Cancel keys (esc) abandon the form. ctrl+c never
// reaches a form inside the TUI (it force-quits forge).
func (k *KeyMap) HuhKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys(k.Input.Cancel.Keys()...), key.WithHelp(k.Input.Cancel.Help().Key, "cancel"))
	return km
}

// FormHelp is the help for embedded Huh forms, for the status bar and the
// help overlay. The keys mirror HuhKeyMap.
func (k *KeyMap) FormHelp() []key.Binding {
	km := k.HuhKeyMap()
	return []key.Binding{km.Quit, km.Input.Prev, km.Text.NewLine, km.Input.Next}
}

// TabIndex returns the zero-based tab index for a GotoTab key press (the
// position of the key in the binding), or -1 if it is not a GotoTab key.
func (k *KeyMap) TabIndex(msg tea.KeyPressMsg) int {
	if !key.Matches(msg, k.Global.GotoTab) {
		return -1
	}
	return slices.Index(k.Global.GotoTab.Keys(), msg.String())
}
