package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/store"
)

// fakeCheckout makes dir look like a clone of repo (github.com).
func fakeCheckout(t *testing.T, dir, repo string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[remote \"origin\"]\n\turl = git@github.com:" + repo + ".git\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveProjects(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	code := filepath.Join(home, "code")
	fakeCheckout(t, filepath.Join(code, "app"), "acme/app")
	fakeCheckout(t, filepath.Join(code, "lib"), "acme/lib")
	other := filepath.Join(home, "elsewhere", "tool") // not under a root
	fakeCheckout(t, other, "acme/tool")

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Projects().SetPlane(ctx, "acme/lib", "plane-lib"); err != nil {
		t.Fatal(err)
	}

	// Started inside tool: remembered, and its parent becomes a root.
	d := deps{getwd: func() string { return filepath.Join(other, "sub") }, homeDir: func() string { return home }}
	if err := os.MkdirAll(filepath.Join(other, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Projects: []config.Project{{Repo: "acme/app", Name: "web", WorktreeCopy: []string{".env"}}}}
	here := d.resolveProjects(ctx, cfg, st, io.Discard)
	if here != "tool" {
		t.Errorf("here = %q", here)
	}
	byName := map[string]config.Project{}
	for _, p := range cfg.Projects {
		byName[p.Name] = p
	}
	if len(cfg.Projects) != 3 || cfg.Projects[0].Name != "tool" { // tool was just used
		t.Fatalf("projects = %+v", cfg.Projects)
	}
	if p := byName["web"]; p.Repo != "acme/app" || p.Path != filepath.Join(code, "app") || len(p.WorktreeCopy) != 1 {
		t.Errorf("web = %+v", p)
	}
	if p := byName["lib"]; p.PlaneProjectID != "plane-lib" || !p.PlaneDecided {
		t.Errorf("lib = %+v", p)
	}

	// Next start from elsewhere: tool is no longer scanned but remembered.
	d.getwd = func() string { return home }
	cfg = &config.Config{}
	if here := d.resolveProjects(ctx, cfg, st, io.Discard); here != "" {
		t.Errorf("here = %q", here)
	}
	found := false
	for _, p := range cfg.Projects {
		if p.Name == "tool" && p.Path == other {
			found = true
		}
	}
	if !found {
		t.Errorf("tool not remembered: %+v", cfg.Projects)
	}
}

func TestNewUsesTheRepoYouAreIn(t *testing.T) {
	d, fake := newEnvCLI(t, "")
	home := t.TempDir()
	repo := filepath.Join(home, "code", "forge")
	fakeCheckout(t, repo, "daan/forge")
	fakeCheckout(t, filepath.Join(home, "code", "other"), "daan/other")
	d.getwd = func() string { return repo }
	d.homeDir = func() string { return home }
	if _, _, err := run(t, d, "new", "--name", "x", "--prompt", "go"); err != nil {
		t.Fatal(err)
	}
	if len(fake.got) != 1 || fake.got[0].Project != "forge" {
		t.Errorf("Start = %+v", fake.got)
	}
}
