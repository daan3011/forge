package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/daan3011/forge/internal/capture"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/projects"
	planetab "github.com/daan3011/forge/internal/tabs/plane"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// Quick capture (P3.5): ctrl+n opens a one-line overlay over any tab.
// enter resolves the tokens and creates the item in the background;
// shift+enter opens the Plane tab's create form prefilled instead.

const (
	// captureTimeout bounds a catalog load or a create.
	captureTimeout = 15 * time.Second
	// captureTTL is how long a loaded catalog is used before ctrl+n
	// fetches it again (it is still shown while that happens).
	captureTTL = 5 * time.Minute
	// captureMaxRounds bounds loads for one submit.
	captureMaxRounds = 3
	// captureWidth is the overlay's widest.
	captureWidth = 72
)

// captureState is the overlay's state beyond the input line.
type captureState struct {
	cat      capture.Catalog
	loadedAt time.Time       // last fresh load
	loading  map[string]bool // details loads in flight, by Plane project ("" = the base load)
	pending  bool            // a submit waits for the catalog
	full     bool            // ... and it asked for the full form
	rounds   int             // loads the pending submit has waited for
	keep     bool            // the last create failed: reopen with its text
	busy     string          // what the pending submit waits for
}

// captureCatalogMsg carries a loaded catalog.
type captureCatalogMsg struct {
	cat    capture.Catalog
	err    error
	cached bool
	need   capture.Need
	key    string // which load: "" the base one, else a project's details
}

// captureCreatedMsg settles a background create.
type captureCreatedMsg struct {
	project string
	key     string // the project's identifier, to name the item
	title   string
	item    plane.WorkItem
	err     error
}

// openCapture shows the capture overlay and loads (or refreshes) what
// completion and resolving need.
func (m Model) openCapture() (Model, tea.Cmd) {
	if m.plane == nil {
		err := m.planeErr
		if err == nil {
			err = errors.New("plane isn't set up")
		}
		return m, m.toast.Show("quick capture: "+err.Error(), ui.ToastError)
	}
	m.helpOpen = false
	m.overlay = overlayCapture
	if !m.cap.keep {
		m.capture.Reset()
	}
	m.cap.keep = false
	m.cap.pending = false
	m.capture.SetBusy("")
	m.startSlideIn()
	m.syncCompleter()
	if m.cap.loading == nil {
		m.cap.loading = map[string]bool{}
	}
	if m.cap.loading[""] || !m.cap.loadedAt.IsZero() && m.clock().Sub(m.cap.loadedAt) < captureTTL {
		return m, nil
	}
	need := m.baseNeed()
	m.cap.loading[""] = true
	var cmds []tea.Cmd
	if m.cap.loadedAt.IsZero() {
		cmds = append(cmds, m.loadCatalog(need, "", true))
	}
	return m, tea.Batch(append(cmds, m.loadCatalog(need, "", false))...)
}

// baseNeed is what ctrl+n loads: projects, you, and the labels and
// members of the default project and those linked to forge projects.
func (m Model) baseNeed() capture.Need {
	n := capture.Need{Projects: true, Me: true}
	add := func(id string) {
		if id != "" && !slices.Contains(n.Details, id) {
			n.Details = append(n.Details, id)
		}
	}
	add(m.captureDefault())
	for _, p := range m.cfg.Projects {
		id, _ := projects.PlaneLinkOf(p, m.cap.cat.Projects)
		add(id)
	}
	return n
}

// loadCatalog fetches need (or reads it from the cache).
func (m Model) loadCatalog(need capture.Need, key string, cached bool) tea.Cmd {
	api, forge := m.plane, m.cfg.Projects
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), captureTimeout)
		defer cancel()
		if cached {
			return captureCatalogMsg{cat: capture.Cached(ctx, api, forge, need), cached: true, need: need, key: key}
		}
		cat, err := capture.Load(ctx, api, forge, need)
		return captureCatalogMsg{cat: cat, err: err, need: need, key: key}
	}
}

// captureDefault is the Plane project a line without #project goes to.
func (m Model) captureDefault() string {
	return capture.DefaultProject(m.project, m.cfg.Projects, m.cap.cat.Projects)
}

func (m *Model) syncCompleter() {
	m.capture.SetCompleter(capture.Completer{Catalog: m.cap.cat, DefaultProject: m.captureDefault()})
}

func (m Model) handleCaptureCatalog(msg captureCatalogMsg) (Model, tea.Cmd) {
	if msg.cached {
		// Never lay a cached catalog over a fresh one.
		if m.cap.loadedAt.IsZero() {
			m.cap.cat = msg.cat.Merge(m.cap.cat)
		}
		m.syncCompleter()
		return m, nil
	}
	delete(m.cap.loading, msg.key)
	m.cap.cat = m.cap.cat.Merge(msg.cat)
	if msg.key == "" && msg.err == nil {
		m.cap.loadedAt = m.clock()
	}
	m.syncCompleter()
	if msg.err != nil {
		if m.cap.pending {
			m.cap.pending = false
			m.capture.SetError(msg.err.Error(), nil)
			return m, nil
		}
		if m.overlay == overlayCapture {
			m.capture.SetInfo("couldn't load everything from Plane: " + msg.err.Error())
		}
		return m, nil
	}
	if m.cap.pending && m.overlay == overlayCapture {
		return m.submitCapture(m.cap.full)
	}
	return m, nil
}

func (m Model) handleCaptureKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch m.capture.Update(msg) {
	case ui.CaptureCancel:
		m.overlay = overlayNone
		m.cap.pending = false
		m.capture.SetBusy("")
		return m, nil
	case ui.CaptureSubmit:
		m.cap.rounds = 0
		return m.submitCapture(false)
	case ui.CaptureFullForm:
		m.cap.rounds = 0
		return m.submitCapture(true)
	case ui.CaptureChanged:
		return m, m.loadLineProject()
	}
	return m, nil
}

// loadLineProject fetches the details of the line's #project when it
// names a project whose labels and members aren't loaded, so +label and
// @person complete there.
func (m *Model) loadLineProject() tea.Cmd {
	d := capture.Parse(m.capture.Value())
	r, err := m.cap.cat.ResolveLenient(d, m.captureDefault())
	if err != nil || r.ProjectID == "" || m.cap.cat.HasDetails(r.ProjectID) || m.cap.loading[r.ProjectID] {
		return nil
	}
	if m.cap.loading == nil {
		m.cap.loading = map[string]bool{}
	}
	m.cap.loading[r.ProjectID] = true
	return m.loadCatalog(capture.Need{Details: []string{r.ProjectID}}, r.ProjectID, false)
}

// submitCapture resolves the line: on success it creates the item (or
// opens the full form); data still loading makes it wait; anything else
// is shown in the overlay.
func (m Model) submitCapture(full bool) (Model, tea.Cmd) {
	d := capture.Parse(m.capture.Value())
	def := m.captureDefault()
	r, err := m.cap.cat.Resolve(d, def)
	var nl *capture.NotLoadedError
	if errors.As(err, &nl) && m.cap.rounds < captureMaxRounds {
		m.cap.rounds++
		m.cap.pending, m.cap.full = true, full
		m.cap.busy = "loading " + nl.Need.String() + "…"
		m.capture.SetBusy(m.cap.busy)
		key := "submit"
		if len(nl.Need.Details) == 1 && !nl.Need.Projects && !nl.Need.Me {
			key = nl.Need.Details[0]
		}
		if m.cap.loading == nil {
			m.cap.loading = map[string]bool{}
		}
		m.cap.loading[key] = true
		return m, m.loadCatalog(nl.Need, key, false)
	}
	m.cap.pending = false
	m.capture.SetBusy("")
	if full {
		lr, lerr := m.cap.cat.ResolveLenient(d, def)
		if lerr != nil {
			m.capture.SetError(lerr.Error(), capture.FixOf(lerr))
			return m, nil
		}
		return m.openFullForm(lr)
	}
	if err != nil {
		m.capture.SetError(err.Error(), capture.FixOf(err))
		return m, nil
	}
	m.overlay = overlayNone
	m.cap.keep = true // until the create succeeds
	return m, tea.Batch(
		func() tea.Msg { return ui.InfoMsg{Text: "creating " + quote(r.Create.Name) + "…"} },
		m.createCaptured(r),
	)
}

func quote(s string) string {
	const maxLen = 40
	if r := []rune(s); len(r) > maxLen {
		s = string(r[:maxLen-1]) + "…"
	}
	return fmt.Sprintf("%q", s)
}

func (m Model) createCaptured(r capture.Result) tea.Cmd {
	api := m.plane
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), captureTimeout)
		defer cancel()
		w, err := api.CreateWorkItem(ctx, r.ProjectID, r.Create)
		return captureCreatedMsg{project: r.ProjectID, key: r.Project.Identifier, title: r.Create.Name, item: w, err: err}
	}
}

func (m Model) handleCaptureCreated(msg captureCreatedMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		return m, func() tea.Msg {
			return ui.ErrMsg{Err: fmt.Errorf("couldn't create %s: %w (%s reopens it)",
				quote(msg.title), msg.err, m.keys.Global.Capture.Help().Key)}
		}
	}
	m.cap.keep = false
	m.capture.Reset()
	w := msg.item
	if w.Identifier == "" && msg.key != "" && w.SequenceID > 0 {
		w.Identifier = fmt.Sprintf("%s-%d", msg.key, w.SequenceID)
	}
	cmds := []tea.Cmd{func() tea.Msg { return ui.SuccessMsg{Text: "created " + w.Key()} }}
	if _, pt := m.planeTab(); pt != nil {
		cmds = append(cmds, pt.Created(msg.project, w))
	}
	return m, tea.Batch(cmds...)
}

// openFullForm switches to the Plane tab and opens its create form
// prefilled from r.
func (m Model) openFullForm(r capture.Result) (Model, tea.Cmd) {
	i, pt := m.planeTab()
	if pt == nil {
		m.capture.SetError("the Plane tab is unavailable", nil)
		return m, nil
	}
	m.overlay = overlayNone
	m.capture.Reset()
	open := pt.NewIssue(r.ProjectID, planetab.IssueDraft{
		Title: r.Create.Name, Priority: r.Create.Priority,
		LabelIDs: r.Create.LabelIDs, AssigneeIDs: r.Create.AssigneeIDs,
	})
	m, sw := m.switchTo(i)
	return m, tea.Batch(sw, open)
}

// planeTab is the Plane tab and its index; nil when there is none.
func (m Model) planeTab() (int, *planetab.Tab) {
	for i, t := range m.tabs {
		if pt, ok := t.(*planetab.Tab); ok {
			return i, pt
		}
	}
	return -1, nil
}

// captureLayer renders the capture overlay a third of the way down.
func (m Model) captureLayer() *lipgloss.Layer {
	w := min(captureWidth, m.width-4)
	if w < 16 || m.height < 7 {
		return nil
	}
	c := m.capture
	if c.Busy() {
		c.SetBusy(anim.HammerFrame(m.anim.Frame(), m.anim.Settings(), m.theme) + " " + m.theme.CaptureInfo.Render(m.cap.busy))
	}
	inner := w - 4 // border + padding
	title := m.theme.PaletteGroup.Render("Quick capture")
	if def := m.captureDefault(); def != "" {
		title += m.theme.CaptureInfo.Render(" · " + m.projectWord(def))
	}
	body := ui.Box(title, inner, 1) + "\n" + c.View(inner)
	view := m.theme.CaptureBox.Width(w).Render(body)
	x := (m.width - lipgloss.Width(view)) / 2
	y := max(m.height/3-lipgloss.Height(view)/2, 0)
	off, _ := m.slideIn.At(m.anim.Frame())
	y = max(y+int(off), 0)
	return lipgloss.NewLayer(view).X(x).Y(y).Z(1)
}

// projectWord names Plane project id for the overlay title.
func (m Model) projectWord(id string) string {
	for _, p := range m.cap.cat.Projects {
		if p.ID == id {
			if p.Identifier != "" {
				return "#" + p.Identifier
			}
			return p.Name
		}
	}
	if m.project != nil {
		return m.project.Name
	}
	return ""
}
