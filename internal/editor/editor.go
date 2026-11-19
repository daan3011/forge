// Package editor opens files in the user's $VISUAL / $EDITOR.
package editor

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Opener opens a file for editing and waits until the user is done.
type Opener interface {
	Open(ctx context.Context, path string) error
}

// Exec runs the user's editor as a child process attached to the given
// streams.
type Exec struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// Command returns the editor argv: $VISUAL, then $EDITOR, then vi. The value
// is split on whitespace so "code -w" works.
func Command() []string {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if f := strings.Fields(os.Getenv(env)); len(f) > 0 {
			return f
		}
	}
	return []string{"vi"}
}

// Cmd builds the process that edits path.
func (e Exec) Cmd(ctx context.Context, path string) *exec.Cmd {
	argv := append(Command(), path)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // the user's own editor
	cmd.Stdin, cmd.Stdout, cmd.Stderr = e.Stdin, e.Stdout, e.Stderr
	return cmd
}

// Open implements Opener.
func (e Exec) Open(ctx context.Context, path string) error {
	cmd := e.Cmd(ctx, path)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor %s: %w", cmd.Path, err)
	}
	return nil
}
