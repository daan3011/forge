// Package browser opens URLs in the user's web browser, and local files in
// their default app.
package browser

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

// Opener opens a URL without waiting for the browser.
type Opener interface {
	Open(ctx context.Context, rawURL string) error
}

// FileOpener opens a local file in its default app (a browser for HTML)
// without waiting for it.
type FileOpener interface {
	OpenFile(ctx context.Context, path string) error
}

// ErrBadPath means the path is not an absolute path to an existing file;
// nothing else is ever handed to the system opener.
var ErrBadPath = errors.New("browser: not an existing absolute file path")

// CheckFile reports ErrBadPath unless path is absolute and exists.
func CheckFile(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%w: %q", ErrBadPath, path)
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("%w: %w", ErrBadPath, err)
	}
	return nil
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

var (
	_ Opener     = Exec{}
	_ FileOpener = Exec{}
)

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

// OpenFile implements FileOpener with the same opener as Open.
func (e Exec) OpenFile(ctx context.Context, path string) error {
	if err := CheckFile(path); err != nil {
		return err
	}
	argv := e.Command(path)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // fixed opener, checked path
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("browser: %s: %w: %s", argv[0], err, out)
	}
	return nil
}

// Fake records opened URLs and files, for tests.
type Fake struct {
	mu     sync.Mutex
	opened []string
	files  []string
	Err    error
}

var (
	_ Opener     = (*Fake)(nil)
	_ FileOpener = (*Fake)(nil)
)

// OpenFile implements FileOpener. It checks the path like Exec does.
func (f *Fake) OpenFile(_ context.Context, path string) error {
	if err := CheckFile(path); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files = append(f.files, path)
	return f.Err
}

// Files returns the files opened so far.
func (f *Fake) Files() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.files...)
}

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
