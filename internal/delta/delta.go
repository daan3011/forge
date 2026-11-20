// Package delta renders unified diffs through the delta pager
// (https://github.com/dandavison/delta) when it is installed.
package delta

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

// Renderer colours diffs.
type Renderer interface {
	// Available reports whether the renderer can be used at all.
	Available() bool
	// Render returns diff coloured for a terminal width columns wide.
	Render(ctx context.Context, diff string, width int) (string, error)
}

// ErrNotInstalled means delta isn't on the PATH.
var ErrNotInstalled = errors.New("delta: not installed")

// Exec runs the delta binary. Argv only, never a shell; the user's own
// delta settings in git config (side-by-side, line numbers…) still apply.
type Exec struct {
	// LookPath finds executables (exec.LookPath when nil); tests replace it.
	LookPath func(string) (string, error)
	// Dark picks delta's dark or light colours. Passing one explicitly also
	// keeps delta from querying the terminal for its background, which
	// would race Bubble Tea for terminal input.
	Dark bool
}

var _ Renderer = Exec{}

func (e Exec) look() (string, error) {
	look := e.LookPath
	if look == nil {
		look = exec.LookPath
	}
	return look("delta")
}

// Available implements Renderer.
func (e Exec) Available() bool {
	_, err := e.look()
	return err == nil
}

// Args is the argv after "delta" for a width.
func (e Exec) Args(width int) []string {
	mode := "--light"
	if e.Dark {
		mode = "--dark"
	}
	return []string{"--paging=never", "--width=" + strconv.Itoa(max(width, 1)), mode}
}

// Render implements Renderer.
func (e Exec) Render(ctx context.Context, diff string, width int) (string, error) {
	path, err := e.look()
	if err != nil {
		return "", ErrNotInstalled
	}
	cmd := exec.CommandContext(ctx, path, e.Args(width)...) //nolint:gosec // fixed binary, fixed flags
	cmd.Stdin = strings.NewReader(diff)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("delta: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// Fake renders diffs with a function, for tests.
type Fake struct {
	// Missing makes the fake unavailable.
	Missing bool
	// Func renders; nil prefixes every line with "Δ ".
	Func func(diff string, width int) (string, error)

	mu    sync.Mutex
	calls []int // widths, one per Render
}

var _ Renderer = (*Fake)(nil)

// Available implements Renderer.
func (f *Fake) Available() bool { return !f.Missing }

// Render implements Renderer.
func (f *Fake) Render(_ context.Context, diff string, width int) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, width)
	f.mu.Unlock()
	if f.Missing {
		return "", ErrNotInstalled
	}
	if f.Func != nil {
		return f.Func(diff, width)
	}
	lines := strings.Split(diff, "\n")
	for i, l := range lines {
		lines[i] = "Δ " + l
	}
	return strings.Join(lines, "\n"), nil
}

// Calls are the widths Render was called with.
func (f *Fake) Calls() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.calls...)
}
