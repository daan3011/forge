package sessions

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	docfs "github.com/daan3011/forge/internal/docs"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/templates"
)

// testConfig's forge project lives at /code/forge: one doc inside the repo,
// one outside it.
var sendDocs = []string{"/code/forge/docs/plan.md", "/notes/forge/ideas.md"}

func docsSource() tabs.Source { return tabs.Source{Docs: sendDocs, Repo: "daan/forge"} }

func TestSendDocsWithoutRunningSessionOpensNewForm(t *testing.T) {
	dead := row("old", store.StatusDead)
	svc := newFakeSvc(dead)
	tab := fixTab(t, svc)
	pumpForm(tab, tab.StartSession(docsSource()))
	if tab.dialog == nil {
		t.Fatal("no new-session form")
	}
	if v := ansi.Strip(tab.View()); !strings.Contains(v, "docs-plan") {
		t.Errorf("form not pre-filled with docs-plan:\n%s", v)
	}
}

func TestSendDocsToRunningSession(t *testing.T) {
	work := row("work", store.StatusIdle)
	svc := newFakeSvc(work, row("gone", store.StatusDead))
	tab := fixTab(t, svc)
	pumpForm(tab, tab.StartSession(docsSource()))
	if tab.dialog == nil {
		t.Fatal("no picker")
	}
	v := ansi.Strip(tab.View())
	if !strings.Contains(v, "New session") || !strings.Contains(v, "work · idle") || strings.Contains(v, "gone ·") {
		t.Fatalf("picker:\n%s", v)
	}
	_, cmd := tab.Update(press("down"))
	pumpForm(tab, cmd)
	_, cmd = tab.Update(press("enter"))
	toasts := pumpForm(tab, cmd)
	if !slices.Contains(toasts, "ok: sent 2 docs to work") {
		t.Errorf("toasts = %q", toasts)
	}
	svc.mu.Lock()
	attached, sent := svc.attached["work"], svc.sent["work"]
	svc.mu.Unlock()
	want := []docfs.Attachment{
		{Src: "/code/forge/docs/plan.md", Rel: "docs/plan.md"},
		{Src: "/notes/forge/ideas.md", Rel: ".forge/attached/ideas.md", Copy: true},
	}
	if !slices.Equal(attached, want) {
		t.Errorf("attached = %v, want %v", attached, want)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "@docs/plan.md @.forge/attached/ideas.md") {
		t.Errorf("sent = %q", sent)
	}
	starts, _, _ := svc.snapshot()
	if len(starts) != 0 {
		t.Errorf("started %+v", starts)
	}
}

func TestSendDocsAttachFailure(t *testing.T) {
	svc := newFakeSvc(row("work", store.StatusIdle))
	svc.attachErr = agent.ErrWorktreeMissing
	tab := fixTab(t, svc)
	toasts := pumpForm(tab, tab.sendDocs(svc.rows[0].Session, sendDocs))
	if !slices.ContainsFunc(toasts, func(s string) bool { return strings.HasPrefix(s, "err: couldn't send docs to work") }) {
		t.Errorf("toasts = %q", toasts)
	}
	if len(svc.sent) != 0 {
		t.Errorf("sent after a failed attach: %v", svc.sent)
	}
}

func TestSendDocsNewSessionMentionsDocs(t *testing.T) {
	tests := []struct {
		template string
		vars     map[string]string
	}{
		{"explore", map[string]string{"question": "how does it fit?"}},                // uses {docs}
		{"refactor", map[string]string{"target": "internal/docs", "goal": "simpler"}}, // doesn't: appended
	}
	for _, tt := range tests {
		t.Run(tt.template, func(t *testing.T) {
			svc := newFakeSvc()
			tab := fixTab(t, svc)
			ts, _ := loadTemplates(tab.tmplDir)
			tmpl, ok := templates.Find(ts, tt.template)
			if !ok {
				t.Fatalf("no %s template", tt.template)
			}
			req := NewRequest{
				Opts: agent.StartOpts{Name: "docs-plan", Project: "forge", Template: tt.template},
				Vars: tt.vars, Docs: sendDocs,
			}
			pumpForm(tab, tab.resolveTemplate(req, tmpl))
			if tab.dialog == nil {
				t.Fatal("no prompt dialog")
			}
			if v := ansi.Strip(tab.View()); !strings.Contains(v, "@docs/plan.md") {
				t.Fatalf("prompt without mentions:\n%s", v)
			}
			d := tab.dialog
			tab.dialog = nil
			pump(tab, d.done())
			starts, _, _ := svc.snapshot()
			if len(starts) != 1 {
				t.Fatalf("starts = %+v", starts)
			}
			o := starts[0]
			if !strings.Contains(o.Prompt, "@docs/plan.md @.forge/attached/ideas.md") {
				t.Errorf("prompt:\n%s", o.Prompt)
			}
			if len(o.Docs) != 2 || o.Docs[1].Rel != ".forge/attached/ideas.md" {
				t.Errorf("docs = %+v", o.Docs)
			}
		})
	}
}

func TestDocsPrompt(t *testing.T) {
	plan := []docfs.Attachment{{Rel: "docs/a.md"}}
	withVar := templates.Template{Body: "see {docs}"}
	without := templates.Template{Body: "do it"}
	tests := []struct {
		name   string
		tmpl   templates.Template
		prompt string
		plan   []docfs.Attachment
		want   string
	}{
		{"no docs", without, "do it\n", nil, "do it\n"},
		{"template places them", withVar, "see @docs/a.md\n", plan, "see @docs/a.md\n"},
		{"appended", without, "do it\n", plan, "do it\n\nRead these docs first: @docs/a.md\n"},
		{"no prompt", templates.Template{}, "", plan, "Read these docs first: @docs/a.md\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DocsPrompt(tt.tmpl, tt.prompt, tt.plan); got != tt.want {
				t.Errorf("DocsPrompt = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDocsSessionName(t *testing.T) {
	tests := []struct {
		paths []string
		want  string
	}{
		{[]string{"/d/Release Plan.md"}, "docs-release-plan"},
		{nil, "docs"},
	}
	for _, tt := range tests {
		if got := docsSessionName(tt.paths); got != tt.want {
			t.Errorf("docsSessionName(%q) = %q, want %q", tt.paths, got, tt.want)
		}
	}
}
