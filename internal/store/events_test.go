package store

import (
	"context"
	"testing"
	"time"
)

func TestEventsAppendResolvesSession(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	x := mustSession(t, s, "a")

	tests := []struct {
		name    string
		in      Event
		wantSID int64
	}{
		{"resolved from claude id", Event{ClaudeSessionID: "uuid-a", Event: "UserPromptSubmit"}, x.ID},
		{"explicit session id", Event{SessionID: x.ID, ClaudeSessionID: "uuid-a", Event: "Stop"}, x.ID},
		{"unknown session kept", Event{ClaudeSessionID: "stranger", Event: "SessionStart"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := s.Events().Append(ctx, tt.in)
			noErr(t, err)
			got, err := s.Events().Latest(ctx, tt.in.ClaudeSessionID)
			noErr(t, err)
			if got.ID != id || got.SessionID != tt.wantSID || got.Event != tt.in.Event || !got.CreatedAt.Equal(t0) {
				t.Errorf("got %+v", got)
			}
		})
	}
}

func TestEventsListAndPrune(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	x := mustSession(t, s, "a")
	for i, name := range []string{"SessionStart", "PreToolUse", "Stop"} {
		_, err := s.Events().Append(ctx, Event{
			ClaudeSessionID: "uuid-a", Event: name, ToolName: "Edit", Payload: `{"i":1}`,
			CreatedAt: t0.Add(time.Duration(i) * time.Minute),
		})
		noErr(t, err)
	}

	tests := []struct {
		limit int
		want  []string
	}{
		{0, []string{"Stop", "PreToolUse", "SessionStart"}},
		{2, []string{"Stop", "PreToolUse"}},
	}
	for _, tt := range tests {
		got, err := s.Events().ListForSession(ctx, x.ID, tt.limit)
		noErr(t, err)
		if len(got) != len(tt.want) {
			t.Fatalf("limit %d: got %d events", tt.limit, len(got))
		}
		for i, e := range got {
			if e.Event != tt.want[i] || e.ToolName != "Edit" || e.Payload != `{"i":1}` {
				t.Errorf("limit %d [%d]: %+v", tt.limit, i, e)
			}
		}
	}

	n, err := s.Events().PruneBefore(ctx, t0.Add(90*time.Second))
	noErr(t, err)
	if n != 2 {
		t.Errorf("pruned %d, want 2", n)
	}
	_, err = s.Events().Latest(ctx, "nobody")
	wantErr(t, err, ErrNotFound)
}

func TestEventsBytesBySession(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	a, b := mustSession(t, s, "a"), mustSession(t, s, "b")
	for _, e := range []Event{
		{SessionID: a.ID, Event: "Stop", Payload: "12345"},
		{SessionID: a.ID, Event: "Stop", Payload: "123"},
		{SessionID: b.ID, Event: "Stop"},
		{ClaudeSessionID: "stranger", Event: "Stop", Payload: "xx"},
	} {
		_, err := s.Events().Append(ctx, e)
		noErr(t, err)
	}
	got, err := s.Events().BytesBySession(ctx)
	noErr(t, err)
	if len(got) != 2 || got[a.ID] != 8 || got[b.ID] != 0 {
		t.Errorf("BytesBySession = %v", got)
	}
}
