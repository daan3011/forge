package artifacts

import (
	"sync"
	"time"

	"github.com/daan3011/forge/internal/fswatch"
)

// Watcher signals when an artifact under the scanned targets may have
// changed.
type Watcher interface {
	// SetTargets replaces what is watched.
	SetTargets(targets []Target) error
	// Events receives one value per burst of changes.
	Events() <-chan struct{}
	Close() error
}

// FSWatcher watches targets with fsnotify. It skips the folders Scan skips
// and only signals for files a target would pick up (plus removals, renames
// and new folders), so an agent editing code doesn't trigger rescans.
type FSWatcher struct {
	w *fswatch.FSWatcher

	mu      sync.RWMutex
	targets []Target
}

var _ Watcher = (*FSWatcher)(nil)

// NewWatcher starts a watcher with no targets. debounce <= 0 uses
// fswatch.DefaultDebounce.
func NewWatcher(debounce time.Duration) (*FSWatcher, error) {
	fw := &FSWatcher{}
	w, err := fswatch.New(fswatch.Options{Debounce: debounce, SkipDir: SkipDir, Filter: fw.match})
	if err != nil {
		return nil, err
	}
	fw.w = w
	return fw, nil
}

func (fw *FSWatcher) match(path string) bool {
	fw.mu.RLock()
	defer fw.mu.RUnlock()
	return Matches(fw.targets, path)
}

// SetTargets implements Watcher.
func (fw *FSWatcher) SetTargets(targets []Target) error {
	fw.mu.Lock()
	fw.targets = append([]Target(nil), targets...)
	fw.mu.Unlock()
	return fw.w.Set(Roots(targets))
}

// Events implements Watcher.
func (fw *FSWatcher) Events() <-chan struct{} { return fw.w.Events() }

// Close implements Watcher.
func (fw *FSWatcher) Close() error { return fw.w.Close() }
