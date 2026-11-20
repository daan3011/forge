package checkout_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daan3011/forge/internal/checkout"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/sessions/worktree"
)

func newGit(t *testing.T) *worktree.Exec {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	return worktree.NewExec(worktree.WithEnv(
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "XDG_CONFIG_HOME="+t.TempDir(),
		"GIT_AUTHOR_NAME=forge", "GIT_AUTHOR_EMAIL=forge@example.invalid",
		"GIT_COMMITTER_NAME=forge", "GIT_COMMITTER_EMAIL=forge@example.invalid",
	))
}

func git(t *testing.T, g *worktree.Exec, dir string, args ...string) string {
	t.Helper()
	out, err := g.Run(context.Background(), dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func commit(t *testing.T, g *worktree.Exec, dir, file string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, g, dir, "add", "-A")
	git(t, g, dir, "commit", "-q", "-m", file)
}

type fixture struct {
	g       *worktree.Exec
	project config.Project
	root    string
	c       *checkout.Checkout
}

// newFixture: a bare origin with main, a branch "feature" (PR #7) and a
// fork PR #9 only as refs/pull/9/head; the project is a clone with only
// main checked out.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	g := newGit(t)
	base := t.TempDir()
	origin, seed := filepath.Join(base, "origin.git"), filepath.Join(base, "seed")
	git(t, g, base, "init", "-q", "--bare", "-b", "main", origin)
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, g, seed, "init", "-q", "-b", "main")
	commit(t, g, seed, "README.md")
	git(t, g, seed, "remote", "add", "origin", origin)
	git(t, g, seed, "push", "-q", "origin", "main")
	git(t, g, seed, "checkout", "-q", "-b", "feature")
	commit(t, g, seed, "feature.txt")
	git(t, g, seed, "push", "-q", "origin", "feature")
	git(t, g, seed, "checkout", "-q", "-b", "fork", "main")
	commit(t, g, seed, "fork.txt")
	git(t, g, seed, "push", "-q", "origin", "fork:refs/pull/9/head")

	f := &fixture{g: g, root: filepath.Join(base, "worktrees"), project: config.Project{
		Name: "forge", Repo: "daan/forge", Path: filepath.Join(base, "checkout"),
	}}
	git(t, g, base, "clone", "-q", origin, f.project.Path)
	f.c = checkout.New(g, worktree.New(f.root, g))
	return f
}

func (f *fixture) head(t *testing.T, dir string) string {
	return git(t, f.g, dir, "rev-parse", "--abbrev-ref", "HEAD")
}

func TestCheckout(t *testing.T) {
	tests := []struct {
		name         string
		opts         checkout.Opts
		dirty        bool
		prepare      func(t *testing.T, f *fixture)
		wantBranch   string
		wantWorktree bool
		wantExisting bool
		wantFile     string
	}{
		{
			name: "clean checkout tracks origin", opts: checkout.Opts{Number: 7, Branch: "feature", Base: "main"},
			wantBranch: "feature", wantFile: "feature.txt",
		},
		{
			name: "existing local branch", opts: checkout.Opts{Number: 7, Branch: "feature", Base: "main"},
			prepare: func(t *testing.T, f *fixture) {
				git(t, f.g, f.project.Path, "branch", "--track", "feature", "origin/feature")
			}, wantBranch: "feature", wantFile: "feature.txt",
		},
		{
			name: "dirty makes a worktree", opts: checkout.Opts{Number: 7, Branch: "feature", Base: "main"}, dirty: true,
			wantBranch: "feature", wantWorktree: true, wantFile: "feature.txt",
		},
		{
			name: "already checked out", opts: checkout.Opts{Number: 7, Branch: "main", Base: "main"},
			wantBranch: "main", wantExisting: true,
		},
		{
			name: "fork PR", opts: checkout.Opts{Number: 9, Branch: "fork", Base: "main", CrossRepo: true},
			wantBranch: "pr-9", wantFile: "fork.txt",
		},
		{
			name: "fork PR dirty", opts: checkout.Opts{Number: 9, Branch: "fork", Base: "main", CrossRepo: true}, dirty: true,
			wantBranch: "pr-9", wantWorktree: true, wantFile: "fork.txt",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			if tt.prepare != nil {
				tt.prepare(t, f)
			}
			if tt.dirty {
				if err := os.WriteFile(filepath.Join(f.project.Path, "README.md"), []byte("edit"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			o := tt.opts
			o.Project = f.project
			res, err := f.c.Checkout(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			if res.Branch != tt.wantBranch || res.Worktree != tt.wantWorktree || res.Existing != tt.wantExisting {
				t.Fatalf("result = %+v", res)
			}
			wantPath := f.project.Path
			if tt.wantWorktree {
				wantPath = worktree.Path(f.root, f.project, checkout.Name(o.Number))
			}
			if res.Path != wantPath && !tt.wantExisting {
				t.Errorf("path = %q, want %q", res.Path, wantPath)
			}
			if got := f.head(t, res.Path); got != tt.wantBranch {
				t.Errorf("HEAD at %s = %q, want %q", res.Path, got, tt.wantBranch)
			}
			if tt.wantFile != "" {
				if _, err := os.Stat(filepath.Join(res.Path, tt.wantFile)); err != nil {
					t.Errorf("%s not checked out: %v", tt.wantFile, err)
				}
			}
			if tt.dirty && f.head(t, f.project.Path) != "main" {
				t.Error("dirty checkout was switched")
			}
		})
	}
}

func TestCheckoutErrors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.c.Checkout(ctx, checkout.Opts{Project: config.Project{Name: "x"}, Number: 1, Branch: "b"}); !errors.Is(err, checkout.ErrNoPath) {
		t.Errorf("no path: err = %v", err)
	}
	if _, err := f.c.Checkout(ctx, checkout.Opts{Project: f.project, Number: 1, Branch: "-x"}); !errors.Is(err, worktree.ErrInvalidName) {
		t.Errorf("bad branch: err = %v", err)
	}
	if _, err := f.c.Checkout(ctx, checkout.Opts{Project: f.project, Number: 1, Branch: "gone", Base: "main"}); err == nil {
		t.Error("missing branch: want an error")
	}
	if got := f.head(t, f.project.Path); got != "main" {
		t.Errorf("HEAD = %q after failures", got)
	}
}
