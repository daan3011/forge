package docs

import (
	"time"

	"github.com/daan3011/forge/internal/fswatch"
)

// Watcher signals when anything under a set of docs roots changes.
type Watcher = fswatch.Watcher

// FSWatcher is the fsnotify Watcher; it skips dot-folders such as .trash.
type FSWatcher = fswatch.FSWatcher

// DefaultDebounce is how long the watcher waits for a burst of changes to
// settle before signalling.
const DefaultDebounce = fswatch.DefaultDebounce

// NewWatcher starts a docs watcher with no roots. debounce <= 0 uses
// DefaultDebounce.
func NewWatcher(debounce time.Duration) (*FSWatcher, error) {
	return fswatch.NewWatcher(debounce)
}
