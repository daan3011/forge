// Package templates loads session templates: markdown files with YAML front
// matter whose body becomes a session's first prompt after {variable}
// substitution.
//
//	---
//	name: implement-issue
//	description: Implement a Plane issue end to end
//	variables:
//	  - issue_title                 # bare name
//	  - name: scope                 # or a mapping
//	    description: what to touch
//	    default: the whole package
//	---
//	Implement {issue}: {issue_title} ...
package templates

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Ext is the file extension of template files.
const Ext = ".md"

// Template is one session template.
type Template struct {
	Name        string
	Description string
	// Variables are declared in the front matter, for descriptions and
	// defaults. Variables used in Body but not declared still count.
	Variables []Var
	Body      string
	Path      string
}

// Var is a declared template variable.
type Var struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Default     string `yaml:"default"`
}

// UnmarshalYAML accepts a bare name or a mapping.
func (v *Var) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		v.Name = n.Value
		return nil
	}
	type plain Var
	return n.Decode((*plain)(v))
}

type frontMatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Variables   []Var  `yaml:"variables"`
}

var delim = []byte("---")

// Parse reads a template from data. path names it in errors and gives the
// default name (the file name without extension). A file without front
// matter is all body.
func Parse(path string, data []byte) (Template, error) {
	t := Template{Path: path, Name: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))}
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(data, append(delim, '\n')) {
		t.Body = string(data)
		return t, nil
	}
	rest := data[len(delim)+1:]
	var fm, body []byte
	switch {
	case bytes.HasPrefix(rest, append(delim, '\n')), bytes.Equal(rest, delim):
		body = bytes.TrimPrefix(rest[len(delim):], []byte("\n"))
	default:
		i := bytes.Index(rest, []byte("\n---\n"))
		if i < 0 {
			if !bytes.HasSuffix(rest, []byte("\n---")) {
				return t, fmt.Errorf("%s: front matter has no closing ---", path)
			}
			i = len(rest) - len("\n---")
			fm = rest[:i]
		} else {
			fm, body = rest[:i], rest[i+len("\n---\n"):]
		}
	}
	var m frontMatter
	if err := yaml.Unmarshal(fm, &m); err != nil {
		return t, fmt.Errorf("%s: front matter: %w", path, err)
	}
	for _, v := range m.Variables {
		if !identRe.MatchString(v.Name) {
			return t, fmt.Errorf("%s: variable %q: use lower-case letters, digits and _", path, v.Name)
		}
	}
	if m.Name != "" {
		t.Name = m.Name
	}
	t.Description, t.Variables, t.Body = m.Description, m.Variables, string(body)
	return t, nil
}

// Var returns the declared variable called name.
func (t Template) Var(name string) (Var, bool) {
	for _, v := range t.Variables {
		if v.Name == name {
			return v, true
		}
	}
	return Var{}, false
}

// Load reads every *.md template in dir, sorted by name. Files that fail to
// parse are skipped and reported. A missing dir is no templates.
func Load(dir string) ([]Template, []error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{fmt.Errorf("templates: %w", err)}
	}
	var (
		out  []Template
		errs []error
		seen = map[string]string{}
	)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != Ext {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path) //nolint:gosec // the user's own templates folder
		if err != nil {
			errs = append(errs, fmt.Errorf("templates: %w", err))
			continue
		}
		t, err := Parse(path, data)
		if err != nil {
			errs = append(errs, fmt.Errorf("templates: %w", err))
			continue
		}
		if other, ok := seen[t.Name]; ok {
			errs = append(errs, fmt.Errorf("templates: %s: name %q is already used by %s", path, t.Name, filepath.Base(other)))
			continue
		}
		seen[t.Name] = path
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b Template) int { return strings.Compare(a.Name, b.Name) })
	return out, errs
}

// Find returns the template called name.
func Find(ts []Template, name string) (Template, bool) {
	for _, t := range ts {
		if t.Name == name {
			return t, true
		}
	}
	return Template{}, false
}
