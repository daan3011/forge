package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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

// lockedBuffer records what the server writes on its protocol stream.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type teeWriteCloser struct {
	io.WriteCloser
	tee io.Writer
}

func (t teeWriteCloser) Write(p []byte) (int, error) {
	_, _ = t.tee.Write(p)
	return t.WriteCloser.Write(p)
}

type recordingSender struct {
	mu    sync.Mutex
	pings []live.Ping
}

func (r *recordingSender) Send(_ context.Context, p live.Ping) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pings = append(r.pings, p)
	return true, nil
}

// TestMCPServesWithCleanStdout runs `forge mcp` end to end over pipes: a
// tool call works, the protocol stream holds only JSON-RPC, and nothing at
// all reaches the process's real stdout.
func TestMCPServesWithCleanStdout(t *testing.T) {
	home := testutil.IsolateHome(t)
	ctx := context.Background()
	db := filepath.Join(home, "forge.db")
	st, err := store.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(home, "wt", "fix")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Sessions().Create(ctx, store.Session{Name: "fix", ClaudeSessionID: "u1", Project: "forge", Worktree: wt}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	srvIn, cliOut := io.Pipe()
	cliIn, srvOut := io.Pipe()
	stream := &lockedBuffer{}
	sender := &recordingSender{}
	d := deps{
		db:           db,
		getenv:       func(k string) string { return map[string]string{"FORGE_SESSION": "fix"}[k] },
		sender:       sender,
		loadConfig:   func() (*config.Config, error) { return config.Default(), nil },
		newPlane:     func(config.Plane, plane.Cache) (plane.API, error) { return nil, errors.New("no key") },
		getwd:        func() string { return wt },
		homeDir:      func() string { return home },
		now:          func() time.Time { return time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC) },
		mcpTransport: func() mcp.Transport { return &mcp.IOTransport{Reader: srvIn, Writer: teeWriteCloser{srvOut, stream}} },
	}

	// Catch anything written to the real stdout while the server runs.
	realStdout := os.Stdout
	rp, wp, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wp
	captured := make(chan string)
	go func() {
		b, _ := io.ReadAll(rp)
		captured <- string(b)
	}()
	defer func() { os.Stdout = realStdout }()

	done := make(chan error, 1)
	go func() {
		_, _, err := run(t, d, "mcp")
		done <- err
	}()

	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(ctx, &mcp.IOTransport{Reader: cliIn, Writer: cliOut}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "create_issue", Arguments: map[string]any{"title": "Add retries to the Plane client"}})
	if err != nil || res.IsError {
		t.Fatalf("create_issue: %+v %v", res, err)
	}
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "write_doc", Arguments: map[string]any{"path": "notes.md", "content": "x"}})
	if err != nil || res.IsError {
		t.Fatalf("write_doc: %+v %v", res, err)
	}
	_ = cs.Close()
	_ = cliOut.Close()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "closed") {
			t.Logf("server returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop after the client hung up")
	}
	os.Stdout = realStdout
	_ = wp.Close()
	if out := <-captured; out != "" {
		t.Errorf("stray stdout output: %q", out)
	}

	// Every line on the protocol stream is a JSON-RPC message.
	sc := bufio.NewScanner(strings.NewReader(stream.String()))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	lines := 0
	for sc.Scan() {
		lines++
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil || m["jsonrpc"] != "2.0" {
			t.Errorf("not JSON-RPC: %q", sc.Text())
		}
	}
	if lines < 3 {
		t.Errorf("only %d protocol lines", lines)
	}

	// The proposal landed, attributed to the session, and the TUI was pinged.
	st, err = store.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ps, err := st.Proposals().List(ctx, store.ProposalFilter{Status: store.ProposalProposed})
	if err != nil || len(ps) != 1 || ps[0].SessionName != "fix" {
		t.Errorf("proposals %+v %v", ps, err)
	}
	if len(sender.pings) == 0 || sender.pings[0].Session != "fix" {
		t.Errorf("pings %+v", sender.pings)
	}
	logb, err := os.ReadFile(mcpLogPath())
	if err != nil || !strings.Contains(string(logb), "forge mcp serving") {
		t.Errorf("mcp.log: %s %v", logb, err)
	}
	if !strings.HasPrefix(mcpLogPath(), filepath.Join(home, ".local", "state")) {
		t.Errorf("log path %s", mcpLogPath())
	}
}

func TestTemplatesSyncCmd(t *testing.T) {
	testutil.IsolateHome(t)
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "forge", "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mine.md"), []byte("Do the thing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, deps{}, "templates", "sync", "--dry-run")
	if err != nil || !strings.Contains(out, "+forge tools (mcp__forge__*)") || !strings.Contains(out, "would update 1") {
		t.Fatalf("dry run: %q %v", out, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "mine.md")); string(b) != "Do the thing.\n" {
		t.Fatal("dry run wrote")
	}
	out, _, err = run(t, deps{}, "templates", "sync")
	if err != nil || !strings.Contains(out, "updated 1") {
		t.Fatalf("sync: %q %v", out, err)
	}
	out, _, err = run(t, deps{}, "templates", "sync")
	if err != nil || strings.TrimSpace(out) != "all templates up to date" {
		t.Fatalf("second sync: %q %v", out, err)
	}
}
