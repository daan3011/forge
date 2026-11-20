// Package notifytest has a recording notify.Notifier for tests.
package notifytest

import (
	"context"
	"sync"

	"github.com/daan3011/forge/internal/notify"
)

// Sent is one recorded notification.
type Sent struct{ Title, Body string }

// Recorder records notifications instead of showing them. Err, if set, is
// returned by Notify (the notification is still recorded).
type Recorder struct {
	mu   sync.Mutex
	sent []Sent
	Err  error
}

var _ notify.Notifier = (*Recorder)(nil)

// Notify implements notify.Notifier.
func (r *Recorder) Notify(_ context.Context, title, body string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, Sent{title, body})
	return r.Err
}

// Sent returns a copy of what was sent so far.
func (r *Recorder) Sent() []Sent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Sent(nil), r.sent...)
}

// Bodies returns the bodies sent so far.
func (r *Recorder) Bodies() []string {
	var out []string
	for _, s := range r.Sent() {
		out = append(out, s.Body)
	}
	return out
}
