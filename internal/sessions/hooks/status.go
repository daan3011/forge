package hooks

import (
	"strings"

	"github.com/daan3011/forge/internal/store"
)

// Hook event names forge registers (see Events).
const (
	SessionStart     = "SessionStart"
	UserPromptSubmit = "UserPromptSubmit"
	PreToolUse       = "PreToolUse"
	PostToolUse      = "PostToolUse"
	Notification     = "Notification"
	Stop             = "Stop"
	StopFailure      = "StopFailure"
	SessionEnd       = "SessionEnd"
)

// Events are the hook events forge registers in a worktree, in order.
var Events = []string{
	SessionStart, UserPromptSubmit, PreToolUse, PostToolUse,
	Notification, Stop, StopFailure, SessionEnd,
}

// NotifyPermission is the Notification type of a permission prompt.
const NotifyPermission = "permission_prompt"

// Next is the P1.4 status machine: the status a session in status cur moves
// to on event p, and false when the event leaves the status alone.
//
//	SessionStart (startup/resume/clear/fork)  -> idle   (compact: no change)
//	UserPromptSubmit, PreToolUse, PostToolUse -> working
//	Notification: permission, elicitation, agent input -> waiting (others: no change)
//	Stop -> idle, StopFailure (API error) -> error
//	SessionEnd -> dead, except reason clear/resume (the session goes on)
//
// Archived sessions never change. A gone tmux window is handled by the
// session list's sweep, not here.
func Next(p Payload, cur store.Status) (store.Status, bool) {
	if cur == store.StatusArchived {
		return cur, false
	}
	var next store.Status
	switch p.Event {
	case SessionStart:
		if p.Source == "compact" {
			return cur, false
		}
		next = store.StatusIdle
	case UserPromptSubmit, PreToolUse, PostToolUse:
		next = store.StatusWorking
	case Notification:
		if !needsInput(p.NotificationType) {
			return cur, false
		}
		next = store.StatusWaiting
	case Stop:
		next = store.StatusIdle
	case StopFailure:
		next = store.StatusError
	case SessionEnd:
		if p.Reason == "clear" || p.Reason == "resume" {
			return cur, false
		}
		next = store.StatusDead
	default:
		return cur, false
	}
	return next, true
}

// needsInput reports whether a Notification type means Claude is blocked on
// the user. idle_prompt is not one: it follows Stop, and the session stays
// idle.
func needsInput(typ string) bool {
	return typ == NotifyPermission || typ == "agent_needs_input" ||
		(strings.HasPrefix(typ, "elicitation_") && typ != "elicitation_complete" && typ != "elicitation_response")
}
