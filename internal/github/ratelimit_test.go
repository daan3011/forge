package github

import (
	"net/http"
	"testing"
	"time"
)

var now = time.Unix(1_700_000_000, 0)

func header(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

func TestParseRateLimit(t *testing.T) {
	reset := now.Add(30 * time.Minute)
	tests := []struct {
		name   string
		h      http.Header
		want   RateLimit
		wantOK bool
	}{
		{"none", header(), RateLimit{}, false},
		{
			"full set",
			header("X-RateLimit-Limit", "5000", "X-RateLimit-Remaining", "4990",
				"X-RateLimit-Used", "10", "X-RateLimit-Reset", "1700001800"),
			RateLimit{Limit: 5000, Remaining: 4990, Used: 10, Reset: reset},
			true,
		},
		{"no reset", header("X-RateLimit-Limit", "60", "X-RateLimit-Remaining", "3"), RateLimit{Limit: 60, Remaining: 3}, true},
		{"garbage", header("X-RateLimit-Limit", "lots", "X-RateLimit-Remaining", "3"), RateLimit{}, false},
		{"retry after seconds", header("Retry-After", "90"), RateLimit{RetryAfter: 90 * time.Second, Reset: now.Add(90 * time.Second)}, true},
		{
			"retry after date",
			header("Retry-After", now.Add(2*time.Minute).UTC().Format(http.TimeFormat)),
			RateLimit{RetryAfter: 2 * time.Minute, Reset: now.Add(2 * time.Minute)},
			true,
		},
		{
			"retry after keeps a later reset",
			header("X-RateLimit-Limit", "5000", "X-RateLimit-Remaining", "4000", "X-RateLimit-Reset", "1700001800", "Retry-After", "60"),
			RateLimit{Limit: 5000, Remaining: 4000, Reset: reset, RetryAfter: time.Minute},
			true,
		},
		{"retry after in the past", header("Retry-After", now.Add(-time.Minute).UTC().Format(http.TimeFormat)), RateLimit{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseRateLimit(tt.h, now)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("got %+v, %v; want %+v, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestObserve(t *testing.T) {
	if Observe(nil, now) != nil {
		t.Fatal("nil response")
	}
	if Observe(&http.Response{Header: header()}, now) != nil {
		t.Fatal("no headers")
	}
	msg, ok := Observe(&http.Response{Header: header("X-RateLimit-Limit", "60", "X-RateLimit-Remaining", "1")}, now).(RateLimitMsg)
	if !ok || msg.Remaining != 1 {
		t.Fatalf("msg = %#v", msg)
	}
}

func TestLow(t *testing.T) {
	tests := []struct {
		rl   RateLimit
		want bool
	}{
		{RateLimit{}, false},
		{RateLimit{Limit: 5000, Remaining: 500}, false},
		{RateLimit{Limit: 5000, Remaining: 499}, true},
		{RateLimit{Limit: 5000, Remaining: 0}, true},
		{RateLimit{Limit: 5000, Remaining: 4000, RetryAfter: time.Second}, true},
	}
	for _, tt := range tests {
		if got := tt.rl.Low(); got != tt.want {
			t.Errorf("%+v.Low() = %v", tt.rl, got)
		}
	}
}

func TestNextDelay(t *testing.T) {
	base := time.Minute
	reset := now.Add(50 * time.Minute)
	tests := []struct {
		name string
		rl   RateLimit
		want time.Duration
	}{
		{"plenty left", RateLimit{Limit: 5000, Remaining: 4000, Reset: reset}, base},
		{"low spreads calls", RateLimit{Limit: 5000, Remaining: 10, Reset: reset}, 5 * time.Minute},
		{"low but spread is faster than base", RateLimit{Limit: 5000, Remaining: 400, Reset: reset}, base},
		{"exhausted waits for reset", RateLimit{Limit: 5000, Remaining: 0, Reset: reset}, 50 * time.Minute},
		{"reset already passed", RateLimit{Limit: 5000, Remaining: 0, Reset: now.Add(-time.Second)}, base},
		{"low without reset", RateLimit{Limit: 60, Remaining: 1}, base},
		{"retry after", RateLimit{RetryAfter: 3 * time.Minute}, 3 * time.Minute},
		{"short retry after", RateLimit{RetryAfter: 10 * time.Second}, base},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NextDelay(tt.rl, base, now); got != tt.want {
				t.Fatalf("NextDelay = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSummary(t *testing.T) {
	tests := []struct {
		rl   RateLimit
		want string
	}{
		{RateLimit{Limit: 5000, Remaining: 312, Reset: now.Add(14*time.Minute + 5*time.Second)}, "gh 312/5000 · resets 14m"},
		{RateLimit{Limit: 60, Remaining: 2}, "gh 2/60"},
		{RateLimit{Limit: 5000, Remaining: 0, RetryAfter: 45 * time.Second, Reset: now.Add(45 * time.Second)}, "gh 0/5000 · resets 45s"},
		{RateLimit{Limit: 5000, Remaining: 0, Reset: now.Add(-time.Minute)}, "gh 0/5000"},
		{RateLimit{Limit: 5000, Remaining: 0, Reset: now.Add(75 * time.Minute)}, "gh 0/5000 · resets 1h15m"},
	}
	for _, tt := range tests {
		if got := tt.rl.Summary(now); got != tt.want {
			t.Errorf("Summary = %q, want %q", got, tt.want)
		}
	}
}
