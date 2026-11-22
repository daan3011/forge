// Package memory finds the memory files (CLAUDE.md, rules, imports, auto
// memory) Claude Code loads for a working directory, following
// https://code.claude.com/docs/en/memory (checked 2026-11-23).
//
// Loaded at launch, in order: the managed policy file, ~/.claude/CLAUDE.md,
// ~/.claude/rules/**/*.md, then CLAUDE.md, .claude/CLAUDE.md and
// CLAUDE.local.md in every directory from / down to the working directory,
// <cwd>/.claude/rules/**/*.md, and the first 200 lines / 25KB of the auto
// memory MEMORY.md. AGENTS.md is read only when no CLAUDE.md was found.
// Nested CLAUDE.md files below the working directory and rules with paths:
// frontmatter load on demand, when Claude touches a matching file.
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Limits from the Claude Code docs.
const (
	MaxImportDepth  = 4        // hops of @path imports
	MaxFileBytes    = 4 << 20  // larger files are skipped entirely
	AutoMemoryLines = 200      // MEMORY.md lines loaded at start
	AutoMemoryBytes = 25 << 10 // MEMORY.md bytes loaded at start
	maxNestedDirs   = 5000     // stop walking huge trees
	maxNestedDepth  = 12       // folders below the working directory
)

// Source says why a file is loaded.
type Source int

// Sources, in load order.
const (
	Managed Source = iota
	User
	UserRule
	Ancestor // CLAUDE.md / CLAUDE.local.md in a folder above the working directory
	Project  // CLAUDE.md or .claude/CLAUDE.md in the working directory
	Local    // CLAUDE.local.md in the working directory
	ProjectRule
	AutoMemory
	AgentsMD
	Import
	Nested   // CLAUDE.md below the working directory (on demand)
	PathRule // a rule with paths: frontmatter (on demand)
)

var sourceNames = [...]string{
	Managed: "managed policy", User: "user", UserRule: "user rule", Ancestor: "parent folder",
	Project: "project", Local: "local", ProjectRule: "project rule", AutoMemory: "auto memory",
	AgentsMD: "AGENTS.md fallback", Import: "import", Nested: "nested", PathRule: "path rule",
}

func (s Source) String() string {
	if int(s) < len(sourceNames) {
		return sourceNames[s]
	}
	return "unknown"
}

// File is one memory file.
type File struct {
	Path      string
	Source    Source
	OnDemand  bool     // not loaded at launch
	Depth     int      // import depth: 0 for files loaded directly
	Parent    string   // the importing file, for imports
	Globs     []string // a path rule's paths: patterns
	Exists    bool     // false for a missing import, or a file that could be created
	TooBig    bool     // over MaxFileBytes: Claude Code skips it
	External  bool     // an import outside the working directory (needs approval)
	Truncated bool     // MEMORY.md longer than what loads at start
	Lines     int      // total lines in the file
	Chars     int      // characters that load (after truncation)
	Tokens    int      // EstimateTokens of what loads
	Hash      string   // sha256 of the whole file
}

// Counts reports whether the file adds to the startup context.
func (f File) Counts() bool { return f.Exists && !f.TooBig && !f.OnDemand }

// Env is where to look. Every path is absolute.
type Env struct {
	Home     string // the user's home folder
	Managed  string // the managed policy CLAUDE.md; "" skips it
	Cwd      string // the folder claude is started in
	MainRoot string // the main checkout (worktrees share its auto memory); "" means Cwd
	// SkipNested leaves out the on-demand nested CLAUDE.md walk.
	SkipNested bool
	// UserOnly lists just the managed and user files (no checkout); Cwd
	// and MainRoot are ignored.
	UserOnly bool
}

// DefaultManaged is the managed policy file for this OS.
func DefaultManaged() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/ClaudeCode/CLAUDE.md"
	case "linux":
		return "/etc/claude-code/CLAUDE.md"
	case "windows":
		return `C:\Program Files\ClaudeCode\CLAUDE.md`
	}
	return ""
}

// EstimateTokens is the rough chars/4 token count.
func EstimateTokens(s string) int { return (utf8.RuneCountInString(s) + 3) / 4 }

// Slug turns a path into the folder name Claude Code uses under
// ~/.claude/projects: every character other than a letter or digit becomes
// "-".
func Slug(path string) string {
	b := []byte(path)
	for i, c := range b {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

// AutoMemoryPath is the MEMORY.md for a checkout.
func AutoMemoryPath(home, mainRoot string) string {
	return filepath.Join(home, ".claude", "projects", Slug(mainRoot), "memory", "MEMORY.md")
}

// Total sums the tokens of the files that load at launch.
func Total(files []File) int {
	n := 0
	for _, f := range files {
		if f.Counts() {
			n += f.Tokens
		}
	}
	return n
}

type resolver struct {
	env   Env
	files []File
	seen  map[string]bool // path -> exists
}

// Resolve lists the memory files for env in load order: launch files first,
// each followed by its imports, then the on-demand ones. Files that could
// be created (~/.claude/CLAUDE.md, <cwd>/CLAUDE.md, <cwd>/CLAUDE.local.md)
// are listed with Exists false when missing.
func Resolve(env Env) []File {
	env.Cwd = filepath.Clean(env.Cwd)
	if env.MainRoot == "" {
		env.MainRoot = env.Cwd
	}
	r := &resolver{env: env, seen: map[string]bool{}}

	if env.Managed != "" {
		r.add(env.Managed, Managed, false, false)
	}
	userFile := filepath.Join(env.Home, ".claude", "CLAUDE.md")
	r.add(userFile, User, false, true)
	r.rules(filepath.Join(env.Home, ".claude", "rules"), UserRule)
	if env.UserOnly {
		return r.sorted()
	}

	found := false
	for _, dir := range ancestors(env.Cwd) {
		here := dir == env.Cwd
		src, local := Ancestor, Ancestor
		if here {
			src, local = Project, Local
		}
		for _, c := range []struct {
			path string
			src  Source
		}{
			{filepath.Join(dir, "CLAUDE.md"), src},
			{filepath.Join(dir, ".claude", "CLAUDE.md"), src},
			{filepath.Join(dir, "CLAUDE.local.md"), local},
		} {
			if r.add(c.path, c.src, false, false) && c.path != userFile {
				found = true
			}
		}
	}
	if !found {
		for _, dir := range ancestors(env.Cwd) {
			r.add(filepath.Join(dir, "AGENTS.md"), AgentsMD, false, false)
		}
		r.placeholder(filepath.Join(env.Cwd, "CLAUDE.md"), Project)
	}
	r.placeholder(filepath.Join(env.Cwd, "CLAUDE.local.md"), Local)
	r.rules(filepath.Join(env.Cwd, ".claude", "rules"), ProjectRule)
	r.autoMemory()

	if !env.SkipNested {
		r.nested()
	}
	return r.sorted()
}

// sorted puts launch files first, keeping each import right after its
// parent.
func (r *resolver) sorted() []File {
	slices.SortStableFunc(r.files, func(a, b File) int {
		switch {
		case a.OnDemand == b.OnDemand:
			return 0
		case b.OnDemand:
			return -1
		}
		return 1
	})
	return r.files
}

// ancestors lists / down to dir.
func ancestors(dir string) []string {
	var out []string
	for d := dir; ; d = filepath.Dir(d) {
		out = append(out, d)
		if p := filepath.Dir(d); p == d {
			break
		}
	}
	slices.Reverse(out)
	return out
}

// add reads path and appends it and its imports; it reports whether the
// file exists. A missing file is appended only when keepMissing.
func (r *resolver) add(path string, src Source, onDemand, keepMissing bool) bool {
	path = filepath.Clean(path)
	if exists, ok := r.seen[path]; ok {
		return exists
	}
	f, text, ok := read(path)
	if !ok {
		if keepMissing {
			r.seen[path] = false
			r.files = append(r.files, File{Path: path, Source: src, OnDemand: onDemand})
		}
		return false
	}
	r.seen[path] = true
	f.Source, f.OnDemand = src, onDemand
	r.files = append(r.files, f)
	r.imports(path, text, src, onDemand, 1)
	return true
}

// placeholder lists a missing file that could be created.
func (r *resolver) placeholder(path string, src Source) {
	if _, ok := r.seen[path]; ok {
		return
	}
	if _, err := os.Stat(path); err == nil {
		return
	}
	r.seen[path] = false
	r.files = append(r.files, File{Path: path, Source: src})
}

// read loads path; ok is false when it isn't a readable regular file.
func read(path string) (File, string, bool) {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return File{}, "", false
	}
	f := File{Path: path, Exists: true}
	if st.Size() > MaxFileBytes {
		f.TooBig = true
		return f, "", true
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return File{}, "", false
	}
	sum := sha256.Sum256(b)
	f.Hash = hex.EncodeToString(sum[:])
	text := string(b)
	f.Lines = lineCount(text)
	f.Chars = utf8.RuneCountInString(text)
	f.Tokens = EstimateTokens(text)
	return f, text, true
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// imports appends the files text imports, depth hops from a launch file.
func (r *resolver) imports(from, text string, top Source, onDemand bool, depth int) {
	if depth > MaxImportDepth {
		return
	}
	for _, p := range Imports(text) {
		path := r.importPath(from, p)
		if _, ok := r.seen[path]; ok {
			continue
		}
		f, body, ok := read(path)
		r.seen[path] = ok
		if !ok {
			f = File{Path: path}
		}
		f.Source, f.OnDemand, f.Depth, f.Parent = Import, onDemand, depth, from
		f.External = r.external(path, top)
		r.files = append(r.files, f)
		if ok {
			r.imports(path, body, top, onDemand, depth+1)
		}
	}
}

func (r *resolver) importPath(from, p string) string {
	switch {
	case p == "~" || strings.HasPrefix(p, "~/"):
		return filepath.Join(r.env.Home, p[1:])
	case filepath.IsAbs(p):
		return filepath.Clean(p)
	}
	return filepath.Join(filepath.Dir(from), p)
}

// external reports whether an import needs approval: it is outside the
// working directory and wasn't pulled in by a user-scope file.
func (r *resolver) external(path string, top Source) bool {
	if top == User || top == UserRule || top == Managed {
		return false
	}
	return !within(r.env.Cwd, path)
}

func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// rules appends the .md files under dir: launch rules as src, the ones with
// paths: frontmatter as on-demand path rules.
func (r *resolver) rules(dir string, src Source) {
	var paths []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // a missing or unreadable rules folder is just empty
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			paths = append(paths, p)
		}
		return nil
	})
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if globs := RuleGlobs(string(b)); len(globs) > 0 {
			before := len(r.files)
			r.add(p, PathRule, true, false)
			if len(r.files) > before {
				r.files[before].Globs = globs
			}
			continue
		}
		r.add(p, src, false, false)
	}
}

// RuleGlobs returns the paths: patterns in a rule's YAML frontmatter, as a
// list or a comma separated string. Frontmatter that doesn't parse counts
// as none (the rule then loads at launch).
func RuleGlobs(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil
	}
	rest := text[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		if !strings.HasPrefix(rest, "---") {
			return nil
		}
		end = 0
	}
	var meta struct {
		Paths any `yaml:"paths"`
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &meta); err != nil {
		return nil
	}
	var out []string
	switch v := meta.Paths.(type) {
	case string:
		for _, g := range strings.Split(v, ",") {
			if g = strings.TrimSpace(g); g != "" {
				out = append(out, g)
			}
		}
	case []any:
		for _, g := range v {
			if s, ok := g.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	}
	return out
}

// autoMemory appends MEMORY.md, counting only what loads at start.
func (r *resolver) autoMemory() {
	path := AutoMemoryPath(r.env.Home, r.env.MainRoot)
	if _, ok := r.seen[path]; ok {
		return
	}
	f, text, ok := read(path)
	if !ok {
		return
	}
	r.seen[path] = true
	f.Source = AutoMemory
	if loaded, cut := TruncateAutoMemory(text); cut {
		f.Truncated = true
		f.Chars = utf8.RuneCountInString(loaded)
		f.Tokens = EstimateTokens(loaded)
	}
	r.files = append(r.files, f)
}

// TruncateAutoMemory returns the part of MEMORY.md that loads at start:
// the first AutoMemoryLines lines or AutoMemoryBytes bytes, whichever is
// shorter, and whether anything was cut.
func TruncateAutoMemory(text string) (string, bool) {
	cut := false
	if len(text) > AutoMemoryBytes {
		text = text[:AutoMemoryBytes]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		cut = true
	}
	idx := 0
	for range AutoMemoryLines {
		i := strings.IndexByte(text[idx:], '\n')
		if i < 0 {
			return text, cut
		}
		idx += i + 1
	}
	if idx < len(text) {
		return text[:idx], true
	}
	return text, cut
}

// skipDir is a folder the nested walk never enters.
func skipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "dist", "build", "target", "out", "coverage", "__pycache__":
		return true
	}
	return false
}

// nested appends the on-demand CLAUDE.md / CLAUDE.local.md files below the
// working directory.
func (r *resolver) nested() {
	root := r.env.Cwd
	dirs := 0
	errStop := errors.New("stop")
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && p != root {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() || p == root {
			return nil
		}
		if skipDir(d.Name()) || strings.Count(p[len(root):], string(filepath.Separator)) > maxNestedDepth {
			return fs.SkipDir
		}
		if _, err := os.Stat(filepath.Join(p, ".git")); err == nil {
			return fs.SkipDir // another checkout (a submodule or worktree inside this one)
		}
		if dirs++; dirs > maxNestedDirs {
			return errStop
		}
		r.add(filepath.Join(p, "CLAUDE.md"), Nested, true, false)
		r.add(filepath.Join(p, ".claude", "CLAUDE.md"), Nested, true, false)
		r.add(filepath.Join(p, "CLAUDE.local.md"), Nested, true, false)
		return nil
	})
}
