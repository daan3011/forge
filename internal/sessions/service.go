package sessions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/sessions/claude"
	"github.com/daan3011/forge/internal/sessions/hooks"
	"github.com/daan3011/forge/internal/sessions/tmux"
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
)

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
	LinksFor(ctx context.Context, ref store.Ref) ([]store.Link, error)
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
	c Config
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
	return &Service{c: c}
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

	wt, err := s.c.Worktrees.Create(ctx, worktree.CreateOpts{Project: p, Session: o.Name, Base: o.Base})
	if err != nil {
		return Started{}, err
	}
	undo = append(undo, func(ctx context.Context) error {
		_, rmErr := s.c.Worktrees.Remove(ctx, worktree.RemoveOpts{Project: p, Session: o.Name, Force: true})
		if rmErr != nil || wt.Reused {
			return rmErr
		}
		return s.c.Worktrees.DeleteBranch(ctx, p, o.Name, true)
	})

	if s.c.ForgeBin != "" {
		// The worktree's removal (undo above) takes the settings file along.
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
		Name:            o.Name,
		ClaudeSessionID: id,
		Project:         p.Name,
		Repo:            p.Repo,
		BaseBranch:      o.Base,
		Branch:          wt.Branch,
		Worktree:        wt.Path,
		TmuxSession:     target.Session,
		TmuxWindow:      target.Window,
		Template:        o.Template,
		Status:          store.StatusStarting,
	})
	if errors.Is(err, store.ErrConflict) {
		err = fmt.Errorf("%w: %s", ErrNameTaken, o.Name)
	}
	if err != nil {
		return Started{}, err
	}
	return Started{Session: x, FetchErr: wt.FetchErr}, nil
}

// Row is a session as the list shows it.
type Row struct {
	store.Session
	// Link is the linked issue or PR, e.g. "#12" or "PLANE-7"; "" if none.
	Link string
	// Links names every linked issue and PR, for search: the full id
	// ("owner/repo#12", "PLANE-7") and, for PRs, the short "#12" too.
	Links []string
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

// linkLabel picks the label of the first PR link, else the first issue.
func linkLabel(links []store.Link) string {
	var issue string
	for _, l := range links {
		switch l.Other.Kind {
		case store.KindGitHubPR:
			if _, num, ok := strings.Cut(l.Other.ID, "#"); ok {
				return "#" + num
			}
			return l.Other.ID
		case store.KindPlaneIssue:
			if issue == "" {
				issue = l.Other.ID
			}
		}
	}
	return issue
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
}

// Killed describes a kill.
type Killed struct {
	Archived bool
	// BranchMerged reports whether the kept branch is already merged into
	// the base (only when the worktree was removed).
	BranchMerged bool
}

// Kill closes the session's tmux window. The row is never deleted: without
// RemoveWorktree the session becomes dead (resumable in place); with it the
// worktree is removed, the branch kept, and the session archived. A dirty
// worktree returns *worktree.DirtyError (the window is already closed and
// the session marked dead); retry with Force to remove it anyway.
func (s *Service) Kill(ctx context.Context, x store.Session, o KillOpts) (Killed, error) {
	if x.Status == store.StatusArchived {
		return Killed{}, fmt.Errorf("%w: %s", ErrArchived, x.Name)
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
	if x.Worktree != "" {
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
