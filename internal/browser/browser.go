// Package browser opens URLs in the user's web browser.
package browser

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"sync"
)

// Opener opens a URL without waiting for the browser.
type Opener interface {
	Open(ctx context.Context, rawURL string) error
}

// ErrBadURL means the URL is not an absolute http(s) URL; nothing else is
// ever handed to the system opener.
var ErrBadURL = errors.New("browser: not an http(s) URL")

// Exec opens URLs with the platform's opener: `open` on macOS, `xdg-open`
// elsewhere. Argv only, never a shell.
type Exec struct {
	// Bin overrides the opener executable.
	Bin string
}

var _ Opener = Exec{}

// Command is the opener argv for rawURL.
func (e Exec) Command(rawURL string) []string {
	bin := e.Bin
	if bin == "" {
		bin = "xdg-open"
		if runtime.GOOS == "darwin" {
			bin = "open"
		}
	}
	return []string{bin, rawURL}
}

// Check reports ErrBadURL unless rawURL is an absolute http(s) URL.
func Check(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w: %q", ErrBadURL, rawURL)
	}
	return nil
}

// Open implements Opener. The opener returns once the URL is handed off.
func (e Exec) Open(ctx context.Context, rawURL string) error {
	if err := Check(rawURL); err != nil {
		return err
	}
	argv := e.Command(rawURL)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // fixed opener, checked URL
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("browser: %s: %w: %s", argv[0], err, out)
	}
	return nil
}

// Fake records opened URLs, for tests.
type Fake struct {
	mu     sync.Mutex
	opened []string
	Err    error
}

var _ Opener = (*Fake)(nil)

// Open implements Opener.
func (f *Fake) Open(_ context.Context, rawURL string) error {
	if err := Check(rawURL); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, rawURL)
	return f.Err
}

// Opened returns the URLs opened so far.
func (f *Fake) Opened() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.opened...)
}
