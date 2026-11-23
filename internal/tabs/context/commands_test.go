package context

import (
	stdctx "context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/slashcmd"
	"github.com/daan3011/forge/internal/testutil"
)

// commandsFixture is the memory fixture with forge's real skill catalog
// (overrides in the fake home) and a recording re-sync.
func commandsFixture(t *testing.T) (*fixture, *Tab, *[]int) {
	t.Helper()
	testutil.IsolateHome(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	f := newFixture(t)
	tab := f.tab(t)
	tab.skills = slashcmd.Catalog{OverridesDir: filepath.Join(f.home, ".config", "forge", "commands")}
	calls := &[]int{}
	tab.syncAll = func(stdctx.Context) ([]sessions.SyncResult, error) {
		*calls = append(*calls, 1)
		return []sessions.SyncResult{
			{Session: "a", Result: slashcmd.Result{Written: []string{"x"}}},
			{Session: "b"},
		}, nil
	}
	return f, tab, calls
}

func showCommands(t *testing.T, tab *Tab) {
	t.Helper()
	pumpAll(tab, tab.Refresh())
	send(tab, "m")
	if tab.view != viewCommands || !tab.cm.loaded {
		t.Fatal("commands view not showing")
	}
}

func selectCommand(t *testing.T, tab *Tab, name string) {
	t.Helper()
	i := slices.IndexFunc(tab.cm.items, func(s slashcmd.Status) bool { return s.Name == name })
	if i < 0 {
		t.Fatalf("no /%s", name)
	}
	tab.setCommandCursor(i)
}

func TestCommandsViewListsBuiltins(t *testing.T) {
	_, tab, _ := commandsFixture(t)
	showCommands(t, tab)
	v := view(tab)
	for _, want := range []string{"Commands", "/fix-ci", "/forge-ticket", "built-in", "re-syncs running sessions"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	selectCommand(t, tab, "forge-ticket")
	if v := view(tab); !strings.Contains(v, "disable-model-invocation: true") {
		t.Errorf("detail doesn't show the SKILL.md:\n%s", v)
	}
	// m again goes back to the memory files.
	send(tab, "m")
	if tab.view != viewMemory {
		t.Error("m didn't toggle back")
	}
}

func TestCommandsShadowBadge(t *testing.T) {
	f, tab, _ := commandsFixture(t)
	if err := os.MkdirAll(filepath.Join(f.repo, ".claude", "skills", "fix-ci"), 0o755); err != nil {
		t.Fatal(err)
	}
	showCommands(t, tab)
	selectCommand(t, tab, "fix-ci")
	if v := view(tab); !strings.Contains(v, "shadowed by repo — use /forge:fix-ci") {
		t.Errorf("no badge:\n%s", v)
	}
}

func TestCommandsEditResetSync(t *testing.T) {
	f, tab, calls := commandsFixture(t)
	showCommands(t, tab)
	selectCommand(t, tab, "forge-status")

	// Reset without an override says so.
	if toasts := send(tab, "X"); tab.CapturingInput() {
		t.Fatalf("asked to reset a built-in: %v", toasts)
	}

	// e copies the built-in into the override, then opens it.
	f.onEdit = func(p string) {
		write(t, p, "---\ndescription: My status\ndisable-model-invocation: true\n---\n$ARGUMENTS\n")
	}
	_, cmd := tab.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	pumpAll(tab, cmd)
	got := f.editedPaths()
	want := filepath.Join(f.home, ".config", "forge", "commands", "forge-status.md")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("edited = %v, want %s", got, want)
	}
	selectCommand(t, tab, "forge-status")
	if v := view(tab); !strings.Contains(v, "My status") || !strings.Contains(v, "override of built-in") {
		t.Errorf("not reloaded after edit:\n%s", v)
	}

	// X asks, then removes the override.
	_, cmd = tab.Update(tea.KeyPressMsg{Code: 'X', Text: "X"})
	pumpAll(tab, cmd)
	if !tab.CapturingInput() || !strings.Contains(view(tab), "Reset /forge-status to the built-in?") {
		t.Fatalf("no confirm:\n%s", view(tab))
	}
	_, cmd = tab.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	pumpAll(tab, cmd)
	_, cmd = tab.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	toasts, _ := pumpAll(tab, cmd)
	if !slices.Contains(toasts, "ok: /forge-status reset") {
		t.Errorf("toasts = %q", toasts)
	}
	if _, err := os.Stat(want); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("override still there: %v", err)
	}
	selectCommand(t, tab, "forge-status")
	if s, _ := tab.selectedCommand(); s.Source != slashcmd.SourceBuiltin {
		t.Errorf("after reset: %s", s.Label())
	}

	// S re-syncs every session.
	_, cmd = tab.Update(tea.KeyPressMsg{Code: 'S', Text: "S"})
	toasts, _ = pumpAll(tab, cmd)
	if len(*calls) != 1 || !slices.Contains(toasts, "ok: skills re-synced in 2 sessions (1 file written, 0 removed)") {
		t.Errorf("calls %d, toasts %q", len(*calls), toasts)
	}
}

func TestCommandsPaletteAndHelp(t *testing.T) {
	_, tab, calls := commandsFixture(t)
	pumpAll(tab, tab.Refresh())
	var ids []string
	var sync func() tea.Cmd
	for _, c := range tab.Commands() {
		ids = append(ids, c.ID)
		if c.ID == "context_commands_sync" {
			sync = c.Run
		}
	}
	for _, id := range []string{"context_commands", "context_commands_reset", "context_commands_sync"} {
		if !slices.Contains(ids, id) {
			t.Errorf("palette lacks %s", id)
		}
	}
	pumpAll(tab, sync())
	if len(*calls) != 1 {
		t.Error("palette sync didn't run")
	}
	var help []string
	for _, g := range tab.FullHelp() {
		for _, b := range g {
			help = append(help, b.Help().Desc)
		}
	}
	for _, d := range []string{"memory / commands", "reset to built-in", "re-sync sessions"} {
		if !slices.Contains(help, d) {
			t.Errorf("help lacks %q", d)
		}
	}
}

func TestSyncSummary(t *testing.T) {
	tests := []struct {
		name string
		res  []sessions.SyncResult
		want []string
	}{
		{"none", nil, []string{"info: no running sessions to sync"}},
		{"up to date", []sessions.SyncResult{{Session: "a"}}, []string{"ok: skills re-synced in 1 session (already up to date)"}},
		{
			"error",
			[]sessions.SyncResult{{Session: "a", Err: errors.New("boom"), Result: slashcmd.Result{Removed: []string{"x"}}}},
			[]string{"ok: skills re-synced in 1 session (0 files written, 1 removed)", "err: a: boom"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, c := range syncSummary(tt.res) {
				g, _ := pumpAll(nil, c)
				got = append(got, g...)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
