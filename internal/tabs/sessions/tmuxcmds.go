package sessions

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/editor"
	"github.com/daan3011/forge/internal/sessions/tmux"
)

// Palette commands that manage forge's tmux server and its config file
// (the TUI side of `forge tmux restart` / `forge tmux config`).

// restartReason is why a restart of forge's tmux server is offered.
type restartReason int

const (
	reasonRestart restartReason = iota // the user asked for a restart
	reasonEdited                       // the config was just edited
	reasonReset                        // the config was just reset
)

type (
	tmuxWindowsMsg struct {
		reason  restartReason
		windows []tmux.Target
		err     error
	}
	tmuxRestartedMsg struct {
		reason restartReason
		asked  bool // the user confirmed stopping running agents
		err    error
	}
	tmuxEditedMsg struct{ err error }
	tmuxResetMsg  struct {
		backup string
		err    error
	}
)

// updateTmux handles the tmux command messages; ok is false for others.
func (t *Tab) updateTmux(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	switch msg := msg.(type) {
	case tmuxWindowsMsg:
		return t.tmuxListed(msg), true
	case tmuxRestartedMsg:
		return t.tmuxRestarted(msg), true
	case tmuxEditedMsg:
		if msg.err != nil {
			return errCmd(fmt.Errorf("edit tmux config: %w", msg.err)), true
		}
		return t.offerTmuxRestart(reasonEdited), true
	case tmuxResetMsg:
		if msg.err != nil {
			return errCmd(fmt.Errorf("reset tmux config: %w", msg.err)), true
		}
		note := "tmux config reset to forge's default"
		if msg.backup != "" {
			note += " — old file kept as " + filepath.Base(msg.backup)
		}
		return tea.Batch(info(note), t.offerTmuxRestart(reasonReset)), true
	}
	return nil, false
}

// offerTmuxRestart lists the running agents, then restarts the server:
// straight away when none run, after a confirmation otherwise.
func (t *Tab) offerTmuxRestart(reason restartReason) tea.Cmd {
	if t.svc == nil {
		return nil
	}
	svc := t.svc
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		ws, err := svc.TmuxWindows(ctx)
		return tmuxWindowsMsg{reason: reason, windows: ws, err: err}
	}
}

func (t *Tab) restartTmux() tea.Cmd { return t.offerTmuxRestart(reasonRestart) }

func (t *Tab) tmuxListed(msg tmuxWindowsMsg) tea.Cmd {
	if msg.err != nil {
		return errCmd(fmt.Errorf("list tmux windows: %w", msg.err))
	}
	if len(msg.windows) == 0 {
		return t.killTmuxServer(msg.reason, false)
	}
	names := make([]string, len(msg.windows))
	for i, w := range msg.windows {
		names[i] = w.Window
	}
	title := "Restart forge's tmux server?"
	if msg.reason != reasonRestart {
		title = "Restart forge's tmux server to apply the new config?"
	}
	yes := false
	f := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Key("restart").
			Title(title).
			Description(fmt.Sprintf("stops %d running agent window(s): %s — sessions stay resumable with claude --resume",
				len(names), strings.Join(names, ", "))).
			Affirmative("Restart").Negative("Later").Value(&yes),
	)).WithTheme(t.theme.Huh()).WithKeyMap(t.keys.HuhKeyMap()).WithShowHelp(false)
	return t.openDialog("forge tmux", f, func() tea.Cmd {
		if !yes {
			if msg.reason == reasonRestart {
				return nil
			}
			return info("new tmux config applies after a restart (palette: tmux: restart forge server)")
		}
		return t.killTmuxServer(msg.reason, true)
	})
}

func (t *Tab) killTmuxServer(reason restartReason, asked bool) tea.Cmd {
	t.busy++
	svc := t.svc
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return tmuxRestartedMsg{reason: reason, asked: asked, err: svc.RestartTmux(ctx)}
	}
}

func (t *Tab) tmuxRestarted(msg tmuxRestartedMsg) tea.Cmd {
	t.busy--
	if msg.err != nil {
		return errCmd(fmt.Errorf("restart forge tmux server: %w", msg.err))
	}
	text := "forge tmux server restarted — the next session starts it with " + t.svc.TmuxConfig()
	switch {
	case msg.asked:
		text = "forge tmux server restarted — stopped sessions resume with claude --resume"
	case msg.reason != reasonRestart:
		text = "tmux config saved — it applies from the next session"
	}
	return tea.Batch(t.forceRefresh(), success(text))
}

// editTmuxConfig opens the config in $EDITOR, then offers a restart.
func (t *Tab) editTmuxConfig() tea.Cmd {
	if t.svc == nil {
		return nil
	}
	path := t.svc.TmuxConfig()
	if path == "" {
		return errCmd(errors.New("no tmux config file configured"))
	}
	c := editor.Exec{}.Cmd(context.Background(), path)
	return tea.ExecProcess(c, func(err error) tea.Msg { return tmuxEditedMsg{err} })
}

// resetTmuxConfig asks, then restores the default config (backing up the
// current one) and offers a restart.
func (t *Tab) resetTmuxConfig() tea.Cmd {
	if t.svc == nil {
		return nil
	}
	yes := false
	f := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Key("reset").
			Title("Reset forge's tmux config to the default?").
			Description("your current " + filepath.Base(t.svc.TmuxConfig()) + " is kept as a timestamped .bak file").
			Affirmative("Reset").Negative("Keep").Value(&yes),
	)).WithTheme(t.theme.Huh()).WithKeyMap(t.keys.HuhKeyMap()).WithShowHelp(false)
	svc := t.svc
	return t.openDialog("forge tmux", f, func() tea.Cmd {
		if !yes {
			return nil
		}
		return func() tea.Msg {
			backup, err := svc.ResetTmuxConfig()
			return tmuxResetMsg{backup: backup, err: err}
		}
	})
}
