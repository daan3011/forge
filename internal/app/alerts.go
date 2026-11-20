package app

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/refresh"
	"github.com/daan3011/forge/internal/sessions/alert"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// alertTimeout bounds one waiting check or notification.
const alertTimeout = 5 * time.Second

// WithAlerts turns on desktop notifications and the waiting count in the
// status bar (P1.8). c's Clock defaults to the model's clock.
func WithAlerts(c alert.Checker) Option {
	return func(m *Model) { m.alerts = &c }
}

// waitingMsg is the result of a waiting check: how many sessions wait.
type waitingMsg struct {
	n   int
	err error
}

// checkWaiting notifies about sessions that have waited long enough and
// counts the waiting ones, off the update loop.
func (m Model) checkWaiting() tea.Cmd {
	if m.alerts == nil {
		return nil
	}
	c := m.checker()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), alertTimeout)
		defer cancel()
		n, err := c.CheckWaiting(ctx)
		return waitingMsg{n: n, err: err}
	}
}

func (m Model) checker() alert.Checker {
	c := *m.alerts
	if c.Clock == nil {
		c.Clock = m.clock
	}
	return c
}

// alertTick adds a waiting check to the sessions refresh tick, which runs
// whether or not the Sessions tab is visible.
func (m Model) alertTick(msg refresh.TickMsg) tea.Cmd {
	if msg.Source != refresh.Sessions {
		return nil
	}
	return m.checkWaiting()
}

// handleWaiting stores the waiting count. A failing check shows one error
// toast, not one every tick.
func (m Model) handleWaiting(msg waitingMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		failing := m.alertsFailing
		m.alertsFailing = true
		if failing {
			return m, nil
		}
		return m, m.toast.Show("notifications: "+msg.err.Error(), ui.ToastError)
	}
	m.waiting, m.alertsFailing = msg.n, false
	return m, nil
}

// handleSessionPing recounts the waiting sessions at once and, while forge
// isn't focused, notifies about errors and finished turns. Waiting itself is
// notified by checkWaiting once it lasts notify_after_minutes.
func (m Model) handleSessionPing(msg tabs.SessionPingMsg) (Model, tea.Cmd) {
	if m.alerts == nil {
		return m, nil
	}
	cmds := []tea.Cmd{m.checkWaiting()}
	p := msg.Ping
	kind, ok := alert.ForEvent(p.Event, store.Status(p.Status), p.Changed)
	if ok && kind != alert.KindWaiting && !m.focused && p.Session != "" {
		c := m.checker()
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), alertTimeout)
			defer cancel()
			if _, err := c.NotifyEvent(ctx, p.Session, kind); err != nil {
				return ui.ErrMsg{Err: err}
			}
			return nil
		})
	}
	return m, tea.Batch(cmds...)
}

// testNotification sends a test notification (palette).
func (m Model) testNotification() tea.Cmd {
	if m.alerts == nil {
		return nil
	}
	c := m.checker()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), alertTimeout)
		defer cancel()
		if err := c.Test(ctx); err != nil {
			return ui.ErrMsg{Err: err}
		}
		return ui.SuccessMsg{Text: "Test notification sent"}
	}
}
