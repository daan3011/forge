// Package slashcmd builds the Claude Code skills (slash commands) forge
// gives every session (P6.7): built-ins embedded in the binary, one per
// session template (/forge-tpl-<name>), and the user's overrides in
// ~/.config/forge/commands/<name>.md (same name wins).
//
// They are written as <root>/.claude/skills/<name>/SKILL.md, where root is a
// folder in the session's data dir that claude gets with --add-dir: nothing
// goes into the worktree or into ~/.claude. Every forge skill has
// disable-model-invocation: true, so it runs only when the user types it.
package slashcmd

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/daan3011/forge/internal/config"
)

//go:embed builtin/*.md
var builtinFS embed.FS

// Source is where a command comes from.
type Source string

// Sources.
const (
	SourceBuiltin  Source = "built-in"
	SourceTemplate Source = "template"
	SourceOverride Source = "override"
)

// TemplatePrefix starts the name of a session template's command.
const TemplatePrefix = "forge-tpl-"

// Command is one skill.
type Command struct {
	Name        string
	Description string
	Hint        string // argument-hint
	Source      Source
	// Overrides is the source an override replaces ("" for a command of
	// the user's own).
	Overrides Source
	// Content is the whole SKILL.md: front matter and body, with forge's
	// {session} / {branch} / {repo} placeholders not yet filled.
	Content []byte
	// Path is the override file; empty for built-ins and templates.
	Path string
}

// Label describes the source for lists: "override of built-in" etc.
func (c Command) Label() string {
	if c.Source == SourceOverride && c.Overrides != "" {
		return "override of " + string(c.Overrides)
	}
	return string(c.Source)
}

var nameRe = regexp.MustCompile(`\A[a-z0-9][a-z0-9-]{0,63}\z`)

// ValidName reports whether name can be a forge skill name.
func ValidName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("slashcmd: %q: use lower-case letters, digits and - (at most 64)", name)
	}
	return nil
}

// Dir is where overrides live: <config dir>/commands.
func Dir() (string, error) {
	d, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "commands"), nil
}

type frontMatter struct {
	Name         string `yaml:"name"`
	Description  string `yaml:"description"`
	ArgumentHint string `yaml:"argument-hint"`
	// DisableModelInvocation is a pointer so a missing field is told apart
	// from false.
	DisableModelInvocation *bool `yaml:"disable-model-invocation"`
	AllowedTools           any   `yaml:"allowed-tools"`
	Arguments              any   `yaml:"arguments"`
}

// frontOf returns a SKILL.md's front matter; a file without any has none.
func frontOf(data []byte) ([]byte, error) {
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return nil, nil
	}
	rest := data[4:]
	if bytes.HasPrefix(rest, []byte("---\n")) {
		return nil, nil
	}
	i := bytes.Index(rest, []byte("\n---\n"))
	if i < 0 {
		if bytes.HasSuffix(rest, []byte("\n---")) {
			return rest[:len(rest)-4], nil
		}
		return nil, errors.New("front matter has no closing ---")
	}
	return rest[:i], nil
}

// Parse reads a SKILL.md; name is used when the front matter has none.
func Parse(name string, data []byte) (Command, error) {
	fm, err := frontOf(data)
	if err != nil {
		return Command{}, fmt.Errorf("%s: %w", name, err)
	}
	var m frontMatter
	if err := yaml.Unmarshal(fm, &m); err != nil {
		return Command{}, fmt.Errorf("%s: front matter: %w", name, err)
	}
	if m.Name != "" && m.Name != name {
		return Command{}, fmt.Errorf("%s: front matter name %q differs from the file name", name, m.Name)
	}
	if err := ValidName(name); err != nil {
		return Command{}, err
	}
	return Command{Name: name, Description: m.Description, Hint: m.ArgumentHint, Content: data}, nil
}

// Builtins returns the embedded commands, sorted by name.
func Builtins() []Command {
	entries, err := fs.ReadDir(builtinFS, "builtin")
	if err != nil {
		panic(err) // embedded: can't fail
	}
	var out []Command
	for _, e := range entries {
		data, err := builtinFS.ReadFile("builtin/" + e.Name())
		if err != nil {
			panic(err)
		}
		c, err := Parse(strings.TrimSuffix(e.Name(), ".md"), data)
		if err != nil {
			panic(err) // covered by tests
		}
		c.Source = SourceBuiltin
		out = append(out, c)
	}
	return out
}

// Builtin returns the built-in called name.
func Builtin(name string) (Command, bool) {
	for _, c := range Builtins() {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// LoadOverrides reads every <name>.md in dir. Bad files are skipped and
// reported; a missing dir is no overrides.
func LoadOverrides(dir string) ([]Command, []error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{err}
	}
	var out []Command
	var errs []error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		c, err := Parse(strings.TrimSuffix(e.Name(), ".md"), data)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		c.Source, c.Path = SourceOverride, path
		out = append(out, c)
	}
	return out, errs
}

// Merge combines the sources: an override replaces the built-in or
// template command of the same name. The result is sorted by name.
func Merge(builtins, tpls, overrides []Command) []Command {
	by := map[string]Command{}
	for _, c := range builtins {
		by[c.Name] = c
	}
	for _, c := range tpls {
		if _, ok := by[c.Name]; !ok {
			by[c.Name] = c
		}
	}
	for _, c := range overrides {
		if prev, ok := by[c.Name]; ok {
			c.Overrides = prev.Source
		}
		by[c.Name] = c
	}
	out := make([]Command, 0, len(by))
	for _, c := range by {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Find returns the command called name.
func Find(cmds []Command, name string) (Command, bool) {
	for _, c := range cmds {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// AllowedTools returns the tools c's front matter pre-approves: a space- or
// comma-separated string, or a list.
func AllowedTools(c Command) []string {
	fm, err := frontOf(c.Content)
	if err != nil {
		return nil
	}
	var m frontMatter
	if yaml.Unmarshal(fm, &m) != nil {
		return nil
	}
	var out []string
	switch v := m.AllowedTools.(type) {
	case string:
		out = strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == ',' })
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
