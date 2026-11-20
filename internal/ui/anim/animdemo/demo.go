// Package animdemo is the `forge demo anim` showcase: every animation in
// internal/ui/anim side by side, with the level switchable live.
package animdemo

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// Demo loop timings, in frames.
const (
	splashPause = 12 // between splash replays
	burstEvery  = 18
	springEvery = 24
	sweepFrames = 48 // heat bar 0 → 100 %
)

// Cell sizes (inner, borders excluded).
const (
	splashW, splashH = anim.SplashMinWidth, anim.SplashMinHeight
	sideW            = 30
	forgeH           = 7
)

// Model is the demo program.
type Model struct {
	theme *ui.Theme
	keys  *ui.KeyMap
	eng   *anim.Engine
	color bool // NO_COLOR is fixed for the run; levels switch live

	splash    anim.Splash
	burst     anim.Burst
	nextBurst int
	spring    anim.Spring
	nextFlip  int
}

// New returns the demo at the given settings.
func New(s anim.Settings) Model {
	m := Model{
		theme: ui.NewTheme(true, nil),
		keys:  ui.DefaultKeyMap(),
		eng:   anim.NewEngine(s),
		color: s.Color,
	}
	m.spring = anim.SpringAt(0)
	return m
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.eng.Sync(true))
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.eng.Update(msg) {
		m.loop()
		return m, m.eng.Sync(true)
	}
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		*m.theme = *ui.NewTheme(msg.IsDark(), nil)
	case tea.KeyPressMsg:
		d := m.keys.Demo
		f := m.eng.Frame()
		switch {
		case key.Matches(msg, d.Quit):
			return m, tea.Quit
		case key.Matches(msg, d.Replay):
			m.splash = anim.Splash{Start: f, Seed: uint64(f)}
		case key.Matches(msg, d.Burst):
			m.burst = anim.Burst{Start: f, Seed: uint64(f)}
		case key.Matches(msg, d.Full):
			m.setLevel(config.AnimationsFull)
		case key.Matches(msg, d.Reduced):
			m.setLevel(config.AnimationsReduced)
		case key.Matches(msg, d.Off):
			m.setLevel(config.AnimationsOff)
		}
	}
	return m, m.eng.Sync(true)
}

func (m *Model) setLevel(l config.Animations) {
	m.eng.SetSettings(anim.Settings{Level: l, Color: m.color})
}

// loop restarts the looping showcases on schedule (full only: in reduced
// and off nothing moves except colour pulses).
func (m *Model) loop() {
	if !m.eng.Settings().Motion() {
		return
	}
	f := m.eng.Frame()
	if f-m.splash.Start >= anim.SplashLength+splashPause {
		m.splash = anim.Splash{Start: f, Seed: uint64(f)}
	}
	if f >= m.nextBurst {
		m.burst = anim.Burst{Start: f, Seed: uint64(f)}
		m.nextBurst = f + burstEvery
	}
	if f >= m.nextFlip {
		pos, _ := m.spring.At(f)
		to := 0.0
		if m.spring.To == 0 {
			to = 1
		}
		m.spring = anim.NewSpring(pos, to, f, m.eng.Settings())
		m.nextFlip = f + springEvery
	}
}

// View implements tea.Model.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.BackgroundColor = m.theme.Bg
	return v
}

func (m Model) render() string {
	th, s, f := m.theme, m.eng.Settings(), m.eng.Frame()

	var splash string
	if s.Motion() {
		splash = m.splash.View(f, splashW, splashH, th)
	} else {
		end := anim.Splash{Start: f - anim.SplashLength + 1}
		splash = end.View(f, splashW, splashH-1, th) + "\n" + th.Hint.Render("(no splash at this level: last frame shown)")
	}

	flames := make([]string, 0, len(anim.Statuses))
	for _, st := range anim.Statuses {
		flames = append(flames, anim.Flame(st, f, s, th)+" "+th.Body.Render(st.String()))
	}

	pct := 0.6
	if s.Motion() {
		pct = float64(f%(sweepFrames+1)) / sweepFrames
	}

	var burst string
	if s.Motion() {
		burst = m.burst.View(f, sideW, 5)
	} else {
		burst = th.Hint.Render("(toast shows a static " + strings.TrimSpace(ui.SuccessMark) + ")")
	}

	pos, _ := m.spring.At(f)
	slot := sideW - 2
	block := strings.Repeat(" ", int(pos*float64(slot-1)+0.5)) + ui.Tint(th.Accent, "██")

	side := lipgloss.JoinVertical(lipgloss.Left,
		m.box("HammerSpinner", anim.HammerFrame(f, s, th)+th.Body.Render(" fetching PRs…"), sideW, 1),
		m.box("HeatBar", anim.HeatBarLabel(pct, sideW, th), sideW, 1),
		m.box("SparkBurst", burst, sideW, 5),
		m.box("StatusFlame", strings.Join(flames, "\n"), sideW, len(flames)),
		m.box("Spring", block, sideW, 1),
	)
	left := lipgloss.JoinVertical(lipgloss.Left,
		m.box("Splash", splash, splashW, splashH),
		m.box("EmptyForge", anim.EmptyForge(splashW, forgeH, th), splashW, forgeH),
	)
	ticking := "no"
	if m.eng.Ticking() {
		ticking = "yes"
	}
	d := m.keys.Demo
	footer := th.DetailLabel.Render(fmt.Sprintf(" level %s · colour %v · frame %d · ticking %s   ", s.Level, s.Color, f, ticking)) +
		ui.HelpLine(th, []key.Binding{d.Replay, d.Burst, d.Full, d.Reduced, d.Off, d.Quit})
	return lipgloss.JoinVertical(lipgloss.Left, lipgloss.JoinHorizontal(lipgloss.Top, left, side), footer)
}

// box draws a titled, bordered cell with a w×h body.
func (m Model) box(title, body string, w, h int) string {
	content := m.theme.DetailTitle.Render(title) + "\n" + ui.Box(body, w, h)
	return m.theme.Pane.Render(content)
}
