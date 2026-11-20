package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/config"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/store"
	sessionstab "github.com/daan3011/forge/internal/tabs/sessions"
	"github.com/daan3011/forge/internal/ui"
)

// newSessionService builds the real session service over forge's tmux
// server, git worktrees and database.
func newSessionService(cfg *config.Config, st *store.Store) (agent.API, error) {
	drv, conf, err := newTmux()
	if err != nil {
		return nil, err
	}
	return agent.New(agent.Config{
		Tmux:       drv,
		TmuxConf:   conf,
		TmuxColors: tmuxColors(),
		Worktrees:  worktree.New(cfg.Sessions.WorktreeRoot, worktree.NewExec()),
		Store:      agent.StoreFrom(st),
		Projects:   cfg.Projects,
		DataDir:    filepath.Join(config.DataHome(), "forge"),
	}), nil
}

func newNewCmd(d deps) *cobra.Command {
	var o agent.StartOpts
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Start a new named agent session",
		Long: `Start a named Claude Code session in its own worktree and tmux window.

Without --name and --project (or with only one project configured), an
interactive form asks for the rest. --prompt - reads the prompt from stdin.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, _, err := config.LoadOrCreate()
			if err != nil {
				return err
			}
			if o.Prompt == "-" {
				b, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				o.Prompt = string(b)
			}
			usable := sessionstab.UsableProjects(cfg.Projects)
			if o.Project == "" && len(usable) == 1 {
				o.Project = usable[0].Name
			}
			if o.Name == "" || o.Project == "" {
				if d.runForm == nil {
					return errors.New("--name and --project are required when not on a terminal")
				}
				keys, err := ui.NewKeyMap(cfg.Keybinds)
				if err != nil {
					return err
				}
				f, vals, err := sessionstab.NewForm(sessionstab.FormInput{
					Theme: ui.NewTheme(lipgloss.HasDarkBackground(os.Stdin, os.Stdout), cfg.UI.Theme),
					Keys:  keys, Projects: cfg.Projects, Values: o,
				})
				if err != nil {
					return err
				}
				if err := d.runForm(f); err != nil {
					return err
				}
				o = *vals
			}

			st, err := store.Open(cmd.Context(), d.dbPath())
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			svc, err := d.sessions(cfg, st)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "starting %s…\n", o.Name)
			res, err := svc.Start(cmd.Context(), o)
			if err != nil {
				return err
			}
			if res.FetchErr != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: couldn't fetch origin; branched from the local base:", res.FetchErr)
			}
			x := res.Session
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "started %s on %s in %s\nattach: tmux -L %s attach -t %s\n",
				x.Name, x.Branch, x.Worktree, tmux.DefaultSocket,
				tmux.Target{Session: x.TmuxSession, Window: x.TmuxWindow}.String())
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.Name, "name", "", "session name (letters, digits, - and _)")
	f.StringVar(&o.Project, "project", "", "project name from the config")
	f.StringVar(&o.Base, "base", "", "base branch (default main)")
	f.StringVar(&o.Template, "template", "", "session template")
	f.StringVar(&o.Prompt, "prompt", "", "first prompt; - reads stdin")
	return cmd
}
