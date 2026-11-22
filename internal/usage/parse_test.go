package usage

import (
	"testing"

	"github.com/daan3011/forge/internal/store"
)

func TestParseLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		ok, bad bool
		want    Message
	}{
		{"blank", "  ", false, false, Message{}},
		{"malformed", `{"type":"assistant","usage":`, false, true, Message{}},
		{"user line", `{"type":"user","message":{"role":"user","content":"hi"}}`, false, false, Message{}},
		{"no usage", `{"type":"assistant","timestamp":"2026-11-20T10:00:00Z","message":{"id":"m1","content":[]}}`, false, false, Message{}},
		{
			"usage, message id", `{"type":"assistant","timestamp":"2026-11-20T10:00:00Z","requestId":"r1","cwd":"/w","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":2,"output_tokens":3,"cache_read_input_tokens":5,"cache_creation_input_tokens":7}}}`,
			true, false,
			Message{Key: "m1", Cwd: "/w", Model: "claude-opus-5-5", Tokens: store.Tokens{Input: 2, Output: 3, CacheRead: 5, CacheWrite: 7}},
		},
		{
			"request id fallback", `{"type":"assistant","timestamp":"2026-11-20T10:00:00Z","requestId":"r1","message":{"usage":{"input_tokens":1}}}`,
			true, false,
			Message{Key: "r1", Tokens: store.Tokens{Input: 1}},
		},
		{"no key", `{"type":"assistant","timestamp":"2026-11-20T10:00:00Z","message":{"usage":{"input_tokens":1}}}`, false, false, Message{}},
		{
			"sidechain counted", `{"type":"assistant","isSidechain":true,"timestamp":"2026-11-20T10:00:00Z","message":{"id":"s1","usage":{"output_tokens":4}}}`,
			true, false,
			Message{Key: "s1", Tokens: store.Tokens{Output: 4}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, bad := ParseLine([]byte(tt.line))
			if ok != tt.ok || bad != tt.bad {
				t.Fatalf("ok, bad = %v, %v; want %v, %v", ok, bad, tt.ok, tt.bad)
			}
			if !ok {
				return
			}
			got.Time = got.Time.UTC()
			if got.Key != tt.want.Key || got.Cwd != tt.want.Cwd || got.Model != tt.want.Model || got.Tokens != tt.want.Tokens || got.Time.IsZero() {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
