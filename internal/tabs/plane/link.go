package plane

import (
	"context"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/projects"
	"github.com/daan3011/forge/internal/tabs"
)

// Projects are detected, not configured, so their Plane projects are
// found here: a repo whose name matches a Plane project's identifier or
// name is linked automatically; for the active project without a match
// the tab asks once ("Which Plane project goes with owner/name?"). Links
// are saved through the ProjectLinker and reported to the root model with
// tabs.PlaneLinkedMsg, which updates the shared project list.

// linkState is the tab's Plane-linking state.
type linkState struct {
	planeProjects []plane.Project
	loaded        bool
	loading       bool
	pending       map[string]bool // repos whose link is being saved
	asked         map[string]bool // repos the picker was shown for this run
	pickAfterLoad bool            // the palette asked for the picker before the list loaded
}

func newLinkState() linkState {
	return linkState{pending: map[string]bool{}, asked: map[string]bool{}}
}

// planeProjectsMsg carries the workspace's Plane projects.
type planeProjectsMsg struct {
	projects []plane.Project
	err      error
}

// TargetTab implements tabs.TargetedMsg.
func (planeProjectsMsg) TargetTab() string { return Title }

// linked reports whether p's Plane link is known (a project, or none).
func linked(p *config.Project) bool { return p.PlaneDecided || p.PlaneProjectID != "" }

// undecided are the projects whose Plane link isn't known yet.
func (t *Tab) undecided() []*config.Project {
	var out []*config.Project
	for i := range t.cfg.Projects {
		p := &t.cfg.Projects[i]
		if p.Repo != "" && !linked(p) && !t.link.pending[strings.ToLower(p.Repo)] {
			out = append(out, p)
		}
	}
	return out
}

// activeUndecided reports whether the active project still needs a link.
func (t *Tab) activeUndecided() bool {
	return t.project != nil && t.project.Repo != "" && !linked(t.project) &&
		!t.link.pending[strings.ToLower(t.project.Repo)]
}

// ensureLinks loads the Plane projects when some repo has no link yet,
// then links what matches and asks about the active project.
func (t *Tab) ensureLinks() tea.Cmd {
	if t.api == nil || len(t.undecided()) == 0 {
		return nil
	}
	if !t.link.loaded {
		return t.loadPlaneProjects()
	}
	return t.autoLink()
}

func (t *Tab) loadPlaneProjects() tea.Cmd {
	if t.link.loading || t.api == nil {
		return nil
	}
	t.link.loading = true
	api := t.api
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), shortTimeout)
		defer cancel()
		ps, err := api.Projects(ctx)
		return planeProjectsMsg{projects: ps, err: err}
	}
}

func (t *Tab) planeProjectsLoaded(msg planeProjectsMsg) tea.Cmd {
	t.link.loading = false
	if msg.err != nil {
		t.link.pickAfterLoad = false
		t.fetchErr = msg.err
		return t.toastOnce(fmt.Errorf("listing projects: %w", msg.err))
	}
	t.link.planeProjects, t.link.loaded = msg.projects, true
	if t.link.pickAfterLoad {
		t.link.pickAfterLoad = false
		return t.openLinkPicker()
	}
	return t.autoLink()
}

// autoLink links every undecided repo that matches exactly one Plane
// project, then opens the picker for the active project if it is still
// undecided and wasn't asked about yet.
func (t *Tab) autoLink() tea.Cmd {
	var links []tabs.PlaneLink
	activeMatched := false
	for _, p := range t.undecided() {
		id, ok := projects.PlaneMatch(p.Repo, t.link.planeProjects)
		if !ok {
			continue
		}
		links = append(links, tabs.PlaneLink{Repo: p.Repo, PlaneProjectID: id})
		if t.project != nil && strings.EqualFold(p.Repo, t.project.Repo) {
			activeMatched = true
		}
	}
	cmds := []tea.Cmd{t.saveLinks(links)}
	if !activeMatched && t.activeUndecided() && !t.link.asked[strings.ToLower(t.project.Repo)] && t.dialog == nil {
		cmds = append(cmds, t.openLinkPicker())
	}
	return tea.Batch(cmds...)
}

// saveLinks stores links and reports them to the root model.
func (t *Tab) saveLinks(links []tabs.PlaneLink) tea.Cmd {
	if len(links) == 0 {
		return nil
	}
	for _, l := range links {
		t.link.pending[strings.ToLower(l.Repo)] = true
	}
	linker := t.linker
	return func() tea.Msg {
		if linker != nil {
			ctx, cancel := context.WithTimeout(context.Background(), shortTimeout)
			defer cancel()
			for _, l := range links {
				if err := linker.SetPlane(ctx, l.Repo, l.PlaneProjectID); err != nil {
					return linkFailedMsg{links: links, err: err}
				}
			}
		}
		return tabs.PlaneLinkedMsg{Links: links}
	}
}

// linkFailedMsg reports links that couldn't be saved.
type linkFailedMsg struct {
	links []tabs.PlaneLink
	err   error
}

// TargetTab implements tabs.TargetedMsg.
func (linkFailedMsg) TargetTab() string { return Title }

func (t *Tab) linkFailed(msg linkFailedMsg) tea.Cmd {
	for _, l := range msg.links {
		delete(t.link.pending, strings.ToLower(l.Repo))
	}
	return errCmd(fmt.Errorf("plane: save project link: %w", msg.err))
}

// linkProject is the palette command: pick the active project's Plane
// project, again.
func (t *Tab) linkProject() tea.Cmd {
	switch {
	case t.api == nil:
		return errCmd(fmt.Errorf("plane isn't set up%s", errSuffix(t.apiErr)))
	case t.project == nil || t.project.Repo == "":
		return errCmd(fmt.Errorf("pick a project first (ctrl+p) to link it to Plane"))
	case !t.link.loaded:
		t.link.pickAfterLoad = true
		return t.loadPlaneProjects()
	}
	return t.openLinkPicker()
}

func errSuffix(err error) string {
	if err == nil {
		return ""
	}
	return ": " + err.Error()
}

// openLinkPicker asks which Plane project goes with the active project.
func (t *Tab) openLinkPicker() tea.Cmd {
	if t.project == nil || t.project.Repo == "" {
		return nil
	}
	repo := t.project.Repo
	t.link.asked[strings.ToLower(repo)] = true
	choice := t.project.PlaneProjectID
	ps := slices.Clone(t.link.planeProjects)
	slices.SortFunc(ps, func(a, b plane.Project) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	var opts []huh.Option[string]
	for _, p := range ps {
		if !p.ArchivedAt.IsZero() {
			continue
		}
		label := p.Name
		if p.Identifier != "" {
			label += " (" + p.Identifier + ")"
		}
		opts = append(opts, huh.NewOption(label, p.ID))
	}
	opts = append(opts, huh.NewOption("None: this repo has no Plane project", ""))
	if !linked(t.project) && len(opts) > 1 {
		choice = opts[0].Value // undecided: start on the first project, not None
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Key("plane_project").
			Title("Which Plane project goes with " + repo + "?").
			Description("forge remembers this; change it later with ctrl+k → Plane: link project").
			Options(opts...).Value(&choice),
	))
	return t.openDialog(form, func() tea.Cmd {
		return t.saveLinks([]tabs.PlaneLink{{Repo: repo, PlaneProjectID: choice}})
	})
}

// linkHint is the empty view text while the active project has no Plane
// project; "" when it has one.
func (t *Tab) linkHint() string {
	cmd := "ctrl+k → Plane: link project"
	switch {
	case !t.link.loaded && t.fetchErr != nil && len(t.undecided()) > 0:
		return "couldn't list Plane projects: " + reason(t.fetchErr)
	case t.project == nil:
		return "no project is linked to Plane yet · switch to one (ctrl+p) to link it"
	case t.project.Repo != "" && !linked(t.project) && !t.link.asked[strings.ToLower(t.project.Repo)]:
		return "linking " + t.project.Name + " to Plane…"
	case t.project.PlaneProjectID == "":
		return t.project.Name + " has no Plane project · " + cmd
	}
	return ""
}
