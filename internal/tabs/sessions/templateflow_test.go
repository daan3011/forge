package sessions

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/templates"
	"github.com/daan3011/forge/internal/ui"
)

// newTemplateTab is newTab with the default templates in a temp folder and
// the given providers.
func newTemplateTab(t *testing.T, svc agent.API, reg *templates.Registry) *Tab {
	t.Helper()
	tab := New(tabkit.Deps{
		Theme: ui.NewTheme(true, nil), Keys: ui.DefaultKeyMap(), Config: testConfig(), Sessions: svc,
		TemplatesDir: filepath.Join(t.TempDir(), "templates"), Providers: reg,
	})
	tab.SetProject(&tab.cfg.Projects[0])
	tab.SetSize(120, 30)
	return tab
}

// fakePlane stands in for the P3.1 plane_issue provider.
func fakePlane(calls *[]string) *templates.Registry {
	reg := templates.NewRegistry()
	reg.Register(store.KindPlaneIssue, templates.ProviderFunc(func(_ context.Context, ref string) (map[string]string, error) {
		*calls = append(*calls, ref)
		if ref != "PROJ-123" {
			return nil, errors.New("not found")
		}
		return map[string]string{
			"issue_title": "Login button does nothing",
			"issue_body":  "Clicking Login on Safari has no effect.",
		}, nil
	}))
	return reg
}

// pickImplementIssue fills the main form: name, the active project, main,
// and the implement-issue template (4th entry after "(none)", explore,
// fix-ci).
func pickImplementIssue(t *testing.T, sc *screen, name string) {
	t.Helper()
	tm := sc.tm
	tm.Send(press("n"))
	sc.wait(t, "New session", "Template", "write-tests")
	tm.Type(name)
	tm.Send(press("enter")) // name
	tm.Send(press("enter")) // project
	tm.Send(press("enter")) // base
	time.Sleep(200 * time.Millisecond)
	for range 3 {
		tm.Send(press("down"))
	}
	// The preview fills what forge knows and leaves the rest as {var}.
	sc.wait(t, "preview", "Implement an issue end to end", "Implement issue {issue}: {issue_title}", "branch agent/"+name)
	tm.Send(press("enter")) // template: the form completes (prompt group hidden)
}

func TestImplementIssueFillsPromptFromProvider(t *testing.T) {
	var calls []string
	svc := newFakeSvc()
	sc := newScreen(t, &host{tab: newTemplateTab(t, svc, fakePlane(&calls))})
	tm := sc.tm
	sc.wait(t, "no sessions yet")

	pickImplementIssue(t, sc, "login")
	sc.wait(t, "Issue", "PROJ-123") // the refs form; PROJ-123 is the example
	time.Sleep(200 * time.Millisecond)
	tm.Type("PROJ-123")
	tm.Send(press("enter"))
	// No variables are missing, so the rendered prompt comes next.
	sc.wait(t, "First prompt", "Implement issue PROJ-123: Login button does nothing")
	time.Sleep(200 * time.Millisecond)
	tm.Send(press("enter"))
	sc.toast(t, "ok: session login started")

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	starts, _, _ := svc.snapshot()
	if len(starts) != 1 {
		t.Fatalf("starts = %+v", starts)
	}
	got := starts[0]
	if got.Template != "implement-issue" || got.Name != "login" || got.Project != "forge" {
		t.Errorf("start = %+v", got)
	}
	for _, want := range []string{
		"Implement issue PROJ-123: Login button does nothing",
		"Clicking Login on Safari has no effect.",
		"You are working in daan/forge on branch agent/login. Project docs are in /code/forge/.forge/docs.",
	} {
		if !strings.Contains(got.Prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, got.Prompt)
		}
	}
	if strings.Contains(got.Prompt, "{") {
		t.Errorf("unfilled variable in prompt:\n%s", got.Prompt)
	}
	if len(calls) != 1 || calls[0] != "PROJ-123" {
		t.Errorf("provider calls = %v", calls)
	}
}

func TestTemplateWithoutProviderAsksForVariables(t *testing.T) {
	svc := newFakeSvc()
	sc := newScreen(t, &host{tab: newTemplateTab(t, svc, nil)})
	tm := sc.tm
	sc.wait(t, "no sessions yet")

	pickImplementIssue(t, sc, "manual")
	sc.wait(t, "Issue")
	time.Sleep(200 * time.Millisecond)
	tm.Type("PROJ-9")
	tm.Send(press("enter"))
	sc.wait(t, "{issue_title}", "{issue_body}") // the variables form
	time.Sleep(200 * time.Millisecond)
	tm.Type("Typed title")
	tm.Send(press("enter"))
	time.Sleep(200 * time.Millisecond)
	tm.Type("Typed body")
	tm.Send(press("enter"))
	sc.wait(t, "First prompt", "Implement issue PROJ-9: Typed title")
	time.Sleep(200 * time.Millisecond)
	tm.Send(press("enter"))
	sc.toast(t, "ok: session manual started")

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	starts, _, _ := svc.snapshot()
	if len(starts) != 1 || !strings.Contains(starts[0].Prompt, "Typed title\n\nTyped body") {
		t.Fatalf("starts = %+v", starts)
	}
}

func TestTemplateFlowCancels(t *testing.T) {
	svc := newFakeSvc()
	sc := newScreen(t, &host{tab: newTemplateTab(t, svc, nil)})
	tm := sc.tm
	sc.wait(t, "no sessions yet")
	pickImplementIssue(t, sc, "gone")
	sc.wait(t, "Issue")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	sc.wait(t, "no sessions yet")
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	if starts, _, _ := svc.snapshot(); len(starts) != 0 {
		t.Fatalf("started: %+v", starts)
	}
}

func TestRefsFormSkipsGivenRefs(t *testing.T) {
	theme, keys := ui.NewTheme(true, nil), ui.DefaultKeyMap()
	impl, _ := templates.Find(templates.Defaults(), "implement-issue")
	given := map[store.LinkKind]string{store.KindPlaneIssue: "PROJ-1"}
	f, get := RefsForm(theme, keys, impl, given)
	if f != nil {
		t.Error("asked for a ref that was given")
	}
	if got := get(); got[store.KindPlaneIssue] != "PROJ-1" {
		t.Errorf("refs = %v", got)
	}
	explore, _ := templates.Find(templates.Defaults(), "explore")
	if f, _ := RefsForm(theme, keys, explore, nil); f != nil {
		t.Error("explore needs no refs")
	}
	if f, _ := VarsForm(theme, keys, explore, nil); f != nil {
		t.Error("nothing missing, yet a vars form")
	}
}
