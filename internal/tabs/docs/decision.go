package docs

import (
	"errors"
	"strings"

	"charm.land/huh/v2"

	docfs "github.com/daan3011/forge/internal/docs"
	"github.com/daan3011/forge/internal/ui"
)

// DecisionValues are what the decision form fills in: the project (by
// name) and the decision itself.
type DecisionValues struct {
	Project string
	docfs.DecisionInput
}

// DecisionForm returns the "log a decision" form (P4.4), the same in the
// Docs tab and in `forge adr`. Projects are offered only when there is a
// choice; v holds the defaults and receives the answers. theme and keys
// may be nil (the tab styles its dialogs itself).
func DecisionForm(theme *ui.Theme, keys *ui.KeyMap, projects []string, v *DecisionValues) *huh.Form {
	var fields []huh.Field
	if len(projects) > 1 {
		opts := make([]huh.Option[string], len(projects))
		for i, p := range projects {
			opts[i] = huh.NewOption(p, p)
		}
		fields = append(fields, huh.NewSelect[string]().Key("project").Title("Project").Options(opts...).Value(&v.Project))
	}
	required := func(what string) func(string) error {
		return func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New(what + " is required")
			}
			return nil
		}
	}
	fields = append(fields,
		huh.NewInput().Key("title").Title("Title").Placeholder("Use SQLite for the cache").
			Value(&v.Title).Validate(required("a title")),
		huh.NewText().Key("context").Title("Context").Description("Why this came up (optional)").
			Lines(3).Value(&v.Context),
		huh.NewText().Key("decision").Title("Decision").Description("What we decided").
			Lines(3).Value(&v.Decision).Validate(required("the decision")),
		huh.NewText().Key("alternatives").Title("Alternatives").Description("What else was considered (optional)").
			Lines(2).Value(&v.Alternatives),
		huh.NewText().Key("consequences").Title("Consequences").Description("What follows from it (optional)").
			Lines(2).Value(&v.Consequences),
	)
	form := huh.NewForm(huh.NewGroup(fields...).Title("Log a decision"))
	if theme != nil {
		form = form.WithTheme(theme.Huh())
	}
	if keys != nil {
		form = form.WithKeyMap(keys.HuhKeyMap())
	}
	return form
}
