package app

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/ui"
)

// capturePlane is fakePlane with the projects, labels and members quick
// capture resolves against.
type capturePlane struct{ fakePlane }

func (*capturePlane) Projects(context.Context) ([]plane.Project, error) {
	return []plane.Project{{ID: "p1", Identifier: "FORGE", Name: "Forge"}, {ID: "p2", Identifier: "ANV", Name: "Anvil"}}, nil
}

func (*capturePlane) Labels(_ context.Context, id string) ([]plane.Label, error) {
	if id != "p1" {
		return nil, nil
	}
	return []plane.Label{{ID: "l-bug", Name: "bug"}, {ID: "l-fe", Name: "frontend"}}, nil
}

func (*capturePlane) ProjectMembers(_ context.Context, id string) ([]plane.Member, error) {
	if id != "p1" {
		return nil, nil
	}
	return []plane.Member{
		{MemberID: "u-me", Member: &plane.User{ID: "u-me", DisplayName: "daan"}},
		{MemberID: "u-bob", Member: &plane.User{ID: "u-bob", DisplayName: "bob"}},
	}, nil
}

func ctrlN() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl} }

func typeLine(tm *teatest.TestModel, s string) {
	for _, r := range s {
		tm.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// TestQuickCapture is the P3.5 flow in the TUI: ctrl+n from any tab, a
// line with tokens, enter, and the item is created in the background.
func TestQuickCapture(t *testing.T) {
	api := &capturePlane{fakePlane{states: []plane.State{{ID: "s-todo", Name: "Todo", Group: plane.GroupUnstarted, IsDefault: true}}}}
	m, err := New(planeConfig(), withFetchDelay(0), WithGitHub(fakeGitHub{}), WithPlane(api, nil))
	if err != nil {
		t.Fatal(err)
	}
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(170, 40))
	var seen []byte
	wait := func(s string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
			if !bytes.Contains(append(slices.Clip(seen), b...), []byte(s)) {
				return false
			}
			seen = append(seen, b...)
			return true
		}, teatest.WithDuration(5*time.Second))
	}
	wait("forge")
	tm.Send(press("2")) // PRs: capture works from any tab
	tm.Send(ctrlN())
	wait("Quick capture")
	typeLine(tm, "fix login !high @me @bob +bug")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	wait("created FORGE-1")

	got := api.createCalls()
	if len(got) != 1 {
		t.Fatalf("creates = %+v", got)
	}
	c := got[0]
	if c.Name != "fix login" || c.Priority != plane.PriorityHigh ||
		!slices.Equal(c.AssigneeIDs, []string{"u-me", "u-bob"}) || !slices.Equal(c.LabelIDs, []string{"l-bug"}) {
		t.Errorf("create = %+v", c)
	}

	// shift+enter: the Plane tab's create form, prefilled.
	tm.Send(ctrlN())
	typeLine(tm, "prefilled title")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	wait("New issue")
	wait("prefilled title")
	if n := len(api.createCalls()); n != 1 {
		t.Errorf("shift+enter created an item (%d creates)", n)
	}

	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// loadedCaptureModel is the model with the capture overlay open and its
// catalog loaded (the load run synchronously).
func loadedCaptureModel(t *testing.T) Model {
	t.Helper()
	api := &capturePlane{}
	m := mustNew(t, planeConfig(), WithPlane(api, nil))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	next, _ = next.Update(ctrlN())
	m = next.(Model)
	if m.overlay != overlayCapture {
		t.Fatalf("overlay = %v, want capture", m.overlay)
	}
	next, _ = m.Update(m.loadCatalog(m.baseNeed(), "", false)())
	return next.(Model)
}

func sendKeys(m Model, keys ...tea.KeyPressMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	var next tea.Model = m
	for _, k := range keys {
		next, cmd = next.Update(k)
	}
	return next.(Model), cmd
}

func lineKeys(s string) []tea.KeyPressMsg {
	var out []tea.KeyPressMsg
	for _, r := range s {
		out = append(out, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return out
}

func TestQuickCaptureUnknownToken(t *testing.T) {
	m := loadedCaptureModel(t)
	m, _ = sendKeys(m, lineKeys("x +frontnd")...)
	m, cmd := sendKeys(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.overlay != overlayCapture {
		t.Fatal("an unknown label closed the overlay")
	}
	view := ansi.Strip(m.render())
	if !strings.Contains(view, `no label "frontnd" — did you mean frontend?`) {
		t.Fatalf("no hint:\n%s", view)
	}
	if cmd != nil {
		for _, msg := range runCmd(cmd) {
			if _, ok := msg.(captureCreatedMsg); ok {
				t.Fatal("created despite the unknown label")
			}
		}
	}
	m, _ = sendKeys(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if got := m.capture.Value(); got != "x +frontend" {
		t.Fatalf("after tab: %q", got)
	}
	m, cmd = sendKeys(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.overlay != overlayNone {
		t.Fatal("overlay still open after a valid enter")
	}
	created := false
	for _, msg := range runCmd(cmd) {
		if c, ok := msg.(captureCreatedMsg); ok {
			created = c.err == nil && c.key == "FORGE"
		}
	}
	if !created {
		t.Fatal("no create")
	}
}

func TestQuickCaptureCompletes(t *testing.T) {
	m := loadedCaptureModel(t)
	m, _ = sendKeys(m, lineKeys("x @b")...)
	if view := ansi.Strip(m.render()); !strings.Contains(view, "@bob") {
		t.Fatalf("no @bob suggestion:\n%s", view)
	}
	m, _ = sendKeys(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if got := m.capture.Value(); got != "x @bob " {
		t.Fatalf("after tab: %q", got)
	}
}

func TestQuickCaptureKey(t *testing.T) {
	t.Run("not while a tab takes text input", func(t *testing.T) {
		m := mustNew(t, planeConfig(), WithPlane(&capturePlane{}, nil))
		m, _ = sendKeys(m, press("/"), ctrlN())
		if m.overlay != overlayNone {
			t.Fatal("ctrl+n opened capture while filtering")
		}
	})
	t.Run("toggles", func(t *testing.T) {
		m := mustNew(t, planeConfig(), WithPlane(&capturePlane{}, nil))
		m, _ = sendKeys(m, ctrlN())
		if m.overlay != overlayCapture {
			t.Fatal("ctrl+n didn't open capture")
		}
		m, _ = sendKeys(m, ctrlN())
		if m.overlay != overlayNone {
			t.Fatal("ctrl+n didn't close capture")
		}
	})
	t.Run("without plane", func(t *testing.T) {
		m := mustNew(t, planeConfig())
		next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
		m, _ = sendKeys(next.(Model), ctrlN())
		if m.overlay != overlayNone {
			t.Fatal("capture opened without Plane")
		}
		if view := ansi.Strip(m.render()); !strings.Contains(view, "quick capture") {
			t.Fatalf("no error toast:\n%s", view)
		}
	})
	t.Run("status keys", func(t *testing.T) {
		m := loadedCaptureModel(t)
		keys := m.statusKeys(m.tabs[m.active])
		if !slices.ContainsFunc(keys, func(b key.Binding) bool { return b.Help().Desc == "full form" }) {
			t.Fatalf("status keys = %v", keys)
		}
	})
}

func TestNoticeShownOnce(t *testing.T) {
	fs := newFakeStore(nil)
	ns := []notice{{id: "tmux-config-v2", text: "forge's tmux config has updates"}}
	first := runCmd(showNotices(fs, ns))
	if len(first) != 1 {
		t.Fatalf("first start: %v", first)
	}
	if info, ok := first[0].(ui.InfoMsg); !ok || !strings.Contains(info.Text, "tmux config") {
		t.Fatalf("first start: %v", first)
	}
	if again := runCmd(showNotices(fs, ns)); len(again) != 0 {
		t.Fatalf("second start: %v", again)
	}
}
