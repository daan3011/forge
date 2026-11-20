package templates

import (
	"regexp"
	"slices"
	"strings"
)

var (
	identRe = regexp.MustCompile(`\A[a-z][a-z0-9_]*\z`)
	// placeholderRe matches {name}, or an escaped \{ so it can be skipped.
	placeholderRe = regexp.MustCompile(`\\\{|\{([a-z][a-z0-9_]*)\}`)
)

// Known are the variables forge can fill itself (or through a context
// provider), with a description for form fields.
var Known = map[string]string{
	"repo":        "GitHub repo (owner/name)",
	"branch":      "the session's branch",
	"pr":          "pull request (owner/repo#n)",
	"pr_title":    "pull request title",
	"pr_body":     "pull request description",
	"issue":       "issue key or id (e.g. PROJ-123)",
	"issue_title": "issue title",
	"issue_body":  "issue description",
	"failing_log": "the failing CI log",
	"docs":        "the project's docs folder",
}

// Vars returns the variable names used in body, in order of first use.
func Vars(body string) []string {
	var out []string
	for _, m := range placeholderRe.FindAllStringSubmatch(body, -1) {
		if m[1] != "" && !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

// Vars returns every variable the template uses: those in its body, then
// declared ones the body doesn't use (they may still be wanted in the form).
func (t Template) Vars() []string {
	out := Vars(t.Body)
	for _, v := range t.Variables {
		if !slices.Contains(out, v.Name) {
			out = append(out, v.Name)
		}
	}
	return out
}

// Render substitutes vals into body. Placeholders without a value stay as
// {name} and are listed in missing (each once); \{ becomes a literal {.
func Render(body string, vals map[string]string) (out string, missing []string) {
	out = placeholderRe.ReplaceAllStringFunc(body, func(s string) string {
		if s == `\{` {
			return "{"
		}
		name := s[1 : len(s)-1]
		if v, ok := vals[name]; ok {
			return v
		}
		if !slices.Contains(missing, name) {
			missing = append(missing, name)
		}
		return s
	})
	return out, missing
}

// Describe returns the form description for variable name: the declared
// one, else the built-in one, else "".
func (t Template) Describe(name string) string {
	if v, ok := t.Var(name); ok && v.Description != "" {
		return v.Description
	}
	return Known[name]
}

// Multiline reports whether a variable usually holds several lines.
func Multiline(name string) bool {
	return strings.HasSuffix(name, "_body") || strings.HasSuffix(name, "_log")
}
