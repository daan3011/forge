package usage

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/daan3011/forge/internal/store"
)

// line is the subset of a transcript line the usage parser reads.
type line struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	RequestID string    `json:"requestId"`
	Cwd       string    `json:"cwd"`
	Message   *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// Message is the usage of one assistant line.
type Message struct {
	// Key is the message id, or the request id when there is none.
	Key   string
	Cwd   string
	Model string
	Time  time.Time
	store.Tokens
}

// ParseLine decodes the usage of one JSONL line. ok is false for blank,
// malformed and non-assistant lines and for lines without usage or a key.
// Unlike the transcript viewer it keeps sidechain lines: subagent work costs
// tokens too. bad reports a line that was not valid JSON.
func ParseLine(raw []byte) (m Message, ok, bad bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return Message{}, false, false
	}
	// Cheap filter: most lines are not assistant messages.
	if !bytes.Contains(raw, []byte(`"usage"`)) {
		return Message{}, false, false
	}
	var l line
	if err := json.Unmarshal(raw, &l); err != nil {
		return Message{}, false, true
	}
	if l.Type != "assistant" || l.Message == nil || l.Message.Usage == nil {
		return Message{}, false, false
	}
	key := l.Message.ID
	if key == "" {
		key = l.RequestID
	}
	if key == "" || l.Timestamp.IsZero() {
		return Message{}, false, false
	}
	u := l.Message.Usage
	return Message{
		Key: key, Cwd: l.Cwd, Model: l.Message.Model, Time: l.Timestamp,
		Tokens: store.Tokens{
			Input: u.InputTokens, Output: u.OutputTokens,
			CacheRead: u.CacheReadInputTokens, CacheWrite: u.CacheCreationInputTokens,
		},
	}, true, false
}
