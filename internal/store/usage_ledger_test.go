package store

import (
	"context"
	"testing"
	"time"
)

func TestUsageLedger(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	u := s.Usage()
	at := t0.Add(-time.Hour)
	msg := func(key, sess string, in int64) UsageMessage {
		return UsageMessage{Key: key, ClaudeSessionID: sess, Cwd: "/w", Model: "opus", Time: at, Tokens: Tokens{Input: in}}
	}
	fa := UsageFile{Path: "/a.jsonl", Size: 10, Offset: 10, Head: 1<<63 + 5, HeadLen: 10}
	noErr(t, u.Ingest(ctx, fa, []UsageMessage{msg("m1", "A", 1), msg("m2", "A", 2)}))
	// Same file again: the last line of a message wins.
	noErr(t, u.Ingest(ctx, fa, []UsageMessage{msg("m2", "A", 20)}))
	// Another file repeating m1 (a resumed session) is ignored for m1.
	fb := UsageFile{Path: "/b.jsonl", Size: 5, Offset: 5, HeadLen: 5}
	noErr(t, u.Ingest(ctx, fb, []UsageMessage{msg("m1", "B", 999), msg("m3", "B", 3)}))

	got, ok, err := u.UsageFile(ctx, "/a.jsonl")
	noErr(t, err)
	if !ok || got != fa {
		t.Errorf("file state = %+v %v, want %+v", got, ok, fa)
	}
	if _, ok, _ := u.UsageFile(ctx, "/none"); ok {
		t.Error("unknown file reported as read")
	}

	groups, err := u.LedgerGroups(ctx)
	noErr(t, err)
	want := map[string]int64{"A": 21, "B": 3}
	if len(groups) != 2 {
		t.Fatalf("groups = %+v", groups)
	}
	for _, g := range groups {
		if g.Input != want[g.ClaudeSessionID] || g.Day.Format(dayLayout) != at.Format(dayLayout) {
			t.Errorf("group %+v, want input %d", g, want[g.ClaudeSessionID])
		}
	}

	noErr(t, u.ForgetFile(ctx, "/a.jsonl"))
	groups, err = u.LedgerGroups(ctx)
	noErr(t, err)
	if len(groups) != 1 || groups[0].ClaudeSessionID != "B" {
		t.Errorf("after forget: %+v", groups)
	}
	if _, ok, _ := u.UsageFile(ctx, "/a.jsonl"); ok {
		t.Error("forgotten file still has state")
	}
}

func TestUsageReplaceRollups(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	u := s.Usage()
	noErr(t, u.Upsert(ctx, UsageRow{t0, "old", "x", "opus", Tokens{Input: 5}}))
	rows := []UsageRow{
		{t0, "forge", "s1", "opus", Tokens{Input: 1, Messages: 1}},
		{t0, "forge", "s1", "opus", Tokens{Input: 2, Messages: 1}}, // same key: summed
		{t0.AddDate(0, 0, -3), "forge", "s2", "sonnet", Tokens{Output: 4, Messages: 1}},
	}
	noErr(t, u.ReplaceRollups(ctx, rows))
	got, err := u.Rows(ctx, t0.AddDate(0, 0, -7), "")
	noErr(t, err)
	if len(got) != 2 || got[0].ClaudeSessionID != "s2" || got[1].Input != 3 || got[1].Messages != 2 {
		t.Errorf("rows = %+v", got)
	}
	got, err = u.Rows(ctx, t0.AddDate(0, 0, -7), "s1")
	noErr(t, err)
	if len(got) != 1 || got[0].ClaudeSessionID != "s1" {
		t.Errorf("session rows = %+v", got)
	}
}
