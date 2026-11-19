package store

import (
	"context"
	"testing"
	"time"
)

func TestUsage(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	u := s.Usage()
	day := func(n int) time.Time { return t0.AddDate(0, 0, n) }
	rows := []UsageRow{
		{day(-2), "forge", "s1", "opus", Tokens{Input: 10, Output: 20, Messages: 1}},
		{day(-1), "forge", "s1", "opus", Tokens{Input: 100, Output: 200, CacheRead: 50, CacheWrite: 5, Messages: 3}},
		{day(-1), "forge", "s1", "sonnet", Tokens{Input: 1, Messages: 1}},
		{day(-1), "other", "s2", "opus", Tokens{Input: 1000, Messages: 2}},
		{day(0), "forge", "s3", "opus", Tokens{Output: 7, Messages: 1}},
	}
	for _, r := range rows {
		noErr(t, u.Upsert(ctx, r))
	}
	// Re-aggregating replaces totals rather than adding to them.
	noErr(t, u.Upsert(ctx, rows[0]))

	t.Run("daily", func(t *testing.T) {
		got, err := u.Daily(ctx, day(-1))
		noErr(t, err)
		want := []DayTotal{
			{day(-1), Tokens{Input: 1101, Output: 200, CacheRead: 50, CacheWrite: 5, Messages: 6}},
			{day(0), Tokens{Output: 7, Messages: 1}},
		}
		if len(got) != len(want) {
			t.Fatalf("got %+v", got)
		}
		for i := range want {
			if got[i].Day.Format(dayLayout) != want[i].Day.Format(dayLayout) || got[i].Tokens != want[i].Tokens {
				t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
			}
		}
	})

	t.Run("by project", func(t *testing.T) {
		got, err := u.ByProject(ctx, day(-7))
		noErr(t, err)
		if len(got) != 2 || got[0].Project != "other" || got[0].Total() != 1000 ||
			got[1].Project != "forge" || got[1].Total() != 10+20+100+200+50+5+1+7 {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("top sessions", func(t *testing.T) {
		got, err := u.TopSessions(ctx, day(-7), 2)
		noErr(t, err)
		if len(got) != 2 || got[0].ClaudeSessionID != "s2" || got[0].Project != "other" || got[1].ClaudeSessionID != "s1" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("session totals", func(t *testing.T) {
		tests := []struct {
			id   string
			want Tokens
		}{
			{"s1", Tokens{Input: 111, Output: 220, CacheRead: 50, CacheWrite: 5, Messages: 5}},
			{"none", Tokens{}},
		}
		for _, tt := range tests {
			got, err := u.SessionTotals(ctx, tt.id)
			noErr(t, err)
			if got != tt.want {
				t.Errorf("%s: got %+v, want %+v", tt.id, got, tt.want)
			}
		}
	})
}
