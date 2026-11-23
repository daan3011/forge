package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daan3011/forge/internal/store"
)

func TestWriteReadListDocs(t *testing.T) {
	e := newEnv(t)
	var w DocOut
	e.call("write_doc", map[string]any{"path": "notes/retries", "content": "# Retries\n"}, &w)
	if w.Path != "notes/retries.md" {
		t.Errorf("write %+v", w)
	}
	abs := filepath.Join(e.wt, ".forge", "docs", "notes", "retries.md")
	if b, _ := os.ReadFile(abs); string(b) != "# Retries\n" {
		t.Errorf("file = %q", b)
	}
	if msg := e.callErr("write_doc", map[string]any{"path": "notes/retries.md", "content": "x"}); !strings.Contains(msg, "already exists") {
		t.Errorf("create twice: %q", msg)
	}
	e.call("write_doc", map[string]any{"path": "notes/retries.md", "content": "more", "mode": "append"}, nil)
	e.call("write_doc", map[string]any{"path": "fresh.md", "content": "a", "mode": "append"}, nil)
	var r ReadDocOut
	e.call("read_doc", map[string]any{"path": "notes/retries.md"}, &r)
	if r.Content != "# Retries\nmore" {
		t.Errorf("after append %q", r.Content)
	}
	e.call("write_doc", map[string]any{"path": "notes/retries.md", "content": "new", "mode": "overwrite"}, nil)
	e.call("read_doc", map[string]any{"path": "notes/retries.md"}, &r)
	if r.Content != "new" {
		t.Errorf("after overwrite %q", r.Content)
	}
	if msg := e.callErr("write_doc", map[string]any{"path": "x.md", "content": "a", "mode": "replace"}); !strings.Contains(msg, "mode") {
		t.Errorf("bad mode: %q", msg)
	}

	var l ListDocsOut
	e.call("list_docs", map[string]any{}, &l)
	var paths []string
	for _, d := range l.Docs {
		paths = append(paths, d.Path)
	}
	if strings.Join(paths, ",") != "notes/retries.md,fresh.md" && strings.Join(paths, ",") != "fresh.md,notes/retries.md" {
		t.Errorf("list %v", paths)
	}
	ls := e.sessionLinks(store.KindDoc)
	if len(ls) != 2 {
		t.Errorf("doc links %+v", ls)
	}
	// The main checkout is untouched: writes stay in the worktree.
	if _, err := os.Stat(filepath.Join(e.checkout, ".forge")); err == nil {
		t.Error("wrote into the main checkout")
	}
}

func TestDocPathRestriction(t *testing.T) {
	e := newEnv(t)
	outside := filepath.Join(filepath.Dir(e.wt), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	docs := filepath.Join(e.wt, ".forge", "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(docs, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(docs, "link.md")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, path, want string
	}{
		{"parent", "../evil.md", "inside the docs folder"},
		{"deep parent", "notes/../../../evil.md", "inside the docs folder"},
		{"absolute", "/etc/passwd", "relative"},
		{"absolute in worktree", filepath.Join(e.wt, "x.md"), "relative"},
		{"hidden", ".git/config", "hidden"},
		{"hidden trash", ".trash/x.md", "hidden"},
		{"symlinked dir", "escape/x.md", "symlink"},
		{"symlinked file", "link.md", "symlink"},
		{"empty", "", "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for tool, args := range map[string]map[string]any{
				"write_doc": {"path": tt.path, "content": "x"},
				"read_doc":  {"path": tt.path},
			} {
				msg := e.callErr(tool, args)
				if !strings.Contains(msg, tt.want) {
					t.Errorf("%s: %q lacks %q", tool, msg, tt.want)
				}
			}
		})
	}
	if _, err := os.Stat(filepath.Join(outside, "x.md")); err == nil {
		t.Error("wrote through the symlink")
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "secret.md")); string(b) != "secret" {
		t.Error("overwrote the symlink target")
	}
}

// .forge itself being a symlink out of the worktree is refused too.
func TestDocsRootSymlinked(t *testing.T) {
	e := newEnv(t)
	outside := filepath.Join(filepath.Dir(e.wt), "elsewhere")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(e.wt, ".forge")); err != nil {
		t.Fatal(err)
	}
	for tool, args := range map[string]map[string]any{
		"write_doc":    {"path": "a.md", "content": "x"},
		"list_docs":    {},
		"log_decision": {"title": "t", "decision": "d"},
	} {
		msg := e.callErr(tool, args)
		if !strings.Contains(msg, "symlink") {
			t.Errorf("%s: %q", tool, msg)
		}
	}
}

func TestLogDecision(t *testing.T) {
	e := newEnv(t)
	var out LogDecisionOut
	e.call("log_decision", map[string]any{
		"title": "Retry Plane requests", "decision": "Use exponential backoff.", "context": "5xx happen.",
	}, &out)
	if out.Number != 1 || !strings.HasPrefix(out.Path, "decisions/0001-") {
		t.Fatalf("out %+v", out)
	}
	b, err := os.ReadFile(filepath.Join(e.wt, ".forge", "docs", filepath.FromSlash(out.Path)))
	if err != nil || !strings.Contains(string(b), "exponential backoff") {
		t.Errorf("decision file %s %v", b, err)
	}
	// Nothing outside .forge/docs: no CLAUDE.md edit.
	if _, err := os.Stat(filepath.Join(e.wt, "CLAUDE.md")); err == nil {
		t.Error("log_decision wrote CLAUDE.md")
	}
	if msg := e.callErr("log_decision", map[string]any{"title": "x", "decision": ""}); msg == "" {
		t.Error("want a validation error")
	}
	if ls := e.sessionLinks(store.KindDoc); len(ls) != 1 {
		t.Errorf("links %+v", ls)
	}
}
