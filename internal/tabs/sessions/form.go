package sessions

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/config"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/worktree"
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
	// Templates offered (P1.7); the field is left out when empty.
	Templates []string
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
	}
	if len(in.Templates) > 0 {
		topts := []huh.Option[string]{huh.NewOption("(none)", "")}
		for _, t := range in.Templates {
			topts = append(topts, huh.NewOption(t, t))
		}
		fields = append(fields, huh.NewSelect[string]().Key("template").Title("Template").
			Options(topts...).Value(&vals.Template))
	}
	fields = append(fields, huh.NewText().Key("prompt").Title("First prompt").
		Description("sent to claude as its first message; alt+enter for a new line, ctrl+e for $EDITOR").
		Lines(6).Value(&vals.Prompt))

	f := huh.NewForm(huh.NewGroup(fields...).Title("New session")).
		WithTheme(in.Theme.Huh()).
		WithKeyMap(in.Keys.HuhKeyMap()).
		WithShowHelp(true)
	return f, vals, nil
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

func hasProject(ps []config.Project, name string) bool {
	for _, p := range ps {
		if p.Name == name {
			return true
		}
	}
	return false
}
