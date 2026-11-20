package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ValidationError is one problem in the config file.
type ValidationError struct {
	File string // config file path; empty when validating in memory
	Line int    // 1-based line; 0 when unknown
	Key  string // dotted key path, e.g. "projects[1].repo"
	Msg  string
}

func (e ValidationError) Error() string {
	var b strings.Builder
	if e.File != "" {
		b.WriteString(e.File)
		if e.Line > 0 {
			fmt.Fprintf(&b, ":%d", e.Line)
		}
		b.WriteString(": ")
	}
	if e.Key != "" {
		b.WriteString(e.Key + ": ")
	}
	b.WriteString(e.Msg)
	return b.String()
}

// Errors is every problem found in the config, sorted by line.
type Errors []ValidationError

func (es Errors) Error() string {
	if len(es) == 1 {
		return es[0].Error()
	}
	lines := make([]string, len(es))
	for i, e := range es {
		lines[i] = "  " + e.Error()
	}
	return fmt.Sprintf("%d problems in config:\n%s", len(es), strings.Join(lines, "\n"))
}

// at sets File on every error and sorts them by line.
func (es Errors) at(file string) Errors {
	for i := range es {
		es[i].File = file
	}
	sort.SliceStable(es, func(i, j int) bool { return es[i].Line < es[j].Line })
	return es
}

var (
	repoRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$`)
	hexRe  = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)
)

// MinRefreshInterval is the smallest allowed refresh_interval for any source.
const MinRefreshInterval = 5 * time.Second

// Validate checks values that YAML types alone cannot. It returns Errors with
// Key set (but no File or Line) or nil.
func (c *Config) Validate() error {
	var es Errors
	add := func(key, format string, args ...any) {
		es = append(es, ValidationError{Key: key, Msg: fmt.Sprintf(format, args...)})
	}

	names := map[string]bool{}
	for i, p := range c.Projects {
		k := fmt.Sprintf("projects[%d]", i)
		switch {
		case p.Name == "":
			add(k+".name", "is required")
		case names[p.Name]:
			add(k+".name", "duplicate project name %q", p.Name)
		}
		names[p.Name] = true
		if !repoRe.MatchString(p.Repo) {
			add(k+".repo", "must be \"owner/name\", got %q", p.Repo)
		}
		if p.Path == "" {
			add(k+".path", "is required")
		} else if !filepath.IsAbs(p.Path) {
			add(k+".path", "must be absolute or start with ~/, got %q", p.Path)
		}
	}

	for _, r := range []struct {
		key string
		d   Duration
	}{
		{"github.refresh_interval", c.GitHub.RefreshInterval},
		{"plane.refresh_interval", c.Plane.RefreshInterval},
		{"sessions.refresh_interval", c.Sessions.RefreshInterval},
	} {
		if r.d.Duration < MinRefreshInterval {
			add(r.key, "must be at least %s, got %s", MinRefreshInterval, r.d)
		}
	}
	for i, s := range c.GitHub.PRSections {
		k := fmt.Sprintf("github.pr_sections[%d]", i)
		if s.Title == "" {
			add(k+".title", "is required")
		}
		if s.Query == "" {
			add(k+".query", "is required")
		}
	}

	if u, err := url.Parse(c.Plane.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		add("plane.base_url", "must be an http(s) URL, got %q", c.Plane.BaseURL)
	}

	if c.Sessions.WorktreeRoot != "" && !filepath.IsAbs(c.Sessions.WorktreeRoot) {
		add("sessions.worktree_root", "must be absolute or start with ~/, got %q", c.Sessions.WorktreeRoot)
	}
	if c.Sessions.NotifyAfterMinutes < 0 {
		add("sessions.notify_after_minutes", "must be 0 or more, got %d", c.Sessions.NotifyAfterMinutes)
	}

	for i, g := range c.Artifacts.Watch {
		if _, err := filepath.Match(g, ""); err != nil {
			add(fmt.Sprintf("artifacts.watch[%d]", i), "invalid glob %q", g)
		}
	}

	switch c.UI.Animations {
	case AnimationsFull, AnimationsReduced, AnimationsOff:
	default:
		add("ui.animations", "must be one of full, reduced, off; got %q", c.UI.Animations)
	}
	for _, name := range sortedKeys(c.UI.Theme) {
		if v := c.UI.Theme[name]; !hexRe.MatchString(v) {
			add("ui.theme."+name, "must be a colour like \"#ff7a1a\", got %q", v)
		}
	}

	for _, action := range sortedKeys(c.Keybinds) {
		keys := c.Keybinds[action]
		if len(keys) == 0 {
			add("keybinds."+action, "needs at least one key")
		}
		for i, k := range keys {
			if strings.TrimSpace(k) == "" {
				add(fmt.Sprintf("keybinds.%s[%d]", action, i), "key must not be empty")
			}
		}
	}

	cmds := map[string]bool{}
	for i, cc := range c.CustomCommands {
		k := fmt.Sprintf("custom_commands[%d]", i)
		switch {
		case cc.Name == "":
			add(k+".name", "is required")
		case cmds[cc.Name]:
			add(k+".name", "duplicate command name %q", cc.Name)
		}
		cmds[cc.Name] = true
		if strings.TrimSpace(cc.Command) == "" {
			add(k+".command", "is required")
		}
	}

	if len(es) == 0 {
		return nil
	}
	return es
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// nodeIndex maps key paths to lines in the YAML file and back.
type nodeIndex struct {
	line  map[string]int // "projects[1].repo" -> line
	keyAt map[int]string // line -> most specific key path on it
}

// lineOf returns the line of key, falling back to its nearest parent that is
// in the file (e.g. a missing "projects[0].repo" points at "projects[0]").
func (x nodeIndex) lineOf(key string) int {
	for key != "" {
		if l, ok := x.line[key]; ok {
			return l
		}
		i := strings.LastIndexAny(key, ".[")
		if i < 0 {
			break
		}
		key = key[:i]
	}
	return 0
}

// indexNode walks the document alongside the Config type, recording key
// lines and reporting keys that don't exist in the schema.
func indexNode(doc *yaml.Node) (nodeIndex, Errors) {
	x := nodeIndex{line: map[string]int{}, keyAt: map[int]string{}}
	var es Errors
	root := doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	walk(root, reflect.TypeFor[Config](), "", &x, &es)
	return x, es
}

func walk(n *yaml.Node, t reflect.Type, path string, x *nodeIndex, es *Errors) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	record := func(p string, line int) {
		x.line[p] = line
		x.keyAt[line] = p
	}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Value == "<<" {
				continue
			}
			p := join(path, k.Value)
			record(p, k.Line)
			switch t.Kind() {
			case reflect.Struct:
				ft, ok := fieldByTag(t, k.Value)
				if !ok {
					*es = append(*es, ValidationError{
						Line: k.Line, Key: p,
						Msg: "unknown key (valid keys: " + strings.Join(tagNames(t), ", ") + ")",
					})
					continue
				}
				walk(v, ft, p, x, es)
			case reflect.Map:
				walk(v, t.Elem(), p, x, es)
			}
		}
	case yaml.SequenceNode:
		if t.Kind() != reflect.Slice {
			return
		}
		for i, item := range n.Content {
			p := fmt.Sprintf("%s[%d]", path, i)
			record(p, item.Line)
			walk(item, t.Elem(), p, x, es)
		}
	}
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func yamlName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
	return name
}

func fieldByTag(t reflect.Type, name string) (reflect.Type, bool) {
	for f := range t.Fields() {
		if f.IsExported() && yamlName(f) == name {
			return f.Type, true
		}
	}
	return nil, false
}

func tagNames(t reflect.Type) []string {
	var names []string
	for f := range t.Fields() {
		if n := yamlName(f); f.IsExported() && n != "" && n != "-" {
			names = append(names, n)
		}
	}
	return names
}
