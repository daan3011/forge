package worktree_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/sessions/worktree"
)

// newGit returns an Exec isolated from the user's and system git config.
func newGit(t *testing.T) *worktree.Exec {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	return worktree.NewExec(worktree.WithEnv(
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		// core.excludesFile defaults to $XDG_CONFIG_HOME/git/ignore even
		// without a global config.
		"XDG_CONFIG_HOME="+t.TempDir(),
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

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, g *worktree.Exec, dir, file, body string) string {
	t.Helper()
	write(t, filepath.Join(dir, file), body)
	git(t, g, dir, "add", "-A")
	git(t, g, dir, "commit", "-q", "-m", "change "+file)
	return git(t, g, dir, "rev-parse", "HEAD")
}

type fixture struct {
	g       *worktree.Exec
	origin  string // bare remote
	project config.Project
	root    string
	m       *worktree.Worktrees
}

// newFixture makes a bare origin with one commit on main, a clone of it as
// the project checkout (with an ignored .env.local) and a worktree root.
func newFixture(t *testing.T, withRemote bool) *fixture {
	t.Helper()
	g := newGit(t)
	base := t.TempDir()
	f := &fixture{
		g:      g,
		origin: filepath.Join(base, "origin.git"),
		root:   filepath.Join(base, "worktrees"),
		project: config.Project{
			Name: "forge", Repo: "daan/forge", Path: filepath.Join(base, "checkout"),
			WorktreeCopy: []string{".env.local", "missing.txt"},
		},
	}
	if err := os.MkdirAll(f.project.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, g, f.project.Path, "init", "-q", "-b", "main")
	write(t, filepath.Join(f.project.Path, ".gitignore"), ".env.local\n")
	commit(t, g, f.project.Path, "README.md", "hello")
	write(t, filepath.Join(f.project.Path, ".env.local"), "SECRET=1")
	if withRemote {
		git(t, g, base, "init", "-q", "--bare", "-b", "main", f.origin)
		git(t, g, f.project.Path, "remote", "add", "origin", f.origin)
		git(t, g, f.project.Path, "push", "-q", "-u", "origin", "main")
	}
	f.m = worktree.New(f.root, g)
	return f
}

func (f *fixture) create(t *testing.T, session string) worktree.Created {
	t.Helper()
	c, err := f.m.Create(context.Background(), worktree.CreateOpts{Project: f.project, Session: session, Base: "main"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCreateFetchesBaseAndCopiesFiles(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, true)

	// Someone else pushes to main; the local checkout doesn't have it yet.
	other := filepath.Join(t.TempDir(), "other")
	git(t, f.g, filepath.Dir(other), "clone", "-q", f.origin, other)
	upstream := commit(t, f.g, other, "new.txt", "upstream")
	git(t, f.g, other, "push", "-q", "origin", "main")

	c := f.create(t, "fix-ci")
	if want := filepath.Join(f.root, "forge", "fix-ci"); c.Path != want {
		t.Errorf("path = %q, want %q", c.Path, want)
	}
	if c.Branch != "agent/fix-ci" || c.StartPoint != "origin/main" || c.Reused || c.FetchErr != nil {
		t.Errorf("created = %+v", c)
	}
	if head := git(t, f.g, c.Path, "rev-parse", "HEAD"); head != upstream {
		t.Errorf("worktree HEAD = %s, want fetched %s", head, upstream)
	}
	if b := git(t, f.g, c.Path, "branch", "--show-current"); b != "agent/fix-ci" {
		t.Errorf("branch = %q", b)
	}
	if _, err := f.g.Run(ctx, c.Path, "rev-parse", "--abbrev-ref", "@{upstream}"); err == nil {
		t.Error("agent branch must not track a remote branch")
	}
	if got, _ := os.ReadFile(filepath.Join(c.Path, ".env.local")); string(got) != "SECRET=1" {
		t.Errorf(".env.local = %q", got)
	}
	if !slices.Equal(c.Copied, []string{".env.local"}) || !slices.Equal(c.Skipped, []string{"missing.txt"}) {
		t.Errorf("copied %v skipped %v", c.Copied, c.Skipped)
	}
}

func TestCreateWithoutRemoteUsesLocalBase(t *testing.T) {
	f := newFixture(t, false)
	c := f.create(t, "local")
	if c.StartPoint != "main" || c.FetchErr != nil {
		t.Errorf("created = %+v", c)
	}
}

func TestCreateOfflineFallsBackToLocalBase(t *testing.T) {
	f := newFixture(t, true)
	git(t, f.g, f.project.Path, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	c := f.create(t, "offline")
	if c.FetchErr == nil {
		t.Error("want FetchErr")
	}
	// origin/main from the earlier push still exists locally.
	if c.StartPoint != "origin/main" {
		t.Errorf("start point = %q", c.StartPoint)
	}
}

func TestCreateErrors(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, true)
	f.create(t, "taken")

	opts := func(session, base string) worktree.CreateOpts {
		return worktree.CreateOpts{Project: f.project, Session: session, Base: base}
	}
	tests := []struct {
		name string
		opts worktree.CreateOpts
		want error
	}{
		{"bad name", opts("a/b", "main"), worktree.ErrInvalidName},
		{"bad base", opts("ok", "--upload-pack=x"), worktree.ErrInvalidName},
		{"path exists", opts("taken", "main"), worktree.ErrPathExists},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := f.m.Create(ctx, tt.opts); !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}

	t.Run("missing base", func(t *testing.T) {
		if _, err := f.m.Create(ctx, opts("nobase", "nope")); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("branch checked out elsewhere", func(t *testing.T) {
		git(t, f.g, f.project.Path, "worktree", "add", "-q", filepath.Join(t.TempDir(), "elsewhere"), "-b", "agent/busy")
		if _, err := f.m.Create(ctx, opts("busy", "main")); !errors.Is(err, worktree.ErrBranchCheckedOut) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("existing branch asks, then reuses", func(t *testing.T) {
		git(t, f.g, f.project.Path, "branch", "agent/again", "main")
		_, err := f.m.Create(ctx, opts("again", "main"))
		var be *worktree.BranchExistsError
		if !errors.Is(err, worktree.ErrBranchExists) || !errors.As(err, &be) || be.Branch != "agent/again" {
			t.Fatalf("err = %v", err)
		}
		o := opts("again", "main")
		o.ReuseBranch = true
		c, err := f.m.Create(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		if !c.Reused || c.StartPoint != "" {
			t.Errorf("created = %+v", c)
		}
		if b := git(t, f.g, c.Path, "branch", "--show-current"); b != "agent/again" {
			t.Errorf("branch = %q", b)
		}
	})
}

func TestCreateRollsBackOnCopyFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read unreadable files")
	}
	ctx := context.Background()
	f := newFixture(t, true)
	secret := filepath.Join(f.project.Path, ".env.local")
	if err := os.Chmod(secret, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(secret, 0o644) })

	_, err := f.m.Create(ctx, worktree.CreateOpts{Project: f.project, Session: "broken", Base: "main"})
	if err == nil {
		t.Fatal("want copy error")
	}
	if _, err := os.Stat(worktree.Path(f.root, f.project, "broken")); !os.IsNotExist(err) {
		t.Errorf("worktree dir left behind: %v", err)
	}
	if wts, _ := f.m.List(ctx, f.project); len(wts) != 0 {
		t.Errorf("worktrees left: %+v", wts)
	}
	if ok, _ := f.g.RefExists(ctx, f.project.Path, "refs/heads/agent/broken"); ok {
		t.Error("branch left behind")
	}
}

func TestListAndReconcile(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, true)
	a := f.create(t, "alpha")
	f.create(t, "beta")
	gone := f.create(t, "gamma")
	if err := os.RemoveAll(gone.Path); err != nil {
		t.Fatal(err)
	}
	// Not forge's: a worktree elsewhere on a normal branch.
	git(t, f.g, f.project.Path, "worktree", "add", "-q", filepath.Join(t.TempDir(), "mine"), "-b", "feature")

	wts, err := f.m.List(ctx, f.project)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, wt := range wts {
		names = append(names, wt.Session)
		if wt.Session == "gamma" && !wt.Prunable {
			t.Error("deleted worktree should be prunable")
		}
		if wt.Session == "alpha" && wt.Branch != "agent/alpha" {
			t.Errorf("alpha branch = %q", wt.Branch)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("sessions = %v", names)
	}

	r := worktree.Reconcile(wts, []worktree.SessionRef{
		{Name: "alpha", Worktree: a.Path},
		{Name: "gamma", Worktree: gone.Path},
		{Name: "delta"},
	})
	if len(r.Matched) != 1 || r.Matched[0].Session.Name != "alpha" || r.Matched[0].Worktree.Session != "alpha" {
		t.Errorf("matched = %+v", r.Matched)
	}
	if len(r.Orphans) != 1 || r.Orphans[0].Session != "beta" {
		t.Errorf("orphans = %+v", r.Orphans)
	}
	var missing []string
	for _, s := range r.Missing {
		missing = append(missing, s.Name)
	}
	if !slices.Equal(missing, []string{"gamma", "delta"}) {
		t.Errorf("missing = %v", missing)
	}
}

func TestRemove(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, true)
	rm := func(session string, force bool) (worktree.Removed, error) {
		return f.m.Remove(ctx, worktree.RemoveOpts{Project: f.project, Session: session, Base: "main", Force: force})
	}

	t.Run("not found", func(t *testing.T) {
		if _, err := rm("nobody", false); !errors.Is(err, worktree.ErrNotFound) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("dirty refuses, force removes", func(t *testing.T) {
		c := f.create(t, "dirty")
		write(t, filepath.Join(c.Path, "README.md"), "edited")
		write(t, filepath.Join(c.Path, "scratch.txt"), "new")
		_, err := rm("dirty", false)
		var de *worktree.DirtyError
		if !errors.As(err, &de) || !errors.Is(err, worktree.ErrDirty) || len(de.Files) != 2 {
			t.Fatalf("err = %v", err)
		}
		if _, err := os.Stat(c.Path); err != nil {
			t.Fatalf("dirty worktree was touched: %v", err)
		}
		if _, err := rm("dirty", true); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(c.Path); !os.IsNotExist(err) {
			t.Errorf("worktree still there: %v", err)
		}
	})

	t.Run("clean and merged: branch can be deleted", func(t *testing.T) {
		c := f.create(t, "merged")
		// Ignored copied files don't count as changes.
		r, err := rm("merged", false)
		if err != nil {
			t.Fatal(err)
		}
		if r.Branch != "agent/merged" || !r.BranchMerged || r.Path == "" {
			t.Errorf("removed = %+v", r)
		}
		if _, err := os.Stat(c.Path); !os.IsNotExist(err) {
			t.Errorf("worktree still there: %v", err)
		}
		if err := f.m.DeleteBranch(ctx, f.project, "agent/merged", false); err != nil {
			t.Fatal(err)
		}
		if ok, _ := f.g.RefExists(ctx, f.project.Path, "refs/heads/agent/merged"); ok {
			t.Error("branch not deleted")
		}
	})

	t.Run("unmerged commit keeps branch", func(t *testing.T) {
		c := f.create(t, "work")
		commit(t, f.g, c.Path, "feature.txt", "wip")
		r, err := rm("work", false)
		if err != nil {
			t.Fatal(err)
		}
		if r.BranchMerged {
			t.Error("branch with new commit reported merged")
		}
		if err := f.m.DeleteBranch(ctx, f.project, "agent/work", false); err == nil {
			t.Error("deleting unmerged branch without force: want error")
		}
		if err := f.m.DeleteBranch(ctx, f.project, "agent/work", true); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("deleted directory is pruned", func(t *testing.T) {
		c := f.create(t, "vanished")
		if err := os.RemoveAll(c.Path); err != nil {
			t.Fatal(err)
		}
		if _, err := rm("vanished", false); err != nil {
			t.Fatal(err)
		}
		if wts, _ := f.m.List(ctx, f.project); len(wts) != 0 {
			t.Errorf("worktrees left: %+v", wts)
		}
		// The name is free again.
		if _, err := f.m.Create(ctx, worktree.CreateOpts{Project: f.project, Session: "vanished", Base: "main", ReuseBranch: true}); err != nil {
			t.Fatal(err)
		}
	})

	if _, err := os.Stat(filepath.Join(f.root, "forge")); err != nil {
		t.Errorf("repo dir with a live worktree should remain: %v", err)
	}
}

func TestEnsureExcluded(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, false)
	wt := f.create(t, "hooks")
	const rel = ".claude/settings.local.json"
	write(t, filepath.Join(wt.Path, rel), "{}")
	if st := git(t, f.g, wt.Path, "status", "--porcelain"); st == "" {
		t.Fatal("precondition: the settings file should show as untracked")
	}
	for range 2 { // idempotent
		if err := f.m.EnsureExcluded(ctx, wt.Path, rel); err != nil {
			t.Fatal(err)
		}
	}
	if st := git(t, f.g, wt.Path, "status", "--porcelain"); st != "" {
		t.Errorf("worktree not clean: %q", st)
	}
	if ok, err := f.g.CheckIgnore(ctx, wt.Path, rel); err != nil || !ok {
		t.Errorf("CheckIgnore = %v, %v", ok, err)
	}
	exclude, err := f.g.GitPath(ctx, wt.Path, "info/exclude")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exclude)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "/"+rel+"\n"); n != 1 {
		t.Errorf("pattern written %d times:\n%s", n, b)
	}
	if checkout, _ := filepath.EvalSymlinks(f.project.Path); !strings.HasPrefix(exclude, checkout) {
		t.Errorf("exclude %s is not the shared one under %s", exclude, f.project.Path)
	}
	// Tracked files are untouched.
	if st := git(t, f.g, f.project.Path, "status", "--porcelain"); st != "" {
		t.Errorf("checkout not clean: %q", st)
	}
}

func TestEnsureExcludedAlreadyIgnored(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, false)
	wt := f.create(t, "ign")
	// .env.local is in the committed .gitignore: nothing is written.
	if err := f.m.EnsureExcluded(ctx, wt.Path, ".env.local"); err != nil {
		t.Fatal(err)
	}
	exclude, _ := f.g.GitPath(ctx, wt.Path, "info/exclude")
	if b, _ := os.ReadFile(exclude); strings.Contains(string(b), ".env.local") {
		t.Errorf("wrote an exclude for an ignored file:\n%s", b)
	}
}

func TestCreateCustomBranch(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, true)
	custom := func(session, branch string) worktree.CreateOpts {
		return worktree.CreateOpts{Project: f.project, Session: session, Base: "main", Branch: branch}
	}

	t.Run("new branch from base", func(t *testing.T) {
		c, err := f.m.Create(ctx, custom("proj-1", "PROJ-1-fix-login"))
		if err != nil {
			t.Fatal(err)
		}
		if c.Branch != "PROJ-1-fix-login" || !c.BranchCreated || c.Reused || c.Tracking != "" || c.StartPoint != "origin/main" {
			t.Errorf("created = %+v", c)
		}
		if b := git(t, f.g, c.Path, "branch", "--show-current"); b != "PROJ-1-fix-login" {
			t.Errorf("branch = %q", b)
		}
		if want := filepath.Join(f.root, "forge", "proj-1"); c.Path != want {
			t.Errorf("path = %q, want %q", c.Path, want)
		}
	})

	t.Run("existing local branch", func(t *testing.T) {
		git(t, f.g, f.project.Path, "branch", "mine", "main")
		c, err := f.m.Create(ctx, custom("mine", "mine"))
		if err != nil {
			t.Fatal(err)
		}
		if !c.Reused || c.BranchCreated || c.Tracking != "" {
			t.Errorf("created = %+v", c)
		}
	})

	t.Run("remote-only branch (PR head)", func(t *testing.T) {
		other := filepath.Join(t.TempDir(), "other")
		git(t, f.g, filepath.Dir(other), "clone", "-q", f.origin, other)
		git(t, f.g, other, "switch", "-q", "-c", "feature/pr")
		head := commit(t, f.g, other, "pr.txt", "pr work")
		git(t, f.g, other, "push", "-q", "origin", "feature/pr")

		c, err := f.m.Create(ctx, custom("pr-7", "feature/pr"))
		if err != nil {
			t.Fatal(err)
		}
		if c.Reused || c.BranchCreated || c.Tracking != "origin/feature/pr" {
			t.Errorf("created = %+v", c)
		}
		if got := git(t, f.g, c.Path, "rev-parse", "HEAD"); got != head {
			t.Errorf("HEAD = %s, want PR head %s", got, head)
		}
		if up := git(t, f.g, c.Path, "rev-parse", "--abbrev-ref", "@{upstream}"); up != "origin/feature/pr" {
			t.Errorf("upstream = %q", up)
		}
	})

	t.Run("checked out in the main checkout", func(t *testing.T) {
		_, err := f.m.Create(ctx, custom("on-main", "main"))
		var co *worktree.BranchCheckedOutError
		if !errors.As(err, &co) || !errors.Is(err, worktree.ErrBranchCheckedOut) {
			t.Fatalf("err = %v", err)
		}
		if co.Branch != "main" || !sameDir(co.Path, f.project.Path) {
			t.Errorf("error = %+v", co)
		}
	})

	t.Run("checked out in another worktree", func(t *testing.T) {
		_, err := f.m.Create(ctx, custom("again", "PROJ-1-fix-login"))
		var co *worktree.BranchCheckedOutError
		if !errors.As(err, &co) || !sameDir(co.Path, filepath.Join(f.root, "forge", "proj-1")) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("invalid branch", func(t *testing.T) {
		if _, err := f.m.Create(ctx, custom("bad", "--force")); !errors.Is(err, worktree.ErrInvalidName) {
			t.Errorf("err = %v", err)
		}
	})
}

func sameDir(a, b string) bool {
	ra, _ := filepath.EvalSymlinks(a)
	rb, _ := filepath.EvalSymlinks(b)
	return ra == rb
}

func TestCreateFromCommit(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, true)
	at := func(session, branch, sha string) worktree.CreateOpts {
		return worktree.CreateOpts{Project: f.project, Session: session, Base: "main", Branch: branch, StartCommit: sha}
	}
	first := git(t, f.g, f.project.Path, "rev-parse", "HEAD")
	commit(t, f.g, f.project.Path, "later.txt", "later")

	t.Run("local commit", func(t *testing.T) {
		c, err := f.m.Create(ctx, at("fix-1", "fix/ci-1", first))
		if err != nil {
			t.Fatal(err)
		}
		if !c.BranchCreated || c.Reused || c.Tracking != "" || c.StartPoint != first {
			t.Errorf("created = %+v", c)
		}
		if head := git(t, f.g, c.Path, "rev-parse", "HEAD"); head != first {
			t.Errorf("HEAD = %s, want %s", head, first)
		}
	})

	t.Run("commit only on origin", func(t *testing.T) {
		other := filepath.Join(t.TempDir(), "other")
		git(t, f.g, filepath.Dir(other), "clone", "-q", f.origin, other)
		git(t, f.g, other, "switch", "-q", "-c", "ci-only")
		sha := commit(t, f.g, other, "ci.txt", "ci")
		git(t, f.g, other, "push", "-q", "origin", "ci-only")

		c, err := f.m.Create(ctx, at("fix-2", "fix/ci-2", sha))
		if err != nil {
			t.Fatal(err)
		}
		if head := git(t, f.g, c.Path, "rev-parse", "HEAD"); head != sha || !c.BranchCreated {
			t.Errorf("HEAD = %s, want %s; created = %+v", head, sha, c)
		}
	})

	t.Run("unknown commit", func(t *testing.T) {
		_, err := f.m.Create(ctx, at("fix-3", "fix/ci-3", strings.Repeat("ab", 20)))
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("not a sha", func(t *testing.T) {
		_, err := f.m.Create(ctx, at("fix-4", "fix/ci-4", "main"))
		if !errors.Is(err, worktree.ErrInvalidName) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("existing fix branch is reused", func(t *testing.T) {
		if _, err := f.m.Remove(ctx, worktree.RemoveOpts{Project: f.project, Session: "fix-1", Force: true}); err != nil {
			t.Skipf("remove: %v", err)
		}
		c, err := f.m.Create(ctx, at("fix-1b", "fix/ci-1", first))
		if err != nil {
			t.Fatal(err)
		}
		if !c.Reused || c.BranchCreated {
			t.Errorf("created = %+v", c)
		}
	})
}
