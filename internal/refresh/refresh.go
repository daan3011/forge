// Package refresh schedules background refreshes: one tick per data source,
// at the interval from config, with an optional backoff override (for
// example when the GitHub rate limit runs low).
package refresh

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/config"
)

// Source is a data source with its own refresh interval.
type Source string

// Data sources.
const (
	GitHub   Source = "github"
	Plane    Source = "plane"
	Sessions Source = "sessions"
)

// All is every source, in a stable order.
var All = []Source{GitHub, Plane, Sessions}

// TickMsg says it is time to refresh tabs that use Source. Gen drops ticks
// from a timer that was replaced by a backoff change.
type TickMsg struct {
	Source Source
	Gen    int
}

// Scheduler arms one tea.Tick per source. It lives in the root model and is
// only touched from Update, so it needs no locking. Its maps are shared
// between copies, which is what the value-typed root model wants.
type Scheduler struct {
	base  map[Source]time.Duration
	delay map[Source]time.Duration // backoff overrides
	gen   map[Source]int
}

// New reads the per-source intervals from cfg.
func New(cfg *config.Config) Scheduler {
	return Scheduler{
		base: map[Source]time.Duration{
			GitHub:   cfg.GitHub.RefreshInterval.Duration,
			Plane:    cfg.Plane.RefreshInterval.Duration,
			Sessions: cfg.Sessions.RefreshInterval.Duration,
		},
		delay: map[Source]time.Duration{},
		gen:   map[Source]int{},
	}
}

// Start arms the first tick of every source.
func (s Scheduler) Start() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(All))
	for _, src := range All {
		cmds = append(cmds, s.arm(src))
	}
	return tea.Batch(cmds...)
}

// Interval is the current delay between ticks of src: the backoff override
// if one is set, otherwise the configured interval.
func (s Scheduler) Interval(src Source) time.Duration {
	if d, ok := s.delay[src]; ok {
		return d
	}
	return s.base[src]
}

// Handle checks a tick. ok is false for a stale tick, which must be ignored.
// For a live tick it re-arms the source and returns that command.
func (s Scheduler) Handle(msg TickMsg) (ok bool, next tea.Cmd) {
	if s.base == nil || msg.Gen != s.gen[msg.Source] {
		return false, nil
	}
	return true, s.arm(msg.Source)
}

// SetDelay overrides the interval of src (backoff) and re-arms it so the
// new delay applies from now.
func (s Scheduler) SetDelay(src Source, d time.Duration) tea.Cmd {
	if cur, ok := s.delay[src]; ok && cur == d {
		return nil
	}
	s.delay[src] = d
	s.gen[src]++
	return s.arm(src)
}

// ClearDelay removes a backoff override of src, re-arming it at its
// configured interval. It does nothing if there was no override.
func (s Scheduler) ClearDelay(src Source) tea.Cmd {
	if _, ok := s.delay[src]; !ok {
		return nil
	}
	delete(s.delay, src)
	s.gen[src]++
	return s.arm(src)
}

// Msg returns a tick for src that Handle accepts, for refreshing right away
// and for tests.
func (s Scheduler) Msg(src Source) TickMsg {
	return TickMsg{Source: src, Gen: s.gen[src]}
}

func (s Scheduler) arm(src Source) tea.Cmd {
	msg := s.Msg(src)
	return tea.Tick(s.Interval(src), func(time.Time) tea.Msg { return msg })
}
