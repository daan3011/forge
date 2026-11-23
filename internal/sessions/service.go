package sessions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/customcmd"
	docfs "github.com/daan3011/forge/internal/docs"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/sessions/claude"
	"github.com/daan3011/forge/internal/sessions/hooks"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/transcript"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/slashcmd"
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
	ErrNoProjectPath = errors.New("no local checkout; clone it under one of your project roots")
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
	Archive(ctx context.Context, id int64, summary string, at time.Time) error
	Unarchive(ctx context.Context, id int64) error
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (store.Session, error)
	LinksFor(ctx context.Context, ref store.Ref) ([]store.Link, error)
	AddLink(ctx context.Context, from, to store.Ref, meta store.LinkMeta) error
	LinkedSessions(ctx context.Context, ref store.Ref) ([]int64, error)
	SetLinkState(ctx context.Context, ref store.Ref, state string) error
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

func (a storeAdapter) SetLinkState(ctx context.Context, ref store.Ref, state string) error {
	return a.links.SetState(ctx, ref, state)
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
	// Recent remembers the repo a session ran in, so it stays in the
	// project list (zero-config projects); nil remembers nothing.
	Recent ProjectRecorder
	// Transcripts persists the transcript index between runs (P5.3); nil
	// keeps it in memory only.
	Transcripts transcript.Persister
	// Skills writes forge's skills (slash commands, P6.7) into a session's
	// SkillsRoot, which claude gets with --add-dir; nil gives none.
	// *slashcmd.Catalog satisfies it.
	Skills SkillSyncer
}

// SkillSyncer writes forge's skills, rendered with vals, under root.
type SkillSyncer interface {
	Sync(root string, vals map[string]string) (slashcmd.Result, error)
}

// ProjectRecorder remembers that a repo was used at a checkout.
// *store.Projects satisfies it.
type ProjectRecorder interface {
	Touch(ctx context.Context, repo, path string) error
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
	return &Service{c: c, transcripts: transcript.NewCache(c.Transcripts)}
}

// Transcript parses the transcript of x (P1.6). Files are parsed
// incrementally, so loading the same session again only reads new lines.
// It returns transcript.ErrNoTranscript when there is none yet.
func (s *Service) Transcript(_ context.Context, x store.Session) (transcript.Transcript, error) {
	path, err := transcript.Locate(x, s.c.ProjectsDir)
	if err != nil {
		return transcript.Transcript{}, err
	}
	return s.transcripts.Load(path, x.Project)
}

// ResetTranscripts drops the in-memory transcript index, after its
// persisted snapshots were cleared (P5.3).
func (s *Service) ResetTranscripts() { s.transcripts.Reset() }

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

// SkillsRoot is the --add-dir folder holding the session's forge skills
// in .claude/skills. It is called "forge", so a skill whose bare name the
// repo or the user's own skills take is still /forge:<name>.
func (s *Service) SkillsRoot(name string) string {
	return filepath.Join(s.SessionDir(name), "forge")
}

// skillVals are the placeholders a session's skills are rendered with.
func (s *Service) skillVals(x store.Session) map[string]string {
	vals := map[string]string{"session": x.Name, "repo": x.Repo, "branch": x.Branch}
	if p, ok := s.Project(x.Project); ok {
		if vals["repo"] == "" {
			vals["repo"] = p.Repo
		}
		if p.Path != "" || p.DocsDir != "" {
			vals["docs"] = p.DocsPath()
		}
	}
	return vals
}

// syncSkills writes x's skills and returns the --add-dir folder, or "" when
// there is none to pass. A failed sync never stops a session: what could be
// written is still passed, and the error is returned to show as a warning.
func (s *Service) syncSkills(x store.Session) (string, error) {
	if s.c.Skills == nil {
		return "", nil
	}
	root := s.SkillsRoot(x.Name)
	_, err := s.c.Skills.Sync(root, s.skillVals(x))
	if err != nil {
		err = fmt.Errorf("forge skills: %w", err)
	}
	if !isDir(root) {
		return "", err
	}
	return root, err
}

// SyncResult is SyncSkills' outcome for one session.
type SyncResult struct {
	Session string
	slashcmd.Result
	Err error
}

// SyncSkills rewrites the skills of every session that isn't archived, so
// overrides and template edits reach them. Running agents pick the change
// up live (Claude Code watches --add-dir skill folders). Sessions without
// a session dir are skipped: they get theirs on resume.
func (s *Service) SyncSkills(ctx context.Context) ([]SyncResult, error) {
	if s.c.Skills == nil {
		return nil, nil
	}
	archived := false
	xs, err := s.c.Store.List(ctx, store.SessionFilter{Archived: &archived})
	if err != nil {
		return nil, err
	}
	var out []SyncResult
	for _, x := range xs {
		if x.Status == store.StatusArchived || !isDir(s.SessionDir(x.Name)) {
			continue
		}
		r, err := s.c.Skills.Sync(s.SkillsRoot(x.Name), s.skillVals(x))
		out = append(out, SyncResult{Session: x.Name, Result: r, Err: err})
	}
	return out, nil
}

// StartOpts are the inputs to Start.
type StartOpts struct {
	Name     string
	Project  string // project name
	Base     string // base branch; empty means DefaultBase
	Template string // template name, recorded only (P1.7)
	// Packs are context pack names chosen in the new-session form (P5.2).
	// The form flow turns them into Docs and the start of Prompt; Start
	// ignores them.
	Packs  []string
	Prompt string // first prompt; empty starts claude without one
	// Branch is the branch to work on; empty means agent/<name>. See
	// worktree.CreateOpts.Branch for how an existing branch is used.
	Branch string
	// Worktree runs the session in an existing worktree that isn't forge's
	// (the path from a BranchInUseError) instead of creating one. forge
	// never removes it and never deletes its branch.
	Worktree string
	// Links are the issue / PR the session is for, saved with it (P1.9).
	Links []LinkSpec
	// StartCommit starts a new branch at this commit instead of Base (P2.8
	// fix branches); see worktree.CreateOpts.StartCommit.
	StartCommit string
	// Docs are put into the worktree before the agent starts (P4.3); the
	// prompt mentions them by their Rel paths.
	Docs []docfs.Attachment
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
	// SkillsErr is set when forge's skills couldn't all be written; the
	// session runs anyway.
	SkillsErr error
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
		return Started{}, fmt.Errorf("%s: %w", p.Name, ErrNoProjectPath)
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

	// The worktree's removal (undo above) takes copied docs along. The
	// prompt was rendered before the worktree existed; docs that had to go
	// elsewhere (a differing version on the branch) are re-mentioned.
	docs, err := s.materialize(ctx, wt.Path, o.Base, o.Docs)
	if err != nil {
		return Started{}, err
	}
	o.Prompt = docfs.Rewrite(o.Prompt, o.Docs, docs)

	id, err := s.c.NewID()
	if err != nil {
		return Started{}, err
	}
	launch := claude.Launch{Bin: s.c.ClaudeBin, SessionID: id}
	if o.Prompt != "" || s.c.ForgeBin != "" {
		dir := s.SessionDir(o.Name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Started{}, fmt.Errorf("session dir: %w", err)
		}
		undo = append(undo, func(context.Context) error { return os.RemoveAll(dir) })
	}
	if s.c.ForgeBin != "" {
		// forge's MCP server for this session only (P6.5): passed with
		// --mcp-config from the session dir, never in any Claude config.
		launch.MCPConfig = filepath.Join(s.SessionDir(o.Name), claude.MCPConfigFile)
		if err := claude.WriteMCPConfig(launch.MCPConfig, s.c.ForgeBin, o.Name); err != nil {
			return Started{}, err
		}
	}
	if s.c.Skills != nil {
		// forge's skills (P6.7) in the session dir, never in the worktree;
		// the dir's removal (undo above) takes them along.
		if err := os.MkdirAll(s.SessionDir(o.Name), 0o700); err != nil {
			return Started{}, fmt.Errorf("session dir: %w", err)
		}
		if o.Prompt == "" && s.c.ForgeBin == "" {
			dir := s.SessionDir(o.Name)
			undo = append(undo, func(context.Context) error { return os.RemoveAll(dir) })
		}
		launch.AddDir, res.SkillsErr = s.syncSkills(store.Session{
			Name: o.Name, Project: p.Name, Repo: p.Repo, Branch: wt.Branch,
		})
	}
	if o.Prompt != "" {
		launch.PromptFile = filepath.Join(s.SessionDir(o.Name), promptFileName)
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
	if s.c.Recent != nil && p.Repo != "" {
		// Best effort: the session runs either way; at worst the repo
		// isn't listed first next time.
		_ = s.c.Recent.Touch(ctx, p.Repo, p.Path)
	}
	return Started{Session: x, FetchErr: wt.FetchErr, SkillsErr: res.SkillsErr}, nil
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
	wt, err := s.c.Worktrees.Create(ctx, worktree.CreateOpts{Project: p, Session: o.Name, Base: o.Base, Branch: o.Branch, StartCommit: o.StartCommit})
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

// Vars are the session's custom command variables (P6.3): its repo,
// branch, worktree (also {path}) and name, plus the PR number and issue
// key of its primary links.
func (r Row) Vars() customcmd.Vars {
	v := customcmd.Vars{Repo: r.Repo, Branch: r.Branch, Worktree: r.Worktree, Session: r.Name, Path: r.Worktree}
	if pr, ok := PrimaryLink(r.LinkList, store.KindGitHubPR); ok {
		if _, num, ok := strings.Cut(pr.Other.ID, "#"); ok {
			v.PR = num
		}
	}
	if issue, ok := PrimaryLink(r.LinkList, store.KindPlaneIssue); ok {
		v.Issue = issue.Other.ID
	}
	return v
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
		links, err := s.c.Store.LinksFor(ctx, store.SessionRef(x.ID))
		if err != nil {
			return nil, err
		}
		rows[i] = newRow(x, links)
	}
	return rows, nil
}

func newRow(x store.Session, links []store.Link) Row {
	return Row{Session: x, Link: linkLabel(links), Links: linkNames(links), LinkList: links}
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
	switch r.Kind {
	case store.KindGitHubPR:
		if _, num, ok := strings.Cut(r.ID, "#"); ok {
			return "#" + num
		}
	case store.KindGitHubRun:
		if _, id, ok := strings.Cut(r.ID, "#"); ok {
			return "run " + id
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

// IssueLinks returns what forge links to a Plane work item key (P3.2):
// the sessions started from it and the PRs linked to those sessions or to
// the item itself, oldest first, each once.
func (s *Service) IssueLinks(ctx context.Context, key string) ([]store.Session, []store.Link, error) {
	ref := store.IssueRef(key)
	direct, err := s.c.Store.LinksFor(ctx, ref)
	if err != nil {
		return nil, nil, err
	}
	var (
		sessions []store.Session
		prs      []store.Link
		seen     = map[store.Ref]bool{}
	)
	addPR := func(l store.Link) {
		if l.Other.Kind == store.KindGitHubPR && !seen[l.Other] {
			seen[l.Other] = true
			prs = append(prs, l)
		}
	}
	for _, l := range direct {
		if l.Other.Kind != store.KindSession {
			addPR(l)
			continue
		}
		id, err := strconv.ParseInt(l.Other.ID, 10, 64)
		if err != nil {
			continue
		}
		x, err := s.c.Store.Get(ctx, id)
		switch {
		case errors.Is(err, store.ErrNotFound):
			continue // a link left behind by a deleted session
		case err != nil:
			return nil, nil, err
		}
		sessions = append(sessions, x)
		links, err := s.c.Store.LinksFor(ctx, store.SessionRef(id))
		if err != nil {
			return nil, nil, err
		}
		for _, sl := range links {
			addPR(sl)
		}
	}
	return sessions, prs, nil
}

// SetIssueStates records the current state of Plane work items (key →
// state name) on every link to them, so session detail panes show it
// without asking Plane. Failures are joined; the other items still update.
func (s *Service) SetIssueStates(ctx context.Context, states map[string]string) error {
	var errs []error
	for key, state := range states {
		if err := s.c.Store.SetLinkState(ctx, store.IssueRef(key), state); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
		}
	}
	return errors.Join(errs...)
}

// IssuePR is a PR whose branch or title names a Plane work item (P3.7).
type IssuePR struct {
	Key  string    // work item key, "PROJ-123"
	PR   store.Ref // store.PRRef
	Meta store.LinkMeta
}

// LinkIssuePRs links each work item to its PR (issue -> PR, with the PR's
// URL, title and state). added are the links that didn't exist before; a
// link whose PR title or state changed is updated but not reported, and an
// unchanged one isn't written. Failures are joined; the others still link.
func (s *Service) LinkIssuePRs(ctx context.Context, links []IssuePR) (added []IssuePR, err error) {
	var errs []error
	for _, l := range links {
		issue := store.IssueRef(l.Key)
		have, err := s.c.Store.LinksFor(ctx, issue)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", l.Key, err))
			continue
		}
		known, same := false, false
		for _, h := range have {
			if h.Other != l.PR {
				continue
			}
			known = true
			// A PR -> issue link (none are made yet) has no PR meta to compare.
			same = same || h.LinkMeta == l.Meta
		}
		if same {
			continue
		}
		if err := s.c.Store.AddLink(ctx, issue, l.PR, l.Meta); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", l.Key, err))
			continue
		}
		if !known {
			added = append(added, l)
		}
	}
	return added, errors.Join(errs...)
}

// PRIssues returns the Plane work item links touching the PR id
// ("owner/repo#12"), oldest first. Links made from the issue side carry
// the PR's meta, not the issue's, so their URL, title and state are left
// empty.
func (s *Service) PRIssues(ctx context.Context, prID string) ([]store.Link, error) {
	links, err := s.c.Store.LinksFor(ctx, store.Ref{Kind: store.KindGitHubPR, ID: prID})
	if err != nil {
		return nil, err
	}
	var out []store.Link
	for _, l := range links {
		if l.Other.Kind == store.KindPlaneIssue {
			out = append(out, l)
		}
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

// SendPrompt pastes text into x's running agent as a new prompt and
// submits it (P2.8 "fix this" on a session that already exists). A window
// that is gone or whose agent exited gives ErrNoWindow.
func (s *Service) SendPrompt(ctx context.Context, x store.Session, text string) error {
	t, err := Target(x)
	if err != nil {
		return err
	}
	alive, err := s.c.Tmux.WindowAlive(ctx, t)
	if err != nil {
		return err
	}
	if !alive {
		return fmt.Errorf("%w: %s", ErrNoWindow, x.Name)
	}
	if err := s.c.Tmux.PasteText(ctx, t, text); err != nil {
		if errors.Is(err, tmux.ErrNotFound) {
			return fmt.Errorf("%w: %s", ErrNoWindow, x.Name)
		}
		return err
	}
	return nil
}

// AttachDocs puts docs into x's worktree so a prompt can mention them
// (P4.3) and returns them as they ended up (see docfs.Materialize). A
// worktree that is gone gives *WorktreeMissingError.
func (s *Service) AttachDocs(ctx context.Context, x store.Session, docs []docfs.Attachment) ([]docfs.Attachment, error) {
	if !isDir(x.Worktree) {
		return nil, &WorktreeMissingError{Path: x.Worktree, Branch: x.Branch}
	}
	base := x.BaseBranch
	if base == "" {
		base = DefaultBase
	}
	return s.materialize(ctx, x.Worktree, base, docs)
}

// materialize copies docs into the worktree at dir (on a branch made from
// base) and git-ignores the copy folder when anything went there.
func (s *Service) materialize(ctx context.Context, dir, base string, docs []docfs.Attachment) ([]docfs.Attachment, error) {
	if len(docs) == 0 {
		return docs, nil
	}
	changed := func(rel string) (bool, error) { return s.c.Worktrees.ChangedOnBranch(ctx, dir, base, rel) }
	out, n, err := docfs.Materialize(dir, docs, changed)
	if err != nil {
		return nil, fmt.Errorf("attach docs: %w", err)
	}
	if n > 0 {
		if err := s.c.Worktrees.EnsureExcluded(ctx, dir, docfs.CopyDir); err != nil {
			return nil, fmt.Errorf("git-ignore copied docs: %w", err)
		}
	}
	return out, nil
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
// worktree is removed, the branch kept, and the session archived (with a
// summary, unless it is in the archive already). A dirty
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
	// Every archived session gets a summary (P1.10); the transcript lives
	// outside the worktree, so it is still there. The row is read again
	// for a current transcript path.
	cur, err := s.c.Store.Get(ctx, x.ID)
	if err != nil {
		return Killed{}, err
	}
	if !cur.Archived() {
		if err := s.archive(ctx, cur); err != nil {
			return Killed{}, err
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

// Purge deletes an archived session for good (P5.3): a worktree still on
// disk is removed first (never forced, so a dirty one returns
// *worktree.DirtyError and nothing is deleted), then the row, events, links
// and data dir go. External worktrees are left alone; branches are kept.
func (s *Service) Purge(ctx context.Context, x store.Session) error {
	if !x.Archived() {
		return fmt.Errorf("%w: %s is %s", ErrNotArchived, x.Name, x.Status)
	}
	if x.Status != store.StatusArchived {
		if t, err := Target(x); err == nil {
			if err := s.c.Tmux.KillWindow(ctx, t); err != nil {
				return err
			}
		}
		if x.Worktree != "" && !x.WorktreeExternal {
			p, ok := s.Project(x.Project)
			if !ok {
				return fmt.Errorf("%w: %q", ErrUnknownProject, x.Project)
			}
			_, err := s.c.Worktrees.Remove(ctx, worktree.RemoveOpts{Project: p, Session: x.Name, Base: x.BaseBranch})
			if err != nil && !errors.Is(err, worktree.ErrNotFound) {
				return err
			}
		}
		if err := s.c.Store.ArchiveRemoved(ctx, x.ID, s.c.Now()); err != nil {
			return err
		}
		x.Status = store.StatusArchived
	}
	return s.Delete(ctx, x)
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

// DefaultCommandSession is the tmux session custom command windows open in
// when no project is known.
const DefaultCommandSession = "forge"

// WindowOpts are the inputs to OpenWindow.
type WindowOpts struct {
	// Project names the tmux session (as for agents); "" uses
	// DefaultCommandSession.
	Project string
	// Name is the window name before sanitising, e.g. "cmd-dev-fix-login".
	Name   string
	Dir    string
	Script string // run with sh -c
	Env    map[string]string
	// Attach shows the window once it runs.
	Attach bool
}

// WindowResult is what OpenWindow did.
type WindowResult struct {
	Target tmux.Target
	// Attachment is set when the window was shown (Attach, or Reused).
	Attachment *tmux.Attachment
	// Reused means the window was already running, so it was shown
	// instead of starting the command again.
	Reused bool
}

// OpenWindow runs a custom command (P6.3) in a window on forge's tmux
// server. A window of the same name that still runs is shown instead of
// starting a second one; one whose command exited is replaced.
func (s *Service) OpenWindow(ctx context.Context, o WindowOpts) (WindowResult, error) {
	project := o.Project
	if project == "" {
		project = DefaultCommandSession
	}
	tsess, err := s.c.Tmux.EnsureSession(ctx, project)
	if err != nil {
		return WindowResult{}, err
	}
	t := tmux.Target{Session: tsess, Window: tmux.SanitizeName(o.Name)}
	if err := t.Validate(); err != nil {
		return WindowResult{}, err
	}
	ws, err := s.c.Tmux.ListWindows(ctx, tsess)
	if err != nil {
		return WindowResult{}, err
	}
	res := WindowResult{Target: t}
	if i := slices.IndexFunc(ws, func(w tmux.Window) bool { return w.Name == t.Window }); i >= 0 {
		if !ws[i].Dead {
			res.Reused = true
			return s.showWindow(ctx, res)
		}
		if err := s.c.Tmux.KillWindow(ctx, t); err != nil {
			return WindowResult{}, err
		}
	}
	if res.Target, err = s.c.Tmux.NewWindow(ctx, tsess, o.Name, o.Dir, []string{"sh", "-c", o.Script}, o.Env); err != nil {
		return WindowResult{}, err
	}
	if !o.Attach {
		return res, nil
	}
	return s.showWindow(ctx, res)
}

func (s *Service) showWindow(ctx context.Context, res WindowResult) (WindowResult, error) {
	a, err := s.c.Tmux.Attach(ctx, res.Target)
	if err != nil {
		return WindowResult{}, err
	}
	res.Attachment = &a
	return res, nil
}

// API is what the UI (the Sessions tab, `forge new`) needs from the session
// service; *Service implements it and tests fake it.
type API interface {
	Start(ctx context.Context, o StartOpts) (Started, error)
	List(ctx context.Context, project string, archived bool) ([]Row, error)
	Preview(ctx context.Context, x store.Session, lines int) (string, error)
	SendPrompt(ctx context.Context, x store.Session, text string) error
	AttachDocs(ctx context.Context, x store.Session, docs []docfs.Attachment) ([]docfs.Attachment, error)
	Kill(ctx context.Context, x store.Session, o KillOpts) (Killed, error)
	Delete(ctx context.Context, x store.Session) error
	ClearWaiting(ctx context.Context, x store.Session) (bool, error)
	Inspect(ctx context.Context, x store.Session) (ResumePlan, error)
	Resume(ctx context.Context, x store.Session, o ResumeOpts) (Resumed, error)
	Attach(ctx context.Context, x store.Session) (tmux.Attachment, error)
	Transcript(ctx context.Context, x store.Session) (transcript.Transcript, error)
	LinkedSessions(ctx context.Context, kind store.LinkKind, id string) ([]store.Session, error)
	IssueLinks(ctx context.Context, key string) ([]store.Session, []store.Link, error)
	SetIssueStates(ctx context.Context, states map[string]string) error
	SyncPRs(ctx context.Context, project string) (bool, error)
	LinkIssuePRs(ctx context.Context, links []IssuePR) ([]IssuePR, error)
	PRIssues(ctx context.Context, prID string) ([]store.Link, error)
	Archive(ctx context.Context, x store.Session) (store.Session, error)
	AutoArchive(ctx context.Context, project string) ([]Row, error)
	Unarchive(ctx context.Context, x store.Session) error
	TmuxControl
}

// TmuxControl manages forge's tmux server and its config file.
type TmuxControl interface {
	TmuxWindows(ctx context.Context) ([]tmux.Target, error)
	RestartTmux(ctx context.Context) error
	TmuxConfig() string
	ResetTmuxConfig() (backup string, err error)
	OpenWindow(ctx context.Context, o WindowOpts) (WindowResult, error)
}

var _ API = (*Service)(nil)
