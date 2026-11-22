// Package packs loads and saves context packs (P5.2): named bundles of
// repo files, docs and a preamble prompt that can be handed to any Claude
// session. Each pack is a YAML file in ~/.config/forge/packs:
//
//	# ~/.config/forge/packs/auth.yaml
//	description: Login, sessions and tokens
//	files:                       # globs, relative to the repo; ** spans folders
//	  - internal/auth/**/*.go
//	  - cmd/server/middleware.go
//	docs:                        # relative to the project's docs folder, ~ or absolute
//	  - auth.md
//	preamble: |
//	  You're working on the auth layer. Keep tokens out of logs.
//
// The pack's name is the file name without .yaml.
package packs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/daan3011/forge/internal/config"
)

// Ext is the file extension of pack files.
const Ext = ".yaml"

// Pack is one context pack.
type Pack struct {
	Name        string   `yaml:"-"`
	Description string   `yaml:"description,omitempty"`
	Files       []string `yaml:"files,omitempty"`
	Docs        []string `yaml:"docs,omitempty"`
	Preamble    string   `yaml:"preamble,omitempty"`
	// Path is the pack's file; empty for a pack not saved yet.
	Path string `yaml:"-"`
}

// ErrInvalidName is returned for pack names that can't be file names.
var ErrInvalidName = errors.New("use letters, digits, - and _ (start with a letter or digit, max 64)")

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// CheckName reports ErrInvalidName for a name that isn't a plain file stem.
func CheckName(name string) error {
	if !nameRe.MatchString(name) {
		return ErrInvalidName
	}
	return nil
}

// Dir is the packs folder: config.Dir()/packs.
func Dir() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "packs"), nil
}

// Parse reads a pack from data; path gives its name and names it in errors.
func Parse(path string, data []byte) (Pack, error) {
	p := Pack{Path: path, Name: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))}
	if err := CheckName(p.Name); err != nil {
		return p, fmt.Errorf("%s: name %q: %w", path, p.Name, err)
	}
	if err := yaml.Unmarshal(data, &p); err != nil {
		return p, fmt.Errorf("%s: %w", path, err)
	}
	p.Files = clean(p.Files)
	p.Docs = clean(p.Docs)
	return p, nil
}

// clean drops blank entries and trims the rest.
func clean(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Load reads every *.yaml pack in dir, sorted by name. Files that fail to
// parse are skipped and reported. A missing dir is no packs.
func Load(dir string) ([]Pack, []error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{fmt.Errorf("packs: %w", err)}
	}
	var (
		out  []Pack
		errs []error
	)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != Ext {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path) //nolint:gosec // the user's own packs folder
		if err != nil {
			errs = append(errs, fmt.Errorf("packs: %w", err))
			continue
		}
		p, err := Parse(path, data)
		if err != nil {
			errs = append(errs, fmt.Errorf("packs: %w", err))
			continue
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Pack) int { return strings.Compare(a.Name, b.Name) })
	return out, errs
}

// Find returns the pack called name.
func Find(ps []Pack, name string) (Pack, bool) {
	for _, p := range ps {
		if p.Name == name {
			return p, true
		}
	}
	return Pack{}, false
}

// Marshal returns p as YAML.
func Marshal(p Pack) ([]byte, error) {
	return yaml.Marshal(p)
}

// Save writes p to dir/<name>.yaml (creating dir) through a temp file, and
// returns p with Path set. With create set it refuses to overwrite a pack
// that already exists.
func Save(dir string, p Pack, create bool) (Pack, error) {
	if err := CheckName(p.Name); err != nil {
		return p, fmt.Errorf("pack %q: %w", p.Name, err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return p, fmt.Errorf("packs: %w", err)
	}
	path := filepath.Join(dir, p.Name+Ext)
	if create {
		if _, err := os.Stat(path); err == nil {
			return p, fmt.Errorf("a pack called %s already exists", p.Name)
		}
	}
	data, err := Marshal(p)
	if err != nil {
		return p, fmt.Errorf("pack %s: %w", p.Name, err)
	}
	tmp, err := os.CreateTemp(dir, "."+p.Name+"-*.tmp")
	if err != nil {
		return p, fmt.Errorf("packs: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return p, fmt.Errorf("packs: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return p, fmt.Errorf("packs: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return p, fmt.Errorf("packs: %w", err)
	}
	p.Path = path
	return p, nil
}

// Delete removes the pack's file.
func Delete(p Pack) error {
	if p.Path == "" {
		return fmt.Errorf("pack %s has no file", p.Name)
	}
	if err := os.Remove(p.Path); err != nil {
		return fmt.Errorf("delete pack %s: %w", p.Name, err)
	}
	return nil
}

// IsGlob reports whether pattern has glob meta characters.
func IsGlob(pattern string) bool { return strings.ContainsAny(pattern, `*?[\`) }

// WithFiles returns p with its literal file entries replaced by files (in
// that order, after the globs it keeps).
func WithFiles(p Pack, files []string) Pack {
	var out []string
	for _, f := range p.Files {
		if IsGlob(f) {
			out = append(out, f)
		}
	}
	for _, f := range files {
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	p.Files = out
	return p
}

// Literals returns p's file entries that aren't globs.
func Literals(p Pack) []string {
	var out []string
	for _, f := range p.Files {
		if !IsGlob(f) {
			out = append(out, f)
		}
	}
	return out
}
