package packs

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	docfs "github.com/daan3011/forge/internal/docs"
)

// Resolved is a pack expanded against a project.
type Resolved struct {
	Name     string
	Preamble string
	// Paths are the absolute files the pack names, in pack order, without
	// duplicates.
	Paths []string
	// Missing are the file / doc entries that matched nothing.
	Missing []string
}

// skipDirs are never walked by ** (and never matched by it).
var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true}

// Expand resolves p's files against repoRoot and its docs against docsRoot.
// A file entry naming a folder means every file below it. Docs may also be
// ~/… or absolute.
func Expand(p Pack, repoRoot, docsRoot string) Resolved {
	r := Resolved{Name: p.Name, Preamble: strings.TrimSpace(p.Preamble)}
	seen := map[string]bool{}
	add := func(entry string, got []string) {
		if len(got) == 0 {
			r.Missing = append(r.Missing, entry)
			return
		}
		for _, g := range got {
			if !seen[g] {
				seen[g] = true
				r.Paths = append(r.Paths, g)
			}
		}
	}
	for _, f := range p.Files {
		if repoRoot == "" {
			r.Missing = append(r.Missing, f)
			continue
		}
		add(f, match(repoRoot, f))
	}
	for _, d := range p.Docs {
		root := docsRoot
		switch {
		case d == "~" || strings.HasPrefix(d, "~/"):
			home, err := os.UserHomeDir()
			if err != nil {
				r.Missing = append(r.Missing, d)
				continue
			}
			root, d = home, strings.TrimPrefix(strings.TrimPrefix(d, "~"), "/")
		case filepath.IsAbs(d):
			root, d = string(filepath.Separator), strings.TrimPrefix(d, string(filepath.Separator))
		}
		if root == "" {
			r.Missing = append(r.Missing, d)
			continue
		}
		add(d, match(root, d))
	}
	return r
}

// match returns the absolute files under root that pattern (slash-separated,
// relative to root) names, sorted. Folders expand to the files below them.
func match(root, pattern string) []string {
	pattern = strings.Trim(filepath.ToSlash(pattern), "/")
	if pattern == "" {
		return nil
	}
	if !IsGlob(pattern) {
		abs := filepath.Join(root, filepath.FromSlash(pattern))
		return filesAt(abs)
	}
	segs := strings.Split(pattern, "/")
	// Walk from the longest literal prefix.
	i := slices.IndexFunc(segs, IsGlob)
	base := filepath.Join(root, filepath.FromSlash(strings.Join(segs[:i], "/")))
	rest := segs[i:]
	var out []string
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable folders are skipped
		}
		if d.IsDir() {
			if p != base && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return nil //nolint:nilerr // can't happen under base
		}
		if matchSegs(rest, strings.Split(filepath.ToSlash(rel), "/")) && regular(p, d) {
			out = append(out, p)
		}
		return nil
	})
	slices.Sort(out)
	return out
}

// regular reports whether the walked entry is a regular file (following a
// symlink).
func regular(p string, d fs.DirEntry) bool {
	if d.Type().IsRegular() {
		return true
	}
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// filesAt is abs itself when it is a file, the files below it when it is a
// folder, else nothing.
func filesAt(abs string) []string {
	fi, err := os.Stat(abs)
	if err != nil {
		return nil
	}
	if fi.Mode().IsRegular() {
		return []string{abs}
	}
	if !fi.IsDir() {
		return nil
	}
	return match(abs, "**")
}

// matchSegs matches path segments against pattern segments, where "**"
// matches any number of segments (including none).
func matchSegs(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			pat = pat[1:]
			if len(pat) == 0 {
				return true
			}
			for k := range len(name) + 1 {
				if matchSegs(pat, name[k:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], name[0]); err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// Tokens is a rough token estimate for the files: bytes / 4, like the
// chars/4 estimate of memory files. Unreadable files count as 0.
func Tokens(paths []string) int {
	n := 0
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			n += int((fi.Size() + 3) / 4)
		}
	}
	return n
}

// Paths returns the files of every resolved pack, without duplicates.
func Paths(rs []Resolved) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range rs {
		for _, p := range r.Paths {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// Names returns the packs' names.
func Names(rs []Resolved) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}

// Prompt puts the packs in front of rest: each preamble, then one line
// mentioning every file (plan is docfs.Plan of Paths(rs)), then rest.
// Without packs it is rest unchanged.
func Prompt(rs []Resolved, plan []docfs.Attachment, rest string) string {
	if len(rs) == 0 {
		return rest
	}
	var parts []string
	for _, r := range rs {
		if r.Preamble != "" {
			parts = append(parts, r.Preamble)
		}
	}
	if len(plan) > 0 {
		label := "context pack " + strings.Join(Names(rs), ", ")
		if len(rs) > 1 {
			label = "context packs " + strings.Join(Names(rs), ", ")
		}
		parts = append(parts, "Read these files first ("+label+"): "+docfs.Mentions(plan))
	}
	if strings.TrimSpace(rest) != "" {
		parts = append(parts, strings.TrimRight(rest, "\n"))
	}
	if len(parts) == 0 {
		return rest
	}
	return strings.Join(parts, "\n\n") + "\n"
}
