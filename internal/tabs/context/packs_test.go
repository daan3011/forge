package context

import (
	stdctx "context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/packs"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

type fakeLister struct {
	files []string
	err   error
}

func (l fakeLister) LsFiles(stdctx.Context, string) ([]string, error) { return l.files, l.err }

// packFixture is the memory fixture plus a packs folder with "auth" and a
// checkout with its files.
func packFixture(t *testing.T) (*fixture, *Tab) {
	t.Helper()
	f := newFixture(t)
	write(t, filepath.Join(f.repo, "internal/auth/token.go"), strings.Repeat("x", 400))
	write(t, filepath.Join(f.repo, "internal/auth/session.go"), strings.Repeat("y", 400))
	tab := f.tab(t)
	tab.packsDir = filepath.Join(f.home, ".config", "forge", "packs")
	tab.lister = fakeLister{files: []string{"CLAUDE.md", "internal/auth/session.go", "internal/auth/token.go", "internal/db/db.go"}}
	if _, err := packs.Save(tab.packsDir, packs.Pack{
		Name: "auth", Description: "Login and tokens", Files: []string{"internal/auth/*.go", "gone/**"},
		Preamble: "Careful with tokens.\n",
	}, true); err != nil {
		t.Fatal(err)
	}
	return f, tab
}

// pumpAll is pump that also feeds back the tab's untargeted messages (huh's
// own) for a few rounds, and returns success / info toasts too.
func pumpAll(tab *Tab, cmd tea.Cmd) (toasts []string, out []tea.Msg) {
	queue := drain(cmd)
	for i := 0; len(queue) > 0 && i < 40; i++ {
		msg := queue[0]
		queue = queue[1:]
		switch m := msg.(type) {
		case nil:
		case ui.ErrMsg:
			toasts = append(toasts, "err: "+m.Err.Error())
		case ui.SuccessMsg:
			toasts = append(toasts, "ok: "+m.Text)
		case ui.InfoMsg:
			toasts = append(toasts, "info: "+m.Text)
		case tabs.StartSessionMsg:
			out = append(out, m)
		case tabs.TargetedMsg:
			_, next := tab.Update(m)
			queue = append(queue, drain(next)...)
		case tea.BatchMsg:
		default:
			if tab.CapturingInput() { // a huh form's own message
				_, next := tab.Update(m)
				queue = append(queue, drain(next)...)
				continue
			}
			out = append(out, m)
		}
	}
	return toasts, out
}

func showPacks(t *testing.T, tab *Tab) {
	t.Helper()
	pumpAll(tab, tab.Refresh())
	send(tab, "p")
	if tab.view != viewPacks || !tab.pk.loaded {
		t.Fatal("packs view not showing")
	}
}

func TestPacksViewListAndDetail(t *testing.T) {
	_, tab := packFixture(t)
	showPacks(t, tab)
	v := view(tab)
	for _, want := range []string{
		"Memory", "Packs (1)", "auth ⚠", "≈200", "Login and tokens · 2 files", "auth ≈ 200 tokens",
		"Files (2)", "internal/auth/session.go", "internal/auth/token.go",
		"Matched nothing", "⚠ gone/**", "Patterns", "internal/auth/*.go", "Preamble", "Careful with tokens.",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if got := tab.StatusDetail(); got != "pack auth ≈200 tokens" {
		t.Errorf("status = %q", got)
	}
	send(tab, "p")
	if v := view(tab); !strings.Contains(v, "Loaded at start") {
		t.Errorf("p should switch back to memory:\n%s", v)
	}
}

func TestPacksViewEmpty(t *testing.T) {
	f := newFixture(t)
	tab := f.tab(t)
	tab.packsDir = filepath.Join(t.TempDir(), "packs")
	showPacks(t, tab)
	if v := view(tab); !strings.Contains(v, "No context packs yet. n to make one") || !strings.Contains(v, "Packs (0)") {
		t.Errorf("view:\n%s", v)
	}
}

func TestPackEditDeleteSend(t *testing.T) {
	f, tab := packFixture(t)
	showPacks(t, tab)

	f.onEdit = func(p string) { write(t, p, "description: Rewritten\nfiles: [internal/auth/token.go]\n") }
	_, cmd := tab.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	pumpAll(tab, cmd)
	if got := f.editedPaths(); len(got) != 1 || filepath.Base(got[0]) != "auth.yaml" {
		t.Fatalf("edited = %v", got)
	}
	if v := view(tab); !strings.Contains(v, "Rewritten · 1 file") {
		t.Errorf("not reloaded after edit:\n%s", v)
	}

	_, cmd = tab.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	_, out := pumpAll(tab, cmd)
	if len(out) != 1 {
		t.Fatalf("send produced %v", out)
	}
	if m, ok := out[0].(tabs.StartSessionMsg); !ok || !slices.Equal(m.Source.Packs, []string{"auth"}) || m.Source.Repo != "o/proj" {
		t.Errorf("send = %+v", out[0])
	}

	_, cmd = tab.Update(tea.KeyPressMsg{Code: 'D', Text: "D"})
	pumpAll(tab, cmd)
	if !tab.CapturingInput() {
		t.Fatal("no delete confirmation")
	}
	if v := view(tab); !strings.Contains(v, "Delete pack auth?") {
		t.Fatalf("confirm:\n%s", v)
	}
	_, cmd = tab.Update(tea.KeyPressMsg{Code: tea.KeyLeft}) // Keep -> Delete
	pumpAll(tab, cmd)
	_, cmd = tab.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	toasts, _ := pumpAll(tab, cmd)
	if !slices.Contains(toasts, "ok: pack auth deleted") {
		t.Errorf("toasts = %q", toasts)
	}
	if _, err := os.Stat(filepath.Join(tab.packsDir, "auth.yaml")); !os.IsNotExist(err) {
		t.Errorf("pack file still there: %v", err)
	}
	if v := view(tab); !strings.Contains(v, "No context packs yet") {
		t.Errorf("view:\n%s", v)
	}
}

func TestPackAddFilesKeepsGlobs(t *testing.T) {
	_, tab := packFixture(t)
	showPacks(t, tab)
	pumpAll(tab, tab.addFiles())
	d := tab.pk.dialog
	if d == nil || d.picker == nil {
		t.Fatal("no picker")
	}
	if v := view(tab); !strings.Contains(v, "Files for auth") || !strings.Contains(v, "0 selected · 4/4") {
		t.Fatalf("picker:\n%s", v)
	}
	for _, k := range []tea.KeyPressMsg{{Code: 'd', Text: "d"}, {Code: 'b', Text: "b"}, {Code: tea.KeyTab}, {Code: tea.KeyEnter}} {
		_, cmd := tab.Update(k)
		pumpAll(tab, cmd)
	}
	if tab.CapturingInput() {
		t.Fatal("picker still open")
	}
	ps, _ := packs.Load(tab.packsDir)
	if len(ps) != 1 || !slices.Equal(ps[0].Files, []string{"internal/auth/*.go", "gone/**", "internal/db/db.go"}) {
		t.Fatalf("packs = %+v", ps)
	}
}

func TestPackListError(t *testing.T) {
	_, tab := packFixture(t)
	tab.lister = fakeLister{err: os.ErrPermission}
	showPacks(t, tab)
	toasts, _ := pumpAll(tab, tab.addFiles())
	if len(toasts) != 1 || !strings.Contains(toasts[0], "couldn't list files") {
		t.Errorf("toasts = %q", toasts)
	}
}

func TestPackCommandsShowPacksView(t *testing.T) {
	_, tab := packFixture(t)
	pumpAll(tab, tab.Refresh())
	var run func() tea.Cmd
	for _, c := range tab.Commands() {
		if c.ID == "context_new_pack" {
			run = c.Run
		}
	}
	if run == nil {
		t.Fatal("no context_new_pack command")
	}
	pumpAll(tab, run())
	if tab.view != viewPacks || !tab.CapturingInput() {
		t.Errorf("on=%v capturing=%v", tab.view == viewPacks, tab.CapturingInput())
	}
}

// TestNewPackFlow: create a pack with the form and the picker, see it in
// the list with its token total.
func TestNewPackFlow(t *testing.T) {
	_, tab := packFixture(t)
	h := &host{tab: tab}
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(120, 30))
	waitFor(t, h, "Total ≈")
	typeKeys := func(s string) {
		for _, r := range s {
			tm.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
	typeKeys("p")
	waitFor(t, h, "Packs (1)", "auth")
	typeKeys("n")
	waitFor(t, h, "New context pack")
	// huh moves to the next field in a command; give it time before typing
	// on, as a person would.
	next := func() {
		tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
		time.Sleep(150 * time.Millisecond)
	}
	typeKeys("tokens")
	next()
	typeKeys("Token handling")
	next()
	typeKeys("Read the token code.")
	next()
	waitFor(t, h, "Files for tokens", "0 selected · 4/4")
	typeKeys("tok")
	waitFor(t, h, "0 selected · 1/4")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyTab})
	waitFor(t, h, "1 selected · 1/4")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, h, "Packs (2)", "tokens ≈ 100 tokens", "Read the token code.")

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))

	p, err := os.ReadFile(filepath.Join(tab.packsDir, "tokens.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"description: Token handling", "- internal/auth/token.go", "preamble: |", "Read the token code."} {
		if !strings.Contains(string(p), want) {
			t.Errorf("tokens.yaml lacks %q:\n%s", want, p)
		}
	}
}
