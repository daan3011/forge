package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daan3011/forge/internal/doctor"
	"github.com/daan3011/forge/internal/sessions/claude"
	"github.com/daan3011/forge/internal/sessions/hooks"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/slashcmd"
	"github.com/daan3011/forge/internal/store"
)

const testForgeBin = "/usr/local/bin/forge"

// doctorDeps is a deps whose doctor sees a healthy machine (the config,
// database and sessions are real, in temp dirs), with one active session
// in a worktree forge never registered anything in.
func doctorDeps(t *testing.T) (deps, *[]string) {
	t.Helper()
	cfgDir, dataDir := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("XDG_DATA_HOME", dataDir)
	db := filepath.Join(dataDir, "forge.db")
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Sessions().Create(context.Background(), store.Session{
		Name: "alpha", ClaudeSessionID: "id-1", Project: "p", Worktree: wt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	var prepared []string
	d := deps{
		db: db,
		doctorFake: func(e *doctor.Env) {
			e.LookPath = func(bin string) (string, error) { return "/usr/bin/" + bin, nil }
			e.Output = func(context.Context, string, ...string) (string, error) { return "v1", nil }
			e.Lstat = os.Lstat
			e.Bin, e.PathDirs = testForgeBin, []string{"/usr/local/bin"}
			e.GitHubViewer = func(context.Context) (string, error) { return "octo", nil }
			e.TmuxVersion = func(context.Context) (tmux.Version, error) { return tmux.Version{Major: 3, Minor: 5}, nil }
			e.PlaneConfigured = false
		},
		doctorPrepare: func(_ context.Context, x store.Session) error {
			prepared = append(prepared, x.Name)
			dir := filepath.Join(dataDir, "forge", "sessions", x.Name)
			if err := hooks.Install(x.Worktree, testForgeBin); err != nil {
				return err
			}
			if err := claude.WriteMCPConfig(filepath.Join(dir, claude.MCPConfigFile), testForgeBin, x.Name); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dir, "forge", slashcmd.ManifestFile), []byte("{}"), 0o600)
		},
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "forge", "sessions", "alpha", "forge"), 0o700); err != nil {
		t.Fatal(err)
	}
	return d, &prepared
}

func TestDoctorReportsAndFixes(t *testing.T) {
	d, prepared := doctorDeps(t)

	out, _, err := run(t, d, "doctor")
	if !errors.Is(err, errDoctorFailed) {
		t.Fatalf("err = %v, want errDoctorFailed\n%s", err, out)
	}
	for _, want := range []string{
		"✗ hooks", "alpha: not registered", "fix: forge doctor --fix",
		"✗ mcp config", "alpha: missing", "✗ skills", "alpha: not synced",
		"! config", "no config yet", "! tmux config", "not written yet",
		"✓ gh", "logged in as octo", "– plane",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(*prepared) != 0 {
		t.Fatalf("doctor without --fix prepared %v", *prepared)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "forge", "config.yaml")); err == nil {
		t.Fatal("doctor without --fix wrote the config")
	}

	out, stderr, err := run(t, d, "doctor", "--fix")
	if err != nil {
		t.Fatalf("--fix: %v\n%s\n%s", err, out, stderr)
	}
	if strings.Join(*prepared, ",") != "alpha" {
		t.Errorf("prepared = %v, want [alpha]", *prepared)
	}
	for _, want := range []string{"wrote the default config", "wrote forge's tmux config", "re-registered hooks, MCP config and skills for alpha"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if !strings.Contains(out, "the forge is hot") {
		t.Errorf("after --fix, want all green:\n%s", out)
	}
}

func TestDoctorJSON(t *testing.T) {
	d, _ := doctorDeps(t)
	out, _, err := run(t, d, "doctor", "--json")
	if !errors.Is(err, errDoctorFailed) {
		t.Fatalf("err = %v", err)
	}
	var cs []doctor.Check
	if err := json.Unmarshal([]byte(out), &cs); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	byName := map[string]doctor.Check{}
	for _, c := range cs {
		byName[c.Name] = c
	}
	if c := byName["hooks"]; c.Status != doctor.Fail || strings.Join(c.Sessions, ",") != "alpha" {
		t.Errorf("hooks = %+v", c)
	}
	if !strings.Contains(out, `"status": "fail"`) {
		t.Errorf("status should be a string:\n%s", out)
	}
}
