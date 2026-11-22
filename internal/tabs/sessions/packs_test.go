package sessions

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/config"
	docfs "github.com/daan3011/forge/internal/docs"
	"github.com/daan3011/forge/internal/packs"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/templates"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// packTab is fixTab with a real checkout for the forge project and an
// "auth" pack: two repo files by glob, a doc outside the repo, a preamble.
func packTab(t *testing.T, svc *fakeSvc) (*Tab, string) {
	t.Helper()
	tab := fixTab(t, svc)
	base := t.TempDir()
	repo := filepath.Join(base, "forge")
	writeFile(t, filepath.Join(repo, "internal/auth/token.go"), "package auth")
	writeFile(t, filepath.Join(repo, "internal/auth/session.go"), "package auth")
	writeFile(t, filepath.Join(base, "notes/auth.md"), "# auth notes")
	tab.cfg.Projects[0].Path = repo
	tab.packsDir = filepath.Join(base, "packs")
	if _, err := packs.Save(tab.packsDir, packs.Pack{
		Name: "auth", Description: "Login and tokens",
		Files: []string{"internal/auth/*.go"}, Docs: []string{filepath.Join(base, "notes/auth.md")},
		Preamble: "You're on the auth layer.\n",
	}, true); err != nil {
		t.Fatal(err)
	}
	return tab, repo
}

// completeDialogs completes every open dialog with its current values.
func completeDialogs(t *testing.T, tab *Tab) []string {
	t.Helper()
	var toasts []string
	for range 5 {
		d := tab.dialog
		if d == nil {
			return toasts
		}
		tab.dialog = nil
		toasts = append(toasts, pumpForm(tab, d.done())...)
	}
	t.Fatal("dialogs never ended")
	return nil
}

func TestNewSessionWithPack(t *testing.T) {
	svc := newFakeSvc()
	tab, repo := packTab(t, svc)
	pumpForm(tab, tab.openNewWith(NewRequest{Opts: agent.StartOpts{
		Name: "login", Project: "forge", Prompt: "Fix the login bug.", Packs: []string{"auth"},
	}}))
	if tab.dialog == nil {
		t.Fatal("no new-session form")
	}
	if v := ansi.Strip(tab.View()); !strings.Contains(v, "Context packs") || !strings.Contains(v, "auth — Login and tokens") {
		t.Fatalf("form has no packs field:\n%s", v)
	}
	completeDialogs(t, tab)
	starts, _, _ := svc.snapshot()
	if len(starts) != 1 {
		t.Fatalf("starts = %+v", starts)
	}
	o := starts[0]
	want := "You're on the auth layer.\n\n" +
		"Read these files first (context pack auth): @internal/auth/session.go @internal/auth/token.go @.forge/attached/auth.md\n\n" +
		"Fix the login bug.\n"
	if o.Prompt != want {
		t.Errorf("prompt =\n%q\nwant\n%q", o.Prompt, want)
	}
	if len(o.Docs) != 3 || o.Docs[0].Src != filepath.Join(repo, "internal/auth/session.go") || !o.Docs[2].Copy {
		t.Errorf("docs = %+v", o.Docs)
	}
}

func TestNewSessionWithPackAndTemplate(t *testing.T) {
	svc := newFakeSvc()
	tab, _ := packTab(t, svc)
	pumpForm(tab, tab.openNewWith(NewRequest{
		Opts: agent.StartOpts{Name: "login", Project: "forge", Template: "refactor", Packs: []string{"auth"}},
		Vars: map[string]string{"target": "internal/auth", "goal": "simpler"},
	}))
	completeDialogs(t, tab) // form, then the prompt for a last look
	starts, _, _ := svc.snapshot()
	if len(starts) != 1 {
		t.Fatalf("starts = %+v", starts)
	}
	if p := starts[0].Prompt; !strings.HasPrefix(p, "You're on the auth layer.\n\nRead these files first (context pack auth):") ||
		!strings.Contains(p, "internal/auth") {
		t.Errorf("prompt:\n%s", p)
	}
}

func TestNewSessionWithoutPacksUnchanged(t *testing.T) {
	svc := newFakeSvc()
	tab, _ := packTab(t, svc)
	pumpForm(tab, tab.openNewWith(NewRequest{Opts: agent.StartOpts{Name: "plain", Project: "forge", Prompt: "hi"}}))
	completeDialogs(t, tab)
	starts, _, _ := svc.snapshot()
	if len(starts) != 1 || starts[0].Prompt != "hi" || len(starts[0].Docs) != 0 {
		t.Fatalf("starts = %+v", starts)
	}
}

func TestNewSessionUnknownPackIsDropped(t *testing.T) {
	svc := newFakeSvc()
	tab, _ := packTab(t, svc)
	pumpForm(tab, tab.openNewWith(NewRequest{Opts: agent.StartOpts{Name: "x", Project: "forge", Prompt: "hi", Packs: []string{"gone"}}}))
	completeDialogs(t, tab)
	starts, _, _ := svc.snapshot()
	if len(starts) != 1 || starts[0].Prompt != "hi" {
		t.Fatalf("starts = %+v", starts)
	}
}

func TestAttachPackToRunningSession(t *testing.T) {
	work := row("work", store.StatusIdle)
	svc := newFakeSvc(work)
	tab, _ := packTab(t, svc)
	pumpForm(tab, tab.attachPack())
	if tab.dialog == nil {
		t.Fatal("no pack picker")
	}
	if v := ansi.Strip(tab.View()); !strings.Contains(v, "Attach context pack to work") {
		t.Fatalf("picker:\n%s", v)
	}
	toasts := completeDialogs(t, tab)
	if !slices.Contains(toasts, "ok: sent pack auth to work") {
		t.Errorf("toasts = %q", toasts)
	}
	svc.mu.Lock()
	attached, sent := svc.attached["work"], svc.sent["work"]
	svc.mu.Unlock()
	if len(attached) != 3 {
		t.Errorf("attached = %+v", attached)
	}
	want := "You're on the auth layer.\n\nRead these files first (context pack auth): @internal/auth/session.go @internal/auth/token.go @.forge/attached/auth.md"
	if len(sent) != 1 || sent[0] != want {
		t.Errorf("sent = %q", sent)
	}
}

func TestAttachPackNeedsRunningSessionAndPacks(t *testing.T) {
	svc := newFakeSvc(row("old", store.StatusDead))
	tab, _ := packTab(t, svc)
	if toasts, _ := pump(tab, tab.attachPack()); !slices.Contains(toasts, "info: old isn't running; resume it first") {
		t.Errorf("toasts = %q", toasts)
	}
	svc = newFakeSvc(row("work", store.StatusIdle))
	tab = fixTab(t, svc)
	tab.packsDir = filepath.Join(t.TempDir(), "none")
	toasts := pumpForm(tab, tab.attachPack())
	if !slices.ContainsFunc(toasts, func(s string) bool { return strings.Contains(s, "no context packs yet") }) {
		t.Errorf("toasts = %q", toasts)
	}
}

func TestSendPackFromContextTab(t *testing.T) {
	// No running session: the new-session form opens with the pack chosen.
	svc := newFakeSvc()
	tab, _ := packTab(t, svc)
	pumpForm(tab, tab.StartSession(tabs.Source{Packs: []string{"auth"}, Repo: "daan/forge"}))
	if tab.dialog == nil {
		t.Fatal("no form")
	}
	if v := ansi.Strip(tab.View()); !strings.Contains(v, "pack-auth") {
		t.Errorf("form not named after the pack:\n%s", v)
	}

	// A running one: pick it and the pack is pasted there.
	svc = newFakeSvc(row("work", store.StatusIdle))
	tab, _ = packTab(t, svc)
	pumpForm(tab, tab.StartSession(tabs.Source{Packs: []string{"auth"}, Repo: "daan/forge"}))
	if v := ansi.Strip(tab.View()); !strings.Contains(v, "Send pack auth to") || !strings.Contains(v, "work · idle") {
		t.Fatalf("picker:\n%s", v)
	}
	answer(tab, press("down"))
	toasts := answer(tab, press("enter"))
	if !slices.Contains(toasts, "ok: sent pack auth to work") {
		t.Errorf("toasts = %q", toasts)
	}
	if tab.busy != 0 {
		t.Errorf("busy = %d", tab.busy)
	}
}

func TestResolvePacks(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "a.go"), "a")
	all := []packs.Pack{{Name: "a", Files: []string{"a.go", "nope/*.go"}}, {Name: "b", Preamble: "B"}}
	rs, notes, err := ResolvePacks(all, []string{"b", "a", "c"}, config.Project{Name: "p", Path: repo})
	if err == nil || !strings.Contains(err.Error(), "no context pack called c") {
		t.Errorf("err = %v", err)
	}
	if len(rs) != 2 || rs[0].Name != "b" || rs[1].Name != "a" || len(rs[1].Paths) != 1 {
		t.Errorf("resolved = %+v", rs)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "nothing matches nope/*.go") {
		t.Errorf("notes = %q", notes)
	}
}

func TestSplitPlanAndWithAttachments(t *testing.T) {
	rs := []packs.Resolved{{Name: "p", Preamble: "Pre.", Paths: []string{"/code/forge/a.go", "/x/notes.md"}}}
	docs := []string{"/code/forge/a.go", "/y/notes.md"} // a.go also in the pack; notes.md clashes by name
	packPlan, docPlan := SplitPlan("/code/forge", rs, docs)
	if len(packPlan) != 2 || len(docPlan) != 1 {
		t.Fatalf("plans = %+v / %+v", packPlan, docPlan)
	}
	if packPlan[1].Rel == docPlan[0].Rel {
		t.Errorf("copies collide: %+v %+v", packPlan[1], docPlan[0])
	}
	o := WithAttachments(agent.StartOpts{}, "/code/forge", rs, docs, templates.Template{}, "Go.")
	if len(o.Docs) != 3 {
		t.Errorf("docs = %+v", o.Docs)
	}
	if !strings.HasPrefix(o.Prompt, "Pre.\n\nRead these files first (context pack p): @a.go @.forge/attached/notes.md\n\nGo.") ||
		!strings.Contains(o.Prompt, "Read these docs first: @"+docPlan[0].Rel) {
		t.Errorf("prompt:\n%s", o.Prompt)
	}
	if got := PackMessage(rs, []docfs.Attachment{{Rel: "a.go"}}); got != "Pre.\n\nRead these files first (context pack p): @a.go" {
		t.Errorf("PackMessage = %q", got)
	}
}
