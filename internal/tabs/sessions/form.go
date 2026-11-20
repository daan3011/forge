package sessions

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/config"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/templates"
	"github.com/daan3011/forge/internal/ui"
)

// FormInput configures the new-session form.
type FormInput struct {
	Theme *ui.Theme
	Keys  *ui.KeyMap
	// Projects offered; only those with a local path can host a session.
	Projects []config.Project
	// Values pre-fills the form (e.g. the active project, CLI flags).
	Values agent.StartOpts
	// Templates offered; the field is left out when empty. With a template
	// chosen the prompt field is hidden: the template flow fills it in.
	Templates []templates.Template
	// Taken reports names already used, for instant feedback. The service
	// checks again against the database.
	Taken func(name string) bool
}

// UsableProjects are the projects a session can start in.
func UsableProjects(ps []config.Project) []config.Project {
	var out []config.Project
	for _, p := range ps {
		if p.Path != "" {
			out = append(out, p)
		}
	}
	return out
}

// ErrNoProjects means no configured project has a path.
var ErrNoProjects = errors.New("no project with a path configured — add one under projects: in your config")

// NewForm returns the new-session form and the values it fills in. The form
// is the same in the TUI and in `forge new`.
func NewForm(in FormInput) (*huh.Form, *agent.StartOpts, error) {
	projects := UsableProjects(in.Projects)
	if len(projects) == 0 {
		return nil, nil, ErrNoProjects
	}
	v := in.Values
	if v.Base == "" {
		v.Base = agent.DefaultBase
	}
	if !hasProject(projects, v.Project) {
		v.Project = projects[0].Name
	}
	vals := &v

	opts := make([]huh.Option[string], len(projects))
	for i, p := range projects {
		label := p.Name
		if p.Repo != "" {
			label += "  (" + p.Repo + ")"
		}
		opts[i] = huh.NewOption(label, p.Name)
	}
	fields := []huh.Field{
		huh.NewInput().Key("name").Title("Name").
			Description("letters, digits, - and _; names the branch, worktree and tmux window").
			Placeholder("fix-login").Value(&vals.Name).
			Validate(func(s string) error { return validateName(s, in.Taken) }),
		huh.NewSelect[string]().Key("project").Title("Project").Options(opts...).Value(&vals.Project),
		huh.NewInput().Key("base").Title("Base branch").Value(&vals.Base).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("base branch is required")
				}
				return nil
			}),
		huh.NewInput().Key("branch").Title("Branch").
			Description("empty: a new agent/<name>; an existing local branch or one on origin (a PR head) is checked out").
			Placeholder("agent/<name>").Value(&vals.Branch).
			Validate(validateBranch),
	}
	if len(in.Templates) > 0 {
		if _, ok := templates.Find(in.Templates, vals.Template); !ok {
			vals.Template = ""
		}
		topts := []huh.Option[string]{huh.NewOption("(none)", "")}
		for _, t := range in.Templates {
			topts = append(topts, huh.NewOption(t.Name, t.Name))
		}
		// The tab shows a preview of the selected template beside the form
		// (previewText). Not a huh Note: its DescriptionFunc runs in a
		// command, racing the fields that write vals.
		fields = append(fields, huh.NewSelect[string]().Key("template").Title("Template").
			Options(topts...).Value(&vals.Template))
	}
	groups := []*huh.Group{huh.NewGroup(fields...).Title("New session")}
	prompt := promptField(&vals.Prompt)
	if len(in.Templates) > 0 {
		groups = append(groups, huh.NewGroup(prompt).Title("New session").
			WithHideFunc(func() bool { return vals.Template != "" }))
	} else {
		groups[0] = huh.NewGroup(append(fields, prompt)...).Title("New session")
	}

	f := huh.NewForm(groups...).
		WithTheme(in.Theme.Huh()).
		WithKeyMap(in.Keys.HuhKeyMap()).
		WithShowHelp(true)
	return f, vals, nil
}

func promptField(v *string) *huh.Text {
	return huh.NewText().Key("prompt").Title("First prompt").
		Description("sent to claude as its first message; alt+enter for a new line, ctrl+e for $EDITOR").
		Lines(6).Value(v)
}

func validateName(s string, taken func(string) bool) error {
	if s == "" {
		return errors.New("name is required")
	}
	if worktree.CheckName(s) != nil {
		return errors.New("use letters, digits, - and _ (start with a letter or digit, max 64)")
	}
	if taken != nil && taken(s) {
		return fmt.Errorf("a session called %s already exists", s)
	}
	return nil
}

func validateBranch(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	if worktree.CheckBranch(strings.TrimSpace(s)) != nil {
		return errors.New("not a valid branch name")
	}
	return nil
}

func hasProject(ps []config.Project, name string) bool {
	for _, p := range ps {
		if p.Name == name {
			return true
		}
	}
	return false
}
