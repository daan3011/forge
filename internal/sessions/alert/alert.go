// Package alert decides when a session deserves a desktop notification
// (P1.8) and sends it: once per waiting spell, and on errors and finished
// turns. Both `forge hook` and the TUI use it; the database claim in
// store.Sessions.ClaimWaitingNotification keeps them from notifying twice.
package alert

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/notify"
	"github.com/daan3011/forge/internal/sessions/hooks"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/store"
)

// Title is the title of every notification.
const Title = "forge"

// Kind is what a notification is about.
type Kind int

// Notification kinds.
const (
	KindWaiting Kind = iota + 1
	KindError
	KindFinished
)

// Policy is when to notify.
type Policy struct {
	// After is how long a session waits before the TUI notifies; 0 never
	// notifies about waiting (neither does the hook).
	After time.Duration
	// OnError and OnFinish notify on errors and finished turns while forge
	// isn't focused.
	OnError, OnFinish bool
}

// PolicyFrom reads the policy from the sessions config.
func PolicyFrom(c config.Sessions) Policy {
	return Policy{
		After:   time.Duration(c.NotifyAfterMinutes) * time.Minute,
		OnError: c.NotifyOnError, OnFinish: c.NotifyOnFinish,
	}
}

// Enabled reports whether k is turned on.
func (p Policy) Enabled(k Kind) bool {
	switch k {
	case KindWaiting:
		return p.After > 0
	case KindError:
		return p.OnError
	case KindFinished:
		return p.OnFinish
	}
	return false
}

// ForEvent is the notification a recorded hook event calls for, given the
// status it left the session in and whether it set one.
func ForEvent(event string, status store.Status, changed bool) (Kind, bool) {
	if !changed {
		return 0, false
	}
	switch {
	case event == hooks.Notification && status == store.StatusWaiting:
		return KindWaiting, true
	case event == hooks.StopFailure && status == store.StatusError:
		return KindError, true
	case event == hooks.Stop && status == store.StatusIdle:
		return KindFinished, true
	}
	return 0, false
}

// Message is the notification text for session name.
func Message(k Kind, name string) string {
	switch k {
	case KindError:
		return name + " hit an error"
	case KindFinished:
		return name + " finished"
	default:
		return name + " is waiting for you"
	}
}

// WaitingDue reports whether s has waited at least after without being
// notified.
func WaitingDue(s store.Session, now time.Time, after time.Duration) bool {
	return after > 0 && s.Status == store.StatusWaiting && s.WaitingNotifiedAt.IsZero() &&
		!s.StatusChangedAt.IsZero() && now.Sub(s.StatusChangedAt) >= after
}

// Store is what the Checker needs from the database (*store.Sessions).
type Store interface {
	GetByName(ctx context.Context, name string) (store.Session, error)
	List(ctx context.Context, f store.SessionFilter) ([]store.Session, error)
	ClaimWaitingNotification(ctx context.Context, id int64, at time.Time) (bool, error)
}

// Watcher tells whether someone is looking at a session's tmux window
// (tmux.Driver).
type Watcher interface {
	WindowWatched(ctx context.Context, t tmux.Target) (bool, error)
}

// Checker sends the notifications. Watcher may be nil (nothing counts as
// watched); Clock nil means time.Now.
type Checker struct {
	Store    Store
	Notifier notify.Notifier
	Watcher  Watcher
	Policy   Policy
	Clock    func() time.Time
}

func (c Checker) now() time.Time {
	if c.Clock != nil {
		return c.Clock()
	}
	return time.Now()
}

// watched reports whether a tmux client shows the session's window. Errors
// count as not watched: a spurious notification beats a missed one.
func (c Checker) watched(ctx context.Context, s store.Session) bool {
	if c.Watcher == nil || s.TmuxSession == "" || s.TmuxWindow == "" {
		return false
	}
	ok, err := c.Watcher.WindowWatched(ctx, tmux.Target{Session: s.TmuxSession, Window: s.TmuxWindow})
	return err == nil && ok
}

// Waiting lists the unarchived waiting sessions of every project.
func (c Checker) Waiting(ctx context.Context) ([]store.Session, error) {
	no := false
	return c.Store.List(ctx, store.SessionFilter{Status: store.StatusWaiting, Archived: &no})
}

// CheckWaiting notifies about every session that has waited longer than
// Policy.After, once per waiting spell, and returns how many sessions are
// waiting. A watched session is skipped without a claim, so it is notified
// once nobody looks at it any more.
func (c Checker) CheckWaiting(ctx context.Context) (int, error) {
	ws, err := c.Waiting(ctx)
	if err != nil {
		return 0, err
	}
	now := c.now()
	var errs []error
	for _, s := range ws {
		if !WaitingDue(s, now, c.Policy.After) {
			continue
		}
		if _, err := c.notifyWaiting(ctx, s); err != nil {
			errs = append(errs, err)
		}
	}
	return len(ws), errors.Join(errs...)
}

// NotifyWaitingNow notifies about s now, ignoring Policy.After (but not a
// disabled policy), unless it is watched or its waiting spell was already
// notified. It reports whether a notification was sent.
func (c Checker) NotifyWaitingNow(ctx context.Context, s store.Session) (bool, error) {
	if !c.Policy.Enabled(KindWaiting) || s.Status != store.StatusWaiting {
		return false, nil
	}
	return c.notifyWaiting(ctx, s)
}

func (c Checker) notifyWaiting(ctx context.Context, s store.Session) (bool, error) {
	if c.watched(ctx, s) {
		return false, nil
	}
	won, err := c.Store.ClaimWaitingNotification(ctx, s.ID, c.now())
	if err != nil || !won {
		return false, err
	}
	return true, c.send(ctx, KindWaiting, s.Name)
}

// NotifyTransition notifies about an error or a finished turn of s, if the
// policy enables k and nobody watches the session's window. The caller
// decides whether forge is focused. It reports whether one was sent.
func (c Checker) NotifyTransition(ctx context.Context, s store.Session, k Kind) (bool, error) {
	if k == KindWaiting {
		return c.NotifyWaitingNow(ctx, s)
	}
	if !c.Policy.Enabled(k) || c.watched(ctx, s) {
		return false, nil
	}
	return true, c.send(ctx, k, s.Name)
}

// NotifyEvent is NotifyTransition for the session called name.
func (c Checker) NotifyEvent(ctx context.Context, name string, k Kind) (bool, error) {
	if !c.Policy.Enabled(k) {
		return false, nil
	}
	s, err := c.Store.GetByName(ctx, name)
	if err != nil {
		return false, err
	}
	return c.NotifyTransition(ctx, s, k)
}

// Test sends a test notification.
func (c Checker) Test(ctx context.Context) error {
	return c.Notifier.Notify(ctx, Title, "notifications work")
}

func (c Checker) send(ctx context.Context, k Kind, name string) error {
	if err := c.Notifier.Notify(ctx, Title, Message(k, name)); err != nil {
		return fmt.Errorf("notify %s: %w", name, err)
	}
	return nil
}
