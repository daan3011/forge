// Package tmux drives forge's own tmux server (`tmux -L forge`), which hosts
// agent sessions: one tmux session per project, one window per agent, named
// after the agent. It never talks to the user's default tmux server.
//
// Window and session names are the stable ids. They are sanitised to
// [A-Za-z0-9_-] so they can never contain tmux target syntax, and every
// target is written in exact-match form ("=session:=window").
//
// Detaching back to forge uses whatever the user's ~/.tmux.conf binds (the
// spec recommends `bind -n M-d detach-client`); forge does not inject keys.
package tmux

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// DefaultSocket is the tmux socket name (`tmux -L`) forge uses.
const DefaultSocket = "forge"

// PlaceholderWindow is created with every session so it outlives its last
// agent window. ListWindows never returns it; SanitizeName can never produce
// it.
const PlaceholderWindow = "_forge"

// maxNameLen caps sanitised names.
const maxNameLen = 64

var (
	// ErrInvalidName means a name is empty after sanitising, or a Target
	// part is not already a sanitised name.
	ErrInvalidName = errors.New("tmux: invalid name")
	// ErrWindowExists means NewWindow was asked for a name already in use.
	ErrWindowExists = errors.New("tmux: window already exists")
	// ErrNotFound means the session, window or server does not exist.
	ErrNotFound = errors.New("tmux: not found")
)

// Target names one window.
type Target struct {
	Session string
	Window  string
}

// String is the exact-match tmux target "=session:=window".
func (t Target) String() string {
	return "=" + t.Session + ":=" + t.Window
}

// Validate reports ErrInvalidName unless both parts are sanitised names.
func (t Target) Validate() error {
	if err := CheckName(t.Session); err != nil {
		return err
	}
	return CheckName(t.Window)
}

// sessionTarget is the exact-match target for a whole session.
func sessionTarget(session string) string {
	return "=" + session + ":"
}

// Window is one agent window as reported by tmux.
type Window struct {
	ID         string // tmux window id, e.g. "@3"; not stable across server restarts
	Index      int
	Name       string
	Dead       bool // the pane's process exited (remain-on-exit keeps it)
	ExitStatus int  // only meaningful when Dead
	Command    string
}

// Driver is everything forge needs from tmux.
type Driver interface {
	// EnsureSession creates the session for project if it doesn't exist and
	// returns its (sanitised) name.
	EnsureSession(ctx context.Context, project string) (string, error)
	// NewWindow starts cmd (argv, no shell; empty means the default shell)
	// in a new detached window. name is sanitised; the returned Target holds
	// the name actually used.
	NewWindow(ctx context.Context, session, name, dir string, cmd []string, env map[string]string) (Target, error)
	// ListWindows returns the session's agent windows; a missing session
	// gives nil, nil.
	ListWindows(ctx context.Context, session string) ([]Window, error)
	// WindowAlive reports whether the window exists and its process runs.
	WindowAlive(ctx context.Context, t Target) (bool, error)
	// KillWindow removes the window; a missing window is not an error.
	KillWindow(ctx context.Context, t Target) error
	// SendKeys types text literally, then presses Enter if enter is set.
	SendKeys(ctx context.Context, t Target, text string, enter bool) error
	// CapturePane returns the last lines lines of the window's pane (the
	// visible screen if lines <= 0), without trailing blank lines.
	CapturePane(ctx context.Context, t Target, lines int) (string, error)
	// Attach shows the window to the user. Inside a client of forge's own
	// tmux server it switches that client and returns nil. Otherwise it
	// returns a command to run with tea.ExecProcess; it exits when the user
	// detaches. Its stdio is left unset for the caller (ExecProcess fills it).
	// ctx bounds only the lookup and switch; cancelling it later does not
	// kill the returned command.
	Attach(ctx context.Context, t Target) (*exec.Cmd, error)
}

// SanitizeName maps s to a safe tmux name: runs of characters outside
// [A-Za-z0-9_-] become "-", leading "-"/"_" and trailing "-" are dropped,
// and the result is capped at 64 bytes. Distinct inputs may collide
// ("a.b", "a:b"); callers that need uniqueness check ListWindows.
func SanitizeName(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
			dash = r == '-'
		case !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimLeft(b.String(), "-_")
	if len(out) > maxNameLen {
		out = out[:maxNameLen]
	}
	return strings.TrimRight(out, "-")
}

// CheckName returns ErrInvalidName unless name is non-empty and already
// sanitised.
func CheckName(name string) error {
	if name == "" || SanitizeName(name) != name {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	return nil
}

// LastLines trims trailing blank lines from s and keeps at most n lines
// (all if n <= 0).
func LastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
