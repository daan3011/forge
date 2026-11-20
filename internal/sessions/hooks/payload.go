package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MaxPayload is the most hook JSON Parse reads.
const MaxPayload = 1 << 20

// MaxStored is the size above which StoredPayload drops the bulky fields.
const MaxStored = 16 << 10

// bulkyFields are dropped from stored payloads over MaxStored.
var bulkyFields = []string{"tool_input", "tool_response", "prompt", "last_assistant_message"}

// ErrEmpty means the hook sent no JSON.
var ErrEmpty = errors.New("hooks: empty payload")

// Payload is a Claude Code hook event as received on stdin. Only the fields
// forge acts on are typed; Raw keeps the rest.
type Payload struct {
	SessionID        string `json:"session_id"`
	TranscriptPath   string `json:"transcript_path"`
	Cwd              string `json:"cwd"`
	Event            string `json:"hook_event_name"`
	ToolName         string `json:"tool_name"`
	NotificationType string `json:"notification_type"`
	Source           string `json:"source"` // SessionStart
	Reason           string `json:"reason"` // SessionEnd
	Error            string `json:"-"`      // StopFailure "error"; a string there, but not on every event

	Raw []byte `json:"-"`
}

// Parse reads one hook payload (at most MaxPayload bytes).
func Parse(r io.Reader) (Payload, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxPayload+1))
	if err != nil {
		return Payload{}, fmt.Errorf("hooks: read payload: %w", err)
	}
	if len(raw) > MaxPayload {
		return Payload{}, fmt.Errorf("hooks: payload over %d bytes", MaxPayload)
	}
	if len(raw) == 0 {
		return Payload{}, ErrEmpty
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return Payload{}, fmt.Errorf("hooks: parse payload: %w", err)
	}
	var extra struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &extra) == nil {
		var s string
		if json.Unmarshal(extra.Error, &s) == nil {
			p.Error = s
		}
	}
	p.Raw = raw
	return p, nil
}

// StoredPayload is the JSON kept in session_events: the raw payload, minus
// tool inputs, prompts and messages when it is larger than MaxStored.
func (p Payload) StoredPayload() string {
	if len(p.Raw) <= MaxStored {
		return string(p.Raw)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(p.Raw, &m); err != nil {
		return ""
	}
	for _, k := range bulkyFields {
		if _, ok := m[k]; ok {
			m[k] = json.RawMessage(`"(dropped by forge)"`)
		}
	}
	b, err := json.Marshal(m)
	if err != nil || len(b) > MaxStored {
		return ""
	}
	return string(b)
}
