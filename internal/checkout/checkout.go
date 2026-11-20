// Package checkout checks a pull request's branch out locally (P2.3): in
// the project's own checkout when it is clean, else in a new worktree.
package checkout

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/sessions/worktree"
)

// Opts is the PR to check out.
type Opts struct {
	Project config.Project
	Number  int
	// Branch is the PR's head branch; Base its base (needed when a
	// worktree is made).
	Branch string
	Base   string
	// CrossRepo is set for a PR from a fork: its head is fetched from
	// refs/pull/<n>/head into the local branch Name(Number).
	CrossRepo bool
}

// Result is where the PR is checked out.
type Result struct {
	Path   string
	Branch string
	// Worktree is set when a new worktree was made because the project's
	// checkout had uncommitted changes.
	Worktree bool
	// Existing is set when the branch was already checked out at Path.
	Existing bool
	// FetchErr is set when fetching failed and a local branch was used.
	FetchErr error
}

// Checker checks out PR branches.
type Checker interface {
	Checkout(ctx context.Context, o Opts) (Result, error)
}

// Git is the git plumbing a Checkout needs; *worktree.Exec satisfies it.
type Git interface {
	Run(ctx context.Context, dir string, args ...string) (string, error)
	Status(ctx context.Context, dir string) ([]string, error)
	WorktreeList(ctx context.Context, repo string) ([]worktree.Worktree, error)
	RefExists(ctx context.Context, repo, ref string) (bool, error)
}

// Creator makes worktrees; *worktree.Worktrees satisfies it.
type Creator interface {
	Create(ctx context.Context, opts worktree.CreateOpts) (worktree.Created, error)
}

// Checkout is the git-backed Checker.
type Checkout struct {
	git Git
	wts Creator
}

var _ Checker = (*Checkout)(nil)

// New returns a Checkout.
func New(g Git, wts Creator) *Checkout { return &Checkout{git: g, wts: wts} }

// Name is the local branch of fork PR n, and the session name of a PR
// worktree.
func Name(n int) string { return "pr-" + strconv.Itoa(n) }

// ErrNoPath means the project has no checkout configured.
var ErrNoPath = errors.New("checkout: project has no path")

// Checkout implements Checker.
func (c *Checkout) Checkout(ctx context.Context, o Opts) (Result, error) {
	repo := o.Project.Path
	if repo == "" {
		return Result{}, fmt.Errorf("%w: %s", ErrNoPath, o.Project.Name)
	}
	branch := o.Branch
	if o.CrossRepo {
		branch = Name(o.Number)
	}
	if err := worktree.CheckBranch(branch); err != nil {
		return Result{}, err
	}

	wts, err := c.git.WorktreeList(ctx, repo)
	if err != nil {
		return Result{}, err
	}
	for _, wt := range wts {
		if wt.Branch == branch && !wt.Prunable {
			return Result{Path: wt.Path, Branch: branch, Existing: true}, nil
		}
	}

	res := Result{Branch: branch}
	if o.CrossRepo {
		// Fast-forward only: a local pr-N with commits of its own is kept.
		ref := fmt.Sprintf("refs/pull/%d/head:refs/heads/%s", o.Number, branch)
		if _, err := c.git.Run(ctx, repo, "fetch", "--quiet", "--no-tags", "origin", ref); err != nil {
			return Result{}, err
		}
	}

	dirty, err := c.git.Status(ctx, repo)
	if err != nil {
		return Result{}, err
	}
	if len(dirty) > 0 {
		created, err := c.wts.Create(ctx, worktree.CreateOpts{
			Project: o.Project, Session: Name(o.Number), Base: o.Base, Branch: branch,
		})
		if err != nil {
			return Result{}, err
		}
		res.Path, res.Worktree, res.FetchErr = created.Path, true, created.FetchErr
		return res, nil
	}

	res.Path = repo
	if o.CrossRepo {
		_, err := c.git.Run(ctx, repo, "checkout", "--quiet", branch)
		return res, err
	}
	refspec := fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branch, branch)
	if _, err := c.git.Run(ctx, repo, "fetch", "--quiet", "--no-tags", "origin", refspec); err != nil {
		res.FetchErr = err
	}
	local, err := c.git.RefExists(ctx, repo, "refs/heads/"+branch)
	if err != nil {
		return Result{}, err
	}
	if local {
		_, err := c.git.Run(ctx, repo, "checkout", "--quiet", branch)
		return res, err
	}
	if res.FetchErr != nil {
		return Result{}, fmt.Errorf("can't fetch %s from origin: %w", branch, res.FetchErr)
	}
	_, err = c.git.Run(ctx, repo, "checkout", "--quiet", "--track", "-b", branch, "origin/"+branch)
	return res, err
}

// Fake records checkouts, for tests.
type Fake struct {
	mu     sync.Mutex
	calls  []Opts
	Result Result
	Err    error
}

var _ Checker = (*Fake)(nil)

// Checkout implements Checker.
func (f *Fake) Checkout(_ context.Context, o Opts) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, o)
	return f.Result, f.Err
}

// Calls returns the checkouts so far.
func (f *Fake) Calls() []Opts {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Opts(nil), f.calls...)
}
