package github

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
)

// lowFraction is the share of the hourly limit below which forge backs off
// and shows the remaining calls.
const lowFraction = 10 // percent

// RateLimit is GitHub's rate limit state, read from response headers.
type RateLimit struct {
	Limit     int
	Remaining int
	Used      int
	// Reset is when calls are available again: the primary limit's reset, or
	// later if Retry-After asks for it.
	Reset time.Time
	// RetryAfter is set by a secondary rate limit (403/429 with Retry-After).
	RetryAfter time.Duration
}

// RateLimitMsg reports the rate limit seen on a GitHub response. The client
// emits it after every response via Observe; the root model backs off on it.
type RateLimitMsg struct{ RateLimit }

// ParseRateLimit reads the X-RateLimit-* and Retry-After headers. ok is
// false when none are present. now resolves an HTTP-date Retry-After.
func ParseRateLimit(h http.Header, now time.Time) (rl RateLimit, ok bool) {
	limit, errL := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	remaining, errR := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	if errL == nil && errR == nil && limit >= 0 && remaining >= 0 {
		rl.Limit, rl.Remaining, ok = limit, remaining, true
		if used, err := strconv.Atoi(h.Get("X-RateLimit-Used")); err == nil {
			rl.Used = used
		}
		if reset, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			rl.Reset = time.Unix(reset, 0)
		}
	}
	if ra := h.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			rl.RetryAfter, ok = time.Duration(secs)*time.Second, true
		} else if t, err := http.ParseTime(ra); err == nil && t.After(now) {
			rl.RetryAfter, ok = t.Sub(now), true
		}
		if until := now.Add(rl.RetryAfter); rl.RetryAfter > 0 && until.After(rl.Reset) {
			rl.Reset = until
		}
	}
	return rl, ok
}

// Observe returns a RateLimitMsg for resp, or nil if it has no rate limit
// headers. Use it as the result of a tea.Cmd or batch it with one.
func Observe(resp *http.Response, now time.Time) tea.Msg {
	if resp == nil {
		return nil
	}
	rl, ok := ParseRateLimit(resp.Header, now)
	if !ok {
		return nil
	}
	return RateLimitMsg{rl}
}

// Low reports whether fewer than 10% of calls are left, or GitHub asked us
// to wait.
func (r RateLimit) Low() bool {
	return r.RetryAfter > 0 || (r.Limit > 0 && r.Remaining*100 < r.Limit*lowFraction)
}

// NextDelay is how long to wait before the next background refresh, given
// the configured interval base:
//   - Retry-After set: at least that long.
//   - no calls left: until the limit resets.
//   - low: spread the remaining calls evenly until the reset.
//   - otherwise: base.
//
// It never returns less than base.
func NextDelay(r RateLimit, base time.Duration, now time.Time) time.Duration {
	untilReset := r.Reset.Sub(now)
	var d time.Duration
	switch {
	case r.RetryAfter > 0:
		d = r.RetryAfter
	case !r.Low() || r.Reset.IsZero() || untilReset <= 0:
		d = base
	case r.Remaining == 0:
		d = untilReset
	default:
		d = untilReset / time.Duration(r.Remaining)
	}
	return max(d, base)
}

// Summary is the status bar text, e.g. "gh 312/5000 · resets 14m".
func (r RateLimit) Summary(now time.Time) string {
	s := fmt.Sprintf("gh %d/%d", r.Remaining, r.Limit)
	if wait := r.Reset.Sub(now); !r.Reset.IsZero() && wait > 0 {
		s += " · resets " + shortDuration(wait)
	}
	return s
}

func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
