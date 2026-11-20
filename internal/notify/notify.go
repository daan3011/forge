// Package notify sends desktop notifications: through gen2brain/beeep, with
// a fallback to osascript (macOS) or notify-send (elsewhere).
package notify

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/gen2brain/beeep"
)

// Notifier shows one desktop notification.
type Notifier interface {
	Notify(ctx context.Context, title, body string) error
}

// Nop drops every notification.
type Nop struct{}

// Notify implements Notifier.
func (Nop) Notify(context.Context, string, string) error { return nil }

// System is the real Notifier. The zero value is ready to use; the fields
// are seams for tests.
type System struct {
	// Beeep replaces beeep.Notify.
	Beeep func(title, body string) error
	// Run replaces running the fallback command.
	Run func(ctx context.Context, argv []string) error
	// GOOS replaces runtime.GOOS when picking the fallback.
	GOOS string
}

// Notify implements Notifier: beeep first, then the platform command.
func (s System) Notify(ctx context.Context, title, body string) error {
	first := s.beeep(ctx, title, body)
	if first == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("notify: %w", errors.Join(first, err))
	}
	argv := FallbackCommand(s.goos(), title, body)
	if err := s.run(ctx, argv); err != nil {
		return fmt.Errorf("notify: %w", errors.Join(first, fmt.Errorf("%s: %w", argv[0], err)))
	}
	return nil
}

// beeep runs beeep.Notify, which takes no context, without outliving ctx.
func (s System) beeep(ctx context.Context, title, body string) error {
	fn := s.Beeep
	if fn == nil {
		fn = func(t, b string) error { return beeep.Notify(t, b, "") }
	}
	done := make(chan error, 1)
	go func() { done <- fn(title, body) }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("beeep: %w", err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("beeep: %w", ctx.Err())
	}
}

func (s System) goos() string {
	if s.GOOS != "" {
		return s.GOOS
	}
	return runtime.GOOS
}

func (s System) run(ctx context.Context, argv []string) error {
	if s.Run != nil {
		return s.Run(ctx, argv)
	}
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput() //nolint:gosec // fixed binaries, text as arguments
	if err != nil && len(out) > 0 {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return err
}

// FallbackCommand is the argv that shows a notification without beeep.
func FallbackCommand(goos, title, body string) []string {
	if goos == "darwin" {
		script := "display notification " + appleString(body) + " with title " + appleString(title)
		return []string{"osascript", "-e", script}
	}
	return []string{"notify-send", "--", title, body}
}

// appleString quotes s as an AppleScript string literal.
func appleString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
	return `"` + s + `"`
}
