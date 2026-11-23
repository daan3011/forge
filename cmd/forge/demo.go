package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/app"
	"github.com/daan3011/forge/internal/config"
	fixture "github.com/daan3011/forge/internal/demo"
	"github.com/daan3011/forge/internal/sessions/tmux/tmuxdemo"
	"github.com/daan3011/forge/internal/ui/anim"
	"github.com/daan3011/forge/internal/ui/anim/animdemo"
)

// newDemoCmd is the hidden `forge demo` group of development showcases.
func newDemoCmd() *cobra.Command {
	demo := &cobra.Command{
		Use:    "demo",
		Short:  "Development showcases",
		Hidden: true,
		Args:   cobra.NoArgs,
	}
	var level string
	animCmd := &cobra.Command{
		Use:   "anim",
		Short: "Show every forge animation side by side",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			lvl, err := demoLevel(level)
			if err != nil {
				return err
			}
			m := animdemo.New(anim.Resolve(lvl, os.Getenv))
			_, err = tea.NewProgram(m, tea.WithContext(cmd.Context())).Run()
			return err
		},
	}
	animCmd.Flags().StringVar(&level, "level", "", "full, reduced or off (default: ui.animations from the config)")
	tmuxCmd := &cobra.Command{
		Use:   "tmux",
		Short: "Run bash on forge's tmux server; attach with enter (a popup inside your tmux), back with Alt+d",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			drv, _, err := newTmux()
			if err != nil {
				return err
			}
			_, err = tea.NewProgram(tmuxdemo.New(drv, dir), tea.WithContext(cmd.Context())).Run()
			return err
		},
	}
	var appLevel string
	appCmd := &cobra.Command{
		Use:   "app",
		Short: "Run the full TUI on fictional data (GitHub, Plane and agents are fakes)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			lvl, err := demoLevel(appLevel)
			if err != nil {
				return err
			}
			return runDemoApp(cmd.Context(), lvl)
		},
	}
	appCmd.Flags().StringVar(&appLevel, "level", "", "full, reduced or off (default: ui.animations from the config)")
	demo.AddCommand(animCmd, tmuxCmd, appCmd)
	return demo
}

// demoLevel is the --level flag, or the configured level if the config can
// be read, or full. It never creates a config file.
func demoLevel(flag string) (config.Animations, error) {
	switch l := config.Animations(flag); l {
	case config.AnimationsFull, config.AnimationsReduced, config.AnimationsOff:
		return l, nil
	case "":
	default:
		return "", fmt.Errorf("--level: must be one of full, reduced, off; got %q", flag)
	}
	if p, err := config.Path(); err == nil {
		if cfg, err := config.Load(p); err == nil {
			return cfg.UI.Animations, nil
		}
	}
	return config.AnimationsFull, nil
}

// runDemoApp runs forge on internal/demo's fixtures in a temp folder that
// also stands in for $HOME, so nothing real is read or shown.
func runDemoApp(ctx context.Context, lvl config.Animations) (err error) {
	base := "" // $TMPDIR; /tmp gives shorter paths on screen when it exists
	if fi, err := os.Stat("/tmp"); err == nil && fi.IsDir() {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "forge-demo-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	for k, v := range fixture.Env(dir) {
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	cfg, opts, closeDB, err := fixture.New(ctx, dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, closeDB()) }()
	cfg.UI.Animations = lvl
	m, err := app.New(cfg, append(opts, app.WithSplash())...)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m, tea.WithContext(ctx)).Run()
	return err
}
