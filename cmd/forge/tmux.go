package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/ui"
)

// tmuxColors is forge's tmux status line palette, from the theme.
func tmuxColors() tmux.ConfigColors {
	bg, fg, accent, muted := ui.TmuxColors()
	return tmux.ConfigColors{Bg: bg, Fg: fg, Accent: accent, Muted: muted}
}

// tmuxConf returns forge's tmux config path, writing the default there on
// first run (an existing file is never touched).
func tmuxConf() (string, error) {
	path, err := config.TmuxConfPath()
	if err != nil {
		return "", err
	}
	if _, err := tmux.EnsureConfig(path, tmuxColors()); err != nil {
		return "", err
	}
	return path, nil
}

// newTmux is the driver for forge's tmux server, started with forge's own
// config file.
func newTmux() (*tmux.Exec, string, error) {
	path, err := tmuxConf()
	if err != nil {
		return nil, "", err
	}
	return tmux.New(tmux.WithConfig(path)), path, nil
}

func (d deps) tmuxDriver() (tmux.Driver, string, error) {
	if d.newTmux != nil {
		path, err := tmuxConf()
		if err != nil {
			return nil, "", err
		}
		return d.newTmux(path), path, nil
	}
	return newTmux()
}

func (d deps) clock() time.Time {
	if d.now != nil {
		return d.now()
	}
	return time.Now()
}

// confirm asks a yes/no question on the terminal; yes skips it.
func (d deps) confirm(title, desc string) (bool, error) {
	if d.ask != nil {
		return d.ask(title, desc)
	}
	if d.runForm == nil {
		return false, errors.New("no terminal to confirm on; pass --yes")
	}
	ok := false
	f := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(title).Description(desc).Affirmative("Restart").Negative("Not now").Value(&ok),
	))
	if err := d.runForm(f); err != nil {
		return false, err
	}
	return ok, nil
}

func newTmuxCmd(d deps) *cobra.Command {
	group := &cobra.Command{
		Use:   "tmux",
		Short: "Manage forge's own tmux server and its config",
		Long: `forge runs agents on its own tmux server (tmux -L forge), started with
~/.config/forge/tmux.conf — never ~/.tmux.conf — and no prefix key.`,
		Args: cobra.NoArgs,
	}

	var yes bool
	restart := &cobra.Command{
		Use:   "restart",
		Short: "Stop forge's tmux server so the next action starts it with the current config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			drv, path, err := d.tmuxDriver()
			if err != nil {
				return err
			}
			return restartTmux(cmd.Context(), d, drv, path, cmd.ErrOrStderr(), yes, true)
		},
	}
	restart.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask, even if agents are running")

	var reset, cfgYes bool
	conf := &cobra.Command{
		Use:   "config",
		Short: "Edit forge's tmux.conf in $EDITOR, then offer to restart the server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			drv, path, err := d.tmuxDriver()
			if err != nil {
				return err
			}
			w := cmd.ErrOrStderr()
			if reset {
				backup, err := tmux.ResetConfig(path, tmuxColors(), d.clock())
				if err != nil {
					return err
				}
				if backup != "" {
					fmt.Fprintln(w, "kept your old config as", backup)
				}
				fmt.Fprintln(w, "wrote forge's default tmux config to", path)
			} else if err := d.opener.Open(cmd.Context(), path); err != nil {
				return err
			}
			return restartTmux(cmd.Context(), d, drv, path, w, cfgYes, false)
		},
	}
	conf.Flags().BoolVar(&reset, "reset", false, "replace the file with forge's default (the old one is kept as tmux.conf.bak.<timestamp>)")
	conf.Flags().BoolVarP(&cfgYes, "yes", "y", false, "restart without asking, even if agents are running")

	group.AddCommand(restart, conf)
	return group
}

// restartTmux kills forge's tmux server, asking first when agents run on
// it. explicit is false when it follows a config edit, where declining
// just leaves the change for the next start.
func restartTmux(ctx context.Context, d deps, drv tmux.Driver, path string, w io.Writer, yes, explicit bool) error {
	ws, err := drv.ListAll(ctx)
	if err != nil {
		return err
	}
	if len(ws) > 0 && !yes {
		fmt.Fprintf(w, "running on forge's tmux server (%d):\n", len(ws))
		for _, t := range ws {
			fmt.Fprintf(w, "  %s / %s\n", t.Session, t.Window)
		}
		title := "Restart forge's tmux server?"
		if !explicit {
			title = "Restart forge's tmux server to apply the new config?"
		}
		ok, err := d.confirm(title, "stops these agents; sessions stay resumable with claude --resume")
		if err != nil {
			return err
		}
		if !ok {
			if explicit {
				_, err := fmt.Fprintln(w, "left forge's tmux server running")
				return err
			}
			_, err := fmt.Fprintln(w, "the new config applies after `forge tmux restart`")
			return err
		}
	}
	if err := drv.KillServer(ctx); err != nil {
		return err
	}
	msg := "forge's tmux server stopped; the next action starts it with " + path
	if len(ws) > 0 {
		names := make([]string, len(ws))
		for i, t := range ws {
			names[i] = t.Window
		}
		msg += "\nstopped " + strings.Join(names, ", ") + " — they stay resumable with claude --resume"
	}
	_, err = fmt.Fprintln(w, msg)
	return err
}
