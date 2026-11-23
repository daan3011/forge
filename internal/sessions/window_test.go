package sessions

import (
	"context"
	"slices"
	"testing"

	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/store"
)

func TestOpenWindow(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	o := WindowOpts{Project: "forge", Name: "cmd-dev fix", Dir: "/wt/fix", Script: "npm run dev", Env: map[string]string{"FORGE_SESSION": "fix"}, Attach: true}

	// New: started with sh -c in the dir, then shown.
	res, err := e.svc.OpenWindow(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	want := tmux.Target{Session: "forge", Window: "cmd-dev-fix"}
	if res.Target != want || res.Reused || res.Attachment == nil || res.Attachment.Mode != tmux.AttachExec {
		t.Fatalf("first = %+v", res)
	}
	dir, cmd, env, ok := e.tmux.Started(want)
	if !ok || dir != "/wt/fix" || !slices.Equal(cmd, []string{"sh", "-c", "npm run dev"}) || env["FORGE_SESSION"] != "fix" {
		t.Fatalf("started %v %q %v %v", ok, dir, cmd, env)
	}

	// Still running: shown again, not started twice.
	res, err = e.svc.OpenWindow(ctx, WindowOpts{Project: "forge", Name: o.Name, Script: "other"})
	if err != nil || !res.Reused || res.Attachment == nil {
		t.Fatalf("reuse = %+v, %v", res, err)
	}
	if _, cmd, _, _ := e.tmux.Started(want); cmd[2] != "npm run dev" {
		t.Fatalf("restarted: %v", cmd)
	}

	// Exited: replaced by a fresh window.
	e.tmux.Exit(want, 1)
	o.Script, o.Attach = "npm run dev -- --port 4000", false
	res, err = e.svc.OpenWindow(ctx, o)
	if err != nil || res.Reused || res.Attachment != nil {
		t.Fatalf("replace = %+v, %v", res, err)
	}
	if _, cmd, _, _ := e.tmux.Started(want); cmd[2] != o.Script {
		t.Fatalf("not replaced: %v", cmd)
	}
	if got := len(e.tmux.Attached()); got != 2 {
		t.Fatalf("attached %d times, want 2", got)
	}
}

func TestOpenWindowDefaultSession(t *testing.T) {
	e := newEnv(t)
	res, err := e.svc.OpenWindow(context.Background(), WindowOpts{Name: "cmd-x", Script: "true"})
	if err != nil || res.Target.Session != DefaultCommandSession {
		t.Fatalf("= %+v, %v", res, err)
	}
	if _, err := e.svc.OpenWindow(context.Background(), WindowOpts{Name: "!!!", Script: "true"}); err == nil {
		t.Fatal("want an error for a name that sanitises to nothing")
	}
}

func TestRowVars(t *testing.T) {
	r := Row{
		Session: store.Session{Name: "fix", Repo: "o/r", Branch: "fix-login", Worktree: "/wt/fix"},
		LinkList: []store.Link{
			{Other: store.PRRef("o/r", 12)},
			{Other: store.IssueRef("PROJ-7")},
		},
	}
	v := r.Vars()
	if v.Repo != "o/r" || v.Branch != "fix-login" || v.Worktree != "/wt/fix" || v.Path != "/wt/fix" || v.Session != "fix" || v.PR != "12" || v.Issue != "PROJ-7" {
		t.Fatalf("Vars = %+v", v)
	}
}
