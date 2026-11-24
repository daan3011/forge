package setup

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/projects"
	"github.com/daan3011/forge/internal/store"
)

// ProjectStore is the part of the store the projects step uses.
// *store.Projects satisfies it.
type ProjectStore interface {
	List(ctx context.Context) ([]store.Project, error)
	SetPlane(ctx context.Context, repo, id string) error
}

// ProjectRow is one discovered repo with its Plane link.
type ProjectRow struct {
	Name, Repo, Path string
	PlaneID          string
	PlaneName        string // "" when unlinked or Plane isn't loaded
	Source           projects.PlaneSource
}

// Discovery is what the projects step shows.
type Discovery struct {
	// Roots are the folders scanned: the configured ones, else forge's
	// defaults (Configured tells which).
	Roots      []string
	Configured bool
	Rows       []ProjectRow
}

// Discover scans the roots (configured, else the defaults) and links each
// repo to its Plane project: a saved link, else the automatic match.
func Discover(ctx context.Context, configured []string, overrides []config.Project, st ProjectStore,
	ps []plane.Project, cwd, home string,
) Discovery {
	roots := projects.Roots(expandAll(configured, home), cwd, home)
	found := projects.Scan(roots, cwd)
	var remembered []projects.Remembered
	if st != nil {
		rows, _ := st.List(ctx)
		for _, r := range rows {
			remembered = append(remembered, projects.Remembered{
				Repo: r.Repo, Path: r.Path, LastUsed: r.LastUsed,
				PlaneProjectID: r.PlaneProjectID, PlaneDecided: r.PlaneDecided,
			})
		}
	}
	merged := projects.Merge(found, remembered, overrides, nil)
	d := Discovery{Roots: roots, Configured: len(configured) > 0}
	for _, p := range merged {
		d.Rows = append(d.Rows, rowFor(p, ps))
	}
	return d
}

func rowFor(p config.Project, ps []plane.Project) ProjectRow {
	id, src := projects.PlaneLinkOf(p, ps)
	r := ProjectRow{Name: p.Name, Repo: p.Repo, Path: p.Path, PlaneID: id, Source: src}
	for _, pp := range ps {
		if pp.ID == id {
			r.PlaneName = pp.Name
		}
	}
	return r
}

// LinkLabel is how a row's Plane link reads in the list.
func (r ProjectRow) LinkLabel() string {
	switch r.Source {
	case projects.PlaneLinked:
		return orID(r.PlaneName, r.PlaneID) + " (linked)"
	case projects.PlaneMatched:
		return orID(r.PlaneName, r.PlaneID) + " (matched by name)"
	case projects.PlaneNone:
		return "no Plane project"
	}
	return "not linked"
}

func orID(name, id string) string {
	if name != "" {
		return name
	}
	return id
}

// SaveLinks stores the automatic matches as real links, so they stay when
// a project is renamed in Plane. Rows already linked or decided are left.
func SaveLinks(ctx context.Context, st ProjectStore, rows []ProjectRow) (saved int, err error) {
	for _, r := range rows {
		if r.Source != projects.PlaneMatched || r.Repo == "" {
			continue
		}
		if err := st.SetPlane(ctx, r.Repo, r.PlaneID); err != nil {
			return saved, err
		}
		saved++
	}
	return saved, nil
}

// expandAll expands ~ in configured roots (config does it on load; the
// step works on what it is about to write).
func expandAll(roots []string, home string) []string {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		out = append(out, ExpandHome(r, home))
	}
	return out
}

// ExpandHome turns a leading ~ into home.
func ExpandHome(p, home string) string {
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/") && home != "":
		return filepath.Join(home, p[2:])
	}
	return p
}

// ContractHome writes paths under home as ~/..., the way the config
// template does.
func ContractHome(p, home string) string {
	if home == "" {
		return p
	}
	p = filepath.Clean(p)
	if p == home {
		return "~"
	}
	if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
		return "~/" + rel
	}
	return p
}

// AddRoot adds dir to the configured roots (as ~/... when under home),
// skipping a folder that is already there in any spelling.
func AddRoot(roots []string, dir, home string) []string {
	abs := filepath.Clean(ExpandHome(dir, home))
	for _, r := range roots {
		if filepath.Clean(ExpandHome(r, home)) == abs {
			return roots
		}
	}
	return append(slices.Clone(roots), ContractHome(abs, home))
}

// RemoveRoot drops dir (in any spelling) from the configured roots.
func RemoveRoot(roots []string, dir, home string) []string {
	abs := filepath.Clean(ExpandHome(dir, home))
	return slices.DeleteFunc(slices.Clone(roots), func(r string) bool {
		return filepath.Clean(ExpandHome(r, home)) == abs
	})
}

// RootSets is the config value for the roots: the list itself.
func RootSets(roots []string) []config.Set {
	if roots == nil {
		roots = []string{}
	}
	return []config.Set{{Key: "project_roots", Value: roots}}
}
