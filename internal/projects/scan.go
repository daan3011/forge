package projects

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DefaultRoots are the code folders scanned (relative to home) when
// project_roots isn't set, besides the parent of the repo forge starts in.
var DefaultRoots = []string{"Documents/Projects", "code", "src", "dev"}

// Found is a checkout with a GitHub remote.
type Found struct {
	Path string
	Repo string // owner/name
}

// RepoRoot returns the checkout containing dir (dir or a parent with a
// .git), or ok false.
func RepoRoot(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	dir = filepath.Clean(dir)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// Roots are the folders to scan: configured, else the parent of the repo
// containing cwd plus the DefaultRoots under home that exist.
func Roots(configured []string, cwd, home string) []string {
	if len(configured) > 0 {
		return dedupe(configured)
	}
	var out []string
	if root, ok := RepoRoot(cwd); ok {
		out = append(out, filepath.Dir(root))
	}
	if home != "" {
		for _, r := range DefaultRoots {
			p := filepath.Join(home, r)
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				out = append(out, p)
			}
		}
	}
	return dedupe(out)
}

// Scan finds the checkouts with a GitHub remote directly under each root,
// and the one containing cwd. Unreadable folders are skipped.
func Scan(roots []string, cwd string) []Found {
	seen := map[string]bool{}
	var out []Found
	add := func(dir string) {
		dir = filepath.Clean(dir)
		if seen[dir] {
			return
		}
		seen[dir] = true
		if repo, ok := RepoOf(dir); ok {
			out = append(out, Found{Path: dir, Repo: repo})
		}
	}
	if root, ok := RepoRoot(cwd); ok {
		add(root)
	}
	for _, r := range roots {
		entries, err := os.ReadDir(r)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p := filepath.Join(r, e.Name())
			if fi, err := os.Stat(p); err != nil || !fi.IsDir() { // follows symlinks
				continue
			}
			add(p)
		}
	}
	slices.SortFunc(out, func(a, b Found) int { return strings.Compare(a.Path, b.Path) })
	return out
}

func dedupe(paths []string) []string {
	var out []string
	for _, p := range paths {
		p = filepath.Clean(p)
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}
