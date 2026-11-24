package setup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"time"

	"github.com/daan3011/forge/internal/sessions/tmux"
)

// ConfState is the state of forge's tmux.conf.
type ConfState int

// The states.
const (
	ConfMissing  ConfState = iota
	ConfCurrent            // the shipped version
	ConfOutdated           // an older version header
	ConfCustom             // no version header: hand-written, left alone
)

// TmuxConfState looks at forge's tmux config at path.
func TmuxConfState(path string) (ConfState, int) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return ConfMissing, 0
	}
	v, ok := tmux.ConfigVersionOf(path)
	switch {
	case !ok:
		return ConfCustom, 0
	case v < tmux.ConfigVersion:
		return ConfOutdated, v
	}
	return ConfCurrent, v
}

// Describe is the state as one line.
func (s ConfState) Describe(path string, v int) string {
	switch s {
	case ConfMissing:
		return "not written yet: " + path
	case ConfOutdated:
		return fmt.Sprintf("%s is v%d; forge ships v%d (%s)", path, v, tmux.ConfigVersion, tmux.ConfigNews)
	case ConfCustom:
		return path + " has no version header (customised); forge leaves it alone"
	}
	return fmt.Sprintf("%s is up to date (v%d)", path, v)
}

// PopupSocket is the throwaway tmux socket of the popup test.
func PopupSocket(pid int) string { return "forge-setup-" + strconv.Itoa(pid) }

// popupTestSession and popupTestWindow name the popup test's window.
const (
	popupTestSession = "forge-setup"
	popupTestWindow  = "key-test"
)

// PopupScript is what the test window shows: how to get back.
func PopupScript(detachKey string) string {
	label := tmux.KeyLabel(detachKey)
	return "printf '\\n  This is a throwaway tmux window on forge'\\''s own server.\\n\\n" +
		"  Press " + label + " to come back to the setup.\\n\\n" +
		"  (If nothing happens, close this terminal tab: the setup explains why.)\\n'; sleep 600"
}

// StartPopupTest opens the test window on d (a driver on a throwaway
// socket, started with forge's tmux.conf) and returns how to attach.
func StartPopupTest(ctx context.Context, d tmux.Driver, detachKey string) (tmux.Attachment, error) {
	s, err := d.EnsureSession(ctx, popupTestSession)
	if err != nil {
		return tmux.Attachment{}, err
	}
	t := tmux.Target{Session: s, Window: popupTestWindow}
	_ = d.KillWindow(ctx, t)
	if _, err := d.NewWindow(ctx, s, popupTestWindow, "", []string{"sh", "-c", PopupScript(detachKey)}, nil); err != nil {
		return tmux.Attachment{}, err
	}
	return d.Attach(ctx, t)
}

// StopPopupTest removes the throwaway server (and its socket file).
func StopPopupTest(ctx context.Context, d tmux.Driver) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return discard(ctx, d)
}

// discard kills a throwaway server, removing its socket file when the
// driver can (the real one; fakes just kill).
func discard(ctx context.Context, d tmux.Driver) error {
	if x, ok := d.(interface{ Discard(context.Context) error }); ok {
		return x.Discard(ctx)
	}
	return d.KillServer(ctx)
}

// ResetHint is the command that rewrites an outdated config by hand.
const ResetHint = "forge tmux config --reset"
