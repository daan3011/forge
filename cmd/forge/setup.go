package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/doctor"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/setup"
	"github.com/daan3011/forge/internal/setup/wizard"
	"github.com/daan3011/forge/internal/slashcmd"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// EnvNoSetup set to anything stops the first launch from running setup.
const EnvNoSetup = "FORGE_NO_SETUP"

func newSetupCmd(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "setup [step]",
		Short: "Guided setup: tools, GitHub, Plane, projects, tmux, animations and a smoke test",
		Long: `Walk through forge's setup one step at a time. Every step can be
skipped (s) and run again later; results go to config.yaml (comments kept)
and forge's database. The first launch runs it on its own.

With a step, only that step runs (setup_completed is left as it is):

  ` + strings.Join(setup.StepIDs(), ", ") + `

Steps:
  deps        git, gh, tmux 3.2+, claude; optional delta, chafa, rsvg-convert
  github      gh auth status, and gh auth login if needed
  plane       cloud or self-hosted URL, workspace, and the NAME of the
              variable holding your API key (the key is never stored)
  projects    project roots (folder picker) and each repo's Plane project
  tmux        forge's tmux.conf, and a test of the key back to forge
  animations  full / reduced / off, with a live preview
  capture     the Alt+c line for your own ~/.tmux.conf (copied, never written)
  smoke       a throwaway Claude Code session on its own tmux server, passing
              once the hooks flip it starting → working → idle`,
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: setup.StepIDs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			var only setup.StepID
			if len(args) == 1 {
				id, err := setup.ParseStep(args[0])
				if err != nil {
					return err
				}
				only = id
			}
			cfg, path, _, err := config.LoadOrCreate()
			if err != nil {
				return err
			}
			st, err := store.Open(cmd.Context(), d.dbPath())
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			return d.runSetup(cmd.Context(), cfg, path, st, only)
		},
	}
}

// runSetup runs the wizard on the terminal.
func (d deps) runSetup(ctx context.Context, cfg *config.Config, path string, st *store.Store, only setup.StepID) error {
	m := wizard.New(d.setupDeps(cfg, path, st, only))
	if d.setupProgram != nil {
		return d.setupProgram(m)
	}
	_, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	return err
}

// setupDeps wires the wizard to the real machine (d's fakes in tests).
func (d deps) setupDeps(cfg *config.Config, path string, st *store.Store, only setup.StepID) wizard.Deps {
	keys, err := ui.NewKeyMap(cfg.Keybinds)
	if err != nil {
		keys = ui.DefaultKeyMap()
	}
	conf, _ := config.TmuxConfPath()
	newTmux := func(socket, conf string) tmux.Driver {
		if d.newTmux != nil {
			return d.newTmux(conf)
		}
		return tmux.New(tmux.WithSocket(socket), tmux.WithConfig(conf))
	}
	getenv := os.Getenv
	if d.getenv != nil {
		getenv = d.getenv
	}
	return wizard.Deps{
		Config: cfg, ConfigPath: path,
		Keys: keys, Anim: anim.Resolve(cfg.UI.Animations, getenv),
		Doctor: func(ctx context.Context, names ...string) []doctor.Check {
			e, cleanup := d.doctorEnv(ctx)
			defer cleanup()
			return doctor.RunOnly(ctx, e, names...)
		},
		Getenv: getenv,
		NewPlane: func(c config.Plane, key string) (plane.API, error) {
			c.APIKey = key // only in memory, for this client
			return d.planeClient(c, nil)
		},
		Store: st.Settings(), Projects: st.Projects(),
		Cwd: d.cwd(), Home: d.home(),
		TmuxConf: conf, TmuxColors: tmuxColors(), NewTmux: newTmux,
		Clipboard: clipboard.Exec{},
		Smoke: setup.LiveSmoke{
			Store: st, ForgeBin: forgeBin(), TmuxConf: conf, TmuxColors: tmuxColors(),
			DataDir: config.DataDir(), Skills: slashcmd.NewCatalog(), NewTmux: newTmux,
		},
		Only: only,
	}
}

// firstRunSetup runs setup on the first launch (a config just created and
// setup never finished) when forge has a terminal. It reports whether it
// ran. Existing installs without the flag get a notice instead (setupNotice).
func (d deps) firstRunSetup(ctx context.Context, cfg *config.Config, path string, created bool, st *store.Store, w io.Writer) (bool, error) {
	if !created || !d.interactive() {
		return false, nil
	}
	getenv := os.Getenv
	if d.getenv != nil {
		getenv = d.getenv
	}
	if getenv(EnvNoSetup) != "" {
		return false, nil
	}
	if _, done, err := st.Settings().SetupCompleted(ctx); err != nil || done {
		return false, err
	}
	fmt.Fprintln(w, "first launch: starting the guided setup (forge setup)")
	return true, d.runSetup(ctx, cfg, path, st, "")
}

// interactive reports whether forge runs on a terminal.
func (d deps) interactive() bool {
	if d.isTTY != nil {
		return d.isTTY()
	}
	return isTerminal(os.Stdin) && isTerminal(os.Stdout)
}

// setupNoticeText is the one-time notice for installs that never ran setup.
const setupNoticeText = "New: `forge setup` walks through tools, GitHub, Plane, projects, tmux keys and a smoke test of the hooks."

// setupNotice is shown once when setup never finished.
func setupNotice(ctx context.Context, st *store.Store) (bool, error) {
	_, done, err := st.Settings().SetupCompleted(ctx)
	return !done && err == nil, err
}
