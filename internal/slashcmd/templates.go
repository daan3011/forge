package slashcmd

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/daan3011/forge/internal/templates"
)

// SessionVars are the placeholders forge fills itself when it writes a
// skill for a session; every other template variable becomes an argument.
var SessionVars = []string{"session", "repo", "branch", "docs"}

// placeholderRe matches {name} or an escaped \{ (templates' syntax).
var placeholderRe = regexp.MustCompile(`\\\{|\{([a-z][a-z0-9_]*)\}`)

// FromTemplates makes a /forge-tpl-<name> command of each session template.
// The template's variables, other than SessionVars, become positional
// arguments in order of use ($ARGUMENTS[0], …), listed in argument-hint;
// SessionVars stay as {name} for Render. Templates whose name doesn't make
// a valid skill name are skipped.
func FromTemplates(tpls []templates.Template) []Command {
	var out []Command
	for _, t := range tpls {
		name := TemplatePrefix + t.Name
		if ValidName(name) != nil {
			continue
		}
		out = append(out, fromTemplate(name, t))
	}
	return out
}

func fromTemplate(name string, t templates.Template) Command {
	var args []string
	for _, v := range t.Vars() {
		if !slices.Contains(SessionVars, v) {
			args = append(args, v)
		}
	}
	body := placeholderRe.ReplaceAllStringFunc(t.Body, func(s string) string {
		if s == `\{` {
			return s // Render unescapes it
		}
		v := s[1 : len(s)-1]
		if i := slices.Index(args, v); i >= 0 {
			return fmt.Sprintf("$ARGUMENTS[%d]", i)
		}
		return s
	})

	desc := t.Description
	if desc == "" {
		desc = "forge session template " + t.Name
	}
	hint := make([]string, len(args))
	for i, a := range args {
		hint[i] = "[" + a + "]"
	}
	fm := struct {
		Name                   string `yaml:"name"`
		Description            string `yaml:"description"`
		ArgumentHint           string `yaml:"argument-hint,omitempty"`
		DisableModelInvocation bool   `yaml:"disable-model-invocation"`
	}{name, desc + " (forge template)", strings.Join(hint, " "), true}
	head, err := yaml.Marshal(fm)
	if err != nil {
		panic(err) // plain strings: can't fail
	}

	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(head)
	b.WriteString("---\n")
	if len(args) > 0 {
		b.WriteString("<!-- forge: arguments of the \"" + t.Name + "\" session template, in order -->\n")
		b.WriteString("Arguments, in order (quote one that has spaces):\n")
		for i, a := range args {
			line := fmt.Sprintf("- `$ARGUMENTS[%d]` is %s", i, a)
			if d := t.Describe(a); d != "" {
				line += ": " + d
			}
			if v, ok := t.Var(a); ok && v.Default != "" {
				line += " (default: " + v.Default + ")"
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("An argument that wasn't given still reads as a literal `$ARGUMENTS[…]` below: " +
			"use its default, or fill it from this conversation and the forge tools " +
			"(e.g. mcp__forge__get_ci_failure for a CI log, mcp__forge__get_issue for an issue).\n\n")
	}
	b.WriteString(body)
	return Command{Name: name, Description: fm.Description, Hint: fm.ArgumentHint, Source: SourceTemplate, Content: b.Bytes()}
}

// Render fills forge's placeholders ({session}, {repo}, {branch}, {docs})
// in c's content. Claude Code's own placeholders ($ARGUMENTS, $0, …) and
// unknown {names} are left alone; \{ becomes {.
func Render(c Command, vals map[string]string) []byte {
	only := map[string]string{}
	for _, k := range SessionVars {
		if v, ok := vals[k]; ok && v != "" {
			only[k] = v
		}
	}
	out, _ := templates.Render(string(c.Content), only)
	return []byte(out)
}
