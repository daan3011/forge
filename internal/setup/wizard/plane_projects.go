package wizard

import (
	"context"
	"errors"
	"fmt"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/projects"
	"github.com/daan3011/forge/internal/setup"
	"github.com/daan3011/forge/internal/ui/anim"
)

const planeTimeout = 20 * time.Second

// --- 3. Plane --------------------------------------------------------------

type planeStep struct {
	base
	in     setup.PlaneInput
	cloud  string // "cloud" / "self" for the select
	busy   bool
	tested bool
	check  setup.PlaneCheck
	err    error
}

type planeTestedMsg struct {
	in    setup.PlaneInput
	check setup.PlaneCheck
	err   error
}

func newPlaneStep(m *Model) *planeStep {
	return &planeStep{base: base{m: m}, in: setup.PlaneInputFrom(m.cfg.Plane)}
}

func (s *planeStep) enter() tea.Cmd { return s.edit() }

// edit opens the Plane form, filled from the current values.
func (s *planeStep) edit() tea.Cmd {
	s.cloud = "cloud"
	if !s.in.Cloud {
		s.cloud = "self"
	}
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().Title("Where is your Plane?").
				Options(huh.NewOption("Plane cloud (app.plane.so)", "cloud"), huh.NewOption("Self-hosted", "self")).
				Value(&s.cloud),
		),
		huh.NewGroup(
			huh.NewInput().Title("Base URL").Description("your instance; it must serve /api/v2").
				Placeholder("https://plane.example.com").Value(&s.in.BaseURL),
		).WithHideFunc(func() bool { return s.cloud == "cloud" }),
		huh.NewGroup(
			huh.NewInput().Title("Workspace slug").Description("the part after the host in Plane URLs").Value(&s.in.Workspace),
			huh.NewInput().Title("API key variable").
				Description("the NAME of the environment variable holding your key; forge never stores the key itself").
				Value(&s.in.KeyEnv),
		),
	)
	return s.openForm(form, s.test)
}

// test checks the connection with the form's values.
func (s *planeStep) test() tea.Cmd {
	s.in.Cloud = s.cloud == "cloud"
	in, base, getenv, newPlane := s.in, s.m.cfg.Plane, s.m.d.Getenv, s.m.d.NewPlane
	if newPlane == nil {
		return nil
	}
	s.busy = true
	return func() tea.Msg {
		ctx, cancel := opCtx(planeTimeout)
		defer cancel()
		c, err := setup.TestPlane(ctx, getenv, newPlane, in, base)
		return planeTestedMsg{in: in, check: c, err: err}
	}
}

func (s *planeStep) update(msg tea.Msg) tea.Cmd {
	t, ok := msg.(planeTestedMsg)
	if !ok {
		cmd, _ := s.updateForm(msg)
		return cmd
	}
	s.busy, s.tested, s.check, s.err = false, true, t.check, t.err
	var cmds []tea.Cmd
	// Valid settings are written even when the key is missing: only the
	// shell line is left to add.
	if t.in.Validate() == nil {
		cmds = append(cmds, s.m.merge(t.in.Sets()...))
	}
	var r setup.Result
	switch {
	case t.err == nil:
		s.m.planeProjects = t.check.Projects
		r = setup.Result{Status: setup.Done, Detail: fmt.Sprintf("connected as %s; %d project(s) in %s", t.check.User, len(t.check.Projects), t.in.Workspace)}
	case errors.Is(t.err, setup.ErrNoKey):
		rc, line := setup.ShellLine(s.m.d.Getenv("SHELL"), t.in.KeyEnv)
		r = setup.Result{Status: setup.Failed, Detail: "$" + t.in.KeyEnv + " is not set", Fix: "add `" + line + "` to " + rc + " and open a new shell"}
	default:
		r = setup.Result{Status: setup.Failed, Detail: t.err.Error(), Fix: "check the settings (e to edit), then r to test again"}
	}
	return tea.Batch(append(cmds, s.m.setResult(setup.StepPlane, r))...)
}

func (s *planeStep) key(msg tea.KeyPressMsg) tea.Cmd {
	if cmd, ok := s.updateForm(msg); ok {
		return cmd
	}
	k := s.m.keys.Setup
	switch {
	case key.Matches(msg, k.Edit) && !s.busy:
		return s.edit()
	case key.Matches(msg, k.Rerun) && !s.busy && s.tested:
		return s.test()
	}
	return nil
}

func (s *planeStep) accept() (tea.Cmd, bool) {
	if s.busy {
		return nil, false
	}
	if !s.tested { // nothing entered: same as skipping
		return s.m.setResult(setup.StepPlane, setup.Result{Status: setup.Skipped, Detail: "skipped"}), true
	}
	return nil, true
}

func (s *planeStep) view(w, _ int) []string {
	if s.form != nil {
		return s.formView()
	}
	th := s.m.theme
	if s.busy {
		return []string{s.m.spinner("connecting to " + s.in.Base() + "…")}
	}
	out := []string{
		th.DetailLabel.Render("base URL   ") + th.Body.Render(s.in.Base()),
		th.DetailLabel.Render("workspace  ") + th.Body.Render(s.in.Workspace),
		th.DetailLabel.Render("key from   ") + th.Body.Render("$"+s.in.KeyEnv),
		"",
	}
	switch {
	case !s.tested:
		out = append(out, th.Hint.Render("Press "+s.m.keys.Setup.Edit.Help().Key+" to set up Plane, or s to skip it."))
	case s.err == nil:
		out = append(out, th.DoctorOK.Render("✓ connected as "+s.check.User))
		for i, p := range s.check.Projects {
			if i == 8 {
				out = append(out, th.Hint.Render(fmt.Sprintf("  … and %d more", len(s.check.Projects)-i)))
				break
			}
			out = append(out, th.Body.Render("  "+p.Identifier+"  "+p.Name))
		}
	case errors.Is(s.err, setup.ErrNoKey):
		rc, line := setup.ShellLine(s.m.d.Getenv("SHELL"), s.in.KeyEnv)
		out = append(out,
			th.DoctorFail.Render("✗ $"+s.in.KeyEnv+" isn't set in this shell."),
			th.Body.Render(wrapText("Create a key in Plane (Profile settings → API tokens), then add this line to "+rc+":", w)),
			"", s.m.code(line), "",
			th.Hint.Render(wrapText("Open a new shell (or `source "+rc+"`) and run `forge setup plane`. The settings above are saved already.", w)))
	default:
		out = append(out, th.DoctorFail.Render(wrapText("✗ "+s.err.Error(), w)))
	}
	return out
}

func (s *planeStep) help() []key.Binding {
	k := s.m.keys.Setup
	if s.tested {
		return []key.Binding{k.Edit, k.Rerun}
	}
	return []key.Binding{k.Edit}
}
func (s *planeStep) ticking() bool { return s.busy && anim.Ticks(s.m.eng.Settings()) }

// --- 4. Projects -----------------------------------------------------------

type projectsStep struct {
	base
	busy bool
	disc setup.Discovery
	ps   []plane.Project
	sel  int // over roots, then rows
	// roots are the configured roots as written in the config (~/...).
	roots []string
}

type discoveredMsg struct {
	disc setup.Discovery
	ps   []plane.Project
}

type linkSavedMsg struct{ err error }

func (s *projectsStep) enter() tea.Cmd {
	s.roots = append([]string(nil), s.m.cfg.ProjectRoots...)
	for i, r := range s.roots {
		s.roots[i] = setup.ContractHome(r, s.m.d.Home)
	}
	return s.discover()
}

// discover scans the roots and loads the Plane projects (when Plane is
// set up and the plane step didn't already list them).
func (s *projectsStep) discover() tea.Cmd {
	s.busy = true
	roots, overrides, st, cwd, home := s.roots, s.m.cfg.Projects, s.m.d.Projects, s.m.d.Cwd, s.m.d.Home
	ps, cfg, newPlane := s.m.planeProjects, s.m.cfg.Plane, s.m.d.NewPlane
	return func() tea.Msg {
		ctx, cancel := opCtx(planeTimeout)
		defer cancel()
		if ps == nil && cfg.Workspace != "" && newPlane != nil {
			if key, err := cfg.ResolveAPIKey(); err == nil && key != "" {
				if api, err := newPlane(cfg, key); err == nil {
					ps, _ = api.Projects(ctx)
				}
			}
		}
		return discoveredMsg{disc: setup.Discover(ctx, roots, overrides, st, ps, cwd, home), ps: ps}
	}
}

func (s *projectsStep) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case discoveredMsg:
		s.busy, s.disc, s.ps = false, msg.disc, msg.ps
		s.sel = min(s.sel, max(s.items()-1, 0))
	case linkSavedMsg:
		if msg.err != nil {
			s.m.flash = "couldn't save the link: " + msg.err.Error()
			return nil
		}
		return s.discover()
	default:
		cmd, _ := s.updateForm(msg)
		return cmd
	}
	return nil
}

func (s *projectsStep) items() int { return len(s.disc.Roots) + len(s.disc.Rows) }

// selectedRow is the project under the cursor, if the cursor is on one.
func (s *projectsStep) selectedRow() (setup.ProjectRow, bool) {
	i := s.sel - len(s.disc.Roots)
	if i < 0 || i >= len(s.disc.Rows) {
		return setup.ProjectRow{}, false
	}
	return s.disc.Rows[i], true
}

func (s *projectsStep) key(msg tea.KeyPressMsg) tea.Cmd {
	if cmd, ok := s.updateForm(msg); ok {
		return cmd
	}
	if s.busy {
		return nil
	}
	k := s.m.keys.Setup
	switch {
	case key.Matches(msg, k.Up):
		s.sel = max(s.sel-1, 0)
	case key.Matches(msg, k.Down):
		s.sel = min(s.sel+1, max(s.items()-1, 0))
	case key.Matches(msg, k.Rerun):
		return s.discover()
	case key.Matches(msg, k.AddRoot):
		return s.addRoot()
	case key.Matches(msg, k.RemoveRoot) && s.sel < len(s.disc.Roots):
		// Removing a default root turns the defaults into a configured list.
		roots := s.roots
		if !s.disc.Configured {
			roots = nil
			for _, r := range s.disc.Roots {
				roots = append(roots, setup.ContractHome(r, s.m.d.Home))
			}
		}
		s.roots = setup.RemoveRoot(roots, s.disc.Roots[s.sel], s.m.d.Home)
		return tea.Batch(s.m.merge(setup.RootSets(s.roots)...), s.discover())
	case key.Matches(msg, k.FixLink):
		if r, ok := s.selectedRow(); ok {
			return s.fixLink(r)
		}
	}
	return nil
}

// addRoot picks a folder and adds it to project_roots.
func (s *projectsStep) addRoot() tea.Cmd {
	var dir string
	start := s.m.d.Home
	if start == "" {
		start = "/"
	}
	_, h := s.m.split.RightSize()
	picker := huh.NewFilePicker().Title("Add a project root").
		Description("a folder whose sub-folders are git checkouts · enter picks, l / → opens").
		CurrentDirectory(start).DirAllowed(true).FileAllowed(false).Picking(true).
		Height(max(h-10, 5)).Value(&dir)
	return s.openForm(huh.NewForm(huh.NewGroup(picker)), func() tea.Cmd {
		if dir == "" {
			return nil
		}
		roots := s.roots
		if !s.disc.Configured {
			roots = nil // the first configured root replaces the defaults
		}
		s.roots = setup.AddRoot(roots, dir, s.m.d.Home)
		return tea.Batch(s.m.merge(setup.RootSets(s.roots)...), s.discover())
	})
}

// fixLink picks the Plane project of a repo (or none).
func (s *projectsStep) fixLink(r setup.ProjectRow) tea.Cmd {
	if len(s.ps) == 0 {
		s.m.flash = "no Plane projects loaded: set up Plane first (forge setup plane)"
		return nil
	}
	id := r.PlaneID
	opts := []huh.Option[string]{huh.NewOption("No Plane project", "")}
	for _, p := range s.ps {
		opts = append(opts, huh.NewOption(p.Identifier+"  "+p.Name, p.ID))
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Plane project for " + r.Repo).Options(opts...).Value(&id),
	))
	st, repo := s.m.d.Projects, r.Repo
	return s.openForm(form, func() tea.Cmd {
		if st == nil {
			return nil
		}
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return linkSavedMsg{st.SetPlane(ctx, repo, id)}
		}
	})
}

func (s *projectsStep) accept() (tea.Cmd, bool) {
	if s.busy {
		return nil, false
	}
	rows, st := s.disc.Rows, s.m.d.Projects
	linked := 0
	for _, r := range rows {
		if r.Source == projects.PlaneLinked || r.Source == projects.PlaneMatched {
			linked++
		}
	}
	detail := fmt.Sprintf("%d repo(s) found", len(rows))
	if len(s.ps) > 0 {
		detail += fmt.Sprintf(", %d linked to Plane", linked)
	}
	r := setup.Result{Status: setup.Done, Detail: detail}
	if len(rows) == 0 {
		r = setup.Result{Status: setup.Failed, Detail: "no repos with a GitHub remote found", Fix: "add a project root (+) where your checkouts live"}
	}
	save := func() tea.Msg {
		if st == nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := setup.SaveLinks(ctx, st, rows)
		return savedMsg{err}
	}
	return tea.Batch(save, s.m.setResult(setup.StepProjects, r)), true
}

func (s *projectsStep) view(w, _ int) []string {
	if s.form != nil {
		return s.formView()
	}
	th := s.m.theme
	if s.busy {
		return []string{s.m.spinner("looking for repos…")}
	}
	label := "Project roots (forge's defaults)"
	if s.disc.Configured {
		label = "Project roots (project_roots)"
	}
	out := []string{th.DetailLabel.Render(label)}
	if len(s.disc.Roots) == 0 {
		out = append(out, th.Hint.Render("  none yet: press + to add one"))
	}
	for i, r := range s.disc.Roots {
		out = append(out, s.row(i, "  "+setup.ContractHome(r, s.m.d.Home), w))
	}
	out = append(out, "", th.DetailLabel.Render(fmt.Sprintf("Repos (%d)", len(s.disc.Rows))))
	for i, r := range s.disc.Rows {
		link := r.LinkLabel()
		if len(s.ps) == 0 && r.Source == projects.PlaneUnresolved {
			link = ""
		}
		out = append(out, s.row(len(s.disc.Roots)+i, "  "+padRight(r.Repo, 28)+" "+link, w))
	}
	return out
}

func (s *projectsStep) row(i int, text string, w int) string {
	if i == s.sel {
		return s.m.theme.TableSelected.Render(padRight(truncate(text, w), w))
	}
	return s.m.theme.Body.Render(truncate(text, w))
}

func truncate(s string, w int) string {
	r := []rune(s)
	if len(r) <= w || w < 2 {
		return s
	}
	return string(r[:w-1]) + "…"
}

func (s *projectsStep) help() []key.Binding {
	k := s.m.keys.Setup
	bs := []key.Binding{k.Up, k.Down, k.AddRoot}
	if s.sel < len(s.disc.Roots) {
		bs = append(bs, k.RemoveRoot)
	} else if len(s.ps) > 0 {
		bs = append(bs, k.FixLink)
	}
	return append(bs, k.Rerun)
}
func (s *projectsStep) ticking() bool { return s.busy && anim.Ticks(s.m.eng.Settings()) }
