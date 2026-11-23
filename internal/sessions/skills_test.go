package sessions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/daan3011/forge/internal/shell"
	"github.com/daan3011/forge/internal/slashcmd"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/testutil"
)

// fakeSkills records syncs and writes one skill, like the real catalog.
type fakeSkills struct {
	syncs []fakeSync
	err   error
}

type fakeSync struct {
	root string
	vals map[string]string
}

func (f *fakeSkills) Sync(root string, vals map[string]string) (slashcmd.Result, error) {
	f.syncs = append(f.syncs, fakeSync{root, vals})
	p := filepath.Join(root, slashcmd.SkillsRel, "fix-ci", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return slashcmd.Result{}, err
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		return slashcmd.Result{}, err
	}
	return slashcmd.Result{Written: []string{"fix-ci"}}, f.err
}

func launchArgv(t *testing.T, e *env, name string) string {
	t.Helper()
	_, argv := mcpConfigOf(t, e, name)
	return argv[2]
}

func TestStartSyncsSkills(t *testing.T) {
	e := newEnv(t)
	sk := &fakeSkills{}
	e.svc.c.Skills = sk
	x := e.start(t, "skilled")
	if len(sk.syncs) != 1 {
		t.Fatalf("syncs = %d", len(sk.syncs))
	}
	root := e.svc.SkillsRoot("skilled")
	if sk.syncs[0].root != root || filepath.Base(root) != "forge" {
		t.Errorf("root = %q", sk.syncs[0].root)
	}
	if v := sk.syncs[0].vals; v["session"] != "skilled" || v["branch"] != x.Branch || v["branch"] == "" {
		t.Errorf("vals = %v", v)
	}
	if argv := launchArgv(t, e, "skilled"); !strings.Contains(argv, "--add-dir "+shell.Quote(root)) {
		t.Errorf("argv %q lacks --add-dir", argv)
	}
	// Nothing goes into the worktree and nothing is excluded for skills.
	if _, err := os.Stat(filepath.Join(x.Worktree, ".claude", "skills")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("skills written into the worktree: %v", err)
	}
	if slices.ContainsFunc(e.wt.excluded, func(p string) bool { return strings.Contains(p, "skills") }) {
		t.Errorf("excluded %v", e.wt.excluded)
	}
}

func TestStartSkillsErrorIsAWarning(t *testing.T) {
	e := newEnv(t)
	e.svc.c.Skills = &fakeSkills{err: errors.New("bad override")}
	res, err := e.svc.Start(context.Background(), StartOpts{Name: "warned", Project: "forge"})
	if err != nil {
		t.Fatal(err)
	}
	if res.SkillsErr == nil || !strings.Contains(res.SkillsErr.Error(), "bad override") {
		t.Errorf("SkillsErr = %v", res.SkillsErr)
	}
	// What could be written is still passed.
	if argv := launchArgv(t, e, "warned"); !strings.Contains(argv, "--add-dir") {
		t.Errorf("argv %q", argv)
	}
}

func TestStartWithoutSkills(t *testing.T) {
	e := newEnv(t)
	e.start(t, "plain")
	if argv := launchArgv(t, e, "plain"); strings.Contains(argv, "--add-dir") {
		t.Errorf("argv %q", argv)
	}
}

func TestResumeSyncsSkills(t *testing.T) {
	e := newEnv(t)
	x := e.dead(t, "old")
	// A session from before P6.7 with its session dir gone.
	if err := os.RemoveAll(e.svc.SessionDir("old")); err != nil {
		t.Fatal(err)
	}
	sk := &fakeSkills{}
	e.svc.c.Skills = sk
	if _, err := e.svc.Resume(context.Background(), x, ResumeOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(sk.syncs) != 1 || sk.syncs[0].vals["session"] != "old" {
		t.Fatalf("syncs = %+v", sk.syncs)
	}
	if argv := launchArgv(t, e, "old"); !strings.Contains(argv, "--add-dir "+shell.Quote(e.svc.SkillsRoot("old"))) {
		t.Errorf("argv %q lacks --add-dir", argv)
	}
}

func TestSyncSkills(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	n := 0
	e.svc.c.NewID = func() (string, error) { n++; return fmt.Sprintf("00000000-0000-4000-8000-%012d", n), nil }
	e.start(t, "live")
	e.dead(t, "stopped")
	gone := e.start(t, "archived")
	if _, err := e.svc.Kill(ctx, gone, KillOpts{RemoveWorktree: true}); err != nil {
		t.Fatal(err)
	}
	if x, _ := e.st.Sessions().GetByName(ctx, "archived"); x.Status != store.StatusArchived {
		t.Fatalf("status = %s", x.Status)
	}
	sk := &fakeSkills{}
	e.svc.c.Skills = sk
	res, err := e.svc.SyncSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range res {
		names = append(names, r.Session)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"live", "stopped"}) {
		t.Errorf("synced %v", names)
	}
}

// TestSyncSkillsRealCatalog runs the real catalog end to end.
func TestSyncSkillsRealCatalog(t *testing.T) {
	testutil.IsolateHome(t)
	e := newEnv(t)
	e.svc.c.Skills = slashcmd.Catalog{OverridesDir: t.TempDir()}
	e.start(t, "real")
	b, err := os.ReadFile(filepath.Join(e.svc.SkillsRoot("real"), slashcmd.SkillsRel, "fix-ci", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "this forge session (real, branch agent/real)") {
		t.Errorf("fix-ci not rendered for the session:\n%s", b)
	}
}
