package capture

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/projects"
)

// Source is the part of the Plane client a catalog loads from.
type Source interface {
	Me(ctx context.Context) (plane.User, error)
	Projects(ctx context.Context) ([]plane.Project, error)
	Labels(ctx context.Context, projectID string) ([]plane.Label, error)
	ProjectMembers(ctx context.Context, projectID string) ([]plane.Member, error)
}

// Details are what +label and @person resolve against in one project.
type Details struct {
	Labels  []plane.Label
	Members []plane.Member
}

// Catalog is everything tokens resolve and complete against. The zero
// value is empty; Loaded says what is in it. Catalogs are values: Merge
// returns a new one and never changes either side.
type Catalog struct {
	Projects []plane.Project // the workspace's projects; nil until loaded
	// Aliases are the forge project names linked to each Plane project,
	// so #<forge name> works too.
	Aliases map[string][]string
	Details map[string]Details // by Plane project id
	Me      plane.User         // zero until loaded
}

// HasProjects reports whether the project list is loaded.
func (c Catalog) HasProjects() bool { return c.Projects != nil }

// HasDetails reports whether project id's labels and members are loaded.
func (c Catalog) HasDetails(id string) bool {
	_, ok := c.Details[id]
	return ok
}

// HasMe reports whether the current user is known.
func (c Catalog) HasMe() bool { return c.Me.ID != "" }

// Merge returns c with what o has loaded laid over it.
func (c Catalog) Merge(o Catalog) Catalog {
	out := Catalog{Projects: c.Projects, Me: c.Me, Aliases: maps.Clone(c.Aliases), Details: maps.Clone(c.Details)}
	if o.Projects != nil {
		out.Projects = o.Projects
	}
	if o.Me.ID != "" {
		out.Me = o.Me
	}
	if len(o.Aliases) > 0 {
		out.Aliases = maps.Clone(o.Aliases)
	}
	if out.Details == nil && len(o.Details) > 0 {
		out.Details = map[string]Details{}
	}
	maps.Copy(out.Details, o.Details)
	return out
}

// project finds a Plane project by id; zero when it isn't loaded.
func (c Catalog) project(id string) plane.Project {
	i := slices.IndexFunc(c.Projects, func(p plane.Project) bool { return p.ID == id })
	if i < 0 {
		return plane.Project{}
	}
	return c.Projects[i]
}

// aliases maps forge project names to their Plane project ids (saved link
// or automatic match), from config.
func aliases(forge []config.Project, ps []plane.Project) map[string][]string {
	out := map[string][]string{}
	for _, p := range forge {
		if id, _ := projects.PlaneLinkOf(p, ps); id != "" {
			out[id] = append(out[id], p.Name)
		}
	}
	return out
}

// Need is what Load fetches: the project list, the current user, and the
// labels and members of these Plane projects.
type Need struct {
	Projects bool
	Me       bool
	Details  []string
}

// Empty reports whether nothing is needed.
func (n Need) Empty() bool { return !n.Projects && !n.Me && len(n.Details) == 0 }

func (n Need) String() string {
	var what []string
	if n.Projects {
		what = append(what, "projects")
	}
	if n.Me {
		what = append(what, "you")
	}
	if len(n.Details) > 0 {
		what = append(what, "labels and members")
	}
	return strings.Join(what, ", ")
}

// Load fetches what n asks for, concurrently, into a catalog (merge it
// over the one you have). On errors it returns what did load, with them.
func Load(ctx context.Context, src Source, forge []config.Project, n Need) (Catalog, error) {
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		out  = Catalog{Details: map[string]Details{}}
		errs []error
	)
	fail := func(what string, err error) {
		mu.Lock()
		errs = append(errs, fmt.Errorf("plane: %s: %w", what, err))
		mu.Unlock()
	}
	if n.Projects {
		wg.Go(func() {
			ps, err := src.Projects(ctx)
			if err != nil {
				fail("projects", err)
				return
			}
			if ps == nil {
				ps = []plane.Project{}
			}
			mu.Lock()
			out.Projects, out.Aliases = ps, aliases(forge, ps)
			mu.Unlock()
		})
	}
	if n.Me {
		wg.Go(func() {
			me, err := src.Me(ctx)
			if err != nil {
				fail("you", err)
				return
			}
			mu.Lock()
			out.Me = me
			mu.Unlock()
		})
	}
	for _, id := range slices.Compact(slices.Sorted(slices.Values(n.Details))) {
		wg.Go(func() {
			var (
				d          Details
				lerr, merr error
				inner      sync.WaitGroup
			)
			inner.Go(func() { d.Labels, lerr = src.Labels(ctx, id) })
			inner.Go(func() { d.Members, merr = src.ProjectMembers(ctx, id) })
			inner.Wait()
			if err := errors.Join(lerr, merr); err != nil {
				fail("labels and members", err)
				return
			}
			mu.Lock()
			out.Details[id] = d
			mu.Unlock()
		})
	}
	wg.Wait()
	return out, errors.Join(errs...)
}

// Cached is the catalog the cache holds for n, read without the network;
// src only needs to be a plane.CacheReader (else the catalog is empty).
// Aliases come from forge's config with the cached projects.
func Cached(ctx context.Context, src any, forge []config.Project, n Need) Catalog {
	out := Catalog{}
	cr, ok := src.(plane.CacheReader)
	if !ok {
		return out
	}
	if got, ok := cr.CachedProjects(ctx); ok {
		out.Projects = got.Items
		if out.Projects == nil {
			out.Projects = []plane.Project{}
		}
		out.Aliases = aliases(forge, out.Projects)
	}
	if me, ok := cr.CachedMe(ctx); ok {
		out.Me = me
	}
	for _, id := range n.Details {
		ls, lok := cr.CachedLabels(ctx, id)
		ms, mok := cr.CachedProjectMembers(ctx, id)
		if lok && mok {
			if out.Details == nil {
				out.Details = map[string]Details{}
			}
			out.Details[id] = Details{Labels: ls.Items, Members: ms.Items}
		}
	}
	return out
}

// DefaultProject is the Plane project captures go to without a #project:
// forge project active's Plane link, else that of the only forge project
// linked to Plane, else "" (a #project is then required).
func DefaultProject(active *config.Project, forge []config.Project, ps []plane.Project) string {
	if active != nil {
		if id, _ := projects.PlaneLinkOf(*active, ps); id != "" {
			return id
		}
		return ""
	}
	var ids []string
	for _, p := range forge {
		if id, _ := projects.PlaneLinkOf(p, ps); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 1 {
		return ids[0]
	}
	return ""
}

// projectWord is the token value naming project p: its identifier, else
// its name.
func projectWord(p plane.Project) string {
	return cmp.Or(p.Identifier, tokenText(p.Name))
}
