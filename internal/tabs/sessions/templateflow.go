package sessions

import (
	"strings"

	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/config"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/templates"
	"github.com/daan3011/forge/internal/ui"
)

// The template flow after the main form, shared by the tab and `forge new`:
//
//	RefsForm   issue / PR the template is for (skipped if given or unused)
//	Resolve    built-ins + context providers + defaults (a tea.Cmd in the tab)
//	VarsForm   whatever is still missing (skipped if nothing is)
//	PromptForm the rendered prompt, editable
//
// Each step is its own form so the resolve in between can run off the
// update loop.

// NewRequest is a new session to ask about: StartOpts pre-fill the form,
// Refs and Vars pre-fill the template (P1.9 passes the issue / PR a session
// is started from, P2.8 the failing log).
type NewRequest struct {
	Opts agent.StartOpts
	Refs map[store.LinkKind]string
	Vars map[string]string
	// Docs are absolute doc paths to hand the session (P4.3); they fill
	// {docs} as @path mentions for the chosen project's worktree.
	Docs []string
}

// TemplateInput is what templates.Resolve needs for the chosen options.
func TemplateInput(projects []config.Project, o agent.StartOpts, refs map[store.LinkKind]string, vars map[string]string) templates.Input {
	in := templates.Input{Refs: refs, Vars: vars}
	for _, p := range projects {
		if p.Name == o.Project {
			in.Project = p
		}
	}
	switch {
	case strings.TrimSpace(o.Branch) != "":
		in.Branch = strings.TrimSpace(o.Branch)
	case o.Name != "":
		in.Branch = worktree.Branch(o.Name)
	}
	return in
}

// previewText renders the selected template with the variables forge knows
// without fetching; the rest stay as {name}. "" without a template.
func previewText(in FormInput, vals agent.StartOpts) string {
	t, ok := templates.Find(in.Templates, vals.Template)
	if !ok {
		return ""
	}
	known := templates.Builtins(TemplateInput(in.Projects, vals, nil, nil))
	out, _ := templates.Render(t.Body, known)
	out = strings.TrimSpace(out)
	if t.Description != "" {
		out = t.Description + "\n\n" + out
	}
	return out
}

// refLabels describes the reference inputs.
var refLabels = map[store.LinkKind][2]string{
	store.KindPlaneIssue: {"Issue", "issue key or id, e.g. PROJ-123; leave blank if there is none"},
	store.KindGitHubPR:   {"Pull request", "owner/repo#123; leave blank if there is none"},
}

// RefsForm asks for the references t needs that refs doesn't have yet. It
// returns nil when there is nothing to ask. get returns refs merged with
// the answers once the form is done.
func RefsForm(theme *ui.Theme, keys *ui.KeyMap, t templates.Template, refs map[store.LinkKind]string) (f *huh.Form, get func() map[store.LinkKind]string) {
	answers := map[store.LinkKind]*string{}
	var fields []huh.Field
	for _, kind := range t.RefKinds() {
		if strings.TrimSpace(refs[kind]) != "" {
			continue
		}
		v := new(string)
		answers[kind] = v
		l := refLabels[kind]
		fields = append(fields, huh.NewInput().Key(string(kind)).Title(l[0]).Description(l[1]).Value(v))
	}
	get = func() map[store.LinkKind]string {
		out := map[store.LinkKind]string{}
		for k, v := range refs {
			out[k] = v
		}
		for k, v := range answers {
			if s := strings.TrimSpace(*v); s != "" {
				out[k] = s
			}
		}
		return out
	}
	if len(fields) == 0 {
		return nil, get
	}
	return styled(theme, keys, huh.NewGroup(fields...).Title("Template "+t.Name)), get
}

// VarsForm asks for the variables Resolve couldn't fill. It returns nil
// when nothing is missing. get returns the answers once the form is done.
func VarsForm(theme *ui.Theme, keys *ui.KeyMap, t templates.Template, missing []string) (f *huh.Form, get func() map[string]string) {
	answers := make([]string, len(missing))
	get = func() map[string]string {
		out := make(map[string]string, len(missing))
		for i, name := range missing {
			out[name] = answers[i]
		}
		return out
	}
	if len(missing) == 0 {
		return nil, get
	}
	fields := make([]huh.Field, len(missing))
	for i, name := range missing {
		desc := t.Describe(name)
		if templates.Multiline(name) {
			fields[i] = huh.NewText().Key(name).Title("{" + name + "}").Description(desc).Lines(4).Value(&answers[i])
		} else {
			fields[i] = huh.NewInput().Key(name).Title("{" + name + "}").Description(desc).Value(&answers[i])
		}
	}
	return styled(theme, keys, huh.NewGroup(fields...).Title("Template "+t.Name)), get
}

// PromptForm shows the rendered prompt for a last look and edit.
func PromptForm(theme *ui.Theme, keys *ui.KeyMap, prompt *string) *huh.Form {
	return styled(theme, keys, huh.NewGroup(promptField(prompt).Lines(12)).Title("New session"))
}

func styled(theme *ui.Theme, keys *ui.KeyMap, g *huh.Group) *huh.Form {
	return huh.NewForm(g).WithTheme(theme.Huh()).WithKeyMap(keys.HuhKeyMap()).WithShowHelp(true)
}

// RenderPrompt renders t with vals as the first prompt. Unfilled
// variables stay as {name}.
func RenderPrompt(t templates.Template, vals map[string]string) string {
	out, _ := templates.Render(t.Body, vals)
	return strings.TrimSpace(out) + "\n"
}
