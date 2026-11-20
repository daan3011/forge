// Package worktree manages the git worktrees agent sessions run in. Each
// session gets <root>/<repo>/<session> on branch agent/<session>, where root
// is sessions.worktree_root.
//
// All git access goes through the Git interface; Exec is the real one. The
// package never prompts: decisions the user has to make (reuse an existing
// branch, delete a merged one, force-remove a dirty worktree) surface as
// typed errors and results for the caller to ask about.
package worktree

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/daan3011/forge/internal/config"
)

// BranchPrefix starts every agent branch.
const BranchPrefix = "agent/"

var (
	// ErrInvalidName means a session name or base branch is not allowed.
	ErrInvalidName = errors.New("worktree: invalid name")
	// ErrPathExists means the worktree directory already exists.
	ErrPathExists = errors.New("worktree: path already exists")
	// ErrBranchExists means agent/<session> already exists; retry with
	// CreateOpts.ReuseBranch to check it out instead.
	ErrBranchExists = errors.New("worktree: branch already exists")
	// ErrBranchCheckedOut means the branch is checked out in another worktree.
	ErrBranchCheckedOut = errors.New("worktree: branch is checked out elsewhere")
	// ErrDirty means the worktree has uncommitted changes; Remove needs Force.
	ErrDirty = errors.New("worktree: uncommitted changes")
	// ErrNotFound means there is no worktree for the session.
	ErrNotFound = errors.New("worktree: not found")
)

// BranchExistsError reports an existing agent branch.
type BranchExistsError struct{ Branch string }

func (e *BranchExistsError) Error() string {
	return fmt.Sprintf("worktree: branch %s already exists", e.Branch)
}

// Is makes errors.Is(err, ErrBranchExists) true.
func (e *BranchExistsError) Is(target error) bool { return target == ErrBranchExists }

// DirtyError lists the uncommitted changes that blocked a Remove.
type DirtyError struct {
	Path  string
	Files []string // `git status --porcelain` lines
}

func (e *DirtyError) Error() string {
	return fmt.Sprintf("worktree: %s has %d uncommitted change(s)", e.Path, len(e.Files))
}

// Is makes errors.Is(err, ErrDirty) true.
func (e *DirtyError) Is(target error) bool { return target == ErrDirty }

// Worktree is one entry of `git worktree list`.
type Worktree struct {
	Path     string
	Head     string // commit sha; empty for a bare repo
	Branch   string // short name, e.g. "agent/fix-ci"; empty when detached
	Session  string // session name for forge worktrees, else ""
	Bare     bool
	Detached bool
	Locked   bool
	Prunable bool // the directory is gone; `git worktree prune` would drop it
}

// CreateOpts are the inputs to Create.
type CreateOpts struct {
	Project config.Project
	Session string
	Base    string // base branch, e.g. "main"
	// ReuseBranch checks out an existing agent/<session> branch instead of
	// failing with ErrBranchExists.
	ReuseBranch bool
}

// Created describes a new worktree.
type Created struct {
	Path       string
	Branch     string
	StartPoint string // ref the branch was created from; empty when reused
	Reused     bool
	Copied     []string // worktree_copy files copied, relative to the worktree
	Skipped    []string // worktree_copy patterns or files not copied
	// FetchErr is set when fetching the base failed and the local base was
	// used instead (e.g. offline).
	FetchErr error
}

// RemoveOpts are the inputs to Remove.
type RemoveOpts struct {
	Project config.Project
	Session string
	// Base is checked for whether the branch is merged; empty skips the check.
	Base  string
	Force bool // remove even with uncommitted changes (or when locked)
}

// Removed describes a removed worktree.
type Removed struct {
	Path   string
	Branch string
	// BranchMerged means the branch is an ancestor of the base, so it can be
	// deleted without losing work. Squash merges are not detected.
	BranchMerged bool
}

// Manager is what the rest of forge uses.
type Manager interface {
	Create(ctx context.Context, opts CreateOpts) (Created, error)
	List(ctx context.Context, project config.Project) ([]Worktree, error)
	Remove(ctx context.Context, opts RemoveOpts) (Removed, error)
	DeleteBranch(ctx context.Context, project config.Project, session string, force bool) error
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// CheckName reports ErrInvalidName unless session is a valid session name:
// the same charset as tmux window names, so one id names the window, branch
// and directory.
func CheckName(session string) error {
	if !nameRe.MatchString(session) {
		return fmt.Errorf("%w: session %q", ErrInvalidName, session)
	}
	return nil
}

// checkBase rejects base branch names that could be read as flags or that
// git would refuse.
func checkBase(base string) error {
	if base == "" || strings.HasPrefix(base, "-") || strings.HasPrefix(base, "/") ||
		strings.HasSuffix(base, "/") || strings.HasSuffix(base, ".lock") ||
		strings.Contains(base, "..") || strings.Contains(base, "@{") ||
		strings.ContainsAny(base, " \t\n~^:?*[\\\x7f") {
		return fmt.Errorf("%w: base %q", ErrInvalidName, base)
	}
	return nil
}

// Branch is the agent branch for session.
func Branch(session string) string { return BranchPrefix + session }

// repoDir is the per-repo folder under root.
func repoDir(root string, p config.Project) string {
	name := p.RepoName()
	if name == "" {
		name = p.Name
	}
	return filepath.Join(root, name)
}

// Path is where session's worktree lives.
func Path(root string, p config.Project, session string) string {
	return filepath.Join(repoDir(root, p), session)
}
