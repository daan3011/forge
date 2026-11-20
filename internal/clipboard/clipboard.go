// Package clipboard copies text to the system clipboard through the
// platform's copy tool (pbcopy, wl-copy, xclip or xsel).
package clipboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// Copier copies text to the clipboard.
type Copier interface {
	Copy(ctx context.Context, text string) error
}

// ErrNoTool means no copy tool was found; callers can fall back to OSC 52
// (tea.SetClipboard).
var ErrNoTool = errors.New("clipboard: no copy tool found")

// Exec copies with the first available tool. Argv only, never a shell.
type Exec struct {
	// LookPath finds executables (exec.LookPath when nil); tests replace it.
	LookPath func(string) (string, error)
	// Getenv reads the environment (os.Getenv when nil).
	Getenv func(string) string
}

var _ Copier = Exec{}

// Command is the argv of the copy tool to use, or nil when there is none.
func (e Exec) Command() []string {
	look, getenv := e.LookPath, e.Getenv
	if look == nil {
		look = exec.LookPath
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	var cands [][]string
	if runtime.GOOS == "darwin" {
		cands = append(cands, []string{"pbcopy"})
	}
	if getenv("WAYLAND_DISPLAY") != "" {
		cands = append(cands, []string{"wl-copy"})
	}
	cands = append(cands, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
	for _, c := range cands {
		if _, err := look(c[0]); err == nil {
			return c
		}
	}
	return nil
}

// Copy implements Copier.
func (e Exec) Copy(ctx context.Context, text string) error {
	argv := e.Command()
	if argv == nil {
		return ErrNoTool
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // fixed tool list
	cmd.Stdin = strings.NewReader(text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("clipboard: %s: %w: %s", argv[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Fake records copied text, for tests.
type Fake struct {
	mu     sync.Mutex
	copied []string
	Err    error
}

var _ Copier = (*Fake)(nil)

// Copy implements Copier.
func (f *Fake) Copy(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	f.copied = append(f.copied, text)
	return nil
}

// Copied returns the text copied so far.
func (f *Fake) Copied() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.copied...)
}
