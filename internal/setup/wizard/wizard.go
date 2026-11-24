// Package wizard is the full-screen guided setup (P6.6): the step list on
// the left (done / skipped / failed), the current step on the right. The
// step logic lives in internal/setup; this package only drives it. All I/O
// runs in tea.Cmds.
package wizard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/doctor"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/setup"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// Deps are the wizard's collaborators; tests pass fakes.
type Deps struct {
	// Config is the loaded config and ConfigPath its file.
	Config     *config.Config
	ConfigPath string
	// Merge writes config values (config.Merge when nil).
	Merge func(path string, sets ...config.Set) error
	// Load reloads the config after a merge (config.Load when nil).
	Load func(path string) (*config.Config, error)

	// Keys and Anim come from the config; nil Keys means the defaults.
	Keys *ui.KeyMap
	Anim anim.Settings

	// Doctor runs the named doctor checks.
	Doctor func(ctx context.Context, names ...string) []doctor.Check
	// GHLogin is `gh auth login` (run with tea.ExecProcess).
	GHLogin func() *exec.Cmd
	// Getenv reads the environment (Plane key, terminal detection, $SHELL).
	Getenv func(string) string
	// NewPlane builds a Plane client for a config and key.
	NewPlane func(config.Plane, string) (plane.API, error)

	// Store keeps step results and the setup_completed flag; Projects is
	// the remembered repos (Plane links).
	Store    setup.Store
	Projects setup.ProjectStore
	Cwd      string
	Home     string

	// TmuxConf is forge's tmux.conf; TmuxColors renders its default.
	TmuxConf   string
	TmuxColors tmux.ConfigColors
	// NewTmux builds a driver on a throwaway socket started with conf.
	NewTmux func(socket, conf string) tmux.Driver

	Clipboard clipboard.Copier
	Smoke     setup.SmokeRunner

	// Now is the clock (time.Now when nil); PID names throwaway sockets.
	Now func() time.Time
	// SmokePoll is how often the smoke test looks (setup.SmokePoll when 0).
	SmokePoll time.Duration
	PID       int
	// Only runs a single step (`forge setup <step>`): setup_completed is
	// left alone.
	Only setup.StepID
}

// Model is the wizard.
type Model struct {
	d     Deps
	cfg   *config.Config
	theme *ui.Theme
	keys  *ui.KeyMap
	eng   *anim.Engine
	split ui.SplitPane

	steps   []setup.Step
	models  map[setup.StepID]stepModel
	entered map[setup.StepID]bool
	results setup.Results

	cur       int
	listSel   int
	focusList bool
	summary   bool
	quitting  bool

	splash *anim.Splash
	armed  bool

	// planeProjects are the Plane projects the Plane step listed, for the
	// projects step's links.
	planeProjects []plane.Project

	flash  string // a one-line note under the step ("copied", errors)
	w, h   int
	frames int
}

// New returns the wizard.
func New(d Deps) *Model {
	if d.Merge == nil {
		d.Merge = config.Merge
	}
	if d.Load == nil {
		d.Load = config.Load
	}
	if d.Keys == nil {
		d.Keys = ui.DefaultKeyMap()
	}
	if d.Getenv == nil {
		d.Getenv = os.Getenv
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.SmokePoll == 0 {
		d.SmokePoll = setup.SmokePoll
	}
	if d.PID == 0 {
		d.PID = os.Getpid()
	}
	if d.GHLogin == nil {
		d.GHLogin = func() *exec.Cmd { return exec.CommandContext(context.Background(), "gh", "auth", "login") }
	}
	cfg := d.Config
	if cfg == nil {
		cfg = config.Default()
	}
	var overrides map[string]string
	if cfg != nil {
		overrides = cfg.UI.Theme
	}
	m := &Model{
		d: d, cfg: cfg, keys: d.Keys,
		theme:   ui.NewTheme(true, overrides),
		eng:     anim.NewEngine(d.Anim),
		models:  map[setup.StepID]stepModel{},
		entered: map[setup.StepID]bool{},
		results: setup.Results{},
		armed:   d.Only == "",
	}
	m.split = ui.NewSplitPane(m.theme)
	m.split.SetRatio(0.28)
	for _, s := range setup.Steps {
		if d.Only != "" && s.ID != d.Only {
			continue
		}
		m.steps = append(m.steps, s)
	}
	m.models[setup.StepDeps] = &depsStep{base: base{m: m}}
	m.models[setup.StepGitHub] = &githubStep{base: base{m: m}}
	m.models[setup.StepPlane] = newPlaneStep(m)
	m.models[setup.StepProjects] = &projectsStep{base: base{m: m}}
	m.models[setup.StepTmux] = &tmuxStep{base: base{m: m}}
	m.models[setup.StepAnimations] = newAnimStep(m)
	m.models[setup.StepCapture] = &captureStep{base: base{m: m}}
	m.models[setup.StepSmoke] = &smokeStep{base: base{m: m}}
	return m
}

// Steps are the steps this run walks (one with Deps.Only).
func (m *Model) Steps() []setup.Step { return m.steps }

// Results are the step results so far (for tests and the CLI).
func (m *Model) Results() setup.Results { return m.results }

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor, m.loadResults()}
	if len(m.steps) > 0 {
		cmds = append(cmds, m.enterStep())
	}
	return tea.Batch(cmds...)
}

// Messages shared by the steps.
type (
	loadedResultsMsg struct{ rs setup.Results }
	savedMsg         struct{ err error }
	configMsg        struct {
		cfg *config.Config
		err error
	}
	completedMsg struct{ err error }
)

func (m *Model) loadResults() tea.Cmd {
	st := m.d.Store
	if st == nil {
		return nil
	}
	return func() tea.Msg { return loadedResultsMsg{setup.LoadResults(context.Background(), st)} }
}

// setResult records the current result of step id and saves it.
func (m *Model) setResult(id setup.StepID, r setup.Result) tea.Cmd {
	m.results[id] = r
	st := m.d.Store
	if st == nil {
		return nil
	}
	return func() tea.Msg { return savedMsg{setup.SaveResult(context.Background(), st, id, r)} }
}

// merge writes config values and reloads the config.
func (m *Model) merge(sets ...config.Set) tea.Cmd {
	path, merge, load := m.d.ConfigPath, m.d.Merge, m.d.Load
	return func() tea.Msg {
		if err := merge(path, sets...); err != nil {
			return configMsg{err: err}
		}
		cfg, err := load(path)
		return configMsg{cfg: cfg, err: err}
	}
}

func (m *Model) current() (setup.Step, stepModel) {
	s := m.steps[m.cur]
	return s, m.models[s.ID]
}

// enterStep runs the current step's start (its checks) the first time it
// is shown.
func (m *Model) enterStep() tea.Cmd {
	s, sm := m.current()
	m.flash = ""
	if m.entered[s.ID] {
		return nil
	}
	m.entered[s.ID] = true
	return sm.enter()
}

// next moves to the following step, or the summary after the last one.
func (m *Model) next() tea.Cmd {
	if m.cur+1 >= len(m.steps) {
		return m.finish()
	}
	m.cur++
	m.listSel = m.cur
	return m.enterStep()
}

// finish shows the summary; a full run marks setup completed.
func (m *Model) finish() tea.Cmd {
	m.summary, m.focusList = true, false
	m.flash = ""
	if m.d.Only != "" || m.d.Store == nil {
		return nil
	}
	st := m.d.Store
	return func() tea.Msg { return completedMsg{st.MarkSetupCompleted(context.Background())} }
}

// jump shows step i.
func (m *Model) jump(i int) tea.Cmd {
	m.summary = false
	m.cur, m.listSel, m.focusList = i, i, false
	return m.enterStep()
}

// quit cleans up whatever a step left running, then quits.
func (m *Model) quit() tea.Cmd {
	m.quitting = true
	var cleanups []tea.Cmd
	for _, sm := range m.models {
		if c := sm.leave(); c != nil {
			cleanups = append(cleanups, c)
		}
	}
	return tea.Sequence(tea.Batch(cleanups...), tea.Quit)
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.update(msg)
	return m, tea.Batch(cmd, m.syncAnim())
}

func (m *Model) update(msg tea.Msg) tea.Cmd {
	if m.eng.Update(msg) {
		m.frames++
		if m.splash != nil && m.splash.Done(m.eng.Frame()) {
			m.splash = nil
		}
		return nil
	}
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		*m.theme = *ui.NewTheme(msg.IsDark(), m.cfg.UI.Theme)
		return nil
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.split.SetSize(m.w, max(m.h-3, 4))
		if m.armed {
			m.armed = false
			if m.eng.Settings().Motion() && anim.SplashFits(m.w, m.h) {
				m.splash = &anim.Splash{Start: m.eng.Frame(), Seed: uint64(m.d.Now().UnixNano())}
			}
		}
		return m.forward(msg)
	case loadedResultsMsg:
		for id, r := range msg.rs {
			if _, ok := m.results[id]; !ok {
				m.results[id] = r
			}
		}
		return nil
	case savedMsg:
		if msg.err != nil {
			m.flash = "couldn't save the result: " + msg.err.Error()
		}
		return nil
	case configMsg:
		if msg.err != nil {
			m.flash = "config not written: " + msg.err.Error()
		} else if msg.cfg != nil {
			m.cfg = msg.cfg
		}
		return m.forward(msg)
	case completedMsg:
		if msg.err != nil {
			m.flash = "couldn't record that setup finished: " + msg.err.Error()
		}
		return nil
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m.forward(msg)
}

// forward hands a message to every step: each acts only on its own.
func (m *Model) forward(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for _, s := range setup.Steps {
		if sm, ok := m.models[s.ID]; ok {
			cmds = append(cmds, sm.update(msg))
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	if m.splash != nil { // any key skips the splash
		m.splash = nil
		return nil
	}
	if m.quitting || len(m.steps) == 0 {
		return nil
	}
	k := m.keys.Setup
	if m.summary {
		switch {
		case key.Matches(msg, k.Quit), key.Matches(msg, k.Next):
			return m.quit()
		case key.Matches(msg, k.Focus):
			m.summary, m.focusList = false, true
		}
		return nil
	}
	_, sm := m.current()
	if sm.grabs() {
		return sm.key(msg)
	}
	switch {
	case key.Matches(msg, k.Quit):
		return m.quit()
	case key.Matches(msg, k.Focus):
		m.focusList = !m.focusList
		m.listSel = m.cur
		return nil
	}
	if m.focusList {
		switch {
		case key.Matches(msg, k.Up):
			m.listSel = max(m.listSel-1, 0)
		case key.Matches(msg, k.Down):
			m.listSel = min(m.listSel+1, len(m.steps)-1)
		case key.Matches(msg, k.Next):
			return m.jump(m.listSel)
		}
		return nil
	}
	switch {
	case key.Matches(msg, k.Skip):
		s, _ := m.current()
		r := setup.Result{Status: setup.Skipped, Detail: "skipped", Fix: m.results[s.ID].Fix}
		return tea.Batch(m.setResult(s.ID, r), m.next())
	case key.Matches(msg, k.Next):
		cmd, ok := sm.accept()
		if !ok {
			return cmd
		}
		return tea.Batch(cmd, m.next())
	}
	return sm.key(msg)
}

// syncAnim keeps the shared clock running only while something visible
// moves, and lets the current step run its own preview clock.
func (m *Model) syncAnim() tea.Cmd {
	want := m.splash != nil
	cur := setup.StepID("")
	if len(m.steps) > 0 && !m.summary {
		s, sm := m.current()
		cur = s.ID
		want = want || sm.ticking()
	}
	cmds := []tea.Cmd{m.eng.Sync(want)}
	for id, sm := range m.models {
		if c, ok := sm.(clocked); ok {
			cmds = append(cmds, c.sync(id == cur && m.splash == nil))
		}
	}
	return tea.Batch(cmds...)
}

// stepModel is one step's UI.
type stepModel interface {
	// enter starts the step (its checks) when first shown.
	enter() tea.Cmd
	// update handles a non-key message (each step ignores others').
	update(tea.Msg) tea.Cmd
	// key handles a key the wizard didn't take.
	key(tea.KeyPressMsg) tea.Cmd
	// accept finishes the step on enter; ok moves on.
	accept() (tea.Cmd, bool)
	view(w, h int) []string
	help() []key.Binding
	// grabs is true while a form or the key test owns the keyboard.
	grabs() bool
	// ticking is true while the step shows a spinner.
	ticking() bool
	// leave cleans up on quit (the smoke test's session).
	leave() tea.Cmd
}

// clocked is a step with its own animation clock (the preview).
type clocked interface {
	sync(visible bool) tea.Cmd
}

// base is shared by the steps: the wizard and an optional huh form.
type base struct {
	m      *Model
	form   *huh.Form
	onDone func() tea.Cmd
}

func (b *base) leave() tea.Cmd { return nil }
func (b *base) grabs() bool    { return b.form != nil }

// openForm shows form in the step; done runs once it is completed.
func (b *base) openForm(form *huh.Form, done func() tea.Cmd) tea.Cmd {
	w, _ := b.m.split.RightSize()
	b.form = form.WithTheme(b.m.theme.Huh()).WithKeyMap(b.m.keys.HuhKeyMap()).WithShowHelp(true).WithWidth(max(w-2, 20))
	b.onDone = done
	return b.form.Init()
}

// updateForm feeds msg to the open form; handled is false without one.
func (b *base) updateForm(msg tea.Msg) (tea.Cmd, bool) {
	if b.form == nil {
		return nil, false
	}
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		return nil, true
	}
	_, cmd := b.form.Update(msg)
	switch b.form.State {
	case huh.StateCompleted:
		done := b.onDone
		b.form, b.onDone = nil, nil
		if done == nil {
			return nil, true
		}
		return done(), true
	case huh.StateAborted:
		b.form, b.onDone = nil, nil
		return nil, true
	}
	return cmd, true
}

func (b *base) formView() []string {
	if b.form == nil {
		return nil
	}
	return splitLines(b.form.View())
}

// ctx bounds one step operation.
func opCtx(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// attachCmd runs a tmux attachment: in the terminal (ExecProcess), or a
// popup of the user's own tmux in the background. done gets the result.
func attachCmd(a tmux.Attachment, done func(error) tea.Msg) tea.Cmd {
	switch a.Mode {
	case tmux.AttachSwitched:
		return func() tea.Msg { return done(nil) }
	case tmux.AttachPopup:
		c := a.Cmd
		return func() tea.Msg {
			out, err := c.CombinedOutput()
			if msg := strings.TrimSpace(string(out)); err != nil && msg != "" {
				err = fmt.Errorf("%w: %s", err, msg)
			}
			return done(err)
		}
	}
	if a.Cmd == nil {
		return func() tea.Msg { return done(fmt.Errorf("nothing to attach")) }
	}
	return tea.ExecProcess(a.Cmd, done)
}
