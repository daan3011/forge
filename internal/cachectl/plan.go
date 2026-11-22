package cachectl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/daan3011/forge/internal/sessions/worktree"
)

// ErrNotPlannable means the Op has no meaning for its Type (e.g. pruning
// the docs index, which is only ever rebuilt).
var ErrNotPlannable = errors.New("nothing to remove for this cache")

// Stats measures every Type. A Type that fails carries its error in Err;
// the others are still measured.
func (c *Controller) Stats(ctx context.Context) (Report, error) {
	r := Report{At: c.c.Now()}
	for _, t := range Types {
		es, err := c.gather(ctx, t)
		s := stat(t, es)
		s.Err = err
		r.Types = append(r.Types, s)
	}
	db, err := c.c.Store.DBStats(ctx)
	r.DB = db
	return r, err
}

// Plan lists what op would remove and what it would skip.
func (c *Controller) Plan(ctx context.Context, op Op) (Plan, error) {
	p, _, err := c.plan(ctx, op)
	return p, err
}

func (c *Controller) plan(ctx context.Context, op Op) (Plan, map[string]entry, error) {
	p := Plan{Op: op}
	switch op.Kind {
	case Vacuum:
		db, err := c.c.Store.DBStats(ctx)
		p.DB = db
		return p, nil, err
	case Orphans:
		op.Type, op.Project = Worktrees, ""
		p.Op = op
	case Prune:
		if op.OlderThan.IsZero() {
			return p, nil, fmt.Errorf("prune: no cut-off")
		}
	}
	if op.Type == Docs {
		return p, nil, ErrNotPlannable
	}
	es, err := c.gather(ctx, op.Type)
	if err != nil && len(es) == 0 {
		return p, nil, err
	}
	// Uncommitted work is never removed; checking up front shows it in the
	// confirmation.
	dirtyCheck := c.c.Dirty != nil && (op.Type == Worktrees || op.Type == Archived)
	byID := map[string]entry{}
	for _, e := range es {
		if op.Project != "" && e.Project != op.Project {
			continue
		}
		if op.Kind == Prune && (e.at.IsZero() || !e.at.Before(op.OlderThan)) {
			continue
		}
		if e.skip == "" && dirtyCheck {
			if dir := e.worktreeDir(); dir != "" {
				if d, derr := c.c.Dirty(ctx, dir); derr == nil && d {
					e.skip = reasonDirty
				}
			}
		}
		if e.skip != "" {
			if e.skip != reasonInUse { // a session's worktree is never a candidate
				p.Skipped = append(p.Skipped, Skip{Label: e.Label, Reason: e.skip})
			}
			continue
		}
		p.Items = append(p.Items, e.Item)
		byID[e.ID] = e
	}
	return p, byID, nil
}

// worktreeDir is the worktree an Apply of e would remove, if any.
func (e entry) worktreeDir() string {
	switch {
	case e.session != "":
		return e.ID
	case e.x.ID != 0 && e.x.Worktree != "" && !e.x.WorktreeExternal:
		if _, err := os.Stat(e.x.Worktree); err == nil {
			return e.x.Worktree
		}
	}
	return ""
}

// Apply carries out a confirmed plan. Only items that still qualify are
// removed (state may have changed while the confirmation was open); the
// rest are reported as skipped.
func (c *Controller) Apply(ctx context.Context, p Plan) (Result, error) {
	res := Result{Op: p.Op, Skipped: append([]Skip(nil), p.Skipped...)}
	if p.Op.Kind == Vacuum {
		return c.vacuum(ctx, res)
	}
	_, fresh, err := c.plan(ctx, p.Op)
	if err != nil {
		return res, err
	}
	var todo []entry
	for _, it := range p.Items {
		e, ok := fresh[it.ID]
		if !ok {
			res.Skipped = append(res.Skipped, Skip{Label: it.Label, Reason: reasonChanged})
			continue
		}
		todo = append(todo, e)
	}
	switch p.Op.Type {
	case GitHub, Plane, Transcripts:
		err = c.applyKV(ctx, todo, &res)
	case Artifacts:
		err = c.applyArtifacts(ctx, todo, &res)
	case Worktrees:
		c.applyWorktrees(ctx, todo, &res)
	case Archived:
		c.applyArchived(ctx, todo, &res)
	}
	return res, err
}

func (c *Controller) applyKV(ctx context.Context, todo []entry, res *Result) error {
	keys := make([]string, len(todo))
	for i, e := range todo {
		keys[i] = e.ID
		res.Bytes += e.Bytes
	}
	n, err := c.c.Store.DeleteCacheKeys(ctx, keys)
	if err != nil {
		res.Bytes = 0
		return err
	}
	res.Removed = int(n)
	if res.Op.Type == Transcripts && c.c.Sessions != nil {
		c.c.Sessions.ResetTranscripts()
	}
	return nil
}

func (c *Controller) applyArtifacts(ctx context.Context, todo []entry, res *Result) error {
	var gone []string
	for _, e := range todo {
		if err := os.Remove(e.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
			res.Skipped = append(res.Skipped, Skip{Label: e.Label, Reason: err.Error()})
			continue
		}
		_ = os.Remove(filepath.Dir(e.ID)) // the session folder, only if empty
		gone = append(gone, e.ID)
		res.Removed++
		res.Bytes += e.Bytes
	}
	return c.c.Store.ForgetArtifacts(ctx, gone)
}

func (c *Controller) applyWorktrees(ctx context.Context, todo []entry, res *Result) {
	for _, e := range todo {
		_, err := c.c.Worktrees.Remove(ctx, worktree.RemoveOpts{Project: e.proj, Session: e.session})
		if skip := skipFor(err); skip != "" {
			res.Skipped = append(res.Skipped, Skip{Label: e.Label, Reason: skip})
			continue
		}
		res.Removed++
		res.Bytes += e.Bytes
	}
}

func (c *Controller) applyArchived(ctx context.Context, todo []entry, res *Result) {
	for _, e := range todo {
		if c.c.Sessions == nil {
			res.Skipped = append(res.Skipped, Skip{Label: e.Label, Reason: "session service unavailable"})
			continue
		}
		if skip := skipFor(c.c.Sessions.Purge(ctx, e.x)); skip != "" {
			res.Skipped = append(res.Skipped, Skip{Label: e.Label, Reason: skip})
			continue
		}
		res.Removed++
		res.Bytes += e.Bytes
	}
}

// skipFor turns a per-item error into a skip reason ("" = it worked).
func skipFor(err error) string {
	var de *worktree.DirtyError
	switch {
	case err == nil, errors.Is(err, worktree.ErrNotFound):
		return ""
	case errors.As(err, &de):
		return reasonDirty
	}
	return err.Error()
}

func (c *Controller) vacuum(ctx context.Context, res Result) (Result, error) {
	before, err := c.c.Store.DBStats(ctx)
	if err != nil {
		return res, err
	}
	if err := c.c.Store.Vacuum(ctx); err != nil {
		return res, err
	}
	after, err := c.c.Store.DBStats(ctx)
	res.DBBefore, res.DBAfter = before.Bytes+before.WAL, after.Bytes+after.WAL
	res.Bytes = max(res.DBBefore-res.DBAfter, 0)
	return res, err
}

// SkipSummary counts skips by reason, most common first:
// "3 pinned, 5 linked". Empty when nothing was skipped.
func SkipSummary(skips []Skip) string {
	counts := map[string]int{}
	var order []string
	for _, s := range skips {
		if counts[s.Reason] == 0 {
			order = append(order, s.Reason)
		}
		counts[s.Reason]++
	}
	sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })
	parts := make([]string, len(order))
	for i, r := range order {
		parts[i] = fmt.Sprintf("%d %s", counts[r], r)
	}
	return strings.Join(parts, ", ")
}
