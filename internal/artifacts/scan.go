package artifacts

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/store"
)

// DefaultWatch are the globs used when the config lists none. Relative
// globs apply inside every project checkout and session worktree.
var DefaultWatch = []string{
	"**/*.html", "**/*.svg", "**/*.png", "design/**", ".forge/artifacts/**",
}

// KeptDir is where Keep copies artifacts, relative to a project checkout.
const KeptDir = ".forge/artifacts"

// MaxFilesPerRoot bounds one root's walk so a huge folder can't stall a
// scan; the rest of that root is left out with ErrTooMany.
const MaxFilesPerRoot = 20000

// ErrTooMany means a root had more than MaxFilesPerRoot entries.
var ErrTooMany = errors.New("artifacts: too many files")

// skipNames are folders never scanned or watched: VCS data, dependencies
// and build output.
var skipNames = map[string]bool{
	"node_modules": true, ".git": true, "dist": true, "build": true, "out": true,
	"target": true, ".next": true, ".nuxt": true, ".svelte-kit": true, ".turbo": true,
	".cache": true, ".parcel-cache": true, "coverage": true, "vendor": true,
	"__pycache__": true, ".venv": true, "venv": true,
}

// SkipDir reports whether the folder at path (named name) is left out of
// scans and watching. Dot-folders are skipped, except .forge, and inside
// .forge only artifacts is kept.
func SkipDir(path, name string) bool {
	if skipNames[name] {
		return true
	}
	if filepath.Base(filepath.Dir(path)) == ".forge" {
		return name != "artifacts"
	}
	return strings.HasPrefix(name, ".") && name != ".forge"
}

// Spec is the resolved watch config.
type Spec struct {
	// Relative globs apply inside every checkout and worktree.
	Relative []string
	// Absolute globs are matched against absolute paths under their Root.
	Absolute []string
}

// NewSpec splits watch globs into relative and absolute ones; an empty
// list means DefaultWatch.
func NewSpec(watch []string) Spec {
	if len(watch) == 0 {
		watch = DefaultWatch
	}
	var s Spec
	for _, g := range watch {
		g = filepath.ToSlash(strings.TrimSpace(g))
		switch {
		case g == "":
		case strings.HasPrefix(g, "/"):
			s.Absolute = append(s.Absolute, g)
		default:
			s.Relative = append(s.Relative, strings.TrimPrefix(g, "./"))
		}
	}
	return s
}

// Target is a folder to scan.
type Target struct {
	Path        string
	Project     string
	SessionID   int64  // 0 for a project checkout
	SessionName string // "" for a project checkout
	// Absolute targets come from absolute globs and match full paths;
	// others match paths relative to Path.
	Absolute bool
	Patterns []string
}

// Targets lists the folders to scan: every project checkout, every
// session worktree that still exists (archived ones too), and the roots of
// absolute globs. A session running in the main checkout counts as the
// checkout.
func Targets(projects []config.Project, sessions []store.Session, spec Spec) []Target {
	var out []Target
	seen := map[string]bool{}
	byName := map[string]string{}
	for _, p := range projects {
		if p.Path == "" {
			continue
		}
		path := filepath.Clean(p.Path)
		byName[p.Name] = path
		if seen[path] || len(spec.Relative) == 0 {
			continue
		}
		seen[path] = true
		out = append(out, Target{Path: path, Project: p.Name, Patterns: spec.Relative})
	}
	for _, x := range sessions {
		if x.Worktree == "" || x.WorktreeExternal || len(spec.Relative) == 0 {
			continue
		}
		path := filepath.Clean(x.Worktree)
		if seen[path] || path == byName[x.Project] || !isDir(path) {
			continue
		}
		seen[path] = true
		out = append(out, Target{
			Path: path, Project: x.Project, SessionID: x.ID, SessionName: x.Name,
			Patterns: spec.Relative,
		})
	}
	abs := map[string][]string{}
	var roots []string
	for _, g := range spec.Absolute {
		r := filepath.FromSlash(Root(g))
		if _, ok := abs[r]; !ok {
			roots = append(roots, r)
		}
		abs[r] = append(abs[r], g)
	}
	for _, r := range roots {
		out = append(out, Target{Path: r, Absolute: true, Patterns: abs[r]})
	}
	return out
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// Type names an artifact's kind by its extension: html, svg, png, jpg,
// gif, webp, pdf, md, or "file".
func Type(path string) string {
	switch ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")); ext {
	case "html", "htm":
		return "html"
	case "jpg", "jpeg":
		return "jpg"
	case "svg", "png", "gif", "webp", "pdf", "md":
		return ext
	default:
		return "file"
	}
}

// Artifact is one file found by Scan.
type Artifact struct {
	Path        string
	Project     string
	SessionID   int64
	SessionName string
	Type        string
	Size        int64
	ModTime     time.Time
}

// Kept reports whether the artifact is a copy in a .forge/artifacts folder.
func (a Artifact) Kept() bool { return IsKept(a.Path) }

// IsKept reports whether path lies in a .forge/artifacts folder.
func IsKept(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/"+KeptDir+"/")
}

// Scan walks every target and returns the matching files, newest first.
// Each file belongs to the deepest checkout or worktree that holds it (so a
// worktree inside a repo keeps its session), and files from absolute globs
// get the project whose checkout holds them. Errors from single roots are
// joined; what was found is still returned.
func Scan(targets []Target) ([]Artifact, error) {
	return scanLimited(targets, MaxFilesPerRoot)
}

func scanLimited(targets []Target, limit int) ([]Artifact, error) {
	var owners []Target
	for _, t := range targets {
		if !t.Absolute {
			owners = append(owners, t)
		}
	}
	// Deepest first, so the first owner containing a path wins.
	sort.SliceStable(owners, func(i, j int) bool { return len(owners[i].Path) > len(owners[j].Path) })

	found := map[string]Artifact{}
	var errs []error
	for _, t := range targets {
		if err := scanTarget(t, owners, found, limit); err != nil {
			errs = append(errs, err)
		}
	}
	out := make([]Artifact, 0, len(found))
	for _, a := range found {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ModTime.Equal(out[j].ModTime) {
			return out[i].ModTime.After(out[j].ModTime)
		}
		return out[i].Path < out[j].Path
	})
	return out, errors.Join(errs...)
}

func scanTarget(t Target, owners []Target, found map[string]Artifact, limit int) error {
	if !isDir(t.Path) {
		return nil // a missing root has nothing yet
	}
	n := 0
	err := filepath.WalkDir(t.Path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable or removed mid-walk
		}
		if n++; n > limit {
			return fmt.Errorf("%w under %s (over %d)", ErrTooMany, t.Path, limit)
		}
		if d.IsDir() {
			if p == t.Path {
				return nil
			}
			if SkipDir(p, d.Name()) {
				return filepath.SkipDir
			}
			// Another checkout or worktree nested here is scanned as its
			// own target.
			if !t.Absolute && ownsOther(p, t, owners) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		name := filepath.ToSlash(p)
		if !t.Absolute {
			rel, err := filepath.Rel(t.Path, p)
			if err != nil {
				return nil //nolint:nilerr // can't happen under the walk root
			}
			name = filepath.ToSlash(rel)
		}
		if !matchAny(t.Patterns, name) {
			return nil
		}
		if _, ok := found[p]; ok {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // removed mid-walk
		}
		a := Artifact{Path: p, Type: Type(p), Size: info.Size(), ModTime: info.ModTime()}
		if o, ok := owner(p, owners); ok {
			a.Project, a.SessionID, a.SessionName = o.Project, o.SessionID, o.SessionName
		}
		found[p] = a
		return nil
	})
	return err
}

// Matches reports whether path is a file one of targets would pick up,
// ignoring skipped folders on the way.
func Matches(targets []Target, path string) bool {
	slash := filepath.ToSlash(path)
	for _, t := range targets {
		name := slash
		if !t.Absolute {
			rel, err := filepath.Rel(t.Path, path)
			if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
				continue
			}
			name = filepath.ToSlash(rel)
		}
		if matchAny(t.Patterns, name) && !skippedOnTheWay(t.Path, path) {
			return true
		}
	}
	return false
}

func skippedOnTheWay(root, path string) bool {
	for dir := filepath.Dir(path); len(dir) > len(root) && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if SkipDir(dir, filepath.Base(dir)) {
			return true
		}
	}
	return false
}

func matchAny(patterns []string, name string) bool {
	for _, g := range patterns {
		if Match(g, name) {
			return true
		}
	}
	return false
}

func ownsOther(dir string, t Target, owners []Target) bool {
	for _, o := range owners {
		if o.Path == dir && o.Path != t.Path {
			return true
		}
	}
	return false
}

func owner(p string, owners []Target) (Target, bool) {
	for _, o := range owners {
		if strings.HasPrefix(p, o.Path+string(filepath.Separator)) {
			return o, true
		}
	}
	return Target{}, false
}

// Roots returns the folders targets cover, for the watcher and for
// pruning the index.
func Roots(targets []Target) []string {
	out := make([]string, len(targets))
	for i, t := range targets {
		out[i] = t.Path
	}
	return out
}
