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
	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/cachectl"
	"github.com/daan3011/forge/internal/checkout"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/packs"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/claude"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/sessions/transcript"
	"github.com/daan3011/forge/internal/sessions/worktree"
	"github.com/daan3011/forge/internal/store"
	sessionstab "github.com/daan3011/forge/internal/tabs/sessions"
	"github.com/daan3011/forge/internal/templates"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/usage"
)

// githubPulls is forge's one GitHub client, through `gh auth`: it finds
// the PRs opened from session branches (P1.9), runs the PRs tab searches
// and resolves {pr_*} template variables (P2.1). The TUI points its
// OnResponse at the program so GitHub's rate limit backs the refresh off;
// the client is made on first use.
var githubPulls = github.NewClient(api.ClientOptions{})

// defaultProviders resolves template variables from links: {pr_title} and
// {pr_body} through GitHub, {issue_title} and {issue_body} through Plane.
func defaultProviders() *templates.Registry {
	r := templates.NewRegistry()
	r.Register(store.KindGitHubPR, templates.ProviderFunc(githubPulls.PRContext))
	r.Register(store.KindPlaneIssue, templates.ProviderFunc(planeIssueContext))
	return r
}

// newCheckout checks PRs out in the project checkout, or in a worktree
// under the sessions' worktree root when the checkout is dirty (P2.3).
func newCheckout(cfg *config.Config) *checkout.Checkout {
	g := worktree.NewExec()
	return checkout.New(g, worktree.New(cfg.Sessions.WorktreeRoot, g))
}

// newCaches builds the Cache view's controller (P5.3) over the database,
// git worktrees and the session service (for purging archived sessions and
// resetting the transcript index).
func newCaches(cfg *config.Config, st *store.Store, svc agent.API) *cachectl.Controller {
	g := worktree.NewExec()
	sessions, _ := svc.(cachectl.Sessions)
	return cachectl.New(cachectl.Config{
		Store:        cachectl.StoreFrom(st),
		Worktrees:    worktree.New(cfg.Sessions.WorktreeRoot, g),
		Sessions:     sessions,
		Projects:     cfg.Projects,
		WorktreeRoot: cfg.Sessions.WorktreeRoot,
		DataDir:      filepath.Join(config.DataHome(), "forge"),
		Dirty:        cachectl.DirtyFromGit(g),
	})
}

// newUsage builds the token usage service over every Claude Code transcript
// (P5.4).
func newUsage(cfg *config.Config, st *store.Store) *usage.Service {
	return usage.New(usage.Config{
		Ledger:      st.Usage(),
		Sessions:    st.Sessions(),
		ProjectsDir: claude.DefaultProjectsDir(),
		Projects:    cfg.Projects,
		Usage:       cfg.Usage,
	})
}

// newSessionService builds the real session service over forge's tmux
// server, git worktrees and database.
func newSessionService(cfg *config.Config, st *store.Store) (agent.API, error) {
	drv, conf, err := newTmux()
	if err != nil {
		return nil, err
	}
	return agent.New(agent.Config{
		Tmux:        drv,
		TmuxConf:    conf,
		TmuxColors:  tmuxColors(),
		Worktrees:   worktree.New(cfg.Sessions.WorktreeRoot, worktree.NewExec()),
		Store:       agent.StoreFrom(st),
		Projects:    cfg.Projects,
		Recent:      st.Projects(),
		Transcripts: transcript.StorePersister(st.Cache()),
		DataDir:     filepath.Join(config.DataHome(), "forge"),
		ForgeBin:    forgeBin(),
		Pulls:       githubPulls,
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
the issue / PR it is for (the session is linked to them); --var name=value
fills any other variable. --branch works on an existing branch (e.g. a PR's
head) or names the new one.

--pack gives the agent a context pack from ~/.config/forge/packs: its
preamble and @path mentions of its files start the first prompt.`,
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
			defer func() { _ = st.Close() }()
			here := d.resolveProjects(cmd.Context(), cfg, st, cmd.ErrOrStderr())
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
			ps := loadPacks(cmd.ErrOrStderr())
			usable := sessionstab.UsableProjects(cfg.Projects)
			switch {
			case o.Project != "":
			case here != "":
				o.Project = here // started inside the repo
			case len(usable) == 1:
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
					Theme: theme, Keys: keys, Projects: cfg.Projects, Values: pre, Templates: ts, Packs: ps,
				})
				if err != nil {
					return err
				}
				if err := d.runForm(f); err != nil {
					return err
				}
				o, asked = *vals, true
			}
			project := projectNamed(cfg.Projects, o.Project)
			rs, notes, err := sessionstab.ResolvePacks(ps, o.Packs, project)
			if err != nil {
				return err
			}
			for _, n := range notes {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", n)
			}
			// The packs' files go into the worktree; their block starts the
			// prompt (before the last look at it, when there is one).
			packPlan, _ := sessionstab.SplitPlan(project.Path, rs, nil)
			o.Docs = packPlan
			withPacks := func(prompt string) string { return packs.Prompt(rs, packPlan, prompt) }
			if o.Template != "" {
				tmpl, ok := templates.Find(ts, o.Template)
				if !ok {
					return fmt.Errorf("no template called %q in %s", o.Template, templatesDirName())
				}
				// An explicit --prompt wins; the template is then recorded only.
				if asked || o.Prompt == "" {
					refs := map[store.LinkKind]string{store.KindPlaneIssue: issue, store.KindGitHubPR: pr}
					if o.Prompt, err = templatePrompt(cmd, d, theme, keys, cfg.Projects, tmpl, o, refs, vars, asked, withPacks); err != nil {
						return err
					}
				} else {
					o.Prompt = withPacks(o.Prompt)
				}
			} else {
				o.Prompt = withPacks(o.Prompt)
			}

			svc, err := d.sessions(cfg, st)
			if err != nil {
				return err
			}
			o = sessionstab.WithRefLinks(cfg, o, map[store.LinkKind]string{store.KindPlaneIssue: issue, store.KindGitHubPR: pr})
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
	f.StringVar(&o.Project, "project", "", "project name (default: the repo you're in)")
	f.StringVar(&o.Base, "base", "", "base branch (default main)")
	f.StringVar(&o.Branch, "branch", "", "branch to work on (default agent/<name>); an existing local or origin branch is checked out")
	f.StringVar(&o.Template, "template", "", "session template (fills the first prompt)")
	f.StringVar(&issue, "issue", "", "issue the session is for (template {issue})")
	f.StringVar(&pr, "pr", "", "pull request the session is for, owner/repo#n (template {pr})")
	f.StringArrayVar(&varFlags, "var", nil, "template variable as name=value (repeatable)")
	f.StringVar(&o.Prompt, "prompt", "", "first prompt; - reads stdin")
	f.StringArrayVar(&o.Packs, "pack", nil, "context pack to give the agent (repeatable)")
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

// loadPacks reads the context packs folder. Broken packs are skipped with
// a warning.
func loadPacks(stderr io.Writer) []packs.Pack {
	dir, err := packs.Dir()
	if err != nil {
		return nil
	}
	ps, errs := packs.Load(dir)
	for _, err := range errs {
		fmt.Fprintln(stderr, "warning:", err)
	}
	return ps
}

// projectNamed is the configured project called name.
func projectNamed(ps []config.Project, name string) config.Project {
	for _, p := range ps {
		if p.Name == name {
			return p
		}
	}
	return config.Project{Name: name}
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
	decorate func(string) string,
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
	prompt := decorate(sessionstab.RenderPrompt(tmpl, vals))
	if asked {
		if err := d.runForm(sessionstab.PromptForm(theme, keys, &prompt)); err != nil {
			return "", err
		}
	}
	return prompt, nil
}
