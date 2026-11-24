package main

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/setup"
	"github.com/daan3011/forge/internal/setup/wizard"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/testutil"
)

func TestSetupRejectsUnknownStep(t *testing.T) {
	testutil.IsolateHome(t)
	ran := false
	d := deps{db: filepath.Join(t.TempDir(), "forge.db"), setupProgram: func(*wizard.Model) error { ran = true; return nil }}
	_, _, err := run(t, d, "setup", "nope")
	if err == nil || !strings.Contains(err.Error(), `unknown setup step "nope"`) {
		t.Fatalf("err = %v", err)
	}
	if ran {
		t.Error("the wizard ran for an unknown step")
	}
	if _, _, err := run(t, d, "setup", "plane", "tmux"); err == nil {
		t.Error("two steps accepted")
	}
}

func TestSetupRunsOneStep(t *testing.T) {
	testutil.IsolateHome(t)
	var steps []setup.Step
	d := deps{db: filepath.Join(t.TempDir(), "forge.db"), setupProgram: func(m *wizard.Model) error { steps = m.Steps(); return nil }}
	if _, _, err := run(t, d, "setup", "plane"); err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].ID != setup.StepPlane {
		t.Errorf("steps = %v", steps)
	}
	if _, _, err := run(t, d, "setup"); err != nil {
		t.Fatal(err)
	}
	if len(steps) != len(setup.Steps) {
		t.Errorf("full run has %d steps", len(steps))
	}
}

func TestFirstRunSetup(t *testing.T) {
	testutil.IsolateHome(t)
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	runs := 0
	env := map[string]string{}
	tty := true
	d := deps{
		setupProgram: func(*wizard.Model) error { runs++; return nil },
		isTTY:        func() bool { return tty },
		getenv:       func(k string) string { return env[k] },
	}
	cfg := config.Default()

	check := func(name string, created, want bool) {
		t.Helper()
		before := runs
		ran, err := d.firstRunSetup(ctx, cfg, "/x/config.yaml", created, st, io.Discard)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if ran != want || (runs > before) != want {
			t.Errorf("%s: ran = %v (runs %d → %d), want %v", name, ran, before, runs, want)
		}
	}
	check("existing install", false, false)
	tty = false
	check("no terminal", true, false)
	tty = true
	env[EnvNoSetup] = "1"
	check("FORGE_NO_SETUP", true, false)
	delete(env, EnvNoSetup)
	check("first launch", true, true)

	// Existing installs get the notice until setup finished.
	if show, err := setupNotice(ctx, st); !show || err != nil {
		t.Errorf("notice before setup: %v %v", show, err)
	}
	if err := st.Settings().MarkSetupCompleted(ctx); err != nil {
		t.Fatal(err)
	}
	check("already set up", true, false)
	if show, _ := setupNotice(ctx, st); show {
		t.Error("notice after setup")
	}
}
