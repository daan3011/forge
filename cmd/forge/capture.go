package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/app"
	"github.com/daan3011/forge/internal/capture"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/store"
	planetab "github.com/daan3011/forge/internal/tabs/plane"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

const (
	// captureTimeout bounds `forge capture`'s Plane calls.
	captureTimeout = 20 * time.Second
	// captureLinger is how long the line shows "created …" before exiting.
	captureLinger = 600 * time.Millisecond
	// captureWidth is the line's widest, borders included.
	captureWidth = 74
)

func newCaptureCmd(d deps) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "capture [title…]",
		Short: "Quick-capture a Plane issue: forge capture \"fix login #forge !high @me +bug\"",
		Long: `Create a Plane work item from one line. Inline tokens:

  #project   a forge project, or a Plane project identifier or name
             (left out: the repo you're in, else the last active project)
  !priority  urgent, high, medium, low or none (prefixes work: !h)
  @person    @me, or a member's display name, first name or email name
  +label     a label of the project (spaces as dashes: +needs-design)

A token that names nothing is an error with the closest match. Without a
title, forge shows a one-line input with completion (tab); enter creates,
shift+enter (or alt+enter) opens the full form. Bind it in ~/.tmux.conf:

  ` + tmux.CaptureSnippet,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cs, err := d.captureSetup(cmd.Context(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			defer cs.close()
			text := strings.TrimSpace(strings.Join(args, " "))
			if project != "" {
				cs.flagProject = project
			}
			if text != "" {
				return cs.captureLine(cmd.Context(), cmd.OutOrStdout(), text)
			}
			return cs.interactive(cmd.Context(), d, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "project when the line has no #project")
	return cmd
}

// captureSession is what `forge capture` works with.
type captureSession struct {
	cfg         *config.Config
	st          *store.Store
	api         plane.API
	active      *config.Project
	flagProject string
	cat         capture.Catalog
}

func (cs *captureSession) close() { _ = cs.st.Close() }

// captureSetup loads the config, the projects and the Plane client, and
// reads the cached catalog.
func (d deps) captureSetup(ctx context.Context, warn io.Writer) (*captureSession, error) {
	cfg, _, _, err := config.LoadOrCreate()
	if err != nil {
		return nil, err
	}
	st, err := store.Open(ctx, d.dbPath())
	if err != nil {
		return nil, err
	}
	here := d.resolveProjects(ctx, cfg, st, warn)
	api, err := d.planeClient(cfg.Plane, st.Cache())
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("plane: %w", err)
	}
	cs := &captureSession{cfg: cfg, st: st, api: api}
	name := here
	if name == "" {
		if saved, found, err := app.LoadActiveProject(ctx, st.Cache()); err == nil && found {
			name = saved
		}
	}
	for i := range cfg.Projects {
		if name != "" && cfg.Projects[i].Name == name {
			cs.active = &cfg.Projects[i]
		}
	}
	cs.cat = capture.Cached(ctx, api, cfg.Projects, capture.Need{Projects: true, Me: true, Details: cs.linkedIDs()})
	return cs, nil
}

// linkedIDs are the Plane projects of the default and the forge projects.
func (cs *captureSession) linkedIDs() []string {
	var ids []string
	add := func(id string) {
		for _, x := range ids {
			if x == id {
				return
			}
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
	add(cs.defaultProject())
	for _, p := range cs.cfg.Projects {
		if p.PlaneProjectID != "" {
			add(p.PlaneProjectID)
		}
	}
	return ids
}

func (cs *captureSession) defaultProject() string {
	return capture.DefaultProject(cs.active, cs.cfg.Projects, cs.cat.Projects)
}

// withFlag adds --project as a #token when the line has none.
func (cs *captureSession) withFlag(text string) string {
	if cs.flagProject == "" || len(capture.Parse(text).Of(capture.Project)) > 0 {
		return text
	}
	return text + " #" + strings.TrimPrefix(cs.flagProject, "#")
}

// resolve resolves text, loading what it needs. Without a default project
// the project list is loaded first, as the default may be a match.
func (cs *captureSession) resolve(ctx context.Context, text string) (capture.Result, error) {
	d := capture.Parse(cs.withFlag(text))
	if cs.defaultProject() == "" && !cs.cat.HasProjects() {
		more, err := capture.Load(ctx, cs.api, cs.cfg.Projects, capture.Need{Projects: true})
		cs.cat = cs.cat.Merge(more)
		if err != nil {
			return capture.Result{}, err
		}
	}
	r, cat, err := capture.ResolveLoading(ctx, cs.api, cs.cfg.Projects, cs.cat, d, cs.defaultProject())
	cs.cat = cat
	return r, err
}

// create creates r and names the new item.
func (cs *captureSession) create(ctx context.Context, r capture.Result) (plane.WorkItem, error) {
	w, err := cs.api.CreateWorkItem(ctx, r.ProjectID, r.Create)
	if err != nil {
		return plane.WorkItem{}, fmt.Errorf("plane: couldn't create the issue: %w", err)
	}
	if w.Identifier == "" && w.SequenceID > 0 {
		ident := r.Project.Identifier
		if ident == "" {
			if p, err := cs.api.Project(ctx, r.ProjectID); err == nil {
				ident = p.Identifier
			}
		}
		if ident != "" {
			w.Identifier = fmt.Sprintf("%s-%d", ident, w.SequenceID)
		}
	}
	return w, nil
}

// createdLine is what `forge capture` prints for a new item.
func (cs *captureSession) createdLine(w plane.WorkItem) string {
	line := "created " + w.Key()
	if u := cs.cfg.Plane.IssueURL(w.Key()); u != "" && w.Identifier != "" {
		line += "  " + u
	}
	return line
}

// captureLine is `forge capture "title …"`.
func (cs *captureSession) captureLine(ctx context.Context, out io.Writer, text string) error {
	ctx, cancel := context.WithTimeout(ctx, captureTimeout)
	defer cancel()
	r, err := cs.resolve(ctx, text)
	if err != nil {
		return err
	}
	w, err := cs.create(ctx, r)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, cs.createdLine(w))
	return err
}

// interactive is `forge capture` with no title: the one-line program,
// then the full form if asked for.
func (cs *captureSession) interactive(ctx context.Context, d deps, out io.Writer) error {
	keys, err := ui.NewKeyMap(cs.cfg.Keybinds)
	if err != nil {
		return err
	}
	theme := ui.NewTheme(true, cs.cfg.UI.Theme)
	m := newCaptureModel(ctx, cs, theme, keys, anim.NewEngine(anim.Resolve(cs.cfg.UI.Animations, os.Getenv)))
	final, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithOutput(out)).Run()
	if err != nil {
		return err
	}
	fm := final.(captureModel)
	switch {
	case fm.created != nil:
		return nil // the line printed it
	case fm.full != nil:
		if d.runForm == nil {
			return errors.New("no terminal for the full form")
		}
		r := *fm.full
		fctx, cancel := context.WithTimeout(ctx, 10*time.Minute) // the form is interactive
		defer cancel()
		w, ok, err := planetab.RunCreateForm(fctx, cs.api, r.ProjectID, planetab.IssueDraft{
			Title: r.Create.Name, Priority: r.Create.Priority,
			LabelIDs: r.Create.LabelIDs, AssigneeIDs: r.Create.AssigneeIDs,
		}, planetab.CreateFormOpts{Theme: theme, Keys: keys, Height: fm.height, Run: d.runForm})
		if err != nil || !ok {
			return err
		}
		_, err = fmt.Fprintln(out, cs.createdLine(w))
		return err
	}
	return nil
}

// --- the one-line program ---------------------------------------------------------------

type (
	catalogMsg struct {
		cat capture.Catalog
		err error
	}
	createdMsg struct {
		item plane.WorkItem
		err  error
	}
	lingerDoneMsg struct{}
)

// captureModel is `forge capture`'s one-line program.
type captureModel struct {
	ctx    context.Context
	cs     *captureSession
	theme  *ui.Theme
	keys   *ui.KeyMap
	engine *anim.Engine
	input  ui.CaptureInput
	width  int
	height int

	busy    string // what is happening, while it happens
	pending bool   // enter waits for the catalog
	full0   bool   // ... and it was shift+enter
	rounds  int

	created *plane.WorkItem // done: created this
	full    *capture.Result // done: open the full form with this
	line    string          // the final "created …" line
	quit    bool            // cancelled: leave nothing on screen
}

func newCaptureModel(ctx context.Context, cs *captureSession, theme *ui.Theme, keys *ui.KeyMap, engine *anim.Engine) captureModel {
	m := captureModel{ctx: ctx, cs: cs, theme: theme, keys: keys, engine: engine, width: captureWidth}
	m.input = ui.NewCaptureInput(theme, keys, m.completer())
	return m
}

func (m captureModel) completer() capture.Completer {
	return capture.Completer{Catalog: m.cs.cat, DefaultProject: m.cs.defaultProject()}
}

// Init loads the fresh catalog (the cached one is already in).
func (m captureModel) Init() tea.Cmd {
	cs := m.cs
	need := capture.Need{Projects: true, Me: true, Details: cs.linkedIDs()}
	ctx := m.ctx
	return tea.Batch(tea.RequestBackgroundColor, func() tea.Msg {
		lctx, cancel := context.WithTimeout(ctx, captureTimeout)
		defer cancel()
		cat, err := capture.Load(lctx, cs.api, cs.cfg.Projects, need)
		return catalogMsg{cat: cat, err: err}
	})
}

func (m captureModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	nm := next.(captureModel)
	return nm, tea.Batch(cmd, nm.engine.Sync(nm.busy != ""))
}

func (m captureModel) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.engine.Update(msg) {
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = min(msg.Width, captureWidth), msg.Height
		return m, nil
	case tea.BackgroundColorMsg:
		*m.theme = *ui.NewTheme(msg.IsDark(), m.cs.cfg.UI.Theme)
		return m, nil
	case catalogMsg:
		m.cs.cat = m.cs.cat.Merge(msg.cat)
		m.input.SetCompleter(m.completer())
		if msg.err != nil && m.pending {
			m.pending, m.busy = false, ""
			m.input.SetBusy("")
			m.input.SetError(msg.err.Error(), nil)
			return m, nil
		}
		if m.pending {
			return m.submit(m.full0)
		}
		return m, nil
	case createdMsg:
		m.busy = ""
		m.input.SetBusy("")
		if msg.err != nil {
			m.input.SetError(msg.err.Error(), nil)
			return m, nil
		}
		w := msg.item
		m.created, m.line = &w, m.cs.createdLine(w)
		return m, tea.Tick(captureLinger, func(time.Time) tea.Msg { return lingerDoneMsg{} })
	case lingerDoneMsg:
		return m, tea.Quit
	case tea.KeyPressMsg:
		if key.Matches(msg, m.keys.Global.ForceQuit) {
			m.quit = true
			return m, tea.Quit
		}
		if m.created != nil {
			return m, tea.Quit // any key skips the linger
		}
		switch m.input.Update(msg) {
		case ui.CaptureCancel:
			m.quit = true
			return m, tea.Quit
		case ui.CaptureSubmit:
			m.rounds = 0
			return m.submit(false)
		case ui.CaptureFullForm:
			m.rounds = 0
			return m.submit(true)
		}
	}
	return m, nil
}

// submit resolves the line: waits for data still loading, shows errors,
// or creates the item (shift+enter: quits to open the full form).
func (m captureModel) submit(full bool) (tea.Model, tea.Cmd) {
	cs := m.cs
	text := cs.withFlag(m.input.Value())
	d := capture.Parse(text)
	def := cs.defaultProject()
	r, err := cs.cat.Resolve(d, def)
	var nl *capture.NotLoadedError
	if errors.As(err, &nl) && m.rounds < 3 {
		m.rounds++
		m.pending, m.full0 = true, full
		m.busy = "loading " + nl.Need.String() + "…"
		m.input.SetBusy(m.busy)
		ctx, need := m.ctx, nl.Need
		return m, func() tea.Msg {
			lctx, cancel := context.WithTimeout(ctx, captureTimeout)
			defer cancel()
			cat, err := capture.Load(lctx, cs.api, cs.cfg.Projects, need)
			return catalogMsg{cat: cat, err: err}
		}
	}
	if errors.Is(err, capture.ErrNoProject) && !cs.cat.HasProjects() && m.rounds < 3 {
		m.rounds++
		m.pending, m.full0 = true, full
		m.busy = "loading projects…"
		m.input.SetBusy(m.busy)
		ctx := m.ctx
		return m, func() tea.Msg {
			lctx, cancel := context.WithTimeout(ctx, captureTimeout)
			defer cancel()
			cat, err := capture.Load(lctx, cs.api, cs.cfg.Projects, capture.Need{Projects: true})
			return catalogMsg{cat: cat, err: err}
		}
	}
	m.pending, m.busy = false, ""
	m.input.SetBusy("")
	if full {
		lr, lerr := cs.cat.ResolveLenient(d, def)
		if lerr != nil {
			m.input.SetError(lerr.Error(), capture.FixOf(lerr))
			return m, nil
		}
		m.full = &lr
		return m, tea.Quit
	}
	if err != nil {
		m.input.SetError(err.Error(), capture.FixOf(err))
		return m, nil
	}
	m.busy = "creating " + r.Create.Name + "…"
	m.input.SetBusy(m.busy)
	ctx := m.ctx
	return m, func() tea.Msg {
		cctx, cancel := context.WithTimeout(ctx, captureTimeout)
		defer cancel()
		w, err := cs.create(cctx, r)
		return createdMsg{item: w, err: err}
	}
}

func (m captureModel) View() tea.View {
	t := m.theme
	inner := max(m.width-4, 16)
	if m.created != nil {
		return tea.NewView(t.CapturePrompt.Render("⚒ ") + t.CaptureToken.Render(m.line) + "\n")
	}
	if m.full != nil || m.quit {
		return tea.NewView("")
	}
	in := m.input
	if in.Busy() {
		in.SetBusy(anim.HammerFrame(m.engine.Frame(), m.engine.Settings(), t) + " " + t.CaptureInfo.Render(m.busy))
	}
	title := t.PaletteGroup.Render("Quick capture")
	if def := m.cs.defaultProject(); def != "" {
		for _, p := range m.cs.cat.Projects {
			if p.ID == def && p.Identifier != "" {
				title += t.CaptureInfo.Render(" · #" + p.Identifier)
			}
		}
	}
	body := ui.Box(title, inner, 1) + "\n" + in.View(inner)
	return tea.NewView(t.CaptureBox.Width(inner+4).Render(body) + "\n")
}

var _ tea.Model = captureModel{}
