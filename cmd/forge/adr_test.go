package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/huh/v2"
)

// newADRCLI configures two projects with checkouts in temp dirs and runs
// forge outside any repo.
func newADRCLI(t *testing.T) (deps, string, string) {
	t.Helper()
	a, b := t.TempDir(), t.TempDir()
	cfg := "projects:\n  - {name: alpha, repo: daan/alpha, path: " + a + "}\n  - {name: beta, repo: daan/beta, path: " + b + "}\n"
	d, _ := newEnvCLI(t, cfg)
	elsewhere := t.TempDir()
	d.getwd = func() string { return elsewhere }
	d.homeDir = func() string { return elsewhere }
	d.now = func() time.Time { return time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC) }
	return d, a, b
}

func TestADRWithFlags(t *testing.T) {
	d, _, b := newADRCLI(t)
	stdout, _, err := run(t, d, "adr", "--project", "beta", "--title", "Use SQLite", "--decision", "modernc sqlite", "--consequences", "no cgo")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(b, ".forge", "docs", "decisions", "0001-use-sqlite.md")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "date: 2026-11-22\nstatus: accepted\n") || !strings.Contains(string(got), "## Consequences\n\nno cgo\n") {
		t.Errorf("decision =\n%s", got)
	}
	if !strings.Contains(stdout, "logged 0001. Use SQLite") || !strings.Contains(stdout, "CLAUDE.md") {
		t.Errorf("stdout = %q", stdout)
	}
	claude, _ := os.ReadFile(filepath.Join(b, "CLAUDE.md"))
	if !strings.Contains(string(claude), ".forge/docs/decisions/") {
		t.Errorf("CLAUDE.md = %q", claude)
	}

	// The second decision is numbered on and leaves CLAUDE.md alone.
	stdout, _, err = run(t, d, "adr", "--project", "beta", "--title", "Next", "--decision", "x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "logged 0002. Next") || strings.Contains(stdout, "CLAUDE.md") {
		t.Errorf("stdout = %q", stdout)
	}
	claude2, _ := os.ReadFile(filepath.Join(b, "CLAUDE.md"))
	if string(claude2) != string(claude) {
		t.Errorf("CLAUDE.md changed: %q", claude2)
	}
}

func TestADRWithoutTerminalNeedsFlags(t *testing.T) {
	d, _, _ := newADRCLI(t)
	for _, args := range [][]string{
		{"adr", "--project", "alpha", "--title", "T"},
		{"adr", "--title", "T", "--decision", "D"}, // two projects, none picked
	} {
		if _, _, err := run(t, d, args...); err == nil || !strings.Contains(err.Error(), "required when not on a terminal") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
}

func TestADRFormPrefilledFromFlags(t *testing.T) {
	d, a, _ := newADRCLI(t)
	forms := 0
	d.runForm = func(*huh.Form) error { forms++; return nil } // accepts what's there
	if _, _, err := run(t, d, "adr", "--title", "T", "--decision", "D"); err != nil {
		t.Fatal(err)
	}
	if forms != 1 {
		t.Fatalf("forms = %d", forms)
	}
	// The form defaulted to the first project.
	if _, err := os.Stat(filepath.Join(a, ".forge", "docs", "decisions", "0001-t.md")); err != nil {
		t.Error(err)
	}
	// Still missing the decision after the form: an error, nothing written.
	if _, _, err := run(t, d, "adr", "--project", "alpha", "--title", "Only title"); err == nil {
		t.Error("missing decision accepted")
	}
	// An aborted form is an error.
	d.runForm = func(*huh.Form) error { return huh.ErrUserAborted }
	if _, _, err := run(t, d, "adr"); !errors.Is(err, huh.ErrUserAborted) {
		t.Errorf("err = %v", err)
	}
}

func TestADRUnknownProject(t *testing.T) {
	d, _, _ := newADRCLI(t)
	if _, _, err := run(t, d, "adr", "--project", "nope", "--title", "T", "--decision", "D"); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("err = %v", err)
	}
}
