package demo

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/app"
	"github.com/daan3011/forge/internal/config"
)

// pump feeds the messages of cmd (and of every command they lead to) back
// into m until nothing arrives for a while. Commands run concurrently, so
// a long tick (auto refresh) never blocks it; it just never answers.
type pump struct {
	t    *testing.T
	m    tea.Model
	msgs chan tea.Msg
}

func newPump(t *testing.T, m tea.Model) *pump {
	return &pump{t: t, m: m, msgs: make(chan tea.Msg, 256)}
}

func (p *pump) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		if msg := cmd(); msg != nil {
			p.msgs <- msg
		}
	}()
}

func (p *pump) send(msg tea.Msg) {
	if b, ok := msg.(tea.BatchMsg); ok {
		for _, c := range b {
			p.run(c)
		}
		return
	}
	// tea.Sequence's message is an unexported []tea.Cmd.
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeFor[tea.Cmd]() {
		for i := range v.Len() {
			p.run(v.Index(i).Interface().(tea.Cmd))
		}
		return
	}
	if _, ok := msg.(tea.QuitMsg); ok {
		return
	}
	var cmd tea.Cmd
	p.m, cmd = p.m.Update(msg)
	p.run(cmd)
}

// settle handles messages until none came for quiet, at most for max.
func (p *pump) settle(quiet, maxWait time.Duration) {
	deadline := time.After(maxWait)
	for {
		select {
		case msg := <-p.msgs:
			p.send(msg)
		case <-time.After(quiet):
			return
		case <-deadline:
			return
		}
	}
}

func (p *pump) press(s string) {
	k := tea.KeyPressMsg{Code: rune(s[0]), Text: s}
	switch s {
	case "esc":
		k = tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		k = tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	p.send(k)
	p.settle(150*time.Millisecond, 3*time.Second)
}

func (p *pump) screen() string { return p.m.View().Content }

// TestDemoTabs renders every tab of the demo at 80x24 and 120x40: the
// screen is exactly the terminal's size, the status bar is there and each
// tab shows its fixture data (the P6.4 content check on a configured
// forge, at the smallest common terminal).
func TestDemoTabs(t *testing.T) {
	dir := t.TempDir()
	for k, v := range Env(dir) {
		t.Setenv(k, v)
	}
	cfg, opts, closeDB, err := New(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeDB() })
	cfg.UI.Animations = config.AnimationsOff

	want := []struct {
		key, title string
		texts      []string
	}{
		{"1", "Home", []string{"bellows-stream"}},
		{"2", "PRs", []string{"Tempering: retry flaky", "My PRs"}},
		{"3", "Pipelines", []string{"quench-retry", "forge/57"}},
		{"4", "Plane", []string{"ANV-41"}},
		{"5", "Sessions", []string{"quench-retry", "bellows-stream", "airflow-presets"}},
		{"6", "Docs", []string{"architecture.md"}},
		{"7", "Artifacts", []string{"heat-map.png"}},
		{"8", "Context", []string{"CLAUDE.md"}},
	}
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		w, h := size[0], size[1]
		m, err := app.New(cfg, opts...)
		if err != nil {
			t.Fatal(err)
		}
		p := newPump(t, m)
		p.send(tea.WindowSizeMsg{Width: w, Height: h})
		p.run(m.Init())
		p.settle(300*time.Millisecond, 5*time.Second)
		for _, tab := range want {
			p.press(tab.key)
			view := p.screen()
			lines := strings.Split(view, "\n")
			if len(lines) != h {
				t.Fatalf("%dx%d %s: %d lines", w, h, tab.title, len(lines))
			}
			for i, l := range lines {
				if lw := lipgloss.Width(l); lw != w {
					t.Fatalf("%dx%d %s: line %d is %d wide", w, h, tab.title, i, lw)
				}
			}
			plain := ansi.Strip(view)
			if status := lines[h-1]; !strings.Contains(ansi.Strip(status), tab.title) {
				t.Errorf("%dx%d %s: status bar lacks the tab: %q", w, h, tab.title, ansi.Strip(status))
			}
			for _, s := range tab.texts {
				if !strings.Contains(plain, s) {
					t.Errorf("%dx%d %s lacks %q:\n%s", w, h, tab.title, s, plain)
				}
			}
			for _, leak := range []string{"heating up", "couldn't", "isn't available", "isn't set up"} {
				if strings.Contains(plain, leak) {
					t.Errorf("%dx%d %s shows %q:\n%s", w, h, tab.title, leak, plain)
				}
			}
		}
	}
}
