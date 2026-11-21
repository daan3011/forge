package projects

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/daan3011/forge/internal/config"
)

// Remembered is a repo forge has seen before (stored in its database): it
// was started in it, ran a session in it or switched to it.
type Remembered struct {
	Repo     string
	Path     string // last known checkout; "" = none
	LastUsed time.Time
	// PlaneProjectID is the Plane project linked to the repo; with
	// PlaneDecided and an empty id the repo has none.
	PlaneProjectID string
	PlaneDecided   bool
}

// Merge builds the project list from detected checkouts, remembered repos
// and config overrides, keyed by repo (case-insensitive):
//   - a detected checkout's path wins over a remembered one;
//   - a remembered repo whose path is gone (exists false) keeps the repo
//     without a path, so GitHub tabs still show it;
//   - config entries override field by field, and entries for repos found
//     nowhere else are added as they are;
//   - names default to the repo name, owner/name where two repos clash;
//   - the Plane link comes from config, else the database;
//   - the order is most recently used first, then by name.
//
// exists reports whether a path is still there (nil: os.Stat).
func Merge(found []Found, remembered []Remembered, overrides []config.Project, exists func(string) bool) []config.Project {
	if exists == nil {
		exists = func(p string) bool { _, err := os.Stat(p); return err == nil }
	}
	type entry struct {
		p        config.Project
		lastUsed time.Time
		named    bool // the name came from config
	}
	var order []string
	byRepo := map[string]*entry{}
	get := func(repo string) *entry {
		k := strings.ToLower(repo)
		e := byRepo[k]
		if e == nil {
			e = &entry{p: config.Project{Repo: repo}}
			byRepo[k] = e
			order = append(order, k)
		}
		return e
	}
	for _, f := range found {
		e := get(f.Repo)
		// Several clones of one repo: keep the folder named after it,
		// else the first found.
		if e.p.Path == "" || !namedAfter(e.p.Path, e.p.Repo) && namedAfter(f.Path, f.Repo) {
			e.p.Path = f.Path
		}
	}
	for _, r := range remembered {
		if r.Repo == "" {
			continue
		}
		e := get(r.Repo)
		e.lastUsed = r.LastUsed
		if e.p.Path == "" && r.Path != "" && exists(r.Path) {
			e.p.Path = r.Path
		}
		if r.PlaneDecided {
			e.p.PlaneProjectID, e.p.PlaneDecided = r.PlaneProjectID, true
		}
	}
	for _, o := range overrides {
		if o.Repo == "" {
			continue
		}
		e := get(o.Repo)
		if o.Name != "" {
			e.p.Name, e.named = o.Name, true
		}
		if o.Path != "" {
			e.p.Path = o.Path
		}
		if o.PlaneProjectID != "" {
			e.p.PlaneProjectID, e.p.PlaneDecided = o.PlaneProjectID, true
		}
		if o.DocsDir != "" {
			e.p.DocsDir = o.DocsDir
		}
		if len(o.WorktreeCopy) > 0 {
			e.p.WorktreeCopy = o.WorktreeCopy
		}
	}

	// Names: the repo name, unless it clashes with another project's.
	taken := map[string]int{}
	for _, k := range order {
		e := byRepo[k]
		if e.named {
			taken[strings.ToLower(e.p.Name)] += 2 // config names always win
		} else {
			taken[strings.ToLower(e.p.RepoName())]++
		}
	}
	out := make([]config.Project, 0, len(order))
	used := map[string]time.Time{}
	for _, k := range order {
		e := byRepo[k]
		if !e.named {
			e.p.Name = e.p.RepoName()
			if taken[strings.ToLower(e.p.Name)] > 1 {
				e.p.Name = e.p.Repo
			}
		}
		used[e.p.Name] = e.lastUsed
		out = append(out, e.p)
	}
	slices.SortStableFunc(out, func(a, b config.Project) int {
		if c := used[b.Name].Compare(used[a.Name]); c != 0 {
			return c
		}
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}

// namedAfter reports whether path's folder has the repo's name.
func namedAfter(path, repo string) bool {
	_, name, _ := strings.Cut(repo, "/")
	return strings.EqualFold(filepath.Base(path), name)
}
