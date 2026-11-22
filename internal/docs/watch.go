package docs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher signals when anything under a set of docs roots changes.
type Watcher interface {
	// Set replaces the watched roots. Roots that don't exist yet are
	// watched through their nearest existing parent and picked up once
	// they are created.
	Set(roots []string) error
	// Events receives one value per burst of changes.
	Events() <-chan struct{}
	Close() error
}

// DefaultDebounce is how long the watcher waits for a burst of changes to
// settle before signalling.
const DefaultDebounce = 150 * time.Millisecond

// FSWatcher is the fsnotify Watcher. fsnotify doesn't recurse, so every
// folder under a root is added, and new folders as they appear.
type FSWatcher struct {
	w        *fsnotify.Watcher
	debounce time.Duration
	events   chan struct{}
	done     chan struct{}

	mu      sync.Mutex
	roots   []string
	watched map[string]bool
	timer   *time.Timer
	closed  bool
}

var _ Watcher = (*FSWatcher)(nil)

// NewWatcher starts an fsnotify watcher with no roots. debounce <= 0 uses
// DefaultDebounce.
func NewWatcher(debounce time.Duration) (*FSWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if debounce <= 0 {
		debounce = DefaultDebounce
	}
	fw := &FSWatcher{
		w: w, debounce: debounce,
		events:  make(chan struct{}, 1),
		done:    make(chan struct{}),
		watched: map[string]bool{},
	}
	go fw.loop()
	return fw, nil
}

// Events implements Watcher.
func (fw *FSWatcher) Events() <-chan struct{} { return fw.events }

// Set implements Watcher.
func (fw *FSWatcher) Set(roots []string) error {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.closed {
		return errors.New("watcher closed")
	}
	fw.roots = append([]string(nil), roots...)
	return fw.resync()
}

// resync makes the watched folders match the roots. Callers hold mu.
func (fw *FSWatcher) resync() error {
	want := map[string]bool{}
	var errs []error
	for _, root := range fw.roots {
		root = filepath.Clean(root)
		if _, err := os.Stat(root); err != nil {
			// Watch the nearest existing parent so the root's creation
			// is seen.
			if p := existingParent(root); p != "" {
				want[p] = true
			}
			continue
		}
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // a folder removed mid-walk
			}
			if !d.IsDir() {
				return nil
			}
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			want[p] = true
			return nil
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	for p := range fw.watched {
		if !want[p] {
			_ = fw.w.Remove(p)
			delete(fw.watched, p)
		}
	}
	for p := range want {
		if fw.watched[p] {
			continue
		}
		if err := fw.w.Add(p); err != nil {
			errs = append(errs, err)
			continue
		}
		fw.watched[p] = true
	}
	return errors.Join(errs...)
}

func existingParent(p string) string {
	for {
		parent := filepath.Dir(p)
		if parent == p {
			return ""
		}
		if info, err := os.Stat(parent); err == nil && info.IsDir() {
			return parent
		}
		p = parent
	}
}

// relevant reports whether a change at p concerns a root. Callers hold mu.
func (fw *FSWatcher) relevant(p string) bool {
	for _, root := range fw.roots {
		root = filepath.Clean(root)
		if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) ||
			strings.HasPrefix(root, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (fw *FSWatcher) loop() {
	for {
		select {
		case <-fw.done:
			return
		case ev, ok := <-fw.w.Events:
			if !ok {
				return
			}
			fw.handle(ev)
		case _, ok := <-fw.w.Errors:
			if !ok {
				return
			}
		}
	}
}

func (fw *FSWatcher) handle(ev fsnotify.Event) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.closed || !fw.relevant(ev.Name) {
		return
	}
	if ev.Has(fsnotify.Create) || ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
		// New or removed folders (or a root appearing) change what is
		// watched.
		_ = fw.resync()
	}
	if fw.timer != nil {
		fw.timer.Stop()
	}
	fw.timer = time.AfterFunc(fw.debounce, fw.fire)
}

func (fw *FSWatcher) fire() {
	select {
	case fw.events <- struct{}{}:
	default: // a signal is already pending
	}
}

// Close implements Watcher.
func (fw *FSWatcher) Close() error {
	fw.mu.Lock()
	if fw.closed {
		fw.mu.Unlock()
		return nil
	}
	fw.closed = true
	if fw.timer != nil {
		fw.timer.Stop()
	}
	fw.mu.Unlock()
	close(fw.done)
	return fw.w.Close()
}
