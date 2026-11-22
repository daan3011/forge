// Package chafa renders images as coloured text through chafa
// (https://hpjansson.org/chafa/) when it is installed: the inline preview
// fallback for terminals without graphics (P4.7).
package chafa

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// Renderer turns an image file into text art.
type Renderer interface {
	// Available reports whether the renderer can be used at all.
	Available() bool
	// Render returns the image at path drawn in at most cols x rows cells.
	Render(ctx context.Context, path string, cols, rows int) (string, error)
}

// ErrNotInstalled means chafa isn't on the PATH.
var ErrNotInstalled = errors.New("chafa: not installed")

// Exec runs the chafa binary. Argv only, never a shell.
type Exec struct {
	// LookPath finds executables (exec.LookPath when nil); tests replace it.
	LookPath func(string) (string, error)
}

var _ Renderer = Exec{}

func (e Exec) look() (string, error) {
	look := e.LookPath
	if look == nil {
		look = exec.LookPath
	}
	return look("chafa")
}

// Available implements Renderer.
func (e Exec) Available() bool {
	_, err := e.look()
	return err == nil
}

// Args is the argv after "chafa". Symbols only (no sixel or kitty output,
// forge decides that), no animation, and polite so chafa writes nothing
// but colours and characters. Colours are full: Bubble Tea downsamples to
// the terminal's profile.
func Args(path string, cols, rows int) []string {
	return []string{
		"--format=symbols",
		"--size=" + strconv.Itoa(max(cols, 1)) + "x" + strconv.Itoa(max(rows, 1)),
		"--animate=off",
		"--polite=on",
		"--colors=full",
		"--", path,
	}
}

// Render implements Renderer.
func (e Exec) Render(ctx context.Context, path string, cols, rows int) (string, error) {
	bin, err := e.look()
	if err != nil {
		return "", ErrNotInstalled
	}
	cmd := exec.CommandContext(ctx, bin, Args(path, cols, rows)...) //nolint:gosec // fixed binary, fixed flags, path after --
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("chafa: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// Fake renders with a function, for tests.
type Fake struct {
	// Missing makes the fake unavailable.
	Missing bool
	// Func renders; nil returns "chafa:<path>:<cols>x<rows>".
	Func func(path string, cols, rows int) (string, error)

	mu    sync.Mutex
	calls []string
}

var _ Renderer = (*Fake)(nil)

// Available implements Renderer.
func (f *Fake) Available() bool { return !f.Missing }

// Render implements Renderer.
func (f *Fake) Render(_ context.Context, path string, cols, rows int) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, path)
	f.mu.Unlock()
	if f.Missing {
		return "", ErrNotInstalled
	}
	if f.Func != nil {
		return f.Func(path, cols, rows)
	}
	return fmt.Sprintf("chafa:%s:%dx%d", path, cols, rows), nil
}

// Calls are the paths Render was called with.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}
