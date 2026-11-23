package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daan3011/forge/internal/store"
)

func TestNoSessionRefusesWrites(t *testing.T) {
	e := newEnv(t, withSession("nope"), withCwd(t.TempDir()))
	if e.srv.Session() != nil {
		t.Fatal("found a session")
	}
	writes := map[string]map[string]any{
		"create_issue":        {"title": "x"},
		"update_issue":        {"id": "P-1", "title": "x"},
		"comment_issue":       {"id": "FORGE-7", "body": "x"},
		"write_doc":           {"path": "a.md", "content": "x"},
		"log_decision":        {"title": "t", "decision": "d"},
		"pin_artifact":        {"path": "a.txt"},
		"set_session_summary": {"summary": "x"},
		"report_progress":     {"message": "x"},
	}
	for tool, args := range writes {
		if msg := e.callErr(tool, args); !strings.Contains(msg, "not inside a forge session") {
			t.Errorf("%s: %q", tool, msg)
		}
	}
	// Reads still answer.
	var c ContextPackOut
	e.call("get_context_pack", map[string]any{}, &c)
	if c.Session != "" || c.Note == "" {
		t.Errorf("context %+v", c)
	}
	n, _ := e.st.Proposals().List(context.Background(), store.ProposalFilter{})
	if len(n) != 0 || e.pingCount() != 0 {
		t.Error("wrote without a session")
	}
}

func TestSessionFoundByWorktree(t *testing.T) {
	// No FORGE_SESSION: the working directory (inside the worktree) finds it.
	base := newEnv(t)
	sub := filepath.Join(base.wt, "internal", "plane")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	srv, err := New(context.Background(), Deps{Store: base.st, Config: base.cfg, Cwd: sub})
	if err != nil {
		t.Fatal(err)
	}
	if srv.Session() == nil || srv.Session().Name != "fix" {
		t.Fatalf("session %+v", srv.Session())
	}
	// FORGE_SESSION wins over the directory.
	other, err := base.st.Sessions().Create(context.Background(), store.Session{Name: "other", ClaudeSessionID: "u2", Project: "forge"})
	if err != nil {
		t.Fatal(err)
	}
	srv, err = New(context.Background(), Deps{Store: base.st, Config: base.cfg, Cwd: sub, SessionName: other.Name})
	if err != nil || srv.Session().Name != "other" {
		t.Fatalf("by name: %+v %v", srv.Session(), err)
	}
}

func TestSummaryAndProgress(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.call("report_progress", map[string]any{"message": "tests\nwritten,   2 of 5 passing"}, nil)
	e.call("set_session_summary", map[string]any{"summary": "Added retries to the Plane client."}, nil)
	x, err := e.st.Sessions().Get(ctx, e.x.ID)
	if err != nil {
		t.Fatal(err)
	}
	if x.Progress != "tests written, 2 of 5 passing" || !x.ProgressAt.Equal(t0) || x.AgentSummary != "Added retries to the Plane client." {
		t.Errorf("session %+v", x)
	}
	if e.pingCount() != 2 {
		t.Errorf("pings = %d", e.pingCount())
	}
	if msg := e.callErr("report_progress", map[string]any{"message": strings.Repeat("x", 600)}); !strings.Contains(msg, "one line") {
		t.Errorf("long: %q", msg)
	}
	if msg := e.callErr("set_session_summary", map[string]any{"summary": " "}); !strings.Contains(msg, "required") {
		t.Errorf("empty: %q", msg)
	}
}

func TestContextPack(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	if err := e.st.Links().Add(ctx, store.SessionRef(e.x.ID), store.PRRef("daan3011/forge", 3), store.LinkMeta{Title: "Fix", State: "open"}); err != nil {
		t.Fatal(err)
	}
	e.call("create_issue", map[string]any{"title": "x"}, nil)
	packs := filepath.Join(filepath.Dir(filepath.Dir(e.wt)), "packs")
	if err := os.MkdirAll(packs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packs, "plane.yaml"), []byte("description: plane client\nfiles: [internal/plane/client.go, nope.go]\npreamble: Mind the rate limit.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.wt, "internal", "plane"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.wt, "internal", "plane", "client.go"), []byte("package plane"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out ContextPackOut
	e.call("get_context_pack", map[string]any{}, &out)
	if out.Session != "fix" || out.Branch != "forge/fix" || out.Worktree != e.wt || len(out.Packs) != 1 || out.Packs[0] != "plane" {
		t.Errorf("context %+v", out)
	}
	kinds := map[string]string{}
	for _, l := range out.Links {
		kinds[l.Kind] = l.ID
	}
	if kinds["github_pr"] != "daan3011/forge#3" || kinds["proposed_issue"] != "P-1" {
		t.Errorf("links %+v", out.Links)
	}

	e.call("get_context_pack", map[string]any{"name": "plane"}, &out)
	if out.Pack == nil || out.Pack.Preamble != "Mind the rate limit." || len(out.Pack.Files) != 1 ||
		out.Pack.Files[0] != filepath.Join(e.wt, "internal", "plane", "client.go") || len(out.Pack.Missing) != 1 {
		t.Errorf("pack %+v", out.Pack)
	}
	if msg := e.callErr("get_context_pack", map[string]any{"name": "nope"}); !strings.Contains(msg, "plane") {
		t.Errorf("unknown pack: %q", msg)
	}
}

func TestPinAndListArtifacts(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	if err := os.MkdirAll(filepath.Join(e.wt, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.wt, "out", "bench.html"), []byte("<p>fast</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out ArtifactOut
	e.call("pin_artifact", map[string]any{"path": "out/bench.html", "title": "Benchmark", "tags": []string{"perf"}}, &out)
	if !strings.HasPrefix(out.Path, filepath.Join(e.checkout, ".forge", "artifacts")) || !out.Pinned {
		t.Fatalf("out %+v", out)
	}
	as, err := e.st.Artifacts().List(ctx, "forge")
	if err != nil || len(as) != 1 {
		t.Fatalf("artifacts %+v %v", as, err)
	}
	a := as[0]
	if !a.Pinned || a.Title != "Benchmark" || len(a.Tags) != 1 || a.Link == nil || a.Link.SessionName != "fix" {
		t.Errorf("artifact %+v", a)
	}
	if ls := e.sessionLinks(store.KindArtifact); len(ls) != 1 || ls[0].Other.ID != out.Path {
		t.Errorf("links %+v", ls)
	}
	var l ListArtifactsOut
	e.call("list_artifacts", map[string]any{"this_session_only": true, "pinned_only": true}, &l)
	if len(l.Artifacts) != 1 || l.Artifacts[0].Session != "fix" {
		t.Errorf("list %+v", l)
	}

	outside := filepath.Join(filepath.Dir(e.wt), "other.txt")
	_ = os.WriteFile(outside, []byte("x"), 0o644)
	for _, p := range []string{outside, "../other.txt", "missing.png"} {
		if msg := e.callErr("pin_artifact", map[string]any{"path": p}); msg == "" {
			t.Errorf("%s: want an error", p)
		}
	}
	if err := os.Symlink(outside, filepath.Join(e.wt, "sneaky.txt")); err != nil {
		t.Fatal(err)
	}
	if msg := e.callErr("pin_artifact", map[string]any{"path": "sneaky.txt"}); !strings.Contains(msg, "outside") {
		t.Errorf("symlink out: %q", msg)
	}
}
