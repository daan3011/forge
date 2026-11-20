// Package claude builds the command lines that start Claude Code agents.
// forge never runs claude itself: the argv built here is handed to the tmux
// driver, which starts it in the agent's window.
package claude

import (
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"

	"github.com/daan3011/forge/internal/shell"
)

// DefaultBin is the claude executable looked up on $PATH.
const DefaultBin = "claude"

// ErrInvalidSessionID means a session id is not a lowercase RFC 4122 UUID.
var ErrInvalidSessionID = errors.New("claude: invalid session id")

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// NewSessionID returns a random (version 4) UUID for `claude --session-id`.
func NewSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("claude: session id: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// ValidSessionID reports whether id is a lowercase UUID.
func ValidSessionID(id string) bool { return uuidRe.MatchString(id) }

// Launch describes a new interactive Claude Code session.
type Launch struct {
	// Bin is the claude executable; empty means DefaultBin.
	Bin string
	// SessionID is passed as --session-id so forge knows the session's
	// transcript and can resume it.
	SessionID string
	// PromptFile, when set, holds the first prompt. It is read by the shell
	// at start-up, so the prompt never has to be quoted into a command line.
	// Command substitution drops trailing newlines from it.
	PromptFile string
}

// Argv returns the command to run in the agent's window:
//
//	sh -c 'exec <bin> --session-id <id> -- "$(cat <file>)"'
//
// The bin and file paths are single-quoted for the shell; the prompt itself
// only ever exists in the file and in the expanded, double-quoted argument,
// so quotes, $, backticks and newlines in it reach claude untouched. exec
// keeps claude as the pane's process, so its exit marks the window dead.
func (l Launch) Argv() ([]string, error) {
	if !ValidSessionID(l.SessionID) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidSessionID, l.SessionID)
	}
	bin := l.Bin
	if bin == "" {
		bin = DefaultBin
	}
	script := "exec " + shell.Quote(bin) + " --session-id " + l.SessionID
	if l.PromptFile != "" {
		script += ` -- "$(cat ` + shell.Quote(l.PromptFile) + `)"`
	}
	return []string{"sh", "-c", script}, nil
}
