package cachectl

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/daan3011/forge/internal/artifacts"
	"github.com/daan3011/forge/internal/cilog"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/docs"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/sessions/transcript"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/store"
)

// TabKind is tabkit.CacheKind, the kind tabs cache their lists under. It is
// repeated here so cachectl stays free of the UI packages; a tabkit test
// checks they match.
const TabKind = "tab"

// Key prefixes of the GitHub data cached under TabKind; the rest of that
// kind (e.g. the Home placeholder) is not API data.
var githubPrefixes = []string{"tab:prs:", "tab:pipelines:", "pr-detail:"}

// Skip reasons.
const (
	reasonPinned   = "pinned"
	reasonLinked   = "linked"
	reasonNoSource = "not from a session"
	reasonInUse    = "in use"
	reasonOutside  = "outside the worktree root"
	reasonDirty    = "dirty"
	reasonChanged  = "changed since"
)

// entry is one measured thing of a Type. A non-empty skip means no Op
// removes it; it still counts in Stats.
type entry struct {
	Item
	at     time.Time
	skip   string
	orphan bool

	// What Apply needs beyond the ID.
	proj    config.Project
	session string
	x       store.Session
}

// gather measures every entry of t.
func (c *Controller) gather(ctx context.Context, t Type) ([]entry, error) {
	switch t {
	case GitHub:
		return c.kvEntries(ctx, c.githubProject,
			store.CacheFilter{Kinds: []string{TabKind}, KeyPrefixes: githubPrefixes},
			store.CacheFilter{Kinds: []string{cilog.Kind}})
	case Plane:
		return c.kvEntries(ctx, c.planeProject, store.CacheFilter{Kinds: []string{plane.CacheKind}})
	case Transcripts:
		return c.kvEntries(ctx, func(r store.CacheRow) string { return orBucket(r.Project, NoProject) },
			store.CacheFilter{Kinds: []string{transcript.CacheKind}})
	case Docs:
		return c.docEntries(), nil
	case Artifacts:
		return c.artifactEntries(ctx)
	case Worktrees:
		return c.worktreeEntries(ctx)
	case Archived:
		return c.archivedEntries(ctx)
	}
	return nil, fmt.Errorf("unknown cache type %q", t)
}

func (c *Controller) kvEntries(ctx context.Context, project func(store.CacheRow) string, fs ...store.CacheFilter) ([]entry, error) {
	var out []entry
	for _, f := range fs {
		rows, err := c.c.Store.CacheRows(ctx, f)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, entry{
				Item: Item{ID: r.Key, Label: r.Key, Project: project(r), Bytes: r.Bytes},
				at:   r.FetchedAt,
			})
		}
	}
	return out, nil
}

// githubProject maps a GitHub cache row to a project: its project column,
// else the repo in its key (pr-detail:owner/repo#N, ci_errors:owner/repo#…).
func (c *Controller) githubProject(r store.CacheRow) string {
	if r.Project != "" {
		return r.Project
	}
	if _, rest, ok := strings.Cut(r.Key, ":"); ok {
		if repo, _, ok := strings.Cut(rest, "#"); ok && strings.Contains(repo, "/") {
			for _, p := range c.c.Projects {
				if strings.EqualFold(p.Repo, repo) {
					return p.Name
				}
			}
			return repo
		}
	}
	return AllProjects
}

// planeProject maps a Plane cache row (project column = Plane project id)
// to the forge project linked to it.
func (c *Controller) planeProject(r store.CacheRow) string {
	if r.Project == "" {
		return Workspace
	}
	for _, p := range c.c.Projects {
		if p.PlaneProjectID == r.Project {
			return p.Name
		}
	}
	return r.Project
}

func orBucket(s, bucket string) string {
	if s == "" {
		return bucket
	}
	return s
}

// docEntries lists the docs the in-memory index holds; their size is what
// it reads (files over its cap are indexed by title only).
func (c *Controller) docEntries() []entry {
	var out []entry
	for _, p := range c.c.Projects {
		root := p.DocsPath()
		if root == "" {
			continue
		}
		tree, err := docs.Scan(root)
		if err != nil || tree == nil {
			continue
		}
		var walk func(n *docs.Node)
		walk = func(n *docs.Node) {
			if n.IsDir {
				for _, ch := range n.Children {
					walk(ch)
				}
				return
			}
			var size int64
			if fi, err := os.Stat(n.Path); err == nil && fi.Size() <= docs.MaxIndexBytes {
				size = fi.Size()
			}
			out = append(out, entry{Item: Item{ID: n.Path, Label: n.Rel, Project: p.Name, Bytes: size}, at: n.ModTime})
		}
		walk(tree)
	}
	return out
}

// artifactEntries lists the kept copies in .forge/artifacts. Only detached
// copies (their worktree is gone) that aren't pinned may be removed.
func (c *Controller) artifactEntries(ctx context.Context) ([]entry, error) {
	all, err := c.c.Store.Artifacts(ctx)
	if err != nil {
		return nil, err
	}
	var out []entry
	for _, a := range all {
		if !artifacts.IsKept(a.Path) {
			continue
		}
		e := entry{
			Item: Item{ID: a.Path, Label: display(a.Path), Project: orBucket(a.Project, NoProject), Bytes: a.Size},
			at:   a.CreatedAt,
		}
		switch {
		case a.Pinned:
			e.skip = reasonPinned
		case a.Link == nil:
			e.skip = reasonNoSource
		case a.Link.Active():
			e.skip = reasonLinked
		}
		out = append(out, e)
	}
	return out, nil
}

// worktreeEntries lists every project's agent worktrees. Orphans (no
// session refers to them) under the worktree root may be removed; the rest
// are in use or not forge's.
func (c *Controller) worktreeEntries(ctx context.Context) ([]entry, error) {
	sess, err := c.c.Store.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	refs := make([]worktree.SessionRef, len(sess))
	byName := map[string]store.Session{}
	for i, x := range sess {
		refs[i] = worktree.SessionRef{Name: x.Name, Worktree: x.Worktree}
		byName[x.Name] = x
	}
	var (
		out  []entry
		errs []error
	)
	for _, p := range c.c.Projects {
		if p.Path == "" {
			continue
		}
		wts, err := c.c.Worktrees.List(ctx, p)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.Name, err))
			continue
		}
		rep := worktree.Reconcile(wts, refs)
		orphan := map[string]bool{}
		for _, o := range rep.Orphans {
			orphan[o.Path] = true
		}
		for _, wt := range wts {
			size, mod := dirSize(wt.Path)
			e := entry{
				Item:    Item{ID: wt.Path, Label: display(wt.Path), Project: p.Name, Bytes: size},
				at:      mod,
				orphan:  orphan[wt.Path],
				proj:    p,
				session: wt.Session,
			}
			if x, ok := byName[wt.Session]; ok && !e.orphan {
				e.at = x.CreatedAt
			}
			switch {
			case !e.orphan:
				e.skip = reasonInUse
			case c.c.WorktreeRoot == "" || !samePath(wt.Path, worktree.Path(c.c.WorktreeRoot, p, wt.Session)):
				e.skip = reasonOutside
			}
			out = append(out, e)
		}
	}
	return out, errors.Join(errs...)
}

// archivedEntries lists archived sessions with what deleting them frees:
// their events, data dir and any worktree still on disk.
func (c *Controller) archivedEntries(ctx context.Context) ([]entry, error) {
	sess, err := c.c.Store.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	ev, err := c.c.Store.EventBytes(ctx)
	if err != nil {
		return nil, err
	}
	var out []entry
	for _, x := range sess {
		if !x.Archived() {
			continue
		}
		size := ev[x.ID]
		if c.c.DataDir != "" && worktree.CheckName(x.Name) == nil {
			n, _ := dirSize(filepath.Join(c.c.DataDir, "sessions", x.Name))
			size += n
		}
		if x.Worktree != "" && !x.WorktreeExternal {
			n, _ := dirSize(x.Worktree)
			size += n
		}
		out = append(out, entry{
			Item: Item{ID: fmt.Sprint(x.ID), Label: x.Name, Project: orBucket(x.Project, NoProject), Bytes: size},
			at:   x.ArchivedAt,
			x:    x,
		})
	}
	return out, nil
}

// dirSize sums the sizes of regular files under dir (0 when missing) and
// returns dir's own modification time.
func dirSize(dir string) (int64, time.Time) {
	fi, err := os.Stat(dir)
	if err != nil {
		return 0, time.Time{}
	}
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable parts are skipped
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n, fi.ModTime()
}

func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// display shortens paths under the home dir to ~/….
func display(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return path
}

// stat aggregates entries into a TypeStat with per-project rows sorted by
// size, largest first.
func stat(t Type, es []entry) TypeStat {
	s := TypeStat{Type: t, InMemory: t == Docs}
	by := map[string]*ProjectStat{}
	for _, e := range es {
		ps := by[e.Project]
		if ps == nil {
			ps = &ProjectStat{Project: e.Project}
			by[e.Project] = ps
		}
		for _, x := range []*int{&s.Entries, &ps.Entries} {
			*x++
		}
		s.Bytes += e.Bytes
		ps.Bytes += e.Bytes
		if e.orphan {
			s.Orphans++
			ps.Orphans++
		}
		if !e.at.IsZero() {
			if s.Oldest.IsZero() || e.at.Before(s.Oldest) {
				s.Oldest = e.at
			}
			if ps.Oldest.IsZero() || e.at.Before(ps.Oldest) {
				ps.Oldest = e.at
			}
		}
	}
	for _, ps := range by {
		s.Projects = append(s.Projects, *ps)
	}
	sort.Slice(s.Projects, func(i, j int) bool {
		a, b := s.Projects[i], s.Projects[j]
		if a.Bytes != b.Bytes {
			return a.Bytes > b.Bytes
		}
		return a.Project < b.Project
	})
	return s
}
