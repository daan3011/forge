package sessions

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// pump runs cmd, feeds the tab's own messages back into it until nothing
// is left, and returns the toasts and any other messages.
func pump(tab *Tab, cmd tea.Cmd) (toasts []string, other []tea.Msg) {
	queue := drain(cmd)
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		switch m := msg.(type) {
		case nil:
		case ui.SuccessMsg:
			toasts = append(toasts, "ok: "+m.Text)
		case ui.InfoMsg:
			toasts = append(toasts, "info: "+m.Text)
		case ui.ErrMsg:
			toasts = append(toasts, "err: "+m.Err.Error())
		case tabs.TargetedMsg:
			_, next := tab.Update(m)
			queue = append(queue, drain(next)...)
		default:
			other = append(other, msg)
		}
	}
	return toasts, other
}

// attachTab is a loaded, visible tab whose preview never schedules ticks.
func attachTab(t *testing.T, svc *fakeSvc) *Tab {
	t.Helper()
	tab := newTab(svc)
	tab.tick = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }
	pump(tab, tab.Refresh())
	pump(tab, tab.SetVisible(true))
	return tab
}

func TestEnterOnLiveSessionAttaches(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusWorking))
	svc.plans["alpha"] = agent.ResumePlan{Alive: true, HasTranscript: true}
	tab := attachTab(t, svc)

	_, cmd := tab.Update(press("enter"))
	toasts, _ := pump(tab, cmd)
	_, resumes, attaches := svc.attachCalls()
	if len(resumes) != 0 || !slices.Equal(attaches, []string{"alpha"}) {
		t.Errorf("resumes = %+v, attaches = %v", resumes, attaches)
	}
	if len(toasts) != 0 {
		t.Errorf("toasts = %q", toasts)
	}
	if tab.busy != 0 || len(tab.resuming) != 0 {
		t.Errorf("busy = %d, resuming = %v", tab.busy, tab.resuming)
	}
}

func TestEnterOnDeadSessionResumesThenAttaches(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusDead))
	svc.plans["alpha"] = agent.ResumePlan{HasTranscript: true}
	tab := attachTab(t, svc)

	_, cmd := tab.Update(press("enter"))
	toasts, _ := pump(tab, cmd)
	_, resumes, attaches := svc.attachCalls()
	if len(resumes) != 1 || resumes[0] != (agent.ResumeOpts{}) || !slices.Equal(attaches, []string{"alpha"}) {
		t.Errorf("resumes = %+v, attaches = %v", resumes, attaches)
	}
	if len(toasts) != 0 {
		t.Errorf("toasts = %q", toasts)
	}
	if r, _ := tab.current(); r.Status != store.StatusStarting {
		t.Errorf("status after resume = %s, want the refreshed row", r.Status)
	}
}

func TestBackgroundResumeNeverAttaches(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusDead))
	svc.plans["alpha"] = agent.ResumePlan{HasTranscript: true}
	tab := attachTab(t, svc)

	_, cmd := tab.Update(press("b"))
	toasts, _ := pump(tab, cmd)
	_, resumes, attaches := svc.attachCalls()
	if len(resumes) != 1 || len(attaches) != 0 {
		t.Errorf("resumes = %+v, attaches = %v", resumes, attaches)
	}
	if !slices.Contains(toasts, "ok: resumed alpha in the background") {
		t.Errorf("toasts = %q", toasts)
	}

	// Again, now that it runs.
	_, cmd = tab.Update(press("b"))
	toasts, _ = pump(tab, cmd)
	if _, resumes, _ := svc.attachCalls(); len(resumes) != 1 || !slices.Contains(toasts, "info: alpha is already running") {
		t.Errorf("resumes = %+v, toasts = %q", resumes, toasts)
	}
}

func TestDoubleEnterIsIgnored(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusDead))
	svc.plans["alpha"] = agent.ResumePlan{HasTranscript: true}
	tab := attachTab(t, svc)

	_, first := tab.Update(press("enter"))
	_, second := tab.Update(press("enter"))
	if second != nil {
		t.Error("second enter started another resume")
	}
	if !tab.Fetching() {
		t.Error("tab not busy while resuming")
	}
	pump(tab, first)
	if inspects, _, _ := svc.attachCalls(); len(inspects) != 1 {
		t.Errorf("inspects = %v", inspects)
	}
	if tab.Fetching() {
		t.Error("still busy after the attach")
	}
}

func TestEnterOnArchivedExplains(t *testing.T) {
	svc := newFakeSvc(row("old", store.StatusArchived))
	tab := attachTab(t, svc)
	pump(tab, tab.toggleArchive())
	_, cmd := tab.Update(press("enter"))
	toasts, _ := pump(tab, cmd)
	if len(toasts) != 1 || !strings.Contains(toasts[0], "is archived — restore it (A") {
		t.Errorf("toasts = %q", toasts)
	}
	if inspects, _, _ := svc.attachCalls(); len(inspects) != 0 {
		t.Errorf("inspected an archived session: %v", inspects)
	}
}

func TestResumeNotes(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusDead))
	svc.plans["alpha"] = agent.ResumePlan{} // no transcript, no prompt file
	tab := attachTab(t, svc)
	_, cmd := tab.Update(press("b"))
	toasts, _ := pump(tab, cmd)
	if !slices.ContainsFunc(toasts, func(s string) bool { return strings.Contains(s, "no conversation to resume: started afresh") }) {
		t.Errorf("toasts = %q", toasts)
	}
}

func TestResumeError(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusDead))
	svc.plans["alpha"] = agent.ResumePlan{HasTranscript: true}
	svc.resumeErr = errors.New("tmux exploded")
	tab := attachTab(t, svc)
	_, cmd := tab.Update(press("enter"))
	toasts, _ := pump(tab, cmd)
	if !slices.Contains(toasts, "err: resume alpha: tmux exploded") {
		t.Errorf("toasts = %q", toasts)
	}
	if _, _, attaches := svc.attachCalls(); len(attaches) != 0 || tab.Fetching() {
		t.Errorf("attaches = %v, busy = %v", attaches, tab.Fetching())
	}
}

func TestAttachModes(t *testing.T) {
	t.Run("exec hands the terminal over", func(t *testing.T) {
		svc := newFakeSvc(row("alpha", store.StatusWorking))
		svc.plans["alpha"] = agent.ResumePlan{Alive: true}
		svc.attachAs = tmux.Attachment{Mode: tmux.AttachExec, Cmd: exec.CommandContext(t.Context(), "true")}
		tab := attachTab(t, svc)
		_, cmd := tab.Update(press("enter"))
		_, other := pump(tab, cmd)
		if len(other) == 0 {
			t.Fatal("no exec command for the program to run")
		}
	})
	t.Run("popup runs in the background and reports the detach", func(t *testing.T) {
		svc := newFakeSvc(row("alpha", store.StatusWorking))
		svc.plans["alpha"] = agent.ResumePlan{Alive: true}
		svc.attachAs = tmux.Attachment{Mode: tmux.AttachPopup, Cmd: exec.CommandContext(t.Context(), "true"), Warning: "M-d is bound"}
		tab := attachTab(t, svc)
		before := svc.previews
		_, cmd := tab.Update(press("enter"))
		toasts, _ := pump(tab, cmd)
		if !slices.Contains(toasts, "info: M-d is bound") {
			t.Errorf("toasts = %q", toasts)
		}
		if svc.previews <= before {
			t.Error("preview not refreshed after the popup closed")
		}
	})
}

func TestDetachRefreshesStatusAndPreview(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusWorking))
	svc.output["alpha"] = "before"
	tab := attachTab(t, svc)
	svc.setStatus("alpha", store.StatusIdle)
	svc.mu.Lock()
	svc.output["alpha"] = "after"
	svc.mu.Unlock()

	pump(tab, func() tea.Msg { return detachedMsg{name: "alpha"} })
	if r, _ := tab.current(); r.Status != store.StatusIdle {
		t.Errorf("status = %s, want idle", r.Status)
	}
	if tab.preview != "after" {
		t.Errorf("preview = %q", tab.preview)
	}
}

func TestAttachSession(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusWorking), row("beta", store.StatusDead))
	svc.plans["beta"] = agent.ResumePlan{HasTranscript: true}
	tab := attachTab(t, svc)
	toasts, _ := pump(tab, tab.AttachSession("beta"))
	if r, _ := tab.current(); r.Name != "beta" {
		t.Errorf("selected %s", r.Name)
	}
	if _, resumes, attaches := svc.attachCalls(); len(resumes) != 1 || !slices.Equal(attaches, []string{"beta"}) {
		t.Errorf("resumes = %+v attaches = %v toasts = %q", resumes, attaches, toasts)
	}
	toasts, _ = pump(tab, tab.AttachSession("nope"))
	if len(toasts) != 1 || !strings.Contains(toasts[0], "isn't in the list") {
		t.Errorf("toasts = %q", toasts)
	}
}

func TestEntitiesSearchLinks(t *testing.T) {
	r := row("alpha", store.StatusIdle)
	r.Link, r.Links = "#12", []string{"daan/forge#12", "#12", "PLANE-7"}
	tab := attachTab(t, newFakeSvc(r))
	es := tab.Entities()
	if len(es) != 1 || !strings.Contains(es[0].Search, "PLANE-7") || !strings.Contains(es[0].Search, "#12") ||
		!strings.Contains(es[0].Detail, "#12") {
		t.Errorf("entities = %+v", es)
	}
}

// The dialogs need real key handling, so these run under teatest.

func TestMissingWorktreeDialog(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusDead))
	svc.plans["alpha"] = agent.ResumePlan{WorktreeMissing: true, HasTranscript: true}
	sc := newScreen(t, &host{tab: newTab(svc)})
	tm := sc.tm
	sc.wait(t, "alpha")
	tm.Send(press("enter"))
	sc.wait(t, "The worktree of alpha is gone", "Recreate it from agent/alpha?")
	tm.Send(press("enter")) // default: recreate
	sc.toast(t, "info: alpha: worktree recreated")
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	_, resumes, attaches := svc.attachCalls()
	if len(resumes) != 1 || !resumes[0].RecreateWorktree || !slices.Equal(attaches, []string{"alpha"}) {
		t.Errorf("resumes = %+v attaches = %v", resumes, attaches)
	}
}

func TestMissingWorktreeDialogCancel(t *testing.T) {
	svc := newFakeSvc(row("alpha", store.StatusDead))
	svc.plans["alpha"] = agent.ResumePlan{WorktreeMissing: true}
	tab := newTab(svc)
	sc := newScreen(t, &host{tab: tab})
	tm := sc.tm
	sc.wait(t, "alpha")
	tm.Send(press("enter"))
	sc.wait(t, "The worktree of alpha is gone")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	sc.wait(t, "agent/alpha (from main)") // back on the list
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	h := tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(*host)
	if _, resumes, _ := svc.attachCalls(); len(resumes) != 0 {
		t.Errorf("resumed after cancel: %+v", resumes)
	}
	if h.tab.Fetching() {
		t.Error("still busy after cancel")
	}
}

func TestResendPromptDialog(t *testing.T) {
	for _, tt := range []struct {
		name string
		keys []tea.KeyPressMsg
		want bool
	}{
		{"default sends it", []tea.KeyPressMsg{press("enter")}, true},
		{"start empty", []tea.KeyPressMsg{press("n")}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := newFakeSvc(row("alpha", store.StatusDead))
			svc.plans["alpha"] = agent.ResumePlan{PromptFile: "/data/sessions/alpha/prompt.md"}
			sc := newScreen(t, &host{tab: newTab(svc)})
			tm := sc.tm
			sc.wait(t, "alpha")
			tm.Send(press("b"))
			sc.wait(t, "alpha has no conversation to resume. Send its first prompt again?")
			for _, k := range tt.keys {
				tm.Send(k)
			}
			sc.toast(t, "ok: resumed alpha in the background")
			tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
			if _, resumes, _ := svc.attachCalls(); len(resumes) != 1 || resumes[0].ResendPrompt != tt.want {
				t.Errorf("resumes = %+v", resumes)
			}
		})
	}
}
