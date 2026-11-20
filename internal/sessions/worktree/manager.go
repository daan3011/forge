package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/daan3011/forge/internal/config"
)

// Remote is the remote base branches are fetched from.
const Remote = "origin"

// Worktrees is the Manager backed by a Git.
type Worktrees struct {
	root string
	git  Git
}

var _ Manager = (*Worktrees)(nil)

// New returns a Manager creating worktrees under root
// (sessions.worktree_root).
func New(root string, g Git) *Worktrees {
	return &Worktrees{root: root, git: g}
}

// Root is the worktree root.
func (w *Worktrees) Root() string { return w.root }

// Create implements Manager.
func (w *Worktrees) Create(ctx context.Context, opts CreateOpts) (Created, error) {
	p := opts.Project
	if err := CheckName(opts.Session); err != nil {
		return Created{}, err
	}
	if err := checkBase(opts.Base); err != nil {
		return Created{}, err
	}
	branch, custom := Branch(opts.Session), opts.Branch != ""
	if custom {
		if err := CheckBranch(opts.Branch); err != nil {
			return Created{}, err
		}
		branch = opts.Branch
	}
	path := Path(w.root, p, opts.Session)
	if _, err := os.Lstat(path); err == nil {
		return Created{}, fmt.Errorf("%w: %s", ErrPathExists, path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Created{}, err
	}

	all, err := w.git.WorktreeList(ctx, p.Path)
	if err != nil {
		return Created{}, err
	}
	stale := false
	for _, wt := range all {
		if wt.Branch == branch && !wt.Prunable {
			return Created{}, &BranchCheckedOutError{Branch: branch, Path: wt.Path}
		}
		if wt.Prunable && samePath(wt.Path, path) {
			stale = true
		}
	}
	if stale {
		// A deleted directory is still registered at this path.
		if err := w.git.WorktreePrune(ctx, p.Path); err != nil {
			return Created{}, err
		}
	}

	res := Created{Path: path, Branch: branch}
	exists, err := w.git.RefExists(ctx, p.Path, "refs/heads/"+branch)
	if err != nil {
		return Created{}, err
	}
	if exists && !opts.ReuseBranch && !custom {
		return Created{}, &BranchExistsError{Branch: branch}
	}
	upstream := ""
	if !exists && custom {
		if upstream, res.FetchErr, err = w.remoteBranch(ctx, p.Path, branch); err != nil {
			return Created{}, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Created{}, err
	}

	switch {
	case exists:
		res.Reused = true
		if err := w.git.WorktreeAddExisting(ctx, p.Path, path, branch); err != nil {
			return Created{}, err
		}
	case upstream != "":
		res.Tracking = upstream
		if err := w.git.WorktreeAddTracking(ctx, p.Path, path, branch, upstream); err != nil {
			return Created{}, err
		}
	default:
		start, fetchErr, err := w.startPoint(ctx, p.Path, opts.Base)
		if err != nil {
			return Created{}, err
		}
		res.StartPoint, res.FetchErr, res.BranchCreated = start, errors.Join(res.FetchErr, fetchErr), true
		if err := w.git.WorktreeAdd(ctx, p.Path, path, branch, start); err != nil {
			return Created{}, err
		}
	}

	res.Copied, res.Skipped, err = copyFiles(p.Path, path, p.WorktreeCopy)
	if err != nil {
		// Only a branch made just now is deleted again; a tracking branch
		// is local too, but its commits live on origin, so it goes as well.
		del := res.BranchCreated || res.Tracking != ""
		return Created{}, errors.Join(fmt.Errorf("worktree: copy files: %w", err), w.rollback(ctx, p, path, branch, del))
	}
	return res, nil
}

// remoteBranch fetches branch from origin and returns "origin/<branch>" if
// it exists there, "" if not (or there is no origin). A failed fetch is
// returned separately and is not fatal.
func (w *Worktrees) remoteBranch(ctx context.Context, repo, branch string) (upstream string, fetchErr, err error) {
	hasRemote, err := w.git.HasRemote(ctx, repo, Remote)
	if err != nil || !hasRemote {
		return "", nil, err
	}
	fetchErr = w.git.FetchBranch(ctx, repo, Remote, branch)
	ref := Remote + "/" + branch
	ok, err := w.git.RefExists(ctx, repo, "refs/remotes/"+ref)
	if err != nil || !ok {
		// A branch missing on origin also fails the fetch: that is the
		// normal "new branch" case, not worth reporting.
		return "", nil, err
	}
	return ref, fetchErr, nil
}

// startPoint fetches base from origin (when there is one) and returns the
// ref to branch from: origin/<base> if it exists, else the local base. A
// failed fetch is returned separately and is not fatal.
func (w *Worktrees) startPoint(ctx context.Context, repo, base string) (start string, fetchErr, err error) {
	hasRemote, err := w.git.HasRemote(ctx, repo, Remote)
	if err != nil {
		return "", nil, err
	}
	if hasRemote {
		fetchErr = w.git.FetchBranch(ctx, repo, Remote, base)
		remoteRef := Remote + "/" + base
		ok, err := w.git.RefExists(ctx, repo, "refs/remotes/"+remoteRef)
		if err != nil {
			return "", nil, err
		}
		if ok {
			return remoteRef, fetchErr, nil
		}
	}
	ok, err := w.git.RefExists(ctx, repo, "refs/heads/"+base)
	if err != nil {
		return "", nil, err
	}
	if !ok {
		if fetchErr != nil {
			return "", nil, fmt.Errorf("worktree: base %q not found locally and fetch failed: %w", base, fetchErr)
		}
		return "", nil, fmt.Errorf("worktree: base branch %q not found", base)
	}
	return base, fetchErr, nil
}

// rollback undoes a half-made worktree.
func (w *Worktrees) rollback(ctx context.Context, p config.Project, path, branch string, deleteBranch bool) error {
	// A cancelled ctx must not leave the mess behind.
	ctx = context.WithoutCancel(ctx)
	var errs []error
	if err := w.git.WorktreeRemove(ctx, p.Path, path, true); err != nil {
		errs = append(errs, fmt.Errorf("rollback: %w", err))
	}
	if deleteBranch {
		if err := w.git.DeleteBranch(ctx, p.Path, branch, true); err != nil {
			errs = append(errs, fmt.Errorf("rollback: %w", err))
		}
	}
	_ = os.Remove(filepath.Dir(path)) // only if empty
	return errors.Join(errs...)
}

// List implements Manager: the project's agent worktrees, i.e. those under
// <root>/<repo>/ or on an agent/ branch. The main checkout is never listed.
func (w *Worktrees) List(ctx context.Context, project config.Project) ([]Worktree, error) {
	all, err := w.git.WorktreeList(ctx, project.Path)
	if err != nil {
		return nil, err
	}
	dir := realPath(repoDir(w.root, project))
	var res []Worktree
	for i, wt := range all {
		if i == 0 || wt.Bare { // the main worktree comes first
			continue
		}
		if parent := filepath.Dir(realPath(wt.Path)); parent == dir {
			wt.Session = filepath.Base(wt.Path)
		} else if s, ok := strings.CutPrefix(wt.Branch, BranchPrefix); ok {
			wt.Session = s
		} else {
			continue
		}
		res = append(res, wt)
	}
	return res, nil
}

// Remove implements Manager.
func (w *Worktrees) Remove(ctx context.Context, opts RemoveOpts) (Removed, error) {
	p := opts.Project
	if err := CheckName(opts.Session); err != nil {
		return Removed{}, err
	}
	if opts.Base != "" {
		if err := checkBase(opts.Base); err != nil {
			return Removed{}, err
		}
	}
	path := Path(w.root, p, opts.Session)
	wts, err := w.List(ctx, p)
	if err != nil {
		return Removed{}, err
	}
	var wt *Worktree
	for i := range wts {
		if samePath(wts[i].Path, path) {
			wt = &wts[i]
			break
		}
	}
	if wt == nil {
		return Removed{}, fmt.Errorf("%w: %s", ErrNotFound, path)
	}

	if wt.Prunable {
		if err := w.git.WorktreePrune(ctx, p.Path); err != nil {
			return Removed{}, err
		}
	} else {
		if !opts.Force {
			files, err := w.git.Status(ctx, wt.Path)
			if err != nil {
				return Removed{}, err
			}
			if len(files) > 0 {
				return Removed{}, &DirtyError{Path: wt.Path, Files: files}
			}
		}
		if err := w.git.WorktreeRemove(ctx, p.Path, wt.Path, opts.Force); err != nil {
			return Removed{}, err
		}
	}
	_ = os.Remove(repoDir(w.root, p)) // only if empty

	res := Removed{Path: wt.Path, Branch: wt.Branch}
	if opts.Base != "" && wt.Branch != "" {
		res.BranchMerged, err = w.merged(ctx, p.Path, wt.Branch, opts.Base)
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

// merged reports whether branch is contained in origin/<base> or <base>.
func (w *Worktrees) merged(ctx context.Context, repo, branch, base string) (bool, error) {
	for _, ref := range []string{"refs/remotes/" + Remote + "/" + base, "refs/heads/" + base} {
		ok, err := w.git.RefExists(ctx, repo, ref)
		if err != nil || !ok {
			if err != nil {
				return false, err
			}
			continue
		}
		in, err := w.git.IsAncestor(ctx, repo, "refs/heads/"+branch, ref)
		if err != nil || in {
			return in, err
		}
	}
	return false, nil
}

// DeleteBranch implements Manager. Without force, git refuses unless the
// branch is merged.
func (w *Worktrees) DeleteBranch(ctx context.Context, project config.Project, branch string, force bool) error {
	if err := CheckBranch(branch); err != nil {
		return err
	}
	return w.git.DeleteBranch(ctx, project.Path, branch, force)
}

// SessionRef is what Reconcile needs to know about a stored session.
type SessionRef struct {
	Name     string
	Worktree string // path; empty when not recorded
}

// Pair is a session and its worktree.
type Pair struct {
	Session  SessionRef
	Worktree Worktree
}

// Report is the result of Reconcile.
type Report struct {
	Matched []Pair
	Orphans []Worktree   // agent worktrees no session refers to
	Missing []SessionRef // sessions whose worktree is gone
}

// Reconcile maps worktrees (from List) to sessions. A session matches a
// worktree by recorded path, or by name when it has no recorded path. A
// matched worktree whose directory is gone counts as Missing.
func Reconcile(wts []Worktree, sessions []SessionRef) Report {
	var r Report
	used := make([]bool, len(wts))
	for _, s := range sessions {
		idx := -1
		for i, wt := range wts {
			if used[i] {
				continue
			}
			if (s.Worktree != "" && samePath(s.Worktree, wt.Path)) ||
				(s.Worktree == "" && s.Name == wt.Session) {
				idx = i
				break
			}
		}
		switch {
		case idx < 0:
			r.Missing = append(r.Missing, s)
		case wts[idx].Prunable:
			used[idx] = true
			r.Missing = append(r.Missing, s)
		default:
			used[idx] = true
			r.Matched = append(r.Matched, Pair{Session: s, Worktree: wts[idx]})
		}
	}
	for i, wt := range wts {
		if !used[i] {
			r.Orphans = append(r.Orphans, wt)
		}
	}
	return r
}

// samePath compares paths after resolving symlinks (macOS temp dirs live
// behind /var -> /private/var, and git reports resolved paths).
func samePath(a, b string) bool {
	return realPath(a) == realPath(b)
}

// realPath resolves symlinks in the longest existing prefix of p, so it
// also works for paths that no longer (or don't yet) exist.
func realPath(p string) string {
	p = filepath.Clean(p)
	var rest []string
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(append([]string{r}, rest...)...)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(append([]string{p}, rest...)...)
		}
		rest = append([]string{filepath.Base(p)}, rest...)
		p = parent
	}
}

// EnsureExcluded implements Manager. The pattern is anchored ("/" + rel) in
// the repo's info/exclude, which all its worktrees share.
func (w *Worktrees) EnsureExcluded(ctx context.Context, dir, rel string) error {
	ignored, err := w.git.CheckIgnore(ctx, dir, rel)
	if err != nil || ignored {
		return err
	}
	path, err := w.git.GitPath(ctx, dir, "info/exclude")
	if err != nil {
		return err
	}
	pattern := "/" + strings.TrimPrefix(filepath.ToSlash(rel), "/")
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("worktree: %w", err)
	}
	for l := range strings.SplitSeq(string(b), "\n") {
		if strings.TrimSpace(l) == pattern {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	line := "# forge: agent session hooks (P1.4)\n" + pattern + "\n"
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		line = "\n" + line
	}
	_, werr := f.WriteString(line)
	return errors.Join(werr, f.Close())
}
