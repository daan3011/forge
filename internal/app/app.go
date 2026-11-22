// Package app holds the root Bubble Tea model, tab routing, global keys and
// the command palette.
package app

import (
	"errors"
	"math"
	"os"
	"slices"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	artfs "github.com/daan3011/forge/internal/artifacts"
	"github.com/daan3011/forge/internal/browser"
	"github.com/daan3011/forge/internal/chafa"
	"github.com/daan3011/forge/internal/checkout"
	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/delta"
	docfs "github.com/daan3011/forge/internal/docs"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/refresh"
	"github.com/daan3011/forge/internal/rsvg"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/alert"
	"github.com/daan3011/forge/internal/sessions/live"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/artifacts"
	contexttab "github.com/daan3011/forge/internal/tabs/context"
	"github.com/daan3011/forge/internal/tabs/docs"
	"github.com/daan3011/forge/internal/tabs/home"
	"github.com/daan3011/forge/internal/tabs/pipelines"
	planetab "github.com/daan3011/forge/internal/tabs/plane"
	"github.com/daan3011/forge/internal/tabs/prs"
	"github.com/daan3011/forge/internal/tabs/sessions"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/templates"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
	"github.com/daan3011/forge/internal/ui/termcaps"
)

// statusBarHeight is the number of rows the status bar takes.
const statusBarHeight = 1

// clockInterval is how often "updated Ns ago" is refreshed.
const clockInterval = time.Second

// clockMsg ticks the status bar clock.
type clockMsg time.Time

// Model is the root model.
type Model struct {
	cfg     *config.Config
	theme   *ui.Theme
	keys    *ui.KeyMap
	tabs    []tabs.Tab
	active  int
	width   int
	height  int
	project *config.Project
	toast   ui.Toast
	status  ui.StatusBar
	message string
	now     time.Time
	clock   func() time.Time
	clockOn bool

	sched   refresh.Scheduler
	anim    *anim.Engine
	splash  *anim.Splash // the launch splash while it plays
	armed   bool         // play the splash on the first window size
	bursts  []anim.Burst // spark bursts of success toasts
	slideIn anim.Spring  // palette overlay offset from its resting row
	ghRate  github.RateLimit
	hasRate bool

	help     ui.HelpView
	helpOpen bool

	palette ui.CommandPalette
	overlay paletteMode
	capture ui.CaptureInput // the quick capture line (P3.5)
	cap     captureState
	notices []notice // shown once ever (WithNotice)
	items   []item   // registry snapshot taken when the palette opened
	shown   []item   // ranked results
	recent  []string
	store   KVStore

	savedProject string // from WithActiveProject
	hasSaved     bool
	fetchDelay   time.Duration // simulated latency of placeholder fetches
	sessions     agent.API     // from WithSessions
	live         <-chan live.Ping
	templatesDir string              // from WithTemplates
	packsDir     string              // from WithPacks
	providers    *templates.Registry // from WithTemplates
	browser      browser.Opener      // from WithBrowser
	github       github.PRClient     // from WithGitHub
	checkout     checkout.Checker    // from WithCheckout
	clipboard    clipboard.Copier    // from WithClipboard
	delta        delta.Renderer      // from WithDelta
	runs         github.RunsClient   // from WithRuns
	pulls        github.PullFinder   // from WithPulls
	projStore    ProjectStore        // from WithProjectStore
	plane        plane.API           // from WithPlane
	planeErr     error               // why plane is nil
	docsWatcher  docfs.Watcher       // from WithDocsWatcher

	artifactIdx   tabs.ArtifactIndex // from WithArtifacts
	artifactWatch artfs.Watcher      // from WithArtifactWatcher
	files         browser.FileOpener // from WithFiles

	capsDet  *termcaps.Detector // from WithCapsDetector; nil: no graphics
	chafa    chafa.Renderer     // from WithPreviewTools
	rsvg     rsvg.Rasterizer    // from WithPreviewTools
	occluded bool               // an overlay or toast covers the active tab

	alerts        *alert.Checker // from WithAlerts; nil: no notifications
	waiting       int            // sessions waiting for input, every project
	alertsFailing bool           // the last waiting check failed
	focused       bool           // the terminal has focus (assumed until told otherwise)
}

// paletteMode is which picker the palette overlay shows, if any.
type paletteMode int

const (
	overlayNone     paletteMode = iota
	overlayCommands             // ctrl+k: every action and entity
	overlayProjects             // ctrl+p: the project switcher
	overlaySessions             // ctrl+g: the session picker
	overlayCapture              // ctrl+n: quick capture (not a picker)
)

// New returns the root model for the given config. It fails if the keybinds
// overrides are invalid.
func New(cfg *config.Config, opts ...Option) (Model, error) {
	keys, err := ui.NewKeyMap(cfg.Keybinds)
	if err != nil {
		return Model{}, err
	}
	theme := ui.NewTheme(true, cfg.UI.Theme)
	m := newModel(cfg, theme, keys, nil)
	for _, o := range opts {
		o(&m)
	}
	d := tabkit.Deps{
		Theme: theme, Keys: keys, Store: m.store, FetchDelay: m.fetchDelay, Anim: m.anim,
		Config: cfg, Sessions: m.sessions, Live: m.live,
		TemplatesDir: m.templatesDir, PacksDir: m.packsDir, Providers: m.providers, Browser: m.browser,
		GitHub: m.github, Checkout: m.checkout, Clipboard: m.clipboard, Delta: m.delta,
		Runs: m.runs, Pulls: m.pulls, Plane: m.plane, PlaneErr: m.planeErr,
		ProjectLinks: linker(m.projStore), DocsWatcher: m.docsWatcher,
		Artifacts: m.artifactIdx, ArtifactWatcher: m.artifactWatch, Files: m.files,
		Chafa: m.chafa, Rsvg: m.rsvg,
	}
	m.tabs = []tabs.Tab{
		home.New(d),
		prs.New(d),
		pipelines.New(d),
		planetab.New(d),
		sessions.New(d),
		docs.New(d),
		artifacts.New(d),
		contexttab.New(d),
	}
	m.resolveProject()
	return m, nil
}

func newModel(cfg *config.Config, theme *ui.Theme, keys *ui.KeyMap, ts []tabs.Tab) Model {
	m := Model{
		cfg:     cfg,
		theme:   theme,
		keys:    keys,
		help:    ui.NewHelpView(theme, keys),
		palette: ui.NewCommandPalette(theme, keys),
		capture: ui.NewCaptureInput(theme, keys, nil),
		tabs:    ts,
		toast:   ui.NewToast(theme),
		status:  ui.NewStatusBar(theme),
		now:     time.Now(),
		clock:   time.Now,
		clockOn: true, // Init starts the first tick
		focused: true,
		sched:   refresh.New(cfg),
		anim:    anim.NewEngine(anim.Resolve(cfg.UI.Animations, os.Getenv)),

		fetchDelay: tabkit.SampleFetchDelay,
	}
	m.resolveProject()
	return m
}

// resolveProject picks the starting project: the saved one if it is still
// configured, all projects if that was saved, otherwise the first project.
// It tells every tab without refreshing them; Init loads their data.
func (m *Model) resolveProject() {
	m.project = nil
	switch {
	case m.hasSaved && m.savedProject == "":
	case m.hasSaved && m.findProject(m.savedProject) != nil:
		m.project = m.findProject(m.savedProject)
	case len(m.cfg.Projects) > 0:
		m.project = &m.cfg.Projects[0]
	}
	for _, t := range m.tabs {
		t.SetProject(m.project)
	}
}

func (m Model) findProject(name string) *config.Project {
	for i := range m.cfg.Projects {
		if m.cfg.Projects[i].Name == name {
			return &m.cfg.Projects[i]
		}
	}
	return nil
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor, clockTick(), loadRecents(m.store), m.sched.Start(), m.checkWaiting(), showNotices(m.store, m.notices)}
	for _, t := range m.tabs {
		cmds = append(cmds, t.Init())
	}
	if len(m.tabs) > 0 {
		cmds = append(cmds, setVisible(m.tabs[m.active], true))
	}
	if m.capsDet != nil {
		cmds = append(cmds, m.capsDet.Start())
	}
	for _, w := range m.Warnings() {
		cmds = append(cmds, func() tea.Msg { return ui.InfoMsg{Text: "warning: " + w} })
	}
	return tea.Batch(cmds...)
}

// Warnings are startup problems worth telling the user about that don't
// stop forge, such as refresh intervals clamped to the minimum.
func (m Model) Warnings() []string { return m.sched.Warnings() }

// setVisible tells a tab that wants to know whether it is on screen.
func setVisible(t tabs.Tab, v bool) tea.Cmd {
	if va, ok := t.(tabs.VisibilityAware); ok {
		return va.SetVisible(v)
	}
	return nil
}

func clockTick() tea.Cmd {
	return tea.Tick(clockInterval, func(t time.Time) tea.Msg { return clockMsg(t) })
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	return next.(Model).settle(cmd)
}

// settle runs after every message: the animation clock runs exactly while
// something visible animates, and the status clock runs while the active
// tab shows an update time.
func (m Model) settle(cmd tea.Cmd) (Model, tea.Cmd) {
	m, occ := m.syncOccluded()
	return m, tea.Batch(cmd, occ, m.anim.Sync(m.animating()), m.ensureClock())
}

// animating reports whether anything on screen moves: the splash, a spark
// burst, the palette sliding in, the hammer spinner while the visible tab
// fetches, or the visible tab's own animation.
func (m Model) animating() bool {
	f := m.anim.Frame()
	switch {
	case m.splash != nil, len(m.bursts) > 0, m.activeFetching():
		return true
	case m.overlay != overlayNone && !m.slideIn.Settled(f):
		return true
	case m.overlay == overlayCapture && m.capture.Busy():
		return true
	case m.helpOpen || len(m.tabs) == 0:
		return false
	}
	a, ok := m.tabs[m.active].(tabs.Animator)
	return ok && a.Animating()
}

// frame drops the splash and bursts that have finished.
func (m Model) frame() Model {
	f := m.anim.Frame()
	if m.splash != nil && m.splash.Done(f) {
		m.splash = nil
	}
	m.bursts = slices.DeleteFunc(slices.Clone(m.bursts), func(b anim.Burst) bool { return b.Done(f) })
	return m
}

// WithSplash plays the launch splash once the terminal size is known, if
// animations are full and the terminal is at least 60x16.
func WithSplash() Option {
	return func(m *Model) { m.armed = true }
}

// withGetenv resolves the animation level against getenv instead of the
// process environment.
func withGetenv(getenv func(string) string) Option {
	return func(m *Model) { m.anim = anim.NewEngine(anim.Resolve(m.cfg.UI.Animations, getenv)) }
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.toast.Update(msg) {
		return m, nil
	}
	if m.anim.Update(msg) {
		return m.frame(), nil
	}
	if next, cmd, handled := m.updateCaps(msg); handled {
		return next, cmd
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.armed {
			m.armed = false
			if m.anim.Settings().Motion() && anim.SplashFits(m.width, m.height) {
				m.splash = &anim.Splash{Start: m.anim.Frame(), Seed: uint64(m.clock().UnixNano())}
			}
		}
		for _, t := range m.tabs {
			t.SetSize(m.contentSize())
		}
		m.help.SetSize(m.width, m.height)
		return m, m.resized()

	case tea.BackgroundColorMsg:
		*m.theme = *ui.NewTheme(msg.IsDark(), m.cfg.UI.Theme)
		return m, nil

	case clockMsg:
		m.now = time.Time(msg)
		if m.activeUpdated().IsZero() {
			m.clockOn = false
			return m, nil
		}
		return m, clockTick()

	case ui.ErrMsg:
		if msg.Err == nil {
			return m, nil
		}
		return m, m.toast.Show(msg.Err.Error(), ui.ToastError)

	case recentsMsg:
		m.recent = mergeRecent(m.recent, msg)
		return m, nil

	case ui.InfoMsg:
		m.message = msg.Text
		return m, m.toast.Show(msg.Text, ui.ToastInfo)

	case ui.SuccessMsg:
		m.message = msg.Text
		if m.anim.Settings().Motion() {
			f := m.anim.Frame()
			m.bursts = append(m.bursts, anim.Burst{Start: f, Seed: uint64(m.clock().UnixNano()) + uint64(len(m.bursts))})
		}
		return m, m.toast.Show(msg.Text, ui.ToastSuccess)

	case refresh.TickMsg:
		return m.handleTick(msg)

	case waitingMsg:
		return m.handleWaiting(msg)

	case tabs.SessionPingMsg:
		return m.handleSessionPing(msg)

	case tabs.StartSessionMsg:
		return m.handleStartSession(msg)

	case tabs.PlaneLinkedMsg:
		return m.handlePlaneLinked(msg)

	case tabs.ShowRunsMsg:
		return m.handleShowRuns(msg)

	case captureCatalogMsg:
		return m.handleCaptureCatalog(msg)

	case captureCreatedMsg:
		return m.handleCaptureCreated(msg)

	case tea.FocusMsg:
		m.focused = true
		return m, nil

	case tea.BlurMsg:
		m.focused = false
		return m, nil

	case github.RateLimitMsg:
		return m.handleRateLimit(msg.RateLimit)

	case tabs.TargetedMsg:
		return m.updateTab(msg.TargetTab(), msg)

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m.updateActive(msg)
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	g := m.keys.Global
	if key.Matches(msg, g.ForceQuit) {
		return m, tea.Quit
	}
	if m.splash != nil { // any key skips the splash
		m.splash = nil
		return m, nil
	}
	if key.Matches(msg, g.Palette) {
		if m.overlay == overlayCommands {
			m.overlay = overlayNone
		} else {
			m.openPalette()
		}
		return m, nil
	}
	if m.overlay == overlayCapture {
		if key.Matches(msg, g.Capture) {
			m.overlay = overlayNone
			return m, nil
		}
		return m.handleCaptureKey(msg)
	}
	if m.overlay != overlayNone {
		return m.handlePaletteKey(msg)
	}
	if key.Matches(msg, g.Projects) {
		return m.openProjects()
	}
	if m.helpOpen {
		return m.handleHelpKey(msg)
	}
	if m.capturingInput() {
		return m.updateActive(msg)
	}
	switch {
	case key.Matches(msg, g.Quit):
		return m, tea.Quit
	case key.Matches(msg, g.Help):
		m.openHelp()
		return m, nil
	case key.Matches(msg, g.GotoSession):
		return m.openSessions()
	case key.Matches(msg, g.Capture):
		return m.openCapture()
	case key.Matches(msg, g.NextTab):
		return m.switchTo((m.active + 1) % len(m.tabs))
	case key.Matches(msg, g.PrevTab):
		return m.switchTo((m.active - 1 + len(m.tabs)) % len(m.tabs))
	case key.Matches(msg, g.GotoTab):
		if i := m.keys.TabIndex(msg); i >= 0 && i < len(m.tabs) {
			return m.switchTo(i)
		}
		return m, nil
	case key.Matches(msg, g.Refresh):
		return m.refresh()
	case key.Matches(msg, g.RefreshAll):
		return m.refreshAll()
	}
	return m.updateActive(msg)
}

// openPalette snapshots the registry and shows every item, recents first.
func (m *Model) openPalette() {
	m.helpOpen = false
	m.items = m.paletteItems()
	m.overlay = overlayCommands
	m.palette.Reset()
	m.rerank()
	m.startSlideIn()
}

// paletteDrop is how many rows above its resting place the palette starts
// sliding in from.
const paletteDrop = 4

func (m *Model) startSlideIn() {
	m.slideIn = anim.NewSpring(-paletteDrop, 0, m.anim.Frame(), m.anim.Settings())
}

// openProjects shows the project switcher with the current project
// selected, or a hint when no projects are configured.
func (m Model) openProjects() (Model, tea.Cmd) {
	if len(m.cfg.Projects) == 0 {
		return m, m.toast.Show("no git repos found — set project_roots in your config", ui.ToastInfo)
	}
	m.helpOpen = false
	m.items = m.projectItems()
	m.overlay = overlayProjects
	m.palette.Reset()
	m.rerank()
	m.startSlideIn()
	for i, it := range m.shown {
		if it.current {
			m.palette.SetCursor(i)
			break
		}
	}
	return m, nil
}

// openSessions shows the session picker, or a hint when there are no
// sessions to pick.
func (m Model) openSessions() (Model, tea.Cmd) {
	items := m.sessionItems()
	if len(items) == 0 {
		return m, m.toast.Show("no sessions to go to — start one on the Sessions tab", ui.ToastInfo)
	}
	m.helpOpen = false
	m.items = items
	m.overlay = overlaySessions
	m.palette.Reset()
	m.rerank()
	m.startSlideIn()
	return m, nil
}

func (m *Model) rerank() {
	var recent []string
	if m.overlay == overlayCommands {
		recent = m.recent
	}
	m.shown = rank(m.items, m.palette.Query(), recent)
	rows := make([]ui.PaletteRow, len(m.shown))
	for i, it := range m.shown {
		rows[i] = ui.PaletteRow{Group: it.group, Title: it.title, Detail: it.detail, Hint: it.hint}
	}
	m.palette.SetRows(rows)
}

func (m Model) handlePaletteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	in := m.keys.Input
	switch {
	case key.Matches(msg, in.Cancel):
		m.overlay = overlayNone
		return m, nil
	case key.Matches(msg, in.Accept):
		i := m.palette.Selected()
		if i < 0 {
			return m, nil
		}
		it := m.shown[i]
		mode := m.overlay
		m.overlay = overlayNone
		m.items, m.shown = nil, nil
		var save tea.Cmd
		if mode == overlayCommands {
			m.recent = pushRecent(m.recent, it.id)
			save = saveRecents(m.store, m.recent)
		}
		var cmd tea.Cmd
		m, cmd = it.run(m)
		return m, tea.Batch(cmd, save)
	}
	if m.palette.Update(msg) {
		m.rerank()
	}
	return m, nil
}

func (m Model) handleHelpKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Global.Help, m.keys.Input.Cancel, m.keys.Global.Quit):
		m.helpOpen = false
	default:
		m.help.Update(msg)
	}
	return m, nil
}

// openHelp shows the help overlay for the global keys and the active tab.
func (m *Model) openHelp() {
	sections := []ui.HelpSection{{Title: "Global", Groups: [][]key.Binding{m.keys.GlobalHelp()}}}
	if len(m.tabs) > 0 {
		t := m.tabs[m.active]
		sections = append(sections, ui.HelpSection{Title: t.Title(), Groups: t.FullHelp()})
	}
	m.help.SetContent(sections)
	m.help.SetSize(m.width, m.height)
	m.helpOpen = true
}

// statusKeys are the status bar hints: the tab's own, plus help unless the
// tab is taking text input (where ? would be typed).
func (m Model) statusKeys(active tabs.Tab) []key.Binding {
	if m.overlay == overlayCapture {
		return m.keys.CaptureHelp()
	}
	if m.overlay != overlayNone {
		in := m.keys.Input
		return []key.Binding{in.Next, in.Prev, in.Cancel, in.Accept}
	}
	if m.capturingInput() {
		return active.ShortHelp()
	}
	return slices.Concat(active.ShortHelp(), []key.Binding{m.keys.Global.Help})
}

func (m Model) capturingInput() bool {
	if len(m.tabs) == 0 {
		return false
	}
	c, ok := m.tabs[m.active].(tabs.InputCapturer)
	return ok && c.CapturingInput()
}

func (m Model) refresh() (Model, tea.Cmd) {
	if len(m.tabs) == 0 {
		return m, nil
	}
	cmd := m.tabs[m.active].Refresh()
	m.now = m.clock()
	return m, tea.Batch(cmd, m.ensureClock())
}

// refreshAll refreshes every tab, visible or not.
func (m Model) refreshAll() (Model, tea.Cmd) {
	cmds := make([]tea.Cmd, 0, len(m.tabs)+1)
	for _, t := range m.tabs {
		cmds = append(cmds, t.Refresh())
	}
	m.now = m.clock()
	return m, tea.Batch(append(cmds, m.ensureClock())...)
}

// switchTo shows tab i. A tab that missed refreshes while hidden (or was
// never loaded for this project) refreshes right away.
func (m Model) switchTo(i int) (Model, tea.Cmd) {
	var vis []tea.Cmd
	if i != m.active {
		vis = append(vis, setVisible(m.tabs[m.active], false), setVisible(m.tabs[i], true))
		if g, ok := graphicsAware(m.tabs[i]); ok {
			vis = append(vis, g.SetOccluded(m.occluded))
		}
	}
	m.active = i
	m.now = m.clock()
	var cmd tea.Cmd
	if m.stale(m.tabs[i]) {
		cmd = m.tabs[i].Refresh()
	}
	return m, tea.Batch(append(vis, cmd, m.ensureClock())...)
}

// stale reports whether t has never loaded, or is older than the shortest
// interval of its sources.
func (m Model) stale(t tabs.Tab) bool {
	u, ok := t.(tabs.Updater)
	if !ok {
		return false
	}
	last := u.LastUpdated()
	if last.IsZero() {
		return true
	}
	ar, ok := t.(tabs.AutoRefresher)
	if !ok {
		return false
	}
	for _, src := range ar.Sources() {
		if m.now.Sub(last) >= m.sched.Interval(src) {
			return true
		}
	}
	return false
}

// refreshesNow reports whether tab i takes background refreshes right now:
// it is visible, or it refreshes even when hidden (Home, Sessions).
func (m Model) refreshesNow(i int) bool {
	if i == m.active {
		return true
	}
	ar, ok := m.tabs[i].(tabs.AutoRefresher)
	return ok && ar.RefreshWhenHidden()
}

// handleTick refreshes the tabs that use the ticking source and are not
// paused, then re-arms the source.
func (m Model) handleTick(msg refresh.TickMsg) (Model, tea.Cmd) {
	ok, next := m.sched.Handle(msg)
	if !ok {
		return m, nil
	}
	cmds := []tea.Cmd{next, m.alertTick(msg)}
	for i, t := range m.tabs {
		ar, isAuto := t.(tabs.AutoRefresher)
		if !isAuto || !slices.Contains(ar.Sources(), msg.Source) || !m.refreshesNow(i) {
			continue
		}
		if sr, ok := t.(tabs.SourceRefresher); ok {
			cmds = append(cmds, sr.RefreshSource(msg.Source))
		} else {
			cmds = append(cmds, t.Refresh())
		}
	}
	return m, tea.Batch(cmds...)
}

// handleStartSession switches to the tab that owns sessions and opens its
// new-session form for the source (tabs.StartSessionFrom, P1.9).
func (m Model) handleStartSession(msg tabs.StartSessionMsg) (Model, tea.Cmd) {
	for i, t := range m.tabs {
		ss, ok := t.(tabs.SessionStarter)
		if !ok {
			continue
		}
		m, switchCmd := m.switchTo(i)
		return m, tea.Batch(switchCmd, ss.StartSession(msg.Source))
	}
	return m, func() tea.Msg { return ui.ErrMsg{Err: errors.New("sessions are unavailable")} }
}

// handleShowRuns switches to the tab that lists workflow runs and shows the
// runs of a repo's branch (tabs.ShowRuns, P2.6).
func (m Model) handleShowRuns(msg tabs.ShowRunsMsg) (Model, tea.Cmd) {
	for i, t := range m.tabs {
		rs, ok := t.(tabs.RunsShower)
		if !ok {
			continue
		}
		show := rs.ShowRuns(msg.Repo, msg.Branch)
		m, switchCmd := m.switchTo(i)
		return m, tea.Batch(switchCmd, show)
	}
	return m, func() tea.Msg { return ui.ErrMsg{Err: errors.New("pipelines are unavailable")} }
}

// handleRateLimit records the GitHub rate limit and backs the GitHub
// refresh off while it is low.
func (m Model) handleRateLimit(rl github.RateLimit) (Model, tea.Cmd) {
	m.ghRate, m.hasRate = rl, true
	if !rl.Low() {
		return m, m.sched.ClearDelay(refresh.GitHub)
	}
	base := m.cfg.GitHub.RefreshInterval.Duration
	return m, m.sched.SetDelay(refresh.GitHub, github.NextDelay(rl, base, m.clock()))
}

// rateBadge is the status bar rate limit text, shown while calls are low
// and the limit has not reset yet.
func (m Model) rateBadge() string {
	r := m.ghRate
	if !m.hasRate || !r.Low() || (!r.Reset.IsZero() && !m.now.Before(r.Reset)) {
		return ""
	}
	return r.Summary(m.now)
}

// ensureClock restarts the clock tick if the active tab shows an update time.
func (m *Model) ensureClock() tea.Cmd {
	if m.clockOn || m.activeUpdated().IsZero() {
		return nil
	}
	m.clockOn = true
	return clockTick()
}

// updateTab delivers msg to the tab titled title, active or not.
func (m Model) updateTab(title string, msg tea.Msg) (tea.Model, tea.Cmd) {
	for i, t := range m.tabs {
		if t.Title() == title {
			var cmd tea.Cmd
			m.tabs[i], cmd = t.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m Model) updateActive(msg tea.Msg) (tea.Model, tea.Cmd) {
	if len(m.tabs) == 0 {
		return m, nil
	}
	t, cmd := m.tabs[m.active].Update(msg)
	m.tabs[m.active] = t
	return m, cmd
}

// activeDetail is the active tab's own status bar text.
func (m Model) activeDetail() string {
	if len(m.tabs) == 0 {
		return ""
	}
	d, ok := m.tabs[m.active].(tabs.StatusDetailer)
	if !ok {
		return ""
	}
	return d.StatusDetail()
}

func (m Model) activeFetching() bool {
	if len(m.tabs) == 0 {
		return false
	}
	if m.activeBusy() != "" {
		return true
	}
	f, ok := m.tabs[m.active].(tabs.Fetcher)
	return ok && f.Fetching()
}

// activeBusy is the visible tab's running action (tabs.Busier), or "".
func (m Model) activeBusy() string {
	if len(m.tabs) == 0 {
		return ""
	}
	if b, ok := m.tabs[m.active].(tabs.Busier); ok {
		return b.Busy()
	}
	return ""
}

func (m Model) activeUpdated() time.Time {
	if len(m.tabs) == 0 {
		return time.Time{}
	}
	if u, ok := m.tabs[m.active].(tabs.Updater); ok {
		return u.LastUpdated()
	}
	return time.Time{}
}

// contentSize is the space left for the active tab.
func (m Model) contentSize() (width, height int) {
	return m.width, max(m.height-ui.TabBarHeight-statusBarHeight, 0)
}

// View implements tea.Model.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.ReportFocus = true // notifications on errors / finished turns only when unfocused
	v.BackgroundColor = m.theme.Bg
	return v
}

func (m Model) render() string {
	if m.width == 0 || m.height == 0 || len(m.tabs) == 0 {
		return m.theme.Wordmark.Render("forge")
	}
	if m.splash != nil {
		return m.splash.View(m.anim.Frame(), m.width, m.height, m.theme)
	}
	if m.helpOpen {
		return m.help.View()
	}
	titles := make([]string, len(m.tabs))
	for i, t := range m.tabs {
		titles[i] = t.Title()
	}
	cw, ch := m.contentSize()
	active := m.tabs[m.active]

	project := ""
	if m.project != nil {
		project = m.project.Name
	}
	screen := lipgloss.JoinVertical(lipgloss.Left,
		ui.TabBar(m.theme, titles, m.active, m.width),
		ui.Box(active.View(), cw, ch),
		m.status.View(m.width, ui.StatusInfo{
			Tab:      active.Title(),
			Project:  project,
			Message:  m.message,
			Updated:  m.activeUpdated(),
			Now:      m.now,
			Fetching: m.activeFetching(),
			Activity: m.activeBusy(),
			Spinner:  anim.HammerFrame(m.anim.Frame(), m.anim.Settings(), m.theme),
			Rate:     m.rateBadge(),
			Waiting:  m.waiting,
			Detail:   m.activeDetail(),
			Keys:     m.statusKeys(active),
		}),
	)
	screen = ui.Box(screen, m.width, m.height)

	layers := []*lipgloss.Layer{lipgloss.NewLayer(screen)}
	if m.overlay != overlayNone {
		l := m.paletteLayer()
		if m.overlay == overlayCapture {
			l = m.captureLayer()
		}
		if l != nil {
			layers = append(layers, l)
		}
	}
	if !m.toast.Empty() {
		maxW := max(m.width*2/5, 24)
		toast := m.toast.View(maxW)
		tw, th := lipgloss.Width(toast), lipgloss.Height(toast)
		x := max(m.width-tw-1, 0)
		y := max(m.height-statusBarHeight-th, 0)
		layers = append(layers, lipgloss.NewLayer(toast).X(x).Y(y).Z(2))
		layers = append(layers, m.burstLayers(x, y, tw, th, maxW)...)
	}
	if len(layers) == 1 {
		return screen
	}
	canvas := lipgloss.NewCanvas(m.width, m.height)
	canvas.Compose(lipgloss.NewCompositor(layers...))
	return ui.Box(canvas.Render(), m.width, m.height) // Render trims trailing spaces
}

// Palette size limits.
const (
	paletteMaxWidth  = 72
	paletteMinHeight = 8
)

// paletteLayer renders the palette centred over the screen.
func (m Model) paletteLayer() *lipgloss.Layer {
	w := min(paletteMaxWidth, m.width-4)
	h := min(max(m.height*3/5, paletteMinHeight), m.height-2)
	view := m.palette.View(w, h)
	if view == "" {
		return nil
	}
	x := (m.width - lipgloss.Width(view)) / 2
	y := (m.height - lipgloss.Height(view)) / 2
	off, _ := m.slideIn.At(m.anim.Frame())
	y = max(y+int(math.Round(off)), 0)
	return lipgloss.NewLayer(view).X(x).Y(y).Z(1)
}

// burstLayers throws the spark bursts from the left edge of the newest
// toast (the bottom one), skipping sparks that would land on the toasts.
func (m Model) burstLayers(x, y, w, h, maxW int) []*lipgloss.Layer {
	if len(m.bursts) == 0 {
		return nil
	}
	lw, lh := m.toast.LastSize(maxW)
	bx, by := x+w-lw, y+h-lh+lh/2
	inside := func(px, py int) bool {
		if px >= m.width || py >= m.height {
			return true
		}
		left := x // older toasts above the newest may be wider
		if py >= y+h-lh {
			left = bx
		}
		return px >= left && px < x+w && py >= y && py < y+h
	}
	var out []*lipgloss.Layer
	for _, b := range m.bursts {
		out = append(out, b.Layers(m.anim.Frame(), bx, by, 3, inside)...)
	}
	return out
}
