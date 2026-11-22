package usage

import (
	"testing"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/store"
)

func TestAggregate(t *testing.T) {
	today := time.Date(2026, 11, 20, 15, 0, 0, 0, time.Local)
	day := func(n int) time.Time { return time.Date(2026, 11, 20+n, 0, 0, 0, 0, time.Local) }
	tok := func(n int64) store.Tokens { return store.Tokens{Input: n, Messages: 1} }
	rows := []store.UsageRow{
		{Day: day(-20), Project: "forge", ClaudeSessionID: "old", Model: "m", Tokens: tok(1000)}, // out of range
		{Day: day(-13), Project: "forge", ClaudeSessionID: "s1", Model: "m", Tokens: tok(10)},
		{Day: day(-1), Project: "forge", ClaudeSessionID: "s1", Model: "free", Tokens: tok(5)},
		{Day: day(-1), Project: "other", ClaudeSessionID: "s2", Model: "m", Tokens: tok(100)},
		{Day: day(0), Project: "forge", ClaudeSessionID: "s3", Model: "m", Tokens: tok(1)},
	}
	res := NewResolver([]store.Session{{Name: "fix-ci", ClaudeSessionID: "s1", Project: "forge"}}, nil)
	in := AggregateInput{
		Rows: rows, Prices: Prices{"m": config.Price{Input: 1e6}}, Resolver: res,
		Cwds: map[string]string{"s2": "/tmp/scratch"}, Today: today, Days: 14, Top: 2,
	}

	r := Aggregate(in)
	if len(r.Days) != 14 || !r.Days[0].Day.Equal(day(-13)) || !r.Days[13].Day.Equal(day(0)) {
		t.Fatalf("days = %v .. %v (%d)", r.Days[0].Day, r.Days[len(r.Days)-1].Day, len(r.Days))
	}
	if r.Days[0].Input != 10 || r.Days[12].Input != 105 || r.Days[13].Input != 1 || r.Days[5].Total() != 0 {
		t.Errorf("day totals wrong: %+v", r.Days)
	}
	if r.Total.Input != 116 || r.Total.Cost != 111 || r.Total.Unpriced != 5 || !r.Unpriced() {
		t.Errorf("total = %+v", r.Total)
	}
	if len(r.Projects) != 2 || r.Projects[0].Project != "other" || r.Projects[1].Input != 16 || !r.Projects[1].Partial() {
		t.Errorf("projects = %+v", r.Projects)
	}
	if len(r.Sessions) != 2 || r.Sessions[0].ClaudeSessionID != "s2" || r.Sessions[1].Name != "fix-ci" {
		t.Fatalf("sessions = %+v", r.Sessions)
	}
	if got := r.Sessions[0].Label(); got != "s2 · scratch" {
		t.Errorf("label = %q", got)
	}
	if !r.Sessions[1].Last.Equal(day(-1)) {
		t.Errorf("last = %v", r.Sessions[1].Last)
	}

	in.Project = "forge"
	r = Aggregate(in)
	if r.Total.Input != 16 || len(r.Projects) != 2 || len(r.Sessions) != 2 || r.Sessions[0].ClaudeSessionID != "s1" {
		t.Errorf("filtered: total %+v, projects %d, sessions %+v", r.Total, len(r.Projects), r.Sessions)
	}
}
