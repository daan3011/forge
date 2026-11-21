package projects

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
)

// How a forge project finds its Plane project, shared by the Plane tab
// and `forge plane ls`:
//
//  1. the forge project, by name (or the repo you're in);
//  2. its saved link: a plane_project_id pinned in config or chosen
//     before (or "none", chosen before);
//  3. the automatic match: the Plane project whose identifier or name
//     equals the repo name (PlaneMatch);
//  4. for a typed name only: a Plane project's identifier, name or UUID.

// PlaneSource says where a Plane link came from.
type PlaneSource int

// Plane link sources.
const (
	PlaneUnresolved PlaneSource = iota // nothing yet: the tab asks, the CLI errors
	PlaneLinked                        // saved link or pinned plane_project_id
	PlaneNone                          // chosen before: the repo has no Plane project
	PlaneMatched                       // automatic match, not saved yet
	PlaneNamed                         // the typed name is a Plane project (step 4)
)

// PlaneLinkOf is steps 2 and 3 for one forge project: its saved link,
// else the automatic match among ps (which may be nil when not loaded).
func PlaneLinkOf(p config.Project, ps []plane.Project) (id string, src PlaneSource) {
	switch {
	case p.PlaneProjectID != "":
		return p.PlaneProjectID, PlaneLinked
	case p.PlaneDecided:
		return "", PlaneNone
	}
	if id, ok := PlaneMatch(p.Repo, ps); ok {
		return id, PlaneMatched
	}
	return "", PlaneUnresolved
}

// PlaneResolution is the Plane project ResolvePlane found.
type PlaneResolution struct {
	ID     string
	Source PlaneSource
	// Project is the forge project it belongs to; nil when the name was a
	// Plane project's (PlaneNamed).
	Project *config.Project
	// Plane is the Plane project when the list was fetched (zero otherwise).
	Plane plane.Project
}

// ResolvePlane runs all four steps for arg, a forge project name or a
// Plane project identifier, name or UUID. An empty arg means here (the
// forge project you're in), else the only project with a saved link. The
// Plane project list is fetched with list only when a step needs it.
// Without a match the error lists the workspace's Plane projects.
func ResolvePlane(ctx context.Context, arg, here string, projects []config.Project,
	list func(context.Context) ([]plane.Project, error),
) (PlaneResolution, error) {
	var (
		ps      []plane.Project
		fetched bool
	)
	fetch := func() ([]plane.Project, error) {
		if !fetched {
			got, err := list(ctx)
			if err != nil {
				return nil, fmt.Errorf("listing Plane projects: %w", err)
			}
			ps, fetched = got, true
		}
		return ps, nil
	}
	byID := func(id string) plane.Project {
		if i := slices.IndexFunc(ps, func(p plane.Project) bool { return p.ID == id }); i >= 0 {
			return ps[i]
		}
		return plane.Project{}
	}

	// 1. The forge project.
	arg = strings.TrimSpace(arg)
	local, err := localProject(arg, here, projects)
	if err != nil {
		return PlaneResolution{}, err
	}

	// 2 and 3. Its saved link, else the automatic match.
	src := PlaneUnresolved
	if local != nil {
		var id string
		id, src = PlaneLinkOf(*local, nil)
		if src == PlaneUnresolved {
			all, err := fetch()
			if err != nil {
				return PlaneResolution{}, err
			}
			id, src = PlaneLinkOf(*local, all)
		}
		if id != "" {
			return PlaneResolution{ID: id, Source: src, Project: local, Plane: byID(id)}, nil
		}
	}

	// 4. A typed name may still be a Plane project's.
	if arg != "" {
		all, err := fetch()
		if err != nil {
			return PlaneResolution{}, err
		}
		for _, p := range all {
			if p.ID == arg || strings.EqualFold(p.Identifier, arg) || strings.EqualFold(p.Name, arg) {
				return PlaneResolution{ID: p.ID, Source: PlaneNamed, Plane: p}, nil
			}
		}
	}

	all, err := fetch()
	if err != nil {
		return PlaneResolution{}, err
	}
	var msg string
	switch {
	case local != nil && src == PlaneNone:
		msg = fmt.Sprintf("%s has no Plane project (chosen before; change it in the Plane tab: ctrl+k → Plane: link project)", local.Name)
	case local != nil:
		msg = fmt.Sprintf("no Plane project matches %s (%s)", local.Name, local.Repo)
	case arg != "":
		msg = fmt.Sprintf("no forge or Plane project %q", arg)
	default:
		msg = "no forge project is linked to Plane"
	}
	return PlaneResolution{}, errors.New(msg + "; " + planeChoices(all))
}

// localProject is step 1: the forge project named arg; for an empty arg,
// the one you're in, else the only one with a saved link. nil when arg
// names no forge project.
func localProject(arg, here string, projects []config.Project) (*config.Project, error) {
	name := arg
	if name == "" {
		name = here
	}
	if name != "" {
		for i := range projects {
			if strings.EqualFold(projects[i].Name, name) {
				return &projects[i], nil
			}
		}
		return nil, nil
	}
	var linked []*config.Project
	for i := range projects {
		if projects[i].PlaneProjectID != "" {
			linked = append(linked, &projects[i])
		}
	}
	switch len(linked) {
	case 0:
		return nil, nil
	case 1:
		return linked[0], nil
	}
	names := make([]string, len(linked))
	for i, p := range linked {
		names[i] = p.Name
	}
	return nil, fmt.Errorf("several projects use Plane; pick one of: %s", strings.Join(names, ", "))
}

// planeChoices names the workspace's Plane projects for an error:
// "name one of: Anvil (ANV), Forge (FORGE)".
func planeChoices(ps []plane.Project) string {
	var names []string
	for _, p := range ps {
		if !p.ArchivedAt.IsZero() {
			continue
		}
		n := p.Name
		if p.Identifier != "" {
			n += " (" + p.Identifier + ")"
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		return "the Plane workspace has no projects"
	}
	slices.SortFunc(names, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return "name one of: " + strings.Join(names, ", ")
}
