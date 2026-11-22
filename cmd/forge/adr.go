package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/docs"
	"github.com/daan3011/forge/internal/store"
	docstab "github.com/daan3011/forge/internal/tabs/docs"
	"github.com/daan3011/forge/internal/ui"
)

// adrProjects are the projects with a local docs folder.
func adrProjects(ps []config.Project) []config.Project {
	var out []config.Project
	for _, p := range ps {
		if p.Path != "" || filepath.IsAbs(p.DocsDir) {
			out = append(out, p)
		}
	}
	return out
}

func newADRCmd(d deps) *cobra.Command {
	var (
		v    docstab.DecisionValues
		edit bool
	)
	cmd := &cobra.Command{
		Use:   "adr",
		Short: "Log an architecture decision in the project's decision log",
		Long: `Log a decision as the next numbered markdown file in the project's
decisions folder (<docs>/decisions/NNNN-slug.md, default .forge/docs/decisions).

Without --title and --decision (or with several projects and no --project
outside a repo), an interactive form asks for them. The project's CLAUDE.md
gets one line pointing agents at the decision log the first time.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, _, err := config.LoadOrCreate()
			if err != nil {
				return err
			}
			st, err := store.Open(cmd.Context(), d.dbPath())
			if err != nil {
				return err
			}
			here := d.resolveProjects(cmd.Context(), cfg, st, cmd.ErrOrStderr())
			_ = st.Close()

			usable := adrProjects(cfg.Projects)
			if len(usable) == 0 {
				return errors.New("no project with a local checkout: run forge inside a repo or add one under projects: in the config")
			}
			names := make([]string, len(usable))
			for i, p := range usable {
				names[i] = p.Name
			}
			project := v.Project
			switch {
			case project != "":
			case here != "":
				project = here
			case len(usable) == 1:
				project = usable[0].Name
			}
			if v.Validate() != nil || project == "" {
				if d.runForm == nil {
					return errors.New("--title and --decision (and --project outside a repo) are required when not on a terminal")
				}
				keys, err := ui.NewKeyMap(cfg.Keybinds)
				if err != nil {
					return err
				}
				theme := ui.NewTheme(lipgloss.HasDarkBackground(os.Stdin, os.Stdout), cfg.UI.Theme)
				v.Project = project
				if v.Project == "" {
					v.Project = names[0]
				}
				if err := d.runForm(docstab.DecisionForm(theme, keys, names, &v)); err != nil {
					return err
				}
				project = v.Project
				if err := v.Validate(); err != nil {
					return err
				}
			}
			var p *config.Project
			for i := range usable {
				if usable[i].Name == project {
					p = &usable[i]
				}
			}
			if p == nil {
				return fmt.Errorf("no project called %q with a local checkout", project)
			}
			now := time.Now
			if d.now != nil {
				now = d.now
			}
			l, err := docs.LogDecision(p.DocsPath(), p.Path, v.DecisionInput, now())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "logged %s\n%s\n", l.Label(), l.Path)
			switch {
			case l.NoteErr != nil:
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: couldn't point CLAUDE.md at the decision log:", l.NoteErr)
			case l.Noted:
				fmt.Fprintln(cmd.OutOrStdout(), "added a decision log note to", filepath.Join(p.Path, "CLAUDE.md"))
			}
			if edit && d.opener != nil {
				return d.opener.Open(cmd.Context(), l.Path)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&v.Project, "project", "", "project name (default: the repo you're in)")
	f.StringVar(&v.Title, "title", "", "short title of the decision")
	f.StringVar(&v.Context, "context", "", "why the decision came up")
	f.StringVar(&v.Decision, "decision", "", "what was decided")
	f.StringVar(&v.Alternatives, "alternatives", "", "what else was considered")
	f.StringVar(&v.Consequences, "consequences", "", "what follows from it")
	f.BoolVar(&edit, "edit", false, "open the new decision in $EDITOR afterwards")
	return cmd
}
