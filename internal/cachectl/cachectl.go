// Package cachectl measures what forge stores and reclaims it (P5.3): API
// caches, the transcript and docs indexes, kept artifact copies, agent
// worktrees and archived sessions, plus the database file itself.
//
// Every destructive action is two-phase. Plan lists exactly what an Op would
// remove (and what it skips, with a reason) so the UI can ask for
// confirmation with real numbers; Apply then removes only planned items that
// still qualify. Nothing here forces anything: dirty worktrees, pinned or
// linked artifact copies and worktrees forge doesn't own are always skipped.
//
// It does blocking I/O and must only be called from a tea.Cmd or the CLI.
package cachectl

import (
	"context"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/store"
)

// Type is one kind of stored data, one row in the Cache view.
type Type string

// Cache types, in display order.
const (
	GitHub      Type = "github"
	Plane       Type = "plane"
	Transcripts Type = "transcripts"
	Docs        Type = "docs"
	Artifacts   Type = "artifacts"
	Worktrees   Type = "worktrees"
	Archived    Type = "archived"
)

// Types lists every Type in display order.
var Types = []Type{GitHub, Plane, Transcripts, Docs, Artifacts, Worktrees, Archived}

// Label is the Type's display name.
func (t Type) Label() string {
	switch t {
	case GitHub:
		return "GitHub API"
	case Plane:
		return "Plane API"
	case Transcripts:
		return "Transcript index"
	case Docs:
		return "Docs index"
	case Artifacts:
		return "Artifact copies"
	case Worktrees:
		return "Worktrees"
	case Archived:
		return "Archived sessions"
	}
	return string(t)
}

// Prunable reports whether entries of t have an age that Prune can use.
func (t Type) Prunable() bool { return t != Docs }

// Bucket names for entries that belong to no single project.
const (
	AllProjects = "(all projects)"
	Workspace   = "(workspace)"
	NoProject   = "(none)"
)

// ProjectStat is one Type's usage for one project.
type ProjectStat struct {
	Project string
	Entries int
	Bytes   int64
	Oldest  time.Time
	// Orphans counts orphaned worktrees (Worktrees only).
	Orphans int
}

// TypeStat is one Type's usage, total and per project.
type TypeStat struct {
	Type    Type
	Entries int
	Bytes   int64
	Oldest  time.Time
	Orphans int
	// InMemory means the data is not persisted (the docs index): Bytes is
	// what the index reads, and clearing it means rebuilding it.
	InMemory bool
	Projects []ProjectStat
	// Err is set when the type could not be measured; the others still are.
	Err error
}

// Report is what Stats measured.
type Report struct {
	Types []TypeStat
	DB    store.DBStats
	At    time.Time
}

// Type returns the stat of t (zero when missing).
func (r Report) Type(t Type) TypeStat {
	for _, s := range r.Types {
		if s.Type == t {
			return s
		}
	}
	return TypeStat{Type: t}
}

// OpKind is what an Op does.
type OpKind int

// Op kinds.
const (
	// Clear removes every entry of the Type (for Op.Project when set).
	Clear OpKind = iota
	// Prune removes entries older than Op.OlderThan.
	Prune
	// Orphans removes orphaned worktrees in every project.
	Orphans
	// Vacuum rebuilds the database file.
	Vacuum
)

// Op is an action to plan.
type Op struct {
	Kind      OpKind
	Type      Type
	Project   string    // a ProjectStat.Project; "" = every project
	OlderThan time.Time // Prune only
}

// Item is one thing a Plan removes. ID is what Apply acts on: a cache key,
// an artifact path, a worktree path or a session id.
type Item struct {
	ID      string
	Label   string
	Project string
	Bytes   int64
}

// Skip is something an Op leaves alone, and why ("pinned", "dirty", ...).
type Skip struct {
	Label  string
	Reason string
}

// Plan is what an Op would do.
type Plan struct {
	Op      Op
	Items   []Item
	Skipped []Skip
	// DB is the database before a Vacuum.
	DB store.DBStats
}

// Bytes is the total size of the planned items.
func (p Plan) Bytes() int64 {
	var n int64
	for _, it := range p.Items {
		n += it.Bytes
	}
	return n
}

// Empty reports whether applying p would do nothing.
func (p Plan) Empty() bool { return p.Op.Kind != Vacuum && len(p.Items) == 0 }

// Result is what Apply did.
type Result struct {
	Op      Op
	Removed int
	Bytes   int64
	Skipped []Skip
	// DBBefore and DBAfter are the file sizes around a Vacuum (WAL
	// included).
	DBBefore, DBAfter int64
}

// Store is the persistence cachectl reads and clears. StoreFrom adapts
// *store.Store.
type Store interface {
	CacheRows(ctx context.Context, f store.CacheFilter) ([]store.CacheRow, error)
	DeleteCacheKeys(ctx context.Context, keys []string) (int64, error)
	Artifacts(ctx context.Context) ([]store.Artifact, error)
	ForgetArtifacts(ctx context.Context, paths []string) error
	Sessions(ctx context.Context) ([]store.Session, error)
	EventBytes(ctx context.Context) (map[int64]int64, error)
	DBStats(ctx context.Context) (store.DBStats, error)
	Vacuum(ctx context.Context) error
}

type storeAdapter struct{ s *store.Store }

// StoreFrom returns the Store backed by the forge database.
func StoreFrom(s *store.Store) Store { return storeAdapter{s} }

func (a storeAdapter) CacheRows(ctx context.Context, f store.CacheFilter) ([]store.CacheRow, error) {
	return a.s.Cache().Rows(ctx, f)
}

func (a storeAdapter) DeleteCacheKeys(ctx context.Context, keys []string) (int64, error) {
	return a.s.Cache().DeleteKeys(ctx, keys)
}

func (a storeAdapter) Artifacts(ctx context.Context) ([]store.Artifact, error) {
	return a.s.Artifacts().List(ctx, "")
}

func (a storeAdapter) ForgetArtifacts(ctx context.Context, paths []string) error {
	return a.s.Artifacts().Forget(ctx, paths)
}

func (a storeAdapter) Sessions(ctx context.Context) ([]store.Session, error) {
	return a.s.Sessions().List(ctx, store.SessionFilter{})
}

func (a storeAdapter) EventBytes(ctx context.Context) (map[int64]int64, error) {
	return a.s.Events().BytesBySession(ctx)
}

func (a storeAdapter) DBStats(ctx context.Context) (store.DBStats, error) { return a.s.DBStats(ctx) }
func (a storeAdapter) Vacuum(ctx context.Context) error                   { return a.s.Vacuum(ctx) }

// Sessions is what cachectl needs from the session service.
type Sessions interface {
	// Purge deletes an archived session, removing a clean worktree first.
	Purge(ctx context.Context, x store.Session) error
	// ResetTranscripts drops the in-memory transcript index.
	ResetTranscripts()
}

// Config wires a Controller.
type Config struct {
	Store     Store
	Worktrees worktree.Manager
	Sessions  Sessions
	Projects  []config.Project
	// WorktreeRoot is sessions.worktree_root: only orphans under it are
	// removed, since forge made those.
	WorktreeRoot string
	// DataDir holds per-session dirs (<DataDir>/sessions/<name>).
	DataDir string
	// Dirty reports uncommitted changes in a worktree, so the plan can show
	// them as skipped up front; nil leaves it to Apply.
	Dirty func(ctx context.Context, dir string) (bool, error)
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Controller measures and clears forge's stored data.
type Controller struct{ c Config }

// New returns a Controller.
func New(c Config) *Controller {
	if c.Now == nil {
		c.Now = time.Now
	}
	return &Controller{c: c}
}

// DirtyFromGit adapts worktree.Git's Status to Config.Dirty.
func DirtyFromGit(g interface {
	Status(ctx context.Context, dir string) ([]string, error)
},
) func(context.Context, string) (bool, error) {
	return func(ctx context.Context, dir string) (bool, error) {
		lines, err := g.Status(ctx, dir)
		return len(lines) > 0, err
	}
}
