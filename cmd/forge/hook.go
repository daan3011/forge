package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/hooks"
	"github.com/daan3011/forge/internal/sessions/live"
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
	return d.liveSender().Send(ctx, live.Ping{
		Session: res.Session, ClaudeSessionID: p.SessionID, Event: p.Event,
		Status: string(res.Status), Changed: res.Changed,
	})
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
