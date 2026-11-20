package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Git is the git plumbing the manager needs. repo is any directory inside
// the repository (the project checkout).
type Git interface {
	// HasRemote reports whether the named remote is configured.
	HasRemote(ctx context.Context, repo, remote string) (bool, error)
	// FetchBranch updates refs/remotes/<remote>/<branch>.
	FetchBranch(ctx context.Context, repo, remote, branch string) error
	// RefExists reports whether ref resolves to a commit.
	RefExists(ctx context.Context, repo, ref string) (bool, error)
	// WorktreeAdd adds path on a new branch from startPoint, without
	// upstream tracking.
	WorktreeAdd(ctx context.Context, repo, path, branch, startPoint string) error
	// WorktreeAddExisting adds path checking out an existing branch.
	WorktreeAddExisting(ctx context.Context, repo, path, branch string) error
	// WorktreeAddTracking adds path on a new local branch that tracks
	// upstream (e.g. "origin/feature").
	WorktreeAddTracking(ctx context.Context, repo, path, branch, upstream string) error
	WorktreeList(ctx context.Context, repo string) ([]Worktree, error)
	// WorktreeRemove removes path; force also discards changes and, when
	// the worktree is locked, the lock.
	WorktreeRemove(ctx context.Context, repo, path string, force bool) error
	WorktreePrune(ctx context.Context, repo string) error
	// Status returns `git status --porcelain` lines for dir (empty = clean).
	Status(ctx context.Context, dir string) ([]string, error)
	// IsAncestor reports whether a is an ancestor of (or equal to) b.
	IsAncestor(ctx context.Context, repo, a, b string) (bool, error)
	// DeleteBranch deletes a local branch; force deletes it even unmerged.
	DeleteBranch(ctx context.Context, repo, branch string, force bool) error
	// CheckIgnore reports whether path (relative to dir) is git-ignored.
	CheckIgnore(ctx context.Context, dir, path string) (bool, error)
	// GitPath resolves a path inside dir's git directory, e.g.
	// "info/exclude" (shared by every worktree of a repo), as an absolute
	// path.
	GitPath(ctx context.Context, dir, name string) (string, error)
}

// Error is a failed git invocation.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

func (e *Error) Unwrap() error { return e.Err }

// exitCode is the process exit code behind err, or -1.
func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// Exec is the Git backed by the git binary.
type Exec struct {
	bin string
	env []string
}

var _ Git = (*Exec)(nil)

// Option configures Exec.
type Option func(*Exec)

// WithBinary overrides the git executable (default "git" on $PATH).
func WithBinary(path string) Option { return func(e *Exec) { e.bin = path } }

// WithEnv adds KEY=VALUE entries to git's environment. Tests use it to
// isolate git from the user's config.
func WithEnv(kv ...string) Option { return func(e *Exec) { e.env = append(e.env, kv...) } }

// NewExec returns the git-binary Git.
func NewExec(opts ...Option) *Exec {
	e := &Exec{bin: "git"}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Run runs `git -C dir args...` and returns stdout. It never prompts.
func (e *Exec) Run(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, e.bin, full...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	cmd.Env = append(cmd.Env, e.env...)
	if err := cmd.Run(); err != nil {
		return "", &Error{Args: args, Stderr: stderr.String(), Err: err}
	}
	return stdout.String(), nil
}

// HasRemote implements Git.
func (e *Exec) HasRemote(ctx context.Context, repo, remote string) (bool, error) {
	out, err := e.Run(ctx, repo, "remote")
	if err != nil {
		return false, err
	}
	return slices.Contains(strings.Fields(out), remote), nil
}

// FetchBranch implements Git.
func (e *Exec) FetchBranch(ctx context.Context, repo, remote, branch string) error {
	refspec := fmt.Sprintf("+refs/heads/%s:refs/remotes/%s/%s", branch, remote, branch)
	_, err := e.Run(ctx, repo, "fetch", "--quiet", "--no-tags", remote, refspec)
	return err
}

// RefExists implements Git.
func (e *Exec) RefExists(ctx context.Context, repo, ref string) (bool, error) {
	_, err := e.Run(ctx, repo, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	if err == nil {
		return true, nil
	}
	if exitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

// WorktreeAdd implements Git.
func (e *Exec) WorktreeAdd(ctx context.Context, repo, path, branch, startPoint string) error {
	_, err := e.Run(ctx, repo, "worktree", "add", "--quiet", "--no-track", "-b", branch, "--", path, startPoint)
	return err
}

// WorktreeAddExisting implements Git.
func (e *Exec) WorktreeAddExisting(ctx context.Context, repo, path, branch string) error {
	_, err := e.Run(ctx, repo, "worktree", "add", "--quiet", "--", path, branch)
	return err
}

// WorktreeAddTracking implements Git.
func (e *Exec) WorktreeAddTracking(ctx context.Context, repo, path, branch, upstream string) error {
	_, err := e.Run(ctx, repo, "worktree", "add", "--quiet", "--track", "-b", branch, "--", path, upstream)
	return err
}

// WorktreeList implements Git.
func (e *Exec) WorktreeList(ctx context.Context, repo string) ([]Worktree, error) {
	out, err := e.Run(ctx, repo, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	return parseList(out)
}

// WorktreeRemove implements Git.
func (e *Exec) WorktreeRemove(ctx context.Context, repo, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		// Twice also removes locked worktrees.
		args = append(args, "--force", "--force")
	}
	_, err := e.Run(ctx, repo, append(args, "--", path)...)
	return err
}

// WorktreePrune implements Git.
func (e *Exec) WorktreePrune(ctx context.Context, repo string) error {
	_, err := e.Run(ctx, repo, "worktree", "prune")
	return err
}

// Status implements Git.
func (e *Exec) Status(ctx context.Context, dir string) ([]string, error) {
	out, err := e.Run(ctx, dir, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	var lines []string
	for l := range strings.SplitSeq(out, "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

// IsAncestor implements Git.
func (e *Exec) IsAncestor(ctx context.Context, repo, a, b string) (bool, error) {
	_, err := e.Run(ctx, repo, "merge-base", "--is-ancestor", "--end-of-options", a, b)
	if err == nil {
		return true, nil
	}
	if exitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

// DeleteBranch implements Git.
func (e *Exec) DeleteBranch(ctx context.Context, repo, branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := e.Run(ctx, repo, "branch", flag, "--", branch)
	return err
}

// CheckIgnore implements Git.
func (e *Exec) CheckIgnore(ctx context.Context, dir, path string) (bool, error) {
	_, err := e.Run(ctx, dir, "check-ignore", "--quiet", "--", path)
	if err == nil {
		return true, nil
	}
	if exitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

// GitPath implements Git.
func (e *Exec) GitPath(ctx context.Context, dir, name string) (string, error) {
	out, err := e.Run(ctx, dir, "rev-parse", "--git-path", name)
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(out)
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return p, nil
}
