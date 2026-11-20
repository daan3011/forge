package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/config"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/store"
	sessionstab "github.com/daan3011/forge/internal/tabs/sessions"
	"github.com/daan3011/forge/internal/templates"
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
		ForgeBin:   forgeBin(),
	}), nil
}

// forgeBin is the absolute path of this forge binary, which the Claude
// Code hooks in new worktrees call. "" (no hooks) if it can't be found.
func forgeBin() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe
}

func newNewCmd(d deps) *cobra.Command {
	var (
		o         agent.StartOpts
		issue, pr string
		varFlags  []string
	)
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Start a new named agent session",
		Long: `Start a named Claude Code session in its own worktree and tmux window.

Without --name and --project (or with only one project configured), an
interactive form asks for the rest. --prompt - reads the prompt from stdin.

--template fills the first prompt from a session template in
~/.config/forge/templates (unless --prompt is given). --issue and --pr name
the issue / PR it is for; --var name=value fills any other variable.`,
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
			vars, err := parseVars(varFlags)
			if err != nil {
				return err
			}
			ts, err := loadTemplates(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			usable := sessionstab.UsableProjects(cfg.Projects)
			if o.Project == "" && len(usable) == 1 {
				o.Project = usable[0].Name
			}
			var theme *ui.Theme
			var keys *ui.KeyMap
			if d.runForm != nil {
				if keys, err = ui.NewKeyMap(cfg.Keybinds); err != nil {
					return err
				}
				theme = ui.NewTheme(lipgloss.HasDarkBackground(os.Stdin, os.Stdout), cfg.UI.Theme)
			}
			asked := false
			if o.Name == "" || o.Project == "" {
				if d.runForm == nil {
					return errors.New("--name and --project are required when not on a terminal")
				}
				pre := o
				if pre.Template == "" && pre.Prompt == "" {
					pre.Template = cfg.Sessions.DefaultTemplate // pre-selected only
				}
				f, vals, err := sessionstab.NewForm(sessionstab.FormInput{
					Theme: theme, Keys: keys, Projects: cfg.Projects, Values: pre, Templates: ts,
				})
				if err != nil {
					return err
				}
				if err := d.runForm(f); err != nil {
					return err
				}
				o, asked = *vals, true
			}
			if o.Template != "" {
				tmpl, ok := templates.Find(ts, o.Template)
				if !ok {
					return fmt.Errorf("no template called %q in %s", o.Template, templatesDirName())
				}
				// An explicit --prompt wins; the template is then recorded only.
				if asked || o.Prompt == "" {
					refs := map[store.LinkKind]string{store.KindPlaneIssue: issue, store.KindGitHubPR: pr}
					if o.Prompt, err = templatePrompt(cmd, d, theme, keys, cfg.Projects, tmpl, o, refs, vars, asked); err != nil {
						return err
					}
				}
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
	f.StringVar(&o.Template, "template", "", "session template (fills the first prompt)")
	f.StringVar(&issue, "issue", "", "issue the session is for (template {issue})")
	f.StringVar(&pr, "pr", "", "pull request the session is for, owner/repo#n (template {pr})")
	f.StringArrayVar(&varFlags, "var", nil, "template variable as name=value (repeatable)")
	f.StringVar(&o.Prompt, "prompt", "", "first prompt; - reads stdin")
	return cmd
}

// parseVars reads --var name=value flags.
func parseVars(flags []string) (map[string]string, error) {
	vars := map[string]string{}
	for _, f := range flags {
		name, value, ok := strings.Cut(f, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("--var %q: use name=value", f)
		}
		vars[name] = value
	}
	return vars, nil
}

func templatesDirName() string {
	dir, err := templates.Dir()
	if err != nil {
		return "the templates folder"
	}
	return dir
}

// loadTemplates reads the templates folder, creating it with the defaults
// on first use. Broken templates are skipped with a warning.
func loadTemplates(stderr io.Writer) ([]templates.Template, error) {
	dir, err := templates.Dir()
	if err != nil {
		return nil, err
	}
	if _, err := templates.EnsureDefaults(dir); err != nil {
		return nil, err
	}
	ts, errs := templates.Load(dir)
	for _, err := range errs {
		fmt.Fprintln(stderr, "warning:", err)
	}
	return ts, nil
}

// templatePrompt runs the template flow: ask for refs and missing
// variables on a terminal (an error naming them without one), then show
// the rendered prompt for editing if the form was used.
func templatePrompt(cmd *cobra.Command, d deps, theme *ui.Theme, keys *ui.KeyMap, projects []config.Project,
	tmpl templates.Template, o agent.StartOpts, refs map[store.LinkKind]string, vars map[string]string, asked bool,
) (string, error) {
	if d.runForm != nil {
		if f, get := sessionstab.RefsForm(theme, keys, tmpl, refs); f != nil {
			if err := d.runForm(f); err != nil {
				return "", err
			}
			refs = get()
		}
	}
	vals, missing, errs := templates.Resolve(cmd.Context(), d.providers, tmpl, sessionstab.TemplateInput(projects, o, refs, vars))
	for _, err := range errs {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning:", err)
	}
	if len(missing) > 0 {
		if d.runForm == nil {
			return "", fmt.Errorf("template %s needs %s: pass --var name=value (or --issue / --pr)",
				tmpl.Name, strings.Join(missing, ", "))
		}
		f, get := sessionstab.VarsForm(theme, keys, tmpl, missing)
		if err := d.runForm(f); err != nil {
			return "", err
		}
		maps.Copy(vals, get())
	}
	prompt := sessionstab.RenderPrompt(tmpl, vals)
	if asked {
		if err := d.runForm(sessionstab.PromptForm(theme, keys, &prompt)); err != nil {
			return "", err
		}
	}
	return prompt, nil
}
