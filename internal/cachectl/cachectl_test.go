package cachectl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/store"
)

var t0 = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

type fakeWT struct {
	worktree.Manager
	trees   map[string][]worktree.Worktree // project -> worktrees
	dirty   map[string]bool                // path -> dirty
	root    string
	removed []string
}

func (f *fakeWT) List(_ context.Context, p config.Project) ([]worktree.Worktree, error) {
	return f.trees[p.Name], nil
}

func (f *fakeWT) Remove(_ context.Context, o worktree.RemoveOpts) (worktree.Removed, error) {
	path := worktree.Path(f.root, o.Project, o.Session)
	if f.dirty[path] && !o.Force {
		return worktree.Removed{}, &worktree.DirtyError{Path: path}
	}
	ts := f.trees[o.Project.Name]
	for i, wt := range ts {
		if wt.Path == path {
			f.trees[o.Project.Name] = slices.Delete(ts, i, i+1)
			f.removed = append(f.removed, path)
			return worktree.Removed{Path: path}, nil
		}
	}
	return worktree.Removed{}, worktree.ErrNotFound
}

type fakeSessions struct {
	st     *store.Store
	dirty  map[string]bool // session name -> dirty worktree
	purged []string
	resets int
}

func (f *fakeSessions) Purge(ctx context.Context, x store.Session) error {
	if f.dirty[x.Name] {
		return &worktree.DirtyError{Path: x.Worktree}
	}
	f.purged = append(f.purged, x.Name)
	return f.st.Sessions().Delete(ctx, x.ID)
}

func (f *fakeSessions) ResetTranscripts() { f.resets++ }

type fixture struct {
	ctl  *Controller
	st   *store.Store
	wt   *fakeWT
	sess *fakeSessions
	dir  string
	proj config.Project
	now  time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	now := t0
	st, err := store.Open(ctx, filepath.Join(dir, "forge.db"), store.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f := &fixture{
		st:   st,
		dir:  dir,
		proj: config.Project{Name: "forge", Repo: "daan/forge", Path: filepath.Join(dir, "repo"), PlaneProjectID: "pl-1"},
		now:  t0.Add(10 * 24 * time.Hour),
	}
	f.wt = &fakeWT{trees: map[string][]worktree.Worktree{}, dirty: map[string]bool{}, root: filepath.Join(dir, "wt")}
	f.sess = &fakeSessions{st: st, dirty: map[string]bool{}}
	f.ctl = New(Config{
		Store: StoreFrom(st), Worktrees: f.wt, Sessions: f.sess,
		Projects:     []config.Project{f.proj, {Name: "other", Repo: "daan/other"}},
		WorktreeRoot: f.wt.root, DataDir: filepath.Join(dir, "data"),
		Dirty: func(_ context.Context, d string) (bool, error) { return f.wt.dirty[d], nil },
		Now:   func() time.Time { return f.now },
	})
	return f
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) put(t *testing.T, key, kind, project string, n int) {
	t.Helper()
	must(t, f.st.Cache().Put(context.Background(), key, kind, project, make([]byte, n), 0))
}

func TestKVStatsAndProjects(t *testing.T) {
	f := newFixture(t)
	f.put(t, "tab:prs:forge:mine:1", "tab", "forge", 10)
	f.put(t, "tab:prs:all:mine:1", "tab", "", 5)
	f.put(t, "pr-detail:daan/forge#3", "tab", "", 7)
	f.put(t, "pr-detail:else/where#1", "tab", "", 1)
	f.put(t, "tab:home:forge", "tab", "forge", 100) // not GitHub data
	f.put(t, "ci_errors:daan/other#1:2", "ci_errors", "other", 3)
	f.put(t, "plane:ws:work_items:pl-1:x", "plane", "pl-1", 4)
	f.put(t, "plane:ws:me::x", "plane", "", 2)
	f.put(t, "transcript:/t.jsonl", "transcript", "forge", 9)
	f.put(t, "palette:recent", "palette", "", 50) // not a cache type

	r, err := f.ctl.Stats(context.Background())
	must(t, err)
	projects := func(ty Type) map[string]int64 {
		out := map[string]int64{}
		for _, p := range r.Type(ty).Projects {
			out[p.Project] = p.Bytes
		}
		return out
	}
	tests := []struct {
		ty      Type
		entries int
		bytes   int64
		proj    map[string]int64
	}{
		{GitHub, 5, 26, map[string]int64{"forge": 17, AllProjects: 5, "else/where": 1, "other": 3}},
		{Plane, 2, 6, map[string]int64{"forge": 4, Workspace: 2}},
		{Transcripts, 1, 9, map[string]int64{"forge": 9}},
	}
	for _, tt := range tests {
		s := r.Type(tt.ty)
		if s.Entries != tt.entries || s.Bytes != tt.bytes || !s.Oldest.Equal(t0) || s.Err != nil {
			t.Errorf("%s: %+v", tt.ty, s)
		}
		if got := projects(tt.ty); !mapsEqual(got, tt.proj) {
			t.Errorf("%s projects = %v, want %v", tt.ty, got, tt.proj)
		}
	}
	if r.DB.Bytes == 0 {
		t.Error("DB size missing")
	}
}

func mapsEqual(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestKVClearAndPrune(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.put(t, "plane:a", "plane", "pl-1", 4)
	f.put(t, "plane:b", "plane", "", 2)
	f.put(t, "transcript:/x", "transcript", "forge", 1)

	// Clear for one project leaves the rest.
	p, err := f.ctl.Plan(ctx, Op{Kind: Clear, Type: Plane, Project: "forge"})
	must(t, err)
	if len(p.Items) != 1 || p.Items[0].ID != "plane:a" || p.Bytes() != 4 {
		t.Fatalf("plan = %+v", p)
	}
	res, err := f.ctl.Apply(ctx, p)
	must(t, err)
	if res.Removed != 1 || res.Bytes != 4 {
		t.Errorf("result = %+v", res)
	}
	if _, err := f.st.Cache().Get(ctx, "plane:b"); err != nil {
		t.Error("workspace row cleared too")
	}

	// Prune keeps rows newer than the cut-off.
	p, err = f.ctl.Plan(ctx, Op{Kind: Prune, Type: Plane, OlderThan: t0})
	must(t, err)
	if !p.Empty() {
		t.Errorf("prune at t0 = %+v", p.Items)
	}
	p, err = f.ctl.Plan(ctx, Op{Kind: Prune, Type: Plane, OlderThan: t0.Add(time.Second)})
	must(t, err)
	if len(p.Items) != 1 {
		t.Errorf("prune after t0 = %+v", p.Items)
	}

	// Clearing transcripts also drops the in-memory index.
	p, err = f.ctl.Plan(ctx, Op{Kind: Clear, Type: Transcripts})
	must(t, err)
	if _, err := f.ctl.Apply(ctx, p); err != nil || f.sess.resets != 1 {
		t.Errorf("transcripts: %v, resets %d", err, f.sess.resets)
	}

	if _, err := f.ctl.Plan(ctx, Op{Kind: Clear, Type: Docs}); !errors.Is(err, ErrNotPlannable) {
		t.Errorf("docs plan err = %v", err)
	}
}

func TestApplyOnlyStillQualifying(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.put(t, "plane:a", "plane", "", 4)
	f.put(t, "plane:b", "plane", "", 4)
	p, err := f.ctl.Plan(ctx, Op{Kind: Clear, Type: Plane})
	must(t, err)
	must(t, f.st.Cache().Delete(ctx, "plane:a")) // gone while confirming
	f.put(t, "plane:c", "plane", "", 4)          // new while confirming
	res, err := f.ctl.Apply(ctx, p)
	must(t, err)
	if res.Removed != 1 || len(res.Skipped) != 1 || res.Skipped[0].Reason != reasonChanged {
		t.Errorf("result = %+v", res)
	}
	if _, err := f.st.Cache().Get(ctx, "plane:c"); err != nil {
		t.Error("unplanned row removed")
	}
}

func TestArtifacts(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	kept := filepath.Join(f.proj.Path, ".forge", "artifacts", "s1")
	must(t, os.MkdirAll(kept, 0o755))
	var found []store.Artifact
	for _, n := range []string{"detached.md", "linked.md", "pinned.md", "plain.md"} {
		p := filepath.Join(kept, n)
		must(t, os.WriteFile(p, []byte("12345"), 0o600))
		found = append(found, store.Artifact{Path: p, Project: "forge", Type: "md", Size: 5, ModifiedAt: t0})
	}
	// A file outside .forge/artifacts is never a kept copy.
	found = append(found, store.Artifact{Path: filepath.Join(f.proj.Path, "x.md"), Project: "forge", Size: 1, ModifiedAt: t0})
	a := f.st.Artifacts()
	must(t, a.Sync(ctx, []string{f.proj.Path}, found))
	for _, n := range []string{"detached.md", "linked.md", "pinned.md"} {
		must(t, a.Link(ctx, store.ArtifactLink{CopyPath: filepath.Join(kept, n), SourcePath: "/wt/" + n, SessionName: "s1"}))
	}
	must(t, a.Detach(ctx, filepath.Join(kept, "detached.md"), "removed", t0))
	must(t, a.Detach(ctx, filepath.Join(kept, "pinned.md"), "removed", t0))
	must(t, a.SetPinned(ctx, filepath.Join(kept, "pinned.md"), "forge", true))
	must(t, a.SetTags(ctx, filepath.Join(kept, "detached.md"), []string{"x"}))

	r, err := f.ctl.Stats(ctx)
	must(t, err)
	if s := r.Type(Artifacts); s.Entries != 4 || s.Bytes != 20 {
		t.Errorf("stats = %+v", s)
	}
	p, err := f.ctl.Plan(ctx, Op{Kind: Clear, Type: Artifacts})
	must(t, err)
	if len(p.Items) != 1 || filepath.Base(p.Items[0].ID) != "detached.md" {
		t.Fatalf("items = %+v", p.Items)
	}
	reasons := map[string]string{}
	for _, s := range p.Skipped {
		reasons[filepath.Base(s.Label)] = s.Reason
	}
	if len(reasons) != 3 || reasons["linked.md"] != reasonLinked || reasons["pinned.md"] != reasonPinned ||
		reasons["plain.md"] != reasonNoSource {
		t.Errorf("skipped = %v", reasons)
	}
	res, err := f.ctl.Apply(ctx, p)
	must(t, err)
	if res.Removed != 1 || res.Bytes != 5 {
		t.Errorf("result = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(kept, "detached.md")); !errors.Is(err, os.ErrNotExist) {
		t.Error("file kept")
	}
	if _, err := os.Stat(filepath.Join(kept, "pinned.md")); err != nil {
		t.Error("pinned file removed")
	}
	list, err := a.List(ctx, "")
	must(t, err)
	if len(list) != 4 {
		t.Errorf("rows left = %d, want 4", len(list))
	}
	tags, err := a.AllTags(ctx)
	must(t, err)
	if len(tags) != 0 {
		t.Errorf("tags left = %v", tags)
	}
}

func TestWorktrees(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	mk := func(session string, inRoot bool) worktree.Worktree {
		p := worktree.Path(f.wt.root, f.proj, session)
		if !inRoot {
			p = filepath.Join(f.dir, "elsewhere", session)
		}
		must(t, os.MkdirAll(p, 0o755))
		must(t, os.WriteFile(filepath.Join(p, "f"), []byte("123"), 0o600))
		return worktree.Worktree{Path: p, Branch: worktree.Branch(session), Session: session}
	}
	f.wt.trees["forge"] = []worktree.Worktree{mk("live", true), mk("orphan", true), mk("dirty", true), mk("outside", false)}
	f.wt.dirty[worktree.Path(f.wt.root, f.proj, "dirty")] = true
	_, err := f.st.Sessions().Create(ctx, store.Session{
		Name: "live", ClaudeSessionID: "c1", Project: "forge",
		Worktree: worktree.Path(f.wt.root, f.proj, "live"),
	})
	must(t, err)

	r, err := f.ctl.Stats(ctx)
	must(t, err)
	if s := r.Type(Worktrees); s.Entries != 4 || s.Orphans != 3 || s.Bytes != 12 {
		t.Errorf("stats = %+v", s)
	}
	p, err := f.ctl.Plan(ctx, Op{Kind: Orphans})
	must(t, err)
	if len(p.Items) != 1 || filepath.Base(p.Items[0].ID) != "orphan" {
		t.Fatalf("items = %+v", p.Items)
	}
	if got := SkipSummary(p.Skipped); got != "1 dirty, 1 outside the worktree root" {
		t.Errorf("skipped = %q", got)
	}
	res, err := f.ctl.Apply(ctx, p)
	must(t, err)
	if res.Removed != 1 || len(f.wt.removed) != 1 {
		t.Errorf("result = %+v, removed %v", res, f.wt.removed)
	}

	// A worktree that turned dirty after the plan is skipped by Apply.
	f.wt.trees["forge"] = append(f.wt.trees["forge"], mk("late", true))
	p, err = f.ctl.Plan(ctx, Op{Kind: Clear, Type: Worktrees, Project: "forge"})
	must(t, err)
	f.wt.dirty[worktree.Path(f.wt.root, f.proj, "late")] = true
	f.ctl.c.Dirty = nil // as if the plan-time check missed it
	res, err = f.ctl.Apply(ctx, p)
	must(t, err)
	if res.Removed != 0 || SkipSummary(res.Skipped) == "" {
		t.Errorf("late dirty: %+v", res)
	}
}

func TestArchived(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	create := func(name string, archived time.Time) store.Session {
		x, err := f.st.Sessions().Create(ctx, store.Session{
			Name: name, ClaudeSessionID: "id-" + name,
			Project: "forge", ArchivedAt: archived, Status: store.StatusArchived,
		})
		must(t, err)
		return x
	}
	create("live", time.Time{})
	create("old", t0)
	create("new", t0.Add(9*24*time.Hour))
	create("dirty", t0)
	f.sess.dirty["dirty"] = true
	dd := filepath.Join(f.dir, "data", "sessions", "old")
	must(t, os.MkdirAll(dd, 0o755))
	must(t, os.WriteFile(filepath.Join(dd, "prompt.md"), []byte("hello"), 0o600))

	r, err := f.ctl.Stats(ctx)
	must(t, err)
	if s := r.Type(Archived); s.Entries != 3 || s.Bytes != 5 || !s.Oldest.Equal(t0) {
		t.Errorf("stats = %+v", s)
	}
	p, err := f.ctl.Plan(ctx, Op{Kind: Prune, Type: Archived, OlderThan: f.now.Add(-5 * 24 * time.Hour)})
	must(t, err)
	if len(p.Items) != 2 {
		t.Fatalf("items = %+v", p.Items)
	}
	res, err := f.ctl.Apply(ctx, p)
	must(t, err)
	if res.Removed != 1 || !slices.Equal(f.sess.purged, []string{"old"}) || SkipSummary(res.Skipped) != "1 dirty" {
		t.Errorf("result = %+v, purged %v", res, f.sess.purged)
	}
}

func TestVacuum(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	for i := range 50 {
		f.put(t, "plane:"+string(rune('a'+i%26))+string(rune('a'+i/26)), "plane", "", 8<<10)
	}
	_, err := f.st.Cache().Clear(ctx, "plane", "")
	must(t, err)
	p, err := f.ctl.Plan(ctx, Op{Kind: Vacuum})
	must(t, err)
	if p.Empty() || p.DB.Free == 0 {
		t.Fatalf("plan = %+v", p)
	}
	res, err := f.ctl.Apply(ctx, p)
	must(t, err)
	if res.DBAfter >= res.DBBefore || res.Bytes == 0 {
		t.Errorf("result = %+v", res)
	}
}

func TestSkipSummary(t *testing.T) {
	tests := []struct {
		in   []Skip
		want string
	}{
		{nil, ""},
		{[]Skip{{Reason: "pinned"}, {Reason: "linked"}, {Reason: "linked"}}, "2 linked, 1 pinned"},
	}
	for _, tt := range tests {
		if got := SkipSummary(tt.in); got != tt.want {
			t.Errorf("SkipSummary(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
