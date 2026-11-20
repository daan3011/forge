package tmuxtest

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/daan3011/forge/internal/sessions/tmux"
)

// RunConformance checks the behaviour every tmux.Driver must share. cmd is
// a long-running argv for test windows (e.g. sleep 600). Each subtest uses
// its own session, so one driver may be shared.
func RunConformance(t *testing.T, d tmux.Driver, cmd []string) {
	t.Helper()
	ctx := context.Background()

	session := func(t *testing.T) string {
		t.Helper()
		s, err := d.EnsureSession(ctx, "conf "+t.Name())
		if err != nil {
			t.Fatalf("EnsureSession: %v", err)
		}
		return s
	}

	t.Run("ensure session is idempotent and sanitised", func(t *testing.T) {
		a, err := d.EnsureSession(ctx, "my.project:x")
		if err != nil {
			t.Fatal(err)
		}
		if a != "my-project-x" {
			t.Errorf("name = %q, want my-project-x", a)
		}
		b, err := d.EnsureSession(ctx, "my.project:x")
		if err != nil || b != a {
			t.Errorf("second EnsureSession = %q, %v", b, err)
		}
		ws, err := d.ListWindows(ctx, a)
		if err != nil || len(ws) != 0 {
			t.Errorf("fresh session windows = %v, %v; want none (placeholder hidden)", ws, err)
		}
	})

	t.Run("invalid names", func(t *testing.T) {
		if _, err := d.EnsureSession(ctx, "..."); !errors.Is(err, tmux.ErrInvalidName) {
			t.Errorf("EnsureSession(...) err = %v", err)
		}
		s := session(t)
		if _, err := d.NewWindow(ctx, s, ":::", "", cmd, nil); !errors.Is(err, tmux.ErrInvalidName) {
			t.Errorf("NewWindow(:::) err = %v", err)
		}
		if _, err := d.WindowAlive(ctx, tmux.Target{Session: s, Window: "a:b"}); !errors.Is(err, tmux.ErrInvalidName) {
			t.Errorf("WindowAlive(a:b) err = %v", err)
		}
		if _, err := d.NewWindow(ctx, s, "w", "", cmd, map[string]string{"A=B": "x"}); err == nil {
			t.Error("NewWindow with bad env key: want error")
		}
	})

	t.Run("window lifecycle", func(t *testing.T) {
		s := session(t)
		tg, err := d.NewWindow(ctx, s, "agent one", "", cmd, map[string]string{"FORGE_TEST": "1"})
		if err != nil {
			t.Fatal(err)
		}
		if want := (tmux.Target{Session: s, Window: "agent-one"}); tg != want {
			t.Errorf("target = %+v, want %+v", tg, want)
		}
		if alive, err := d.WindowAlive(ctx, tg); err != nil || !alive {
			t.Errorf("WindowAlive = %v, %v; want true", alive, err)
		}
		ws, err := d.ListWindows(ctx, s)
		if err != nil || len(ws) != 1 || ws[0].Name != "agent-one" || ws[0].Dead || ws[0].ID == "" {
			t.Errorf("ListWindows = %+v, %v", ws, err)
		}
		if _, err := d.NewWindow(ctx, s, "agent-one", "", cmd, nil); !errors.Is(err, tmux.ErrWindowExists) {
			t.Errorf("duplicate NewWindow err = %v", err)
		}
		if err := d.SendKeys(ctx, tg, "hello", true); err != nil {
			t.Errorf("SendKeys: %v", err)
		}
		if err := d.PasteText(ctx, tg, "line one\nline \"two\" $HOME `x`"); err != nil {
			t.Errorf("PasteText: %v", err)
		}
		if _, err := d.CapturePane(ctx, tg, 10); err != nil {
			t.Errorf("CapturePane: %v", err)
		}
		if err := d.KillWindow(ctx, tg); err != nil {
			t.Fatal(err)
		}
		if alive, err := d.WindowAlive(ctx, tg); err != nil || alive {
			t.Errorf("after kill WindowAlive = %v, %v", alive, err)
		}
		if err := d.KillWindow(ctx, tg); err != nil {
			t.Errorf("second KillWindow: %v", err)
		}
	})

	t.Run("missing session and window", func(t *testing.T) {
		missing := tmux.Target{Session: "forge-conf-missing", Window: "w"}
		if ws, err := d.ListWindows(ctx, missing.Session); err != nil || ws != nil {
			t.Errorf("ListWindows(missing) = %v, %v", ws, err)
		}
		if alive, err := d.WindowAlive(ctx, missing); err != nil || alive {
			t.Errorf("WindowAlive(missing) = %v, %v", alive, err)
		}
		if err := d.KillWindow(ctx, missing); err != nil {
			t.Errorf("KillWindow(missing): %v", err)
		}
		if _, err := d.NewWindow(ctx, missing.Session, "w", "", cmd, nil); !errors.Is(err, tmux.ErrNotFound) {
			t.Errorf("NewWindow(missing session) err = %v", err)
		}
		s := session(t)
		gone := tmux.Target{Session: s, Window: "gone"}
		if err := d.SendKeys(ctx, gone, "x", false); !errors.Is(err, tmux.ErrNotFound) {
			t.Errorf("SendKeys(gone) err = %v", err)
		}
		if err := d.PasteText(ctx, gone, "x"); !errors.Is(err, tmux.ErrNotFound) {
			t.Errorf("PasteText(gone) err = %v", err)
		}
		if _, err := d.CapturePane(ctx, gone, 5); !errors.Is(err, tmux.ErrNotFound) {
			t.Errorf("CapturePane(gone) err = %v", err)
		}
		if _, err := d.Attach(ctx, gone); !errors.Is(err, tmux.ErrNotFound) {
			t.Errorf("Attach(gone) err = %v", err)
		}
	})
	t.Run("list all", func(t *testing.T) {
		s := session(t)
		tg, err := d.NewWindow(ctx, s, "listed", "", cmd, nil)
		if err != nil {
			t.Fatal(err)
		}
		all, err := d.ListAll(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(all, tg) {
			t.Errorf("ListAll = %v, missing %v", all, tg)
		}
		for _, x := range all {
			if x.Window == tmux.PlaceholderWindow {
				t.Errorf("ListAll returned the placeholder: %v", x)
			}
		}
	})

	// Last: it ends every window of every subtest above.
	t.Run("kill server", func(t *testing.T) {
		s := session(t)
		if _, err := d.NewWindow(ctx, s, "doomed", "", cmd, nil); err != nil {
			t.Fatal(err)
		}
		if err := d.KillServer(ctx); err != nil {
			t.Fatal(err)
		}
		if all, err := d.ListAll(ctx); err != nil || len(all) != 0 {
			t.Errorf("ListAll after KillServer = %v, %v", all, err)
		}
		if err := d.KillServer(ctx); err != nil {
			t.Errorf("KillServer without a server: %v", err)
		}
	})
}
