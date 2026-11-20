package sessions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/daan3011/forge/internal/sessions/claude"
	"github.com/daan3011/forge/internal/sessions/hooks"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/store"
)

// ResumePlan is what Resume would have to do for a session, so the UI can
// ask about it first.
type ResumePlan struct {
	// Alive means the agent still runs: attach, nothing to resume.
	Alive bool
	// WorktreeMissing means the worktree directory is gone; Resume needs
	// RecreateWorktree.
	WorktreeMissing bool
	// HasTranscript means Claude Code has the conversation, so it can be
	// continued with --resume. Without one the session starts afresh.
	HasTranscript bool
	// PromptFile is the session's first prompt, if it is still on disk; it
	// can be sent again when the session starts afresh.
	PromptFile string
}

// Inspect reports what resuming x involves.
func (s *Service) Inspect(ctx context.Context, x store.Session) (ResumePlan, error) {
	if x.Status == store.StatusArchived {
		return ResumePlan{}, fmt.Errorf("%w: %s", ErrArchived, x.Name)
	}
	var p ResumePlan
	alive, err := s.alive(ctx, x)
	if err != nil {
		return ResumePlan{}, err
	}
	p.Alive = alive
	p.WorktreeMissing = !isDir(x.Worktree)
	_, p.HasTranscript = claude.FindTranscript(s.c.ProjectsDir, x.ClaudeSessionID)
	if f := s.promptFile(x.Name); isFile(f) {
		p.PromptFile = f
	}
	return p, nil
}

// ResumeOpts are the inputs to Resume.
type ResumeOpts struct {
	// RecreateWorktree recreates a missing worktree from the session's
	// branch (or from the base, if the branch is gone too).
	RecreateWorktree bool
	// ResendPrompt sends the first prompt again when the session has no
	// transcript and starts afresh.
	ResendPrompt bool
}

// Resumed describes a resume.
type Resumed struct {
	// Session is the session as stored after the resume.
	Session store.Session
	// AlreadyRunning means the agent was alive; nothing was done.
	AlreadyRunning bool
	// Fresh means there was no conversation to resume: claude was started
	// again with the session's id (and the first prompt, if asked).
	Fresh bool
	// NewID means claude refused the session id as already in use, so the
	// session got a new one.
	NewID bool
	// WorktreeRecreated means the worktree was gone and was recreated;
	// BranchRecreated that its branch was gone too and was started from
	// the base again.
	WorktreeRecreated bool
	BranchRecreated   bool
	// FetchErr is set when a recreated branch couldn't fetch its base.
	FetchErr error
}

// Resume brings a dead session back: it opens a new tmux window in the
// session's worktree running `claude --resume <uuid>`, or, when Claude Code
// has no transcript for it, `claude --session-id <uuid>` again. Forge's
// hooks are (re)installed in the worktree first. A missing worktree returns
// *WorktreeMissingError unless RecreateWorktree is set. A live session is
// left alone.
func (s *Service) Resume(ctx context.Context, x store.Session, o ResumeOpts) (res Resumed, err error) {
	// The caller's row may be stale: work from the stored one.
	x, err = s.c.Store.GetByName(ctx, x.Name)
	if err != nil {
		return Resumed{}, err
	}
	if x.Status == store.StatusArchived {
		return Resumed{}, fmt.Errorf("%w: %s", ErrArchived, x.Name)
	}
	p, ok := s.Project(x.Project)
	if !ok {
		return Resumed{}, fmt.Errorf("%w: %q", ErrUnknownProject, x.Project)
	}
	switch alive, err := s.alive(ctx, x); {
	case err != nil:
		return Resumed{}, err
	case alive:
		return Resumed{Session: x, AlreadyRunning: true}, nil
	}

	if !isDir(x.Worktree) {
		if !o.RecreateWorktree {
			return Resumed{}, &WorktreeMissingError{Path: x.Worktree, Branch: x.Branch}
		}
		if p.Path == "" {
			return Resumed{}, fmt.Errorf("%w: %s", ErrNoProjectPath, p.Name)
		}
		base := x.BaseBranch
		if base == "" {
			base = DefaultBase
		}
		wt, err := s.c.Worktrees.Create(ctx, worktree.CreateOpts{Project: p, Session: x.Name, Base: base, ReuseBranch: true})
		if err != nil {
			return Resumed{}, fmt.Errorf("recreate worktree: %w", err)
		}
		res.WorktreeRecreated, res.BranchRecreated, res.FetchErr = true, !wt.Reused, wt.FetchErr
		if wt.Path != x.Worktree || wt.Branch != x.Branch {
			x.Worktree, x.Branch = wt.Path, wt.Branch
			if err := s.c.Store.Update(ctx, x); err != nil {
				return Resumed{}, err
			}
		}
	}

	if s.c.ForgeBin != "" {
		// Sessions from before P1.4, or started by a forge binary that has
		// since moved, get working hooks.
		if err := hooks.Install(x.Worktree, s.c.ForgeBin); err != nil {
			return Resumed{}, err
		}
		if err := s.c.Worktrees.EnsureExcluded(ctx, x.Worktree, hooks.SettingsRel); err != nil {
			return Resumed{}, fmt.Errorf("git-ignore hook settings: %w", err)
		}
	}

	launch := claude.Launch{Bin: s.c.ClaudeBin, SessionID: x.ClaudeSessionID}
	if _, ok := claude.FindTranscript(s.c.ProjectsDir, x.ClaudeSessionID); ok {
		launch.Resume = true
	} else {
		res.Fresh = true
		if f := s.promptFile(x.Name); o.ResendPrompt && isFile(f) {
			launch.PromptFile = f
		}
	}

	tsess, err := s.c.Tmux.EnsureSession(ctx, p.Name)
	if err != nil {
		return Resumed{}, err
	}
	// remain-on-exit keeps the dead pane under the window's name: close it
	// (and the window at the old target, if the session moved).
	fresh := tmux.Target{Session: tsess, Window: tmux.SanitizeName(x.Name)}
	old, oldErr := Target(x)
	if oldErr == nil && old != fresh {
		if err := s.c.Tmux.KillWindow(ctx, old); err != nil && !errors.Is(err, tmux.ErrInvalidName) {
			return Resumed{}, err
		}
	}
	if err := s.c.Tmux.KillWindow(ctx, fresh); err != nil {
		return Resumed{}, err
	}

	target, err := s.open(ctx, tsess, x, launch)
	if err != nil {
		return Resumed{}, err
	}
	defer func() {
		if err != nil && target.Window != "" {
			uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if kErr := s.c.Tmux.KillWindow(uctx, target); kErr != nil {
				err = errors.Join(err, fmt.Errorf("rollback: %w", kErr))
			}
		}
	}()

	if res.Fresh && s.idRefused(ctx, target) {
		id, err := s.c.NewID()
		if err != nil {
			return Resumed{}, err
		}
		if err := s.c.Tmux.KillWindow(ctx, target); err != nil {
			return Resumed{}, err
		}
		launch.SessionID = id
		if target, err = s.open(ctx, tsess, x, launch); err != nil {
			return Resumed{}, err
		}
		if err := s.c.Store.SetClaudeID(ctx, x.ID, id); err != nil {
			return Resumed{}, err
		}
		res.NewID = true
	}

	if oldErr != nil || old != target {
		if err := s.c.Store.SetWindow(ctx, x.ID, target.Session, target.Window); err != nil {
			return Resumed{}, err
		}
	}
	if err := s.c.Store.SetStatus(ctx, x.ID, store.StatusStarting, s.c.Now()); err != nil {
		return Resumed{}, err
	}
	if res.Session, err = s.c.Store.GetByName(ctx, x.Name); err != nil {
		return Resumed{}, err
	}
	return res, nil
}

// open starts launch in a new window for x in tmux session tsess.
func (s *Service) open(ctx context.Context, tsess string, x store.Session, launch claude.Launch) (tmux.Target, error) {
	argv, err := launch.Argv()
	if err != nil {
		return tmux.Target{}, err
	}
	return s.c.Tmux.NewWindow(ctx, tsess, x.Name, x.Worktree, argv, map[string]string{EnvSessionName: x.Name})
}

// idRefused watches a freshly started claude for up to StartupCheck and
// reports whether it exited refusing its session id as already in use.
func (s *Service) idRefused(ctx context.Context, t tmux.Target) bool {
	deadline := time.Now().Add(s.c.StartupCheck)
	for {
		alive, err := s.c.Tmux.WindowAlive(ctx, t)
		if err != nil {
			return false
		}
		if !alive {
			pane, err := s.c.Tmux.CapturePane(ctx, t, 0)
			return err == nil && claude.UUIDInUse(pane)
		}
		if !time.Now().Before(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(min(startupPoll, time.Until(deadline))):
		}
	}
}

// Attach shows the session's window to the user (see tmux.Driver.Attach).
// It does not resume: call Resume first for a dead session.
func (s *Service) Attach(ctx context.Context, x store.Session) (tmux.Attachment, error) {
	x, err := s.c.Store.GetByName(ctx, x.Name)
	if err != nil {
		return tmux.Attachment{}, err
	}
	t, err := Target(x)
	if err != nil {
		return tmux.Attachment{}, err
	}
	return s.c.Tmux.Attach(ctx, t)
}

// alive reports whether x's window exists and its agent runs.
func (s *Service) alive(ctx context.Context, x store.Session) (bool, error) {
	t, err := Target(x)
	if err != nil {
		return false, nil
	}
	return s.c.Tmux.WindowAlive(ctx, t)
}

func (s *Service) promptFile(name string) string {
	if s.c.DataDir == "" || worktree.CheckName(name) != nil {
		return ""
	}
	return filepath.Join(s.SessionDir(name), promptFileName)
}

func isDir(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func isFile(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}
