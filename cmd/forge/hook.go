package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/notify"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/alert"
	"github.com/daan3011/forge/internal/sessions/hooks"
	"github.com/daan3011/forge/internal/sessions/live"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/store"
)

// hookTimeout bounds a whole `forge hook` run. Claude Code waits for hooks
// (forge registers them with a 5 s timeout), so it gives up well before.
const hookTimeout = 2 * time.Second

// hookLogMax is the size at which hook.log is rotated to hook.log.1.
const hookLogMax = 1 << 20

func newHookCmd(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "hook <event>",
		Short: "Ingest a Claude Code hook event (called by Claude Code)",
		Long: `Reads a Claude Code hook event as JSON on stdin, records it, updates the
session's status and pings a running forge. Registered by forge in each
session worktree's .claude/settings.local.json.

It never fails the hook: it always exits 0 and prints nothing on stdout.
Problems are logged to hook.log next to forge's database.`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true, // a hook must never fail on arguments
		RunE: func(cmd *cobra.Command, args []string) error {
			in := cmd.InOrStdin()
			if f, ok := in.(*os.File); ok && term.IsTerminal(f.Fd()) {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "forge hook reads a Claude Code hook event on stdin; it is not meant to be run by hand")
				return nil
			}
			d.runHook(cmd.Context(), args, in)
			return nil
		},
	}
}

// runHook ingests one event. Every failure, even a panic, ends up in the
// hook log instead of failing the hook.
func (d deps) runHook(ctx context.Context, args []string, stdin io.Reader) {
	event := ""
	if len(args) > 0 {
		event = args[0]
	}
	defer func() {
		if r := recover(); r != nil {
			d.logHook(event, fmt.Errorf("panic: %v", r))
		}
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()
	if err := d.hook(ctx, event, stdin); err != nil {
		d.logHook(event, err)
	}
}

func (d deps) hook(ctx context.Context, event string, stdin io.Reader) error {
	p, err := hooks.Parse(stdin)
	if err != nil {
		return err
	}
	if p.Event == "" {
		p.Event = event
	}
	st, err := store.Open(ctx, d.dbPath())
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	res, err := hooks.Ingest(ctx, hooks.StoreFrom(st), p, d.env(agent.EnvSessionName), d.clock())
	if err != nil {
		return err
	}
	if res.Session == "" {
		return nil // not a forge session: nothing to show
	}
	delivered, err := d.liveSender().Send(ctx, live.Ping{
		Session: res.Session, ClaudeSessionID: p.SessionID, Event: p.Event,
		Status: string(res.Status), Changed: res.Changed,
	})
	if delivered {
		return nil // the running TUI notifies (P1.8)
	}
	return errors.Join(err, d.hookNotify(ctx, st, p.Event, res))
}

// hookNotify sends the desktop notification for an event when no forge TUI
// is running (P1.8). Waiting notifies at once (there is nobody to wait
// notify_after_minutes), debounced in the database; errors and finished
// turns notify since forge can't be focused.
func (d deps) hookNotify(ctx context.Context, st *store.Store, event string, res hooks.Result) error {
	kind, ok := alert.ForEvent(event, res.Status, res.Changed)
	if !ok {
		return nil
	}
	cfg, err := d.hookConfig()
	if err != nil {
		d.logHook(event, err) // notify with the defaults anyway
	}
	c := alert.Checker{
		Store: st.Sessions(), Notifier: d.notifications(), Watcher: d.tmuxWatcher(),
		Policy: alert.PolicyFrom(cfg.Sessions), Clock: d.clock,
	}
	_, err = c.NotifyEvent(ctx, res.Session, kind)
	return err
}

// hookConfig loads the config without creating it; a missing file is the
// defaults. On other errors it returns the defaults and the error.
func (d deps) hookConfig() (*config.Config, error) {
	load := d.loadConfig
	if load == nil {
		load = func() (*config.Config, error) {
			path, err := config.Path()
			if err != nil {
				return nil, err
			}
			return config.Load(path)
		}
	}
	cfg, err := load()
	switch {
	case err == nil:
		return cfg, nil
	case errors.Is(err, fs.ErrNotExist):
		return config.Default(), nil
	default:
		return config.Default(), err
	}
}

// tmuxWatcher is the tmux driver asked whether a window is being
// watched. It only queries, so it needs no config file.
func (d deps) tmuxWatcher() alert.Watcher {
	if d.newTmux != nil {
		return d.newTmux("")
	}
	return tmux.New()
}

// notifications is the desktop notifier.
func (d deps) notifications() notify.Notifier {
	if d.notifier != nil {
		return d.notifier
	}
	return notify.System{}
}

func (d deps) env(key string) string {
	if d.getenv != nil {
		return d.getenv(key)
	}
	return os.Getenv(key)
}

func (d deps) liveSender() live.Sender {
	if d.sender != nil {
		return d.sender
	}
	return live.Socket{Path: live.DefaultPath()}
}

// hookLogPath is hook.log next to the database.
func (d deps) hookLogPath() string {
	return filepath.Join(filepath.Dir(d.dbPath()), "hook.log")
}

// logHook appends one line to the hook log, best effort.
func (d deps) logHook(event string, err error) {
	path := d.hookLogPath()
	if fi, statErr := os.Stat(path); statErr == nil && fi.Size() > hookLogMax {
		_ = os.Rename(path, path+".1")
	}
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o700); mkErr != nil {
		return
	}
	f, openErr := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if openErr != nil {
		return
	}
	defer func() { _ = f.Close() }()
	msg := strings.ReplaceAll(err.Error(), "\n", " ")
	if errors.Is(err, context.DeadlineExceeded) {
		msg += " (gave up after " + hookTimeout.String() + ")"
	}
	_, _ = fmt.Fprintf(f, "%s %s: %s\n", d.clock().Format(time.RFC3339), event, msg)
}
