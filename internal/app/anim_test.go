package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// env returns a getenv with only the given variables set.
func env(kv ...string) func(string) string {
	vars := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		vars[kv[i]] = kv[i+1]
	}
	return func(k string) string { return vars[k] }
}

func animModel(t *testing.T, level config.Animations, getenv func(string) string, opts ...Option) Model {
	t.Helper()
	cfg := config.Default()
	cfg.UI.Animations = level
	return mustNew(t, cfg, append([]Option{withGetenv(getenv)}, opts...)...)
}

func update(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

// step advances the model by one animation frame, as the real clock would.
// It waits one frame interval.
func step(m Model) Model {
	m.anim.Sync(false)
	tick := m.anim.Sync(true)
	if tick == nil {
		return m
	}
	return update(m, tick())
}

func TestSplashPlaysOnLaunch(t *testing.T) {
	m := animModel(t, config.AnimationsFull, env(), WithSplash())
	m = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.splash == nil || !m.anim.Ticking() {
		t.Fatalf("splash %v, ticking %v", m.splash, m.anim.Ticking())
	}
	view := ansi.Strip(m.render())
	if !strings.Contains(view, "┏━╸┏━┓") || strings.Contains(view, "Pipelines") {
		t.Fatalf("splash not shown:\n%s", view)
	}

	// Any key skips it and is not passed on: "2" does not switch tabs.
	m = update(m, press("2"))
	if m.splash != nil || m.active != 0 {
		t.Fatalf("splash %v, active %d", m.splash, m.active)
	}
	if m.anim.Ticking() {
		t.Fatal("clock kept running after the splash")
	}
	// A resize later does not replay it.
	if m = update(m, tea.WindowSizeMsg{Width: 100, Height: 30}); m.splash != nil {
		t.Fatal("splash replayed on resize")
	}
}

func TestSplashEndsByItself(t *testing.T) {
	m := animModel(t, config.AnimationsFull, env(), WithSplash())
	m = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	for range anim.SplashLength {
		m = step(m)
	}
	if m.splash != nil || m.anim.Ticking() {
		t.Fatalf("after %d frames: splash %v, ticking %v", anim.SplashLength, m.splash, m.anim.Ticking())
	}
}

func TestSplashSkipped(t *testing.T) {
	tests := []struct {
		name   string
		level  config.Animations
		getenv func(string) string
		w, h   int
	}{
		{"too narrow", config.AnimationsFull, env(), 59, 24},
		{"too short", config.AnimationsFull, env(), 80, 15},
		{"reduced", config.AnimationsReduced, env(), 80, 24},
		{"off", config.AnimationsOff, env(), 80, 24},
		{"FORGE_NO_ANIM", config.AnimationsFull, env(anim.EnvNoAnim, "1"), 80, 24},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := animModel(t, tt.level, tt.getenv, WithSplash())
			if m = update(m, tea.WindowSizeMsg{Width: tt.w, Height: tt.h}); m.splash != nil {
				t.Fatal("splash shown")
			}
		})
	}
	// Without WithSplash (tests, embedding) there is never a splash.
	if m := update(animModel(t, config.AnimationsFull, env()), tea.WindowSizeMsg{Width: 80, Height: 24}); m.splash != nil {
		t.Fatal("splash shown without WithSplash")
	}
}

// TestIdleDoesNotTick is the "CPU stays near idle" check: with nothing
// moving on screen the animation clock is stopped.
func TestIdleDoesNotTick(t *testing.T) {
	m := animModel(t, config.AnimationsFull, env())
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 30})
	for _, k := range []string{"2", "j", "k", "3"} {
		if m = update(m, press(k)); m.anim.Ticking() {
			t.Fatalf("clock ticking after %q with nothing animating", k)
		}
	}
}

func TestNoAnimNeverTicks(t *testing.T) {
	for _, tt := range []struct {
		name   string
		level  config.Animations
		getenv func(string) string
	}{
		{"off", config.AnimationsOff, env()},
		{"FORGE_NO_ANIM", config.AnimationsFull, env(anim.EnvNoAnim, "yes")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := animModel(t, tt.level, tt.getenv, withFetchDelay(0))
			m = update(m, tea.WindowSizeMsg{Width: 120, Height: 30})
			m = update(m, press("r")) // the visible tab starts fetching
			m = update(m, ui.SuccessMsg{Text: "merged"})
			m = update(m, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
			m = update(m, press(">"))
			if m.anim.Ticking() || len(m.bursts) > 0 {
				t.Fatalf("ticking %v, bursts %d", m.anim.Ticking(), len(m.bursts))
			}
			if view := ansi.Strip(m.render()); !strings.Contains(view, "✦ merged") {
				t.Fatalf("static success mark missing:\n%s", view)
			}
		})
	}
}

func TestSuccessBurst(t *testing.T) {
	m := animModel(t, config.AnimationsFull, env())
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = update(m, ui.SuccessMsg{Text: "PR merged"})
	if len(m.bursts) != 1 || !m.anim.Ticking() {
		t.Fatalf("bursts %d, ticking %v", len(m.bursts), m.anim.Ticking())
	}
	if view := ansi.Strip(m.render()); !strings.Contains(view, "✦ PR merged") || !strings.ContainsAny(view, "✦*·") {
		t.Fatalf("toast or sparks missing:\n%s", view)
	}
	for range anim.BurstDuration {
		m = step(m)
	}
	if len(m.bursts) != 0 || m.anim.Ticking() {
		t.Fatalf("after the burst: bursts %d, ticking %v", len(m.bursts), m.anim.Ticking())
	}
	if m.toast.Empty() {
		t.Fatal("toast went with the burst")
	}
}

func TestPaletteSlidesIn(t *testing.T) {
	m := animModel(t, config.AnimationsFull, env())
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = update(m, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if m.overlay != overlayCommands || !m.anim.Ticking() {
		t.Fatalf("overlay %v, ticking %v", m.overlay, m.anim.Ticking())
	}
	start, _ := m.slideIn.At(m.anim.Frame())
	if start != -paletteDrop {
		t.Fatalf("slide starts at %v", start)
	}
	for i := 0; m.anim.Ticking(); i++ {
		if i > anim.FPS*2 {
			t.Fatal("palette never settled")
		}
		m = step(m)
	}
	if pos, _ := m.slideIn.At(m.anim.Frame()); pos != 0 {
		t.Fatalf("palette rests at offset %v", pos)
	}
}
