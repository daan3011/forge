package sessions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/sessions/claude"
	"github.com/daan3011/forge/internal/sessions/hooks"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/transcript"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/store"
)

// EnvSessionName is set in every agent window to the forge session name, so
// hooks (P1.4) can tell which session they belong to.
const EnvSessionName = "FORGE_SESSION"

// DefaultBase is the base branch when none is given.
const DefaultBase = "main"

// promptFileName is the first prompt's file inside a session's data dir.
const promptFileName = "prompt.md"

var (
	// ErrNameTaken means a session (active or archived) already has the name.
	ErrNameTaken = errors.New("session name already taken")
	// ErrUnknownProject means the session's project is not configured.
	ErrUnknownProject = errors.New("project not configured")
	// ErrNoProjectPath means the project has no local checkout to branch from.
	ErrNoProjectPath = errors.New("project has no path configured")
	// ErrArchived means the action is not possible on an archived session.
	ErrArchived = errors.New("session is archived")
	// ErrNotArchived means Delete was called on a session that isn't archived.
	ErrNotArchived = errors.New("only archived sessions can be deleted")
	// ErrNoWindow means the session has no tmux window to look at.
	ErrNoWindow = errors.New("session has no tmux window")
	// ErrWorktreeMissing means the session's worktree directory is gone;
	// Resume with RecreateWorktree brings it back from the branch.
	ErrWorktreeMissing = errors.New("session worktree is gone")
	// ErrBranchInUse means the branch is checked out in another worktree.
	ErrBranchInUse = errors.New("branch is checked out elsewhere")
	// ErrNotOwnBranch means forge was asked to delete a branch it didn't
	// create (a pre-existing or PR head branch); it never does.
	ErrNotOwnBranch = errors.New("forge didn't create this branch")
	// ErrExternalWorktree means the session runs in a worktree forge
	// didn't create, which it never removes or recreates.
	ErrExternalWorktree = errors.New("worktree isn't forge's")
)

// BranchInUseError reports that a new session's branch is checked out in
// another worktree at Path. Session names the forge session running there,
// "" when the worktree isn't a forge session's (e.g. the main checkout).
// Starting again with StartOpts.Worktree = Path runs the session there.
type BranchInUseError struct {
	Branch  string
	Path    string
	Session string
}

func (e *BranchInUseError) Error() string {
	if e.Session != "" {
		return fmt.Sprintf("branch %s is checked out by session %s (%s)", e.Branch, e.Session, e.Path)
	}
	return fmt.Sprintf("branch %s is checked out at %s", e.Branch, e.Path)
}

// Is makes errors.Is(err, ErrBranchInUse) true.
func (e *BranchInUseError) Is(target error) bool { return target == ErrBranchInUse }

// WorktreeMissingError reports a session whose worktree is gone.
type WorktreeMissingError struct {
	Path   string
	Branch string
}

func (e *WorktreeMissingError) Error() string {
	return fmt.Sprintf("worktree %s is gone", e.Path)
}

// Is makes errors.Is(err, ErrWorktreeMissing) true.
func (e *WorktreeMissingError) Is(target error) bool { return target == ErrWorktreeMissing }

// Store is the persistence the service needs. StoreFrom adapts *store.Store.
type Store interface {
	Create(ctx context.Context, x store.Session) (store.Session, error)
	GetByName(ctx context.Context, name string) (store.Session, error)
	List(ctx context.Context, f store.SessionFilter) ([]store.Session, error)
	SetStatus(ctx context.Context, id int64, st store.Status, at time.Time) error
	SetStatusIf(ctx context.Context, id int64, from, to store.Status, at time.Time) (bool, error)
	SetWindow(ctx context.Context, id int64, tmuxSession, window string) error
	SetClaudeID(ctx context.Context, id int64, claudeID string) error
	Update(ctx context.Context, x store.Session) error
	LatestEvent(ctx context.Context, claudeID string) (store.Event, error)
	ArchiveRemoved(ctx context.Context, id int64, at time.Time) error
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (store.Session, error)
	LinksFor(ctx context.Context, ref store.Ref) ([]store.Link, error)
	AddLink(ctx context.Context, from, to store.Ref, meta store.LinkMeta) error
	LinkedSessions(ctx context.Context, ref store.Ref) ([]int64, error)
}

type storeAdapter struct {
	*store.Sessions
	links  *store.Links
	events *store.Events
}

func (a storeAdapter) LatestEvent(ctx context.Context, claudeID string) (store.Event, error) {
	return a.events.Latest(ctx, claudeID)
}

func (a storeAdapter) LinksFor(ctx context.Context, ref store.Ref) ([]store.Link, error) {
	return a.links.For(ctx, ref)
}

func (a storeAdapter) AddLink(ctx context.Context, from, to store.Ref, meta store.LinkMeta) error {
	return a.links.Add(ctx, from, to, meta)
}

func (a storeAdapter) LinkedSessions(ctx context.Context, ref store.Ref) ([]int64, error) {
	return a.links.LinkedSessions(ctx, ref)
}

// StoreFrom returns the Store backed by the forge database.
func StoreFrom(st *store.Store) Store {
	return storeAdapter{Sessions: st.Sessions(), links: st.Links(), events: st.Events()}
}

// Config wires a Service.
type Config struct {
	Tmux      tmux.Driver
	Worktrees worktree.Manager
	Store     Store
	Projects  []config.Project
	// DataDir holds per-session files (<DataDir>/sessions/<name>/).
	DataDir string
	// ClaudeBin is the claude executable; empty means "claude".
	ClaudeBin string
	// ForgeBin is the absolute path of the forge binary that Claude Code
	// hooks in new worktrees call (`forge hook <event>`, P1.4); empty
	// installs no hooks.
	ForgeBin string
	// NewID makes Claude session ids; nil means claude.NewSessionID.
	NewID func() (string, error)
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// TmuxConf is the config file forge's tmux server starts with, and
	// TmuxColors the colours its default is rendered with.
	TmuxConf   string
	TmuxColors tmux.ConfigColors
	// ProjectsDir is where Claude Code keeps transcripts; empty means
	// claude.DefaultProjectsDir().
	ProjectsDir string
	// Pulls finds the PRs opened from a session's branch (P1.9); nil
	// disables SyncPRs.
	Pulls github.PullFinder
	// StartupCheck is how long a resumed session that had to start afresh
	// is watched for claude refusing its session id; zero means
	// DefaultStartupCheck. The pane is always checked at least once.
	StartupCheck time.Duration
}

// DefaultStartupCheck is the default Config.StartupCheck.
const DefaultStartupCheck = 2 * time.Second

// startupPoll is how often the startup check looks at the window.
const startupPoll = 200 * time.Millisecond

// Service starts, lists, previews and kills agent sessions. It does blocking
// I/O and must only be called from a tea.Cmd or the CLI.
type Service struct {
	c           Config
	transcripts *transcript.Cache
}

// New returns a Service.
func New(c Config) *Service {
	if c.NewID == nil {
		c.NewID = claude.NewSessionID
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.ProjectsDir == "" {
		c.ProjectsDir = claude.DefaultProjectsDir()
	}
	if c.StartupCheck == 0 {
		c.StartupCheck = DefaultStartupCheck
	}
	return &Service{c: c, transcripts: transcript.NewCache()}
}

// Transcript parses the transcript of x (P1.6). Files are parsed
// incrementally, so loading the same session again only reads new lines.
// It returns transcript.ErrNoTranscript when there is none yet.
func (s *Service) Transcript(_ context.Context, x store.Session) (transcript.Transcript, error) {
	path, err := transcript.Locate(x, s.c.ProjectsDir)
	if err != nil {
		return transcript.Transcript{}, err
	}
	return s.transcripts.Load(path)
}

// Project returns the configured project called name.
func (s *Service) Project(name string) (config.Project, bool) {
	for _, p := range s.c.Projects {
		if p.Name == name {
			return p, true
		}
	}
	return config.Project{}, false
}

// SessionDir is where a session's files (the first prompt) live.
func (s *Service) SessionDir(name string) string {
	return filepath.Join(s.c.DataDir, "sessions", name)
}

// StartOpts are the inputs to Start.
type StartOpts struct {
	Name     string
	Project  string // project name
	Base     string // base branch; empty means DefaultBase
	Template string // template name, recorded only (P1.7)
	Prompt   string // first prompt; empty starts claude without one
	// Branch is the branch to work on; empty means agent/<name>. See
	// worktree.CreateOpts.Branch for how an existing branch is used.
	Branch string
	// Worktree runs the session in an existing worktree that isn't forge's
	// (the path from a BranchInUseError) instead of creating one. forge
	// never removes it and never deletes its branch.
	Worktree string
	// Links are the issue / PR the session is for, saved with it (P1.9).
	Links []LinkSpec
}

// LinkSpec is a link a new session gets: To and what forge knows of it.
type LinkSpec struct {
	To   store.Ref
	Meta store.LinkMeta
}

// Started is a new session.
type Started struct {
	Session store.Session
	// FetchErr is set when the base could not be fetched and the local base
	// branch was used (e.g. offline).
	FetchErr error
}

// Start creates the worktree, writes the first prompt to the session's data
// dir, opens a tmux window in the worktree running claude with a fresh
// session id, and saves the session. Anything already done is undone if a
// later step fails.
func (s *Service) Start(ctx context.Context, o StartOpts) (res Started, err error) {
	if err := worktree.CheckName(o.Name); err != nil {
		return Started{}, err
	}
	p, ok := s.Project(o.Project)
	if !ok {
		return Started{}, fmt.Errorf("%w: %q", ErrUnknownProject, o.Project)
	}
	if p.Path == "" {
		return Started{}, fmt.Errorf("%w: %s", ErrNoProjectPath, p.Name)
	}
	if o.Base == "" {
		o.Base = DefaultBase
	}
	switch _, err := s.c.Store.GetByName(ctx, o.Name); {
	case err == nil:
		return Started{}, fmt.Errorf("%w: %s", ErrNameTaken, o.Name)
	case !errors.Is(err, store.ErrNotFound):
		return Started{}, err
	}

	var undo []func(context.Context) error
	defer func() {
		if err == nil {
			return
		}
		// Undo with a fresh context: ctx may be what just expired.
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		var errs []error
		for i := len(undo) - 1; i >= 0; i-- {
			errs = append(errs, undo[i](uctx))
		}
		if rbErr := errors.Join(errs...); rbErr != nil {
			err = errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
		}
	}()

	wt, err := s.worktree(ctx, p, o)
	if err != nil {
		return Started{}, err
	}
	if !o.external() {
		undo = append(undo, func(ctx context.Context) error {
			_, rmErr := s.c.Worktrees.Remove(ctx, worktree.RemoveOpts{Project: p, Session: o.Name, Force: true})
			if rmErr != nil || !wt.BranchCreated {
				return rmErr
			}
			return s.c.Worktrees.DeleteBranch(ctx, p, wt.Branch, true)
		})
	}

	if s.c.ForgeBin != "" {
		// The worktree's removal (undo above) takes the settings file along;
		// in an external worktree it stays (it is git-ignored and harmless).
		if err := hooks.Install(wt.Path, s.c.ForgeBin); err != nil {
			return Started{}, err
		}
		if err := s.c.Worktrees.EnsureExcluded(ctx, wt.Path, hooks.SettingsRel); err != nil {
			return Started{}, fmt.Errorf("git-ignore hook settings: %w", err)
		}
	}

	id, err := s.c.NewID()
	if err != nil {
		return Started{}, err
	}
	launch := claude.Launch{Bin: s.c.ClaudeBin, SessionID: id}
	if o.Prompt != "" {
		dir := s.SessionDir(o.Name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Started{}, fmt.Errorf("session dir: %w", err)
		}
		undo = append(undo, func(context.Context) error { return os.RemoveAll(dir) })
		launch.PromptFile = filepath.Join(dir, promptFileName)
		if err := os.WriteFile(launch.PromptFile, []byte(o.Prompt), 0o600); err != nil {
			return Started{}, fmt.Errorf("write prompt: %w", err)
		}
	}
	argv, err := launch.Argv()
	if err != nil {
		return Started{}, err
	}

	tsess, err := s.c.Tmux.EnsureSession(ctx, p.Name)
	if err != nil {
		return Started{}, err
	}
	target, err := s.c.Tmux.NewWindow(ctx, tsess, o.Name, wt.Path, argv,
		map[string]string{EnvSessionName: o.Name})
	if err != nil {
		return Started{}, err
	}
	undo = append(undo, func(ctx context.Context) error { return s.c.Tmux.KillWindow(ctx, target) })

	x, err := s.c.Store.Create(ctx, store.Session{
		Name:             o.Name,
		ClaudeSessionID:  id,
		Project:          p.Name,
		Repo:             p.Repo,
		BaseBranch:       o.Base,
		Branch:           wt.Branch,
		Worktree:         wt.Path,
		TmuxSession:      target.Session,
		TmuxWindow:       target.Window,
		Template:         o.Template,
		Status:           store.StatusStarting,
		BranchCreated:    wt.BranchCreated,
		WorktreeExternal: o.external(),
	})
	if errors.Is(err, store.ErrConflict) {
		err = fmt.Errorf("%w: %s", ErrNameTaken, o.Name)
	}
	if err != nil {
		return Started{}, err
	}
	undo = append(undo, func(ctx context.Context) error { return s.c.Store.Delete(ctx, x.ID) })
	for _, l := range o.Links {
		if err := s.c.Store.AddLink(ctx, store.SessionRef(x.ID), l.To, l.Meta); err != nil {
			return Started{}, err
		}
	}
	return Started{Session: x, FetchErr: wt.FetchErr}, nil
}

func (o StartOpts) external() bool { return o.Worktree != "" }

// worktree creates the new session's worktree, or describes the external
// one it was asked to use. A branch checked out elsewhere becomes a
// *BranchInUseError naming the forge session there, if any.
func (s *Service) worktree(ctx context.Context, p config.Project, o StartOpts) (worktree.Created, error) {
	if o.external() {
		if !isDir(o.Worktree) {
			return worktree.Created{}, fmt.Errorf("worktree %s: %w", o.Worktree, os.ErrNotExist)
		}
		return worktree.Created{Path: o.Worktree, Branch: o.Branch, Reused: true}, nil
	}
	wt, err := s.c.Worktrees.Create(ctx, worktree.CreateOpts{Project: p, Session: o.Name, Base: o.Base, Branch: o.Branch})
	var co *worktree.BranchCheckedOutError
	if !errors.As(err, &co) {
		return wt, err
	}
	inUse := &BranchInUseError{Branch: co.Branch, Path: co.Path}
	archived := false
	xs, err := s.c.Store.List(ctx, store.SessionFilter{Project: p.Name, Archived: &archived})
	if err != nil {
		return wt, err
	}
	for _, x := range xs {
		if x.Worktree != "" && samePath(x.Worktree, co.Path) {
			inUse.Session = x.Name
			break
		}
	}
	return wt, inUse
}

// samePath compares paths with symlinks resolved: git reports resolved
// paths (macOS temp dirs live behind /var -> /private/var).
func samePath(a, b string) bool {
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	return resolve(a) == resolve(b)
}

// Row is a session as the list shows it.
type Row struct {
	store.Session
	// Link is the linked issue or PR, e.g. "#12" or "PLANE-7"; "" if none.
	Link string
	// Links names every linked issue and PR, for search: the full id
	// ("owner/repo#12", "PLANE-7") and, for PRs, the short "#12" too.
	Links []string
	// LinkList is every link of the session, oldest first, with the cached
	// url, title and state of the far end (P1.9 detail pane).
	LinkList []store.Link
}

// List returns the project's sessions (all projects when project is ""),
// the archive when archived is set. Before listing it marks sessions whose
// tmux window is gone or whose agent exited as dead.
func (s *Service) List(ctx context.Context, project string, archived bool) ([]Row, error) {
	f := store.SessionFilter{Project: project, Archived: &archived}
	xs, err := s.c.Store.List(ctx, f)
	if err != nil {
		return nil, err
	}
	if !archived {
		changed, err := s.sweep(ctx, xs)
		if err != nil {
			return nil, err
		}
		if changed {
			if xs, err = s.c.Store.List(ctx, f); err != nil {
				return nil, err
			}
		}
	}
	rows := make([]Row, len(xs))
	for i, x := range xs {
		rows[i] = Row{Session: x}
		links, err := s.c.Store.LinksFor(ctx, store.SessionRef(x.ID))
		if err != nil {
			return nil, err
		}
		rows[i].Link = linkLabel(links)
		rows[i].Links = linkNames(links)
		rows[i].LinkList = links
	}
	return rows, nil
}

// sweep marks live-looking sessions dead when their window is missing or
// their process exited. Hooks (P1.4) report every other status; this
// catches agents that died without a SessionEnd hook (killed window,
// crashed claude, tmux server restart).
func (s *Service) sweep(ctx context.Context, xs []store.Session) (changed bool, err error) {
	windows := map[string]map[string]tmux.Window{}
	now := s.c.Now()
	for _, x := range xs {
		if x.Status == store.StatusDead || x.Status == store.StatusArchived {
			continue
		}
		alive := false
		if x.TmuxSession != "" && x.TmuxWindow != "" {
			ws, ok := windows[x.TmuxSession]
			if !ok {
				list, err := s.c.Tmux.ListWindows(ctx, x.TmuxSession)
				if err != nil {
					return changed, err
				}
				ws = make(map[string]tmux.Window, len(list))
				for _, w := range list {
					ws[w.Name] = w
				}
				windows[x.TmuxSession] = ws
			}
			w, ok := ws[x.TmuxWindow]
			alive = ok && !w.Dead
		}
		if !alive {
			if err := s.c.Store.SetStatus(ctx, x.ID, store.StatusDead, now); err != nil {
				return changed, err
			}
			changed = true
		}
	}
	return changed, nil
}

// linkLabel picks the label of the newest open PR, else the newest PR,
// else the first issue.
func linkLabel(links []store.Link) string {
	if pr, ok := PrimaryLink(links, store.KindGitHubPR); ok {
		return ShortRef(pr.Other)
	}
	if issue, ok := PrimaryLink(links, store.KindPlaneIssue); ok {
		return issue.Other.ID
	}
	return ""
}

// ShortRef is a link's short label: "#12" for a PR, the key for an issue.
func ShortRef(r store.Ref) string {
	if r.Kind == store.KindGitHubPR {
		if _, num, ok := strings.Cut(r.ID, "#"); ok {
			return "#" + num
		}
	}
	return r.ID
}

// LinksOf returns the links of the given kind, the most relevant first:
// open (or draft) ones before the rest, newer before older.
func LinksOf(links []store.Link, kind store.LinkKind) []store.Link {
	var out []store.Link
	for _, l := range links {
		if l.Other.Kind == kind {
			out = append(out, l)
		}
	}
	open := func(l store.Link) bool { return l.State != "merged" && l.State != "closed" }
	slices.SortStableFunc(out, func(a, b store.Link) int {
		if oa, ob := open(a), open(b); oa != ob {
			if oa {
				return -1
			}
			return 1
		}
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return out
}

// PrimaryLink is the most relevant link of the kind (see LinksOf).
func PrimaryLink(links []store.Link, kind store.LinkKind) (store.Link, bool) {
	ls := LinksOf(links, kind)
	if len(ls) == 0 {
		return store.Link{}, false
	}
	return ls[0], true
}

// LinkedSessions returns the sessions linked to the issue or PR id (an
// issue key, or "owner/repo#12"), oldest link first. It is the query the
// PR and Plane tabs use to show a ticket's sessions.
func (s *Service) LinkedSessions(ctx context.Context, kind store.LinkKind, id string) ([]store.Session, error) {
	ids, err := s.c.Store.LinkedSessions(ctx, store.Ref{Kind: kind, ID: id})
	if err != nil {
		return nil, err
	}
	out := make([]store.Session, 0, len(ids))
	for _, sid := range ids {
		x, err := s.c.Store.Get(ctx, sid)
		switch {
		case errors.Is(err, store.ErrNotFound):
			continue // a link left behind by a deleted session
		case err != nil:
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}

// maxSyncedPulls is how many PRs per branch SyncPRs links.
const maxSyncedPulls = 5

// SyncPRs links every active session of the project (all projects when
// "") to the PRs opened from its branch, and refreshes the title and state
// of PRs already linked. A session whose branch is its base is skipped.
// changed reports whether any link was added or changed. Lookups that fail
// for one session are joined into err; the others still sync.
func (s *Service) SyncPRs(ctx context.Context, project string) (changed bool, err error) {
	if s.c.Pulls == nil {
		return false, nil
	}
	archived := false
	xs, err := s.c.Store.List(ctx, store.SessionFilter{Project: project, Archived: &archived})
	if err != nil {
		return false, err
	}
	var errs []error
	for _, x := range xs {
		if x.Repo == "" || x.Branch == "" || x.Branch == x.BaseBranch {
			continue
		}
		pulls, err := s.c.Pulls.PullsForBranch(ctx, x.Repo, x.Branch)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", x.Name, err))
			continue
		}
		if len(pulls) == 0 {
			continue
		}
		have, err := s.c.Store.LinksFor(ctx, store.SessionRef(x.ID))
		if err != nil {
			return changed, err
		}
		known := map[store.Ref]store.Link{}
		for _, l := range have {
			known[l.Other] = l
		}
		for _, p := range pulls[:min(len(pulls), maxSyncedPulls)] {
			ref := store.PRRef(p.Repo, p.Number)
			meta := store.LinkMeta{URL: p.URL, Title: p.Title, State: p.State}
			if l, ok := known[ref]; ok && l.LinkMeta == meta {
				continue
			}
			if err := s.c.Store.AddLink(ctx, store.SessionRef(x.ID), ref, meta); err != nil {
				return changed, err
			}
			changed = true
		}
	}
	return changed, errors.Join(errs...)
}

// linkNames lists every link's id, plus "#<n>" for PRs.
func linkNames(links []store.Link) []string {
	var out []string
	for _, l := range links {
		out = append(out, l.Other.ID)
		if l.Other.Kind == store.KindGitHubPR {
			if _, num, ok := strings.Cut(l.Other.ID, "#"); ok {
				out = append(out, "#"+num)
			}
		}
	}
	return out
}

// ClearWaiting moves a session that is waiting on a permission prompt back
// to working, for when the TUI sees the prompt has gone from the pane
// (claude.PermissionPromptVisible) before any hook says so. It does nothing
// unless the session is still waiting and its latest event is a permission
// Notification, and never overwrites a status a hook wrote meanwhile.
func (s *Service) ClearWaiting(ctx context.Context, x store.Session) (bool, error) {
	if x.Status != store.StatusWaiting {
		return false, nil
	}
	e, err := s.c.Store.LatestEvent(ctx, x.ClaudeSessionID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return false, nil
	case err != nil:
		return false, err
	}
	if e.Event != hooks.Notification {
		return false, nil
	}
	// An unreadable payload is simply not a permission prompt.
	if p, perr := hooks.Parse(strings.NewReader(e.Payload)); perr != nil || p.NotificationType != hooks.NotifyPermission {
		return false, nil
	}
	return s.c.Store.SetStatusIf(ctx, x.ID, store.StatusWaiting, store.StatusWorking, s.c.Now())
}

// Target is the session's tmux window.
func Target(x store.Session) (tmux.Target, error) {
	if x.TmuxSession == "" || x.TmuxWindow == "" {
		return tmux.Target{}, fmt.Errorf("%w: %s", ErrNoWindow, x.Name)
	}
	return tmux.Target{Session: x.TmuxSession, Window: x.TmuxWindow}, nil
}

// Preview returns the last lines lines of the session's pane.
func (s *Service) Preview(ctx context.Context, x store.Session, lines int) (string, error) {
	t, err := Target(x)
	if err != nil {
		return "", err
	}
	return s.c.Tmux.CapturePane(ctx, t, lines)
}

// KillOpts are the inputs to Kill.
type KillOpts struct {
	// RemoveWorktree also removes the worktree and archives the session.
	RemoveWorktree bool
	// Force removes the worktree even with uncommitted changes.
	Force bool
	// DeleteBranch also force-deletes the session's branch once the
	// worktree is gone. Only allowed when forge created the branch
	// (store.Session.BranchCreated); otherwise Kill returns ErrNotOwnBranch
	// before doing anything.
	DeleteBranch bool
}

// CanDeleteBranch reports whether forge may offer to delete x's branch: it
// created the branch itself, in a worktree of its own.
func CanDeleteBranch(x store.Session) bool {
	return x.BranchCreated && !x.WorktreeExternal && x.Branch != ""
}

// Killed describes a kill.
type Killed struct {
	Archived bool
	// BranchMerged reports whether the kept branch is already merged into
	// the base (only when the worktree was removed).
	BranchMerged bool
	// BranchDeleted reports whether the branch was deleted (DeleteBranch).
	BranchDeleted bool
}

// Kill closes the session's tmux window. The row is never deleted: without
// RemoveWorktree the session becomes dead (resumable in place); with it the
// worktree is removed, the branch kept, and the session archived. A dirty
// worktree returns *worktree.DirtyError (the window is already closed and
// the session marked dead); retry with Force to remove it anyway. An
// external worktree (StartOpts.Worktree) is never removed: RemoveWorktree
// then only archives the session.
func (s *Service) Kill(ctx context.Context, x store.Session, o KillOpts) (Killed, error) {
	if x.Status == store.StatusArchived {
		return Killed{}, fmt.Errorf("%w: %s", ErrArchived, x.Name)
	}
	if o.DeleteBranch && (!o.RemoveWorktree || !CanDeleteBranch(x)) {
		return Killed{}, fmt.Errorf("%w: %s", ErrNotOwnBranch, x.Branch)
	}
	if t, err := Target(x); err == nil {
		if err := s.c.Tmux.KillWindow(ctx, t); err != nil {
			return Killed{}, err
		}
	}
	now := s.c.Now()
	if err := s.c.Store.SetStatus(ctx, x.ID, store.StatusDead, now); err != nil {
		return Killed{}, err
	}
	if !o.RemoveWorktree {
		return Killed{}, nil
	}
	var res Killed
	if x.Worktree != "" && !x.WorktreeExternal {
		p, ok := s.Project(x.Project)
		if !ok {
			return Killed{}, fmt.Errorf("%w: %q", ErrUnknownProject, x.Project)
		}
		rm, err := s.c.Worktrees.Remove(ctx, worktree.RemoveOpts{
			Project: p, Session: x.Name, Base: x.BaseBranch, Force: o.Force,
		})
		switch {
		case errors.Is(err, worktree.ErrNotFound): // already gone
		case err != nil:
			return Killed{}, err
		default:
			res.BranchMerged = rm.BranchMerged
		}
		if o.DeleteBranch {
			if err := s.c.Worktrees.DeleteBranch(ctx, p, x.Branch, true); err != nil {
				return Killed{}, fmt.Errorf("delete branch %s: %w", x.Branch, err)
			}
			res.BranchDeleted = true
		}
	}
	if err := s.c.Store.ArchiveRemoved(ctx, x.ID, now); err != nil {
		return Killed{}, err
	}
	res.Archived = true
	return res, nil
}

// Delete removes an archived session's row, events, links and data dir. The
// agent branch is left alone.
func (s *Service) Delete(ctx context.Context, x store.Session) error {
	if x.Status != store.StatusArchived {
		return fmt.Errorf("%w: %s is %s", ErrNotArchived, x.Name, x.Status)
	}
	if err := s.c.Store.Delete(ctx, x.ID); err != nil {
		return err
	}
	if s.c.DataDir == "" || worktree.CheckName(x.Name) != nil {
		return nil
	}
	return os.RemoveAll(s.SessionDir(x.Name))
}

// TmuxWindows lists every agent window on forge's tmux server; none when
// the server isn't running.
func (s *Service) TmuxWindows(ctx context.Context) ([]tmux.Target, error) {
	return s.c.Tmux.ListAll(ctx)
}

// RestartTmux stops forge's tmux server and every agent window on it; the
// next session start brings it back with the current config. The stopped
// sessions read as dead on the next list refresh and stay resumable.
func (s *Service) RestartTmux(ctx context.Context) error {
	return s.c.Tmux.KillServer(ctx)
}

// TmuxConfig is the config file forge's tmux server uses.
func (s *Service) TmuxConfig() string { return s.c.TmuxConf }

// ResetTmuxConfig replaces the tmux config with forge's default, keeping
// the old file as a timestamped backup whose path it returns.
func (s *Service) ResetTmuxConfig() (backup string, err error) {
	if s.c.TmuxConf == "" {
		return "", errors.New("sessions: no tmux config path")
	}
	return tmux.ResetConfig(s.c.TmuxConf, s.c.TmuxColors, s.c.Now())
}

// API is what the UI (the Sessions tab, `forge new`) needs from the session
// service; *Service implements it and tests fake it.
type API interface {
	Start(ctx context.Context, o StartOpts) (Started, error)
	List(ctx context.Context, project string, archived bool) ([]Row, error)
	Preview(ctx context.Context, x store.Session, lines int) (string, error)
	Kill(ctx context.Context, x store.Session, o KillOpts) (Killed, error)
	Delete(ctx context.Context, x store.Session) error
	ClearWaiting(ctx context.Context, x store.Session) (bool, error)
	Inspect(ctx context.Context, x store.Session) (ResumePlan, error)
	Resume(ctx context.Context, x store.Session, o ResumeOpts) (Resumed, error)
	Attach(ctx context.Context, x store.Session) (tmux.Attachment, error)
	Transcript(ctx context.Context, x store.Session) (transcript.Transcript, error)
	LinkedSessions(ctx context.Context, kind store.LinkKind, id string) ([]store.Session, error)
	SyncPRs(ctx context.Context, project string) (bool, error)
	TmuxControl
}

// TmuxControl manages forge's tmux server and its config file.
type TmuxControl interface {
	TmuxWindows(ctx context.Context) ([]tmux.Target, error)
	RestartTmux(ctx context.Context) error
	TmuxConfig() string
	ResetTmuxConfig() (backup string, err error)
}

var _ API = (*Service)(nil)
