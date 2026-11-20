package anim

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/ui"
)

// Regenerate goldens with: go test ./internal/ui/anim -update

var (
	full    = Settings{Level: config.AnimationsFull, Color: true}
	reduced = Settings{Level: config.AnimationsReduced, Color: true}
	off     = Settings{Level: config.AnimationsOff, Color: true}
	levels  = []Settings{full, reduced, off}
)

func theme() *ui.Theme { return ui.NewTheme(true, nil) }

// assertSize checks that out is exactly w×h cells.
func assertSize(t *testing.T, out string, w, h int) {
	t.Helper()
	lines := strings.Split(out, "\n")
	if len(lines) != h {
		t.Fatalf("got %d lines, want %d", len(lines), h)
	}
	for i, l := range lines {
		if got := ansi.StringWidth(l); got != w {
			t.Fatalf("line %d is %d wide, want %d: %q", i, got, w, ansi.Strip(l))
		}
	}
}

func TestSplashGolden(t *testing.T) {
	s := Splash{Start: 100, Seed: 42}
	for _, size := range [][2]int{{60, 16}, {80, 24}} {
		for _, f := range []int{0, 3, 6, 10} {
			t.Run(fmt.Sprintf("%dx%d/frame%02d", size[0], size[1], f), func(t *testing.T) {
				out := s.View(s.Start+f, size[0], size[1], theme())
				assertSize(t, out, size[0], size[1])
				golden.RequireEqual(t, out)
			})
		}
	}
}

func TestSplashTiming(t *testing.T) {
	s := Splash{Start: 5}
	if s.Done(5 + SplashLength - 1) {
		t.Fatal("done before the last frame")
	}
	if !s.Done(5 + SplashLength) {
		t.Fatal("not done after SplashLength frames")
	}
	if SplashLength*Interval >= 1e9 {
		t.Fatalf("splash lasts %v, want under 1s", SplashLength*Interval)
	}
	tests := []struct {
		w, h int
		want bool
	}{{60, 16, true}, {59, 16, false}, {60, 15, false}, {200, 60, true}}
	for _, tt := range tests {
		if got := SplashFits(tt.w, tt.h); got != tt.want {
			t.Errorf("SplashFits(%d, %d) = %v", tt.w, tt.h, got)
		}
	}
	// The wordmark heats up: the first and last frames differ in colour only.
	first, last := s.View(5, 60, 16, theme()), s.View(5+SplashLength-1, 60, 16, theme())
	if first == last {
		t.Fatal("wordmark did not change colour")
	}
}

func TestHammerGolden(t *testing.T) {
	for _, s := range levels {
		var frames []string
		for f := range 8 {
			frames = append(frames, HammerFrame(f, s, theme()))
		}
		t.Run(string(s.Level), func(t *testing.T) {
			golden.RequireEqual(t, strings.Join(frames, "\n"))
		})
	}
	for _, f := range Hammer.Frames {
		if w := ansi.StringWidth(f); w != 3 {
			t.Fatalf("bubbles frame %q is %d wide", f, w)
		}
	}
}

func TestHeatBarGolden(t *testing.T) {
	for _, pct := range []float64{0, 0.33, 0.5, 1} {
		t.Run(fmt.Sprintf("pct%03.0f", pct*100), func(t *testing.T) {
			out := HeatBar(pct, 20, theme())
			assertSize(t, out, 20, 1)
			golden.RequireEqual(t, out)
		})
	}
}

func TestHeatBarEdges(t *testing.T) {
	th := theme()
	tests := []struct {
		pct   float64
		width int
		fill  int // full blocks
	}{
		{-1, 10, 0}, {2, 10, 10}, {0.5, 10, 5}, {0.55, 10, 5}, {1, 1, 1},
	}
	for _, tt := range tests {
		out := ansi.Strip(HeatBar(tt.pct, tt.width, th))
		if got := strings.Count(out, "█"); got != tt.fill {
			t.Errorf("HeatBar(%v, %d) = %q: %d full blocks, want %d", tt.pct, tt.width, out, got, tt.fill)
		}
	}
	if HeatBar(0.5, 0, th) != "" {
		t.Fatal("zero width should render nothing")
	}
	if got := ansi.Strip(HeatBarLabel(0.6, 20, th)); !strings.HasSuffix(got, " 60%") || ansi.StringWidth(got) != 20 {
		t.Fatalf("label = %q", got)
	}
}

func TestBurstGolden(t *testing.T) {
	b := Burst{Start: 10, Seed: 7}
	for _, f := range []int{0, 2, 4} {
		t.Run(fmt.Sprintf("frame%d", f), func(t *testing.T) {
			out := b.View(b.Start+f, 15, 7)
			assertSize(t, out, 15, 7)
			golden.RequireEqual(t, out)
		})
	}
}

func TestBurstLifetime(t *testing.T) {
	b := Burst{Start: 10, Seed: 1}
	if len(b.Particles(9)) != 0 || len(b.Particles(10+BurstDuration)) != 0 {
		t.Fatal("particles outside the burst's lifetime")
	}
	if got := len(b.Particles(10)); got != burstSparks {
		t.Fatalf("%d sparks at start, want %d", got, burstSparks)
	}
	if b.Done(10+BurstDuration-1) || !b.Done(10+BurstDuration) {
		t.Fatal("Done boundary wrong")
	}
	if BurstDuration*Interval < 350e6 || BurstDuration*Interval > 450e6 {
		t.Fatalf("burst lasts %v, want ~400ms", BurstDuration*Interval)
	}
	skipAll := func(int, int) bool { return true }
	if len(b.Layers(10, 5, 5, 1, skipAll)) != 0 {
		t.Fatal("skip ignored")
	}
}

func TestFlameGolden(t *testing.T) {
	for _, s := range levels {
		t.Run(string(s.Level), func(t *testing.T) {
			var lines []string
			for _, st := range Statuses {
				var cells []string
				for _, f := range []int{0, 6, 12} {
					cell := Flame(st, f, s, theme())
					if w := ansi.StringWidth(cell); w != 1 {
						t.Fatalf("%s frame %d is %d wide", st, f, w)
					}
					cells = append(cells, cell)
				}
				lines = append(lines, st.String()+" "+strings.Join(cells, " "))
			}
			golden.RequireEqual(t, strings.Join(lines, "\n"))
		})
	}
}

func TestFlameAnimating(t *testing.T) {
	noColor := Settings{Level: config.AnimationsReduced, Color: false}
	tests := []struct {
		st   Status
		s    Settings
		want bool
	}{
		{StatusWorking, full, true},
		{StatusWorking, reduced, true},
		{StatusWorking, off, false},
		{StatusWorking, noColor, false},
		{StatusWaiting, full, true},
		{StatusWaiting, off, false},
		{StatusIdle, full, false},
		{StatusError, full, false},
		{StatusDead, full, false},
	}
	for _, tt := range tests {
		if got := FlameAnimating(tt.st, tt.s); got != tt.want {
			t.Errorf("FlameAnimating(%s, %+v) = %v", tt.st, tt.s, got)
		}
		// Whatever does not animate must look the same on every frame.
		if !tt.want && Flame(tt.st, 0, tt.s, theme()) != Flame(tt.st, 7, tt.s, theme()) {
			t.Errorf("%s at %+v changes but reports not animating", tt.st, tt.s)
		}
	}
}

func TestEmptyForgeGolden(t *testing.T) {
	for _, size := range [][2]int{{40, 8}, {20, 2}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			out := EmptyForge(size[0], size[1], theme())
			assertSize(t, out, size[0], size[1])
			if !strings.Contains(ansi.Strip(out), "nothing on the anvil") {
				t.Fatal("caption missing")
			}
			golden.RequireEqual(t, out)
		})
	}
	if EmptyForge(0, 5, theme()) != "" {
		t.Fatal("zero width should render nothing")
	}
}

func TestResolve(t *testing.T) {
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	tests := []struct {
		name  string
		level config.Animations
		env   func(string) string
		want  Settings
	}{
		{"full", config.AnimationsFull, env(), full},
		{"reduced", config.AnimationsReduced, env(), reduced},
		{"unset defaults to full", "", env(), full},
		{"FORGE_NO_ANIM", config.AnimationsFull, env(EnvNoAnim, "1"), off},
		{"NO_COLOR", config.AnimationsFull, env(EnvNoColor, "1"), Settings{Level: config.AnimationsFull}},
		{"both", config.AnimationsReduced, env(EnvNoColor, "x", EnvNoAnim, "x"), Settings{Level: config.AnimationsOff}},
		{"nil env", config.AnimationsOff, nil, off},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Resolve(tt.level, tt.env); got != tt.want {
				t.Fatalf("Resolve = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSpring(t *testing.T) {
	sp := NewSpring(0, 10, 100, full)
	if pos, settled := sp.At(100); pos != 0 || settled {
		t.Fatalf("at start: %v %v", pos, settled)
	}
	prev := 0.0
	settledAt := -1
	for f := 101; f < 100+springMaxSteps+2; f++ {
		pos, settled := sp.At(f)
		if settled {
			if pos != 10 {
				t.Fatalf("settled at %v", pos)
			}
			settledAt = f - 100
			break
		}
		if f < 104 && pos <= prev {
			t.Fatalf("frame %d: %v did not move towards 10 from %v", f, pos, prev)
		}
		prev = pos
	}
	if settledAt < 0 || settledAt > FPS {
		t.Fatalf("settled after %d frames, want within a second", settledAt)
	}
	if a, _ := sp.At(104); a != func() float64 { b, _ := sp.At(104); return b }() {
		t.Fatal("At is not pure")
	}
	for _, s := range []Settings{reduced, off} {
		if pos, settled := NewSpring(0, 10, 0, s).At(0); pos != 10 || !settled {
			t.Fatalf("%s spring did not snap: %v %v", s.Level, pos, settled)
		}
	}
	if pos, settled := SpringAt(3).At(0); pos != 3 || !settled {
		t.Fatal("SpringAt not at rest")
	}
}

func TestEngine(t *testing.T) {
	t.Run("off never ticks", func(t *testing.T) {
		e := NewEngine(off)
		if e.Sync(true) != nil || e.Ticking() {
			t.Fatal("off engine scheduled a tick")
		}
	})
	t.Run("one tick at a time", func(t *testing.T) {
		e := NewEngine(full)
		cmd := e.Sync(true)
		if cmd == nil || e.Sync(true) != nil {
			t.Fatal("want exactly one tick in flight")
		}
		msg := cmd()
		if !e.Update(msg) || e.Frame() != 1 || e.Ticking() {
			t.Fatalf("frame %d ticking %v", e.Frame(), e.Ticking())
		}
		if !e.Update(msg) || e.Frame() != 1 {
			t.Fatal("a replayed tick must be swallowed without advancing")
		}
	})
	t.Run("stop drops the tick in flight", func(t *testing.T) {
		e := NewEngine(full)
		stale := e.Sync(true)()
		if e.Sync(false) != nil || e.Ticking() {
			t.Fatal("Sync(false) left a tick pending")
		}
		next := e.Sync(true)
		if !e.Update(stale) || e.Frame() != 0 {
			t.Fatal("stale tick advanced the frame")
		}
		if !e.Update(next()) || e.Frame() != 1 {
			t.Fatal("current tick dropped")
		}
	})
	t.Run("other engines' ticks", func(t *testing.T) {
		a, b := NewEngine(full), NewEngine(full)
		if b.Update(a.Sync(true)()) {
			t.Fatal("handled another engine's tick")
		}
		if b.Update("not a frame") {
			t.Fatal("handled a foreign message")
		}
	})
	t.Run("reduced keeps frame timings", func(t *testing.T) {
		e := NewEngine(reduced)
		e.Update(e.Sync(true)())
		if e.Frame() != reducedStep {
			t.Fatalf("frame = %d", e.Frame())
		}
	})
	t.Run("switching to off stops", func(t *testing.T) {
		e := NewEngine(full)
		tick := e.Sync(true)()
		e.SetSettings(off)
		if e.Sync(true) != nil {
			t.Fatal("ticked after switching off")
		}
		e.Update(tick)
		if e.Frame() != 0 {
			t.Fatal("in-flight tick advanced after switching off")
		}
	})
}
