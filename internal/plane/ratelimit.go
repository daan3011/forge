package plane

import (
	"net/http"
	"strconv"
	"time"
)

const (
	backoffBase = time.Second
	backoffMax  = 30 * time.Second
)

// RateLimit is what a response says about the API key's rate limit.
type RateLimit struct {
	// Remaining is -1 when the response didn't say.
	Remaining int
	// Reset is when the window resets (zero if unknown).
	Reset time.Time
	// RetryAfter is how long a 429 asks to wait (0 if not given).
	RetryAfter time.Duration
}

// ParseRateLimit reads Retry-After (seconds or HTTP-date) and the
// X-RateLimit-Remaining / X-RateLimit-Reset (epoch seconds) headers. ok is
// false when none are present.
func ParseRateLimit(h http.Header, now time.Time) (rl RateLimit, ok bool) {
	rl.Remaining = -1
	if n, err := strconv.Atoi(h.Get("X-RateLimit-Remaining")); err == nil && n >= 0 {
		rl.Remaining, ok = n, true
	}
	if s, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil && s > 0 {
		rl.Reset, ok = time.Unix(s, 0), true
	}
	if ra := h.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs >= 0 {
			rl.RetryAfter, ok = time.Duration(secs)*time.Second, true
		} else if t, err := http.ParseTime(ra); err == nil {
			ok = true
			if t.After(now) {
				rl.RetryAfter = t.Sub(now)
			}
		}
	}
	return rl, ok
}

// retryDelay is how long to wait before retry number attempt+1 of a 429:
// Retry-After, else until X-RateLimit-Reset, else exponential backoff
// (1s, 2s, 4s, …), clamped to 1s–30s. A longer Retry-After is honoured up
// to 2 minutes.
func retryDelay(h http.Header, now time.Time, attempt int) time.Duration {
	rl, _ := ParseRateLimit(h, now)
	var d time.Duration
	switch {
	case rl.RetryAfter > 0:
		return min(max(rl.RetryAfter, backoffBase), 2*time.Minute)
	case !rl.Reset.IsZero() && rl.Reset.After(now):
		d = rl.Reset.Sub(now)
	default:
		d = backoffBase << min(attempt, 5)
	}
	return min(max(d, backoffBase), backoffMax)
}
