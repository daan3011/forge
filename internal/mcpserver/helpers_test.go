package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/sessions/live"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/testutil"
)

var t0 = time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

type env struct {
	t        *testing.T
	st       *store.Store
	plane    *testutil.FakePlane
	cfg      *config.Config
	x        store.Session
	wt       string // session worktree
	checkout string // project main checkout
	srv      *Server
	cs       *mcp.ClientSession

	mu    sync.Mutex
	pings []live.Ping
}

type opt func(*Deps)

func withSession(name string) opt { return func(d *Deps) { d.SessionName = name } }
func withCwd(dir string) opt      { return func(d *Deps) { d.Cwd = dir } }
func withAutoCreate() opt         { return func(d *Deps) { d.Config.MCP.AutoCreateIssues = true } }
func withoutPlane() opt {
	return func(d *Deps) { d.Plane, d.PlaneErr = nil, os.ErrNotExist }
}

// newEnv makes a store with session "fix" (worktree wt, project forge
// linked to Plane project pp-1) and connects a client to a server for it.
func newEnv(t *testing.T, opts ...opt) *env {
	t.Helper()
	home := testutil.IsolateHome(t)
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(home, "forge.db"), store.WithClock(func() time.Time { return t0 }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e := &env{t: t, st: st, wt: filepath.Join(home, "wt", "fix"), checkout: filepath.Join(home, "src", "forge")}
	for _, d := range []string{e.wt, e.checkout} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.x, err = st.Sessions().Create(ctx, store.Session{
		Name: "fix", ClaudeSessionID: "uuid-fix", Project: "forge", Repo: "daan3011/forge",
		Branch: "forge/fix", BaseBranch: "main", Worktree: e.wt,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.plane = &testutil.FakePlane{
		MeUser:      plane.User{ID: "me"},
		ProjectList: []plane.Project{{ID: "pp-1", Name: "forge", Identifier: "FORGE"}},
		StateMap: map[string][]plane.State{"pp-1": {
			{ID: "s-todo", Name: "Todo", Group: plane.GroupUnstarted},
			{ID: "s-doing", Name: "In Progress", Group: plane.GroupStarted},
		}},
		LabelMap: map[string][]plane.Label{"pp-1": {{ID: "l-bug", Name: "bug"}}},
		Items: []plane.WorkItem{
			{
				ID: "wi-a", Name: "Existing bug", Identifier: "FORGE-7", SequenceID: 7, ProjectID: "pp-1", StateID: "s-todo",
				AssigneeIDs: []string{"me"}, DescriptionHTML: "<p>It <strong>breaks</strong></p>",
			},
			{ID: "wi-b", Name: "Started thing", Identifier: "FORGE-8", SequenceID: 8, ProjectID: "pp-1", StateID: "s-doing"},
		},
		CommentMap: map[string][]plane.Comment{"wi-a": {{ID: "c1", CommentHTML: "<p>seen it</p>"}}},
	}
	e.cfg = config.Default()
	e.cfg.Projects = []config.Project{{Name: "forge", Repo: "daan3011/forge", Path: e.checkout, PlaneProjectID: "pp-1"}}
	d := Deps{
		Store: st, Plane: e.plane, Config: e.cfg, SessionName: "fix", Now: func() time.Time { return t0 },
		PacksDir: filepath.Join(home, "packs"),
		Ping: func(_ context.Context, p live.Ping) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.pings = append(e.pings, p)
		},
	}
	for _, o := range opts {
		o(&d)
	}
	e.srv, err = New(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	e.cs = connect(t, e.srv)
	return e
}

// connect runs srv over an in-memory transport and returns a client.
func connect(t *testing.T, srv *Server) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.MCP().Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// call calls tool and decodes its structured result into out (may be nil).
// It fails the test on a tool error.
func (e *env) call(tool string, args map[string]any, out any) {
	e.t.Helper()
	if msg, ok := e.try(tool, args, out); !ok {
		e.t.Fatalf("%s: tool error: %s", tool, msg)
	}
}

// callErr calls tool, expects a tool error and returns its text.
func (e *env) callErr(tool string, args map[string]any) string {
	e.t.Helper()
	msg, ok := e.try(tool, args, nil)
	if ok {
		e.t.Fatalf("%s: want a tool error", tool)
	}
	return msg
}

func (e *env) try(tool string, args map[string]any, out any) (string, bool) {
	e.t.Helper()
	res, err := e.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		e.t.Fatalf("%s: protocol error: %v", tool, err)
	}
	var text []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text = append(text, tc.Text)
		}
	}
	if res.IsError {
		return strings.Join(text, "\n"), false
	}
	if out != nil {
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			e.t.Fatal(err)
		}
		if err := json.Unmarshal(b, out); err != nil {
			e.t.Fatalf("%s: decode %s: %v", tool, b, err)
		}
	}
	return strings.Join(text, "\n"), true
}

func (e *env) pingCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.pings)
}

// sessionLinks returns the session's links by kind.
func (e *env) sessionLinks(kind store.LinkKind) []store.Link {
	e.t.Helper()
	ls, err := e.st.Links().For(context.Background(), store.SessionRef(e.x.ID))
	if err != nil {
		e.t.Fatal(err)
	}
	var out []store.Link
	for _, l := range ls {
		if l.Other.Kind == kind {
			out = append(out, l)
		}
	}
	return out
}
