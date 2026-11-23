package slashcmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/daan3011/forge/internal/templates"
	"github.com/daan3011/forge/internal/testutil"
)

func front(t *testing.T, data []byte) frontMatter {
	t.Helper()
	fm, err := frontOf(data)
	if err != nil {
		t.Fatal(err)
	}
	var m frontMatter
	if err := yaml.Unmarshal(fm, &m); err != nil {
		t.Fatalf("front matter: %v\n%s", err, fm)
	}
	return m
}

func TestBuiltins(t *testing.T) {
	want := []string{"fix-ci", "forge-adr", "forge-doc", "forge-pack", "forge-pin", "forge-status", "forge-ticket"}
	var got []string
	for _, c := range Builtins() {
		got = append(got, c.Name)
		m := front(t, c.Content)
		if m.Name != c.Name {
			t.Errorf("%s: name %q", c.Name, m.Name)
		}
		if m.Description == "" || m.ArgumentHint == "" {
			t.Errorf("%s: description and argument-hint are required", c.Name)
		}
		if m.DisableModelInvocation == nil || !*m.DisableModelInvocation {
			t.Errorf("%s: disable-model-invocation must be true", c.Name)
		}
		if !strings.HasPrefix(m.ArgumentHint, "[") {
			t.Errorf("%s: argument-hint %q should use square brackets", c.Name, m.ArgumentHint)
		}
		if !strings.Contains(string(c.Content), "$ARGUMENTS") && !strings.Contains(string(c.Content), "$0") {
			t.Errorf("%s: never uses its arguments", c.Name)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("built-ins = %v, want %v", got, want)
	}
}

func TestAllowedToolsAreMCPNames(t *testing.T) {
	for _, c := range Builtins() {
		tools := AllowedTools(c)
		if len(tools) == 0 {
			t.Errorf("%s: no allowed-tools", c.Name)
		}
		for _, tool := range tools {
			if tool != "Read" && !strings.HasPrefix(tool, "mcp__forge__") {
				t.Errorf("%s: unexpected tool %q", c.Name, tool)
			}
		}
	}
}

func TestRender(t *testing.T) {
	c, ok := Builtin("fix-ci")
	if !ok {
		t.Fatal("no fix-ci")
	}
	tests := []struct {
		name string
		vals map[string]string
		want []string
		not  []string
	}{
		{
			"filled",
			map[string]string{"session": "fix-1", "branch": "agent/fix-1", "other": "x"},
			[]string{"(fix-1, branch agent/fix-1)", "`run_url` set to `$ARGUMENTS`"},
			[]string{"{session}"},
		},
		{
			"empty values stay as placeholders",
			map[string]string{"session": ""},
			[]string{"{session}", "{branch}", "$ARGUMENTS"},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := string(Render(c, tt.vals))
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q in\n%s", w, out)
				}
			}
			for _, n := range tt.not {
				if strings.Contains(out, n) {
					t.Errorf("unexpected %q", n)
				}
			}
		})
	}
}

func TestFromTemplates(t *testing.T) {
	tpl, err := templates.Parse("review.md", []byte(`---
description: Review a PR
variables:
  - name: focus
    description: what to look at
    default: everything
---
Review {pr} in {repo} on {branch}, focusing on {focus}. Again: {pr}. Literal \{x}.
`))
	if err != nil {
		t.Fatal(err)
	}
	bad := templates.Template{Name: "Bad Name", Body: "x"}
	cmds := FromTemplates([]templates.Template{tpl, bad})
	if len(cmds) != 1 {
		t.Fatalf("got %d commands, want 1 (bad name skipped)", len(cmds))
	}
	c := cmds[0]
	if c.Name != "forge-tpl-review" || c.Source != SourceTemplate {
		t.Fatalf("command = %s (%s)", c.Name, c.Source)
	}
	m := front(t, c.Content)
	if m.ArgumentHint != "[pr] [focus]" {
		t.Errorf("argument-hint = %q", m.ArgumentHint)
	}
	if m.DisableModelInvocation == nil || !*m.DisableModelInvocation {
		t.Error("disable-model-invocation must be true")
	}
	out := string(Render(c, map[string]string{"repo": "o/r", "branch": "main"}))
	for _, w := range []string{
		"Review $ARGUMENTS[0] in o/r on main, focusing on $ARGUMENTS[1]. Again: $ARGUMENTS[0]. Literal {x}.",
		"`$ARGUMENTS[1]` is focus: what to look at (default: everything)",
		"`$ARGUMENTS[0]` is pr: pull request",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in\n%s", w, out)
		}
	}
}

func TestMergeOverridePrecedence(t *testing.T) {
	b := []Command{{Name: "fix-ci", Source: SourceBuiltin}, {Name: "forge-pin", Source: SourceBuiltin}}
	tp := []Command{{Name: "forge-tpl-x", Source: SourceTemplate}, {Name: "fix-ci", Source: SourceTemplate}}
	ov := []Command{
		{Name: "fix-ci", Source: SourceOverride},
		{Name: "forge-tpl-x", Source: SourceOverride},
		{Name: "forge-mine", Source: SourceOverride},
	}
	got := Merge(b, tp, ov)
	want := map[string]string{
		"fix-ci":      "override of built-in",
		"forge-mine":  "override",
		"forge-pin":   "built-in",
		"forge-tpl-x": "override of template",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d commands: %+v", len(got), got)
	}
	for _, c := range got {
		if c.Label() != want[c.Name] {
			t.Errorf("%s: %q, want %q", c.Name, c.Label(), want[c.Name])
		}
	}
}

func TestLoadOverrides(t *testing.T) {
	testutil.IsolateHome(t)
	dir := t.TempDir()
	files := map[string]string{
		"fix-ci.md":       "---\ndescription: mine\n---\nbody $ARGUMENTS\n",
		"Bad_Name.md":     "x",
		"forge-other.md":  "---\nname: different\n---\nx",
		"broken.md":       "---\nname: [\n---\nx",
		"notes.txt":       "ignored",
		".hidden.md":      "ignored",
		"forge-nofm.md":   "just a body",
		"forge-unterm.md": "---\ndescription: x\n",
	}
	for n, s := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, errs := LoadOverrides(dir)
	var names []string
	for _, c := range got {
		names = append(names, c.Name)
		if c.Source != SourceOverride || c.Path == "" {
			t.Errorf("%s: source %s path %q", c.Name, c.Source, c.Path)
		}
	}
	if !reflect.DeepEqual(names, []string{"fix-ci", "forge-nofm"}) {
		t.Errorf("loaded %v", names)
	}
	if len(errs) != 4 {
		t.Errorf("errors = %v, want 4", errs)
	}
	if got, errs := LoadOverrides(filepath.Join(dir, "missing")); got != nil || errs != nil {
		t.Errorf("missing dir: %v %v", got, errs)
	}
}

func TestCatalogEditAndReset(t *testing.T) {
	testutil.IsolateHome(t)
	c := Catalog{OverridesDir: t.TempDir(), TemplatesDir: ""}
	path, err := c.EnsureOverride("forge-status")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Builtin("forge-status")
	if data, _ := os.ReadFile(path); string(data) != string(b.Content) {
		t.Fatal("override doesn't start as a copy of the built-in")
	}
	if err := os.WriteFile(path, []byte("---\ndescription: edited\n---\nnew $ARGUMENTS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p2, err := c.EnsureOverride("forge-status"); err != nil || p2 != path {
		t.Fatalf("second EnsureOverride: %v %q", err, p2)
	}
	cmds, errs := c.Commands()
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	got, _ := Find(cmds, "forge-status")
	if got.Description != "edited" || got.Label() != "override of built-in" {
		t.Errorf("after edit: %+v", got)
	}
	if err := c.Reset("forge-status"); err != nil {
		t.Fatal(err)
	}
	if err := c.Reset("forge-status"); err != nil {
		t.Fatalf("second reset: %v", err)
	}
	cmds, _ = c.Commands()
	if got, _ := Find(cmds, "forge-status"); got.Source != SourceBuiltin {
		t.Errorf("after reset: %s", got.Label())
	}
	if _, err := c.EnsureOverride("../evil"); err == nil {
		t.Error("bad name accepted")
	}
	p, err := c.EnsureOverride("forge-new")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse("forge-new", mustRead(t, p)); err != nil {
		t.Errorf("new override doesn't parse: %v", err)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
