package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/store"
)

const testID = "0b5f3c3e-8a1d-4c2b-9f00-1234567890ab"

var t0 = time.Date(2026, 11, 20, 9, 0, 0, 0, time.Local)

func fixture(t *testing.T, name string) Payload {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse %s: %v", name, err)
	}
	return p
}

func TestParse(t *testing.T) {
	tests := []struct {
		file string
		want Payload
	}{
		{"session_start.json", Payload{Event: SessionStart, Source: "startup"}},
		{"user_prompt_submit.json", Payload{Event: UserPromptSubmit}},
		{"pre_tool_use.json", Payload{Event: PreToolUse, ToolName: "Bash"}},
		{"post_tool_use.json", Payload{Event: PostToolUse, ToolName: "Bash"}},
		{"notification_permission.json", Payload{Event: Notification, NotificationType: "permission_prompt"}},
		{"notification_idle.json", Payload{Event: Notification, NotificationType: "idle_prompt"}},
		{"stop.json", Payload{Event: Stop}},
		{"stop_failure.json", Payload{Event: StopFailure, Error: "rate_limit"}},
		{"session_end.json", Payload{Event: SessionEnd, Reason: "prompt_input_exit"}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			p := fixture(t, tt.file)
			if p.SessionID != testID || p.Cwd != "/wt/forge/fix" || !strings.HasSuffix(p.TranscriptPath, testID+".jsonl") {
				t.Errorf("common fields: %+v", p)
			}
			p.SessionID, p.Cwd, p.TranscriptPath, p.Raw = "", "", "", nil
			if !reflect.DeepEqual(p, tt.want) {
				t.Errorf("got %+v\nwant %+v", p, tt.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"garbage", "not json"},
		{"too big", `{"x":"` + strings.Repeat("a", MaxPayload) + `"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse(strings.NewReader(tt.in)); err == nil {
				t.Error("want an error")
			}
		})
	}
	// A non-string "error" (e.g. a future object) is not fatal.
	p, err := Parse(strings.NewReader(`{"hook_event_name":"Stop","error":{"code":1}}`))
	if err != nil || p.Error != "" || p.Event != Stop {
		t.Errorf("got %+v, %v", p, err)
	}
}

func TestStoredPayload(t *testing.T) {
	small := fixture(t, "pre_tool_use.json")
	if got := small.StoredPayload(); got != string(small.Raw) {
		t.Errorf("small payload changed: %s", got)
	}
	big, err := Parse(strings.NewReader(`{"session_id":"s","hook_event_name":"PreToolUse","tool_name":"Write",` +
		`"tool_input":{"content":"` + strings.Repeat("x", 2*MaxStored) + `"}}`))
	if err != nil {
		t.Fatal(err)
	}
	got := big.StoredPayload()
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("stored payload is not JSON: %v", err)
	}
	if m["tool_name"] != "Write" || m["tool_input"] != "(dropped by forge)" || len(got) > MaxStored {
		t.Errorf("stored %d bytes: %.200s", len(got), got)
	}
}

func TestNext(t *testing.T) {
	ev := func(event string) Payload { return Payload{Event: event} }
	tests := []struct {
		name string
		p    Payload
		cur  store.Status
		want store.Status
		ok   bool
	}{
		{"start", Payload{Event: SessionStart, Source: "startup"}, store.StatusStarting, store.StatusIdle, true},
		{"resume", Payload{Event: SessionStart, Source: "resume"}, store.StatusDead, store.StatusIdle, true},
		{"clear", Payload{Event: SessionStart, Source: "clear"}, store.StatusIdle, store.StatusIdle, true},
		{"fork", Payload{Event: SessionStart, Source: "fork"}, store.StatusIdle, store.StatusIdle, true},
		{"compact keeps working", Payload{Event: SessionStart, Source: "compact"}, store.StatusWorking, store.StatusWorking, false},
		{"prompt", ev(UserPromptSubmit), store.StatusIdle, store.StatusWorking, true},
		{"pre tool", ev(PreToolUse), store.StatusIdle, store.StatusWorking, true},
		{"post tool after approval", ev(PostToolUse), store.StatusWaiting, store.StatusWorking, true},
		{"permission", Payload{Event: Notification, NotificationType: "permission_prompt"}, store.StatusWorking, store.StatusWaiting, true},
		{"elicitation", Payload{Event: Notification, NotificationType: "elicitation_dialog"}, store.StatusWorking, store.StatusWaiting, true},
		{"elicitation url", Payload{Event: Notification, NotificationType: "elicitation_url_dialog"}, store.StatusWorking, store.StatusWaiting, true},
		{"agent input", Payload{Event: Notification, NotificationType: "agent_needs_input"}, store.StatusWorking, store.StatusWaiting, true},
		{"elicitation done", Payload{Event: Notification, NotificationType: "elicitation_complete"}, store.StatusWaiting, store.StatusWaiting, false},
		{"idle prompt stays idle", Payload{Event: Notification, NotificationType: "idle_prompt"}, store.StatusIdle, store.StatusIdle, false},
		{"auth", Payload{Event: Notification, NotificationType: "auth_success"}, store.StatusIdle, store.StatusIdle, false},
		{"stop", ev(Stop), store.StatusWorking, store.StatusIdle, true},
		{"stop failure", Payload{Event: StopFailure, Error: "rate_limit"}, store.StatusWorking, store.StatusError, true},
		{"end", Payload{Event: SessionEnd, Reason: "prompt_input_exit"}, store.StatusIdle, store.StatusDead, true},
		{"end other", Payload{Event: SessionEnd, Reason: "other"}, store.StatusIdle, store.StatusDead, true},
		{"end by clear", Payload{Event: SessionEnd, Reason: "clear"}, store.StatusIdle, store.StatusIdle, false},
		{"end by resume", Payload{Event: SessionEnd, Reason: "resume"}, store.StatusIdle, store.StatusIdle, false},
		{"unknown event", ev("SubagentStop"), store.StatusWorking, store.StatusWorking, false},
		{"archived never changes", ev(PreToolUse), store.StatusArchived, store.StatusArchived, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Next(tt.p, tt.cur)
			if got != tt.want || ok != tt.ok {
				t.Errorf("Next = %s, %v; want %s, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestIsForgeCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{Command("/usr/local/bin/forge", Stop), true},
		{Command("/Users/me/go bin/forge", PreToolUse), true},
		{Command("/it's/forge", Stop), true},
		{"forge hook Notification", true},
		{"/usr/bin/forge hook Unknown", false},
		{"/usr/bin/forgery hook Stop", false},
		{"say hook Stop", false},
		{"./notify.sh", false},
	}
	for _, tt := range tests {
		if got := isForgeCommand(tt.cmd); got != tt.want {
			t.Errorf("isForgeCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

// settings is the parsed settings file, for assertions.
type settings struct {
	Permissions map[string]any `json:"permissions"`
	Hooks       map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func readSettings(t *testing.T, dir string) settings {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, SettingsRel))
	if err != nil {
		t.Fatal(err)
	}
	var s settings
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("settings: %v\n%s", err, b)
	}
	return s
}

func TestInstallFresh(t *testing.T) {
	dir := t.TempDir()
	if err := Install(dir, "/opt/forge/bin/forge"); err != nil {
		t.Fatal(err)
	}
	s := readSettings(t, dir)
	if len(s.Hooks) != len(Events) {
		t.Fatalf("got %d events, want %d", len(s.Hooks), len(Events))
	}
	for _, ev := range Events {
		gs := s.Hooks[ev]
		if len(gs) != 1 || len(gs[0].Hooks) != 1 || gs[0].Matcher != "" {
			t.Fatalf("%s: %+v", ev, gs)
		}
		h := gs[0].Hooks[0]
		if h.Type != "command" || h.Command != "'/opt/forge/bin/forge' hook "+ev || h.Timeout != HookTimeout {
			t.Errorf("%s: %+v", ev, h)
		}
	}
	fi, err := os.Stat(filepath.Join(dir, SettingsRel))
	if err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("mode: %v %v", fi.Mode(), err)
	}
}

func TestInstallMergesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	existing := `{
  "permissions": {"allow": ["Bash(make test)"]},
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "./notify.sh"}, {"type": "command", "command": "'/old/forge' hook Stop"}]}
    ],
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "./guard.sh"}]},
      {"hooks": [{"type": "command", "command": "/old/forge hook PreToolUse"}]}
    ]
  }
}`
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SettingsRel), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Install(dir, "/new/forge"); err != nil {
			t.Fatal(err)
		}
	}
	s := readSettings(t, dir)
	if s.Permissions == nil {
		t.Error("permissions dropped")
	}
	var cmds []string
	for _, ev := range []string{Stop, PreToolUse} {
		for _, g := range s.Hooks[ev] {
			for _, h := range g.Hooks {
				cmds = append(cmds, g.Matcher+"|"+h.Command)
			}
		}
	}
	want := []string{
		"|./notify.sh", "|'/new/forge' hook Stop",
		"Bash|./guard.sh", "|'/new/forge' hook PreToolUse",
	}
	if strings.Join(cmds, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(cmds, "\n"), strings.Join(want, "\n"))
	}
}

func TestInstallErrors(t *testing.T) {
	if err := Install(t.TempDir(), ""); err == nil {
		t.Error("empty binary: want an error")
	}
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".claude"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, SettingsRel), []byte("{broken"), 0o600)
	if err := Install(dir, "/f/forge"); err == nil {
		t.Error("broken settings: want an error, not an overwrite")
	}
}

func TestInstallAllowsForgeMCP(t *testing.T) {
	allowOf := func(t *testing.T, dir string) (allow []string, rest map[string]any) {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, SettingsRel))
		if err != nil {
			t.Fatal(err)
		}
		var top struct {
			Permissions map[string]any `json:"permissions"`
		}
		if err := json.Unmarshal(b, &top); err != nil {
			t.Fatal(err)
		}
		for _, r := range top.Permissions["allow"].([]any) {
			allow = append(allow, r.(string))
		}
		delete(top.Permissions, "allow")
		return allow, top.Permissions
	}
	tests := []struct {
		name     string
		existing string
		want     []string
		rest     []string // other permission keys that must survive
	}{
		{"fresh", "", []string{MCPAllowRule}, nil},
		{"no permissions", `{"model": "opus"}`, []string{MCPAllowRule}, nil},
		{
			"keeps rules", `{"permissions": {"allow": ["Bash(make test)"], "deny": ["Bash(rm:*)"], "ask": ["WebFetch"], "defaultMode": "acceptEdits"}}`,
			[]string{"Bash(make test)", MCPAllowRule},
			[]string{"deny", "ask", "defaultMode"},
		},
		{"already there", `{"permissions": {"allow": ["mcp__forge__*", "Read"]}}`, []string{MCPAllowRule, "Read"}, nil},
		{"null allow", `{"permissions": {"allow": null}}`, []string{MCPAllowRule}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.existing != "" {
				_ = os.MkdirAll(filepath.Join(dir, ".claude"), 0o755)
				if err := os.WriteFile(filepath.Join(dir, SettingsRel), []byte(tt.existing), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := Install(dir, "/f/forge"); err != nil {
				t.Fatal(err)
			}
			first, err := os.ReadFile(filepath.Join(dir, SettingsRel))
			if err != nil {
				t.Fatal(err)
			}
			if err := Install(dir, "/f/forge"); err != nil {
				t.Fatal(err)
			}
			second, _ := os.ReadFile(filepath.Join(dir, SettingsRel))
			if string(first) != string(second) {
				t.Errorf("second install changed the file:\n%s\n---\n%s", first, second)
			}
			allow, rest := allowOf(t, dir)
			if !reflect.DeepEqual(allow, tt.want) {
				t.Errorf("allow = %v, want %v", allow, tt.want)
			}
			for _, k := range tt.rest {
				if _, ok := rest[k]; !ok {
					t.Errorf("permissions.%s dropped: %v", k, rest)
				}
			}
		})
	}
}

func TestInstallBadAllow(t *testing.T) {
	for _, bad := range []string{`{"permissions": {"allow": "mcp__forge__*"}}`, `{"permissions": ["x"]}`} {
		dir := t.TempDir()
		_ = os.MkdirAll(filepath.Join(dir, ".claude"), 0o755)
		_ = os.WriteFile(filepath.Join(dir, SettingsRel), []byte(bad), 0o600)
		if err := Install(dir, "/f/forge"); err == nil {
			t.Errorf("%s: want an error, not an overwrite", bad)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, SettingsRel)); string(b) != bad {
			t.Errorf("file changed: %s", b)
		}
	}
}

// --- Ingest -------------------------------------------------------------------

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "forge.db"),
		store.WithClock(func() time.Time { return t0 }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func mkSession(t *testing.T, st *store.Store, name, claudeID string, status store.Status) store.Session {
	t.Helper()
	x, err := st.Sessions().Create(context.Background(), store.Session{
		Name: name, ClaudeSessionID: claudeID, Project: "forge", Status: status,
	})
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func TestIngest(t *testing.T) {
	ctx := context.Background()
	at := t0.Add(time.Minute)
	tests := []struct {
		name       string
		status     store.Status
		p          Payload
		env        string
		want       Result
		wantStatus store.Status
		wantClaude string
		wantLinked bool
		wantTransc bool
	}{
		{
			name: "known session goes working", status: store.StatusIdle,
			p:          Payload{SessionID: testID, Event: PreToolUse, ToolName: "Bash", TranscriptPath: "/t.jsonl"},
			want:       Result{Session: "fix", Status: store.StatusWorking, Changed: true},
			wantStatus: store.StatusWorking, wantClaude: testID, wantLinked: true, wantTransc: true,
		},
		{
			name: "no status change", status: store.StatusIdle,
			p:          Payload{SessionID: testID, Event: Notification, NotificationType: "idle_prompt"},
			want:       Result{Session: "fix", Status: store.StatusIdle},
			wantStatus: store.StatusIdle, wantClaude: testID, wantLinked: true,
		},
		{
			name: "clear adopts the new id via FORGE_SESSION", status: store.StatusIdle, env: "fix",
			p:          Payload{SessionID: "new-id", Event: SessionStart, Source: "clear"},
			want:       Result{Session: "fix", Status: store.StatusIdle, Changed: true},
			wantStatus: store.StatusIdle, wantClaude: "new-id", wantLinked: true,
		},
		{
			name: "nested claude is not adopted", status: store.StatusWorking, env: "fix",
			p:    Payload{SessionID: "nested", Event: SessionStart, Source: "startup"},
			want: Result{}, wantStatus: store.StatusWorking, wantClaude: testID,
		},
		{
			name: "nested claude's stop changes nothing", status: store.StatusWorking, env: "fix",
			p:    Payload{SessionID: "nested", Event: Stop},
			want: Result{}, wantStatus: store.StatusWorking, wantClaude: testID,
		},
		{
			name: "unknown env name", status: store.StatusIdle, env: "other",
			p:    Payload{SessionID: "new-id", Event: SessionStart, Source: "resume"},
			want: Result{}, wantStatus: store.StatusIdle, wantClaude: testID,
		},
		{
			name: "archived is recorded, not changed", status: store.StatusArchived,
			p:          Payload{SessionID: testID, Event: PreToolUse},
			want:       Result{Session: "fix", Status: store.StatusArchived},
			wantStatus: store.StatusArchived, wantClaude: testID, wantLinked: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newStore(t)
			x := mkSession(t, st, "fix", testID, tt.status)
			res, err := Ingest(ctx, StoreFrom(st), tt.p, tt.env, at)
			if err != nil {
				t.Fatal(err)
			}
			if res != tt.want {
				t.Errorf("result %+v, want %+v", res, tt.want)
			}
			got, err := st.Sessions().Get(ctx, x.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tt.wantStatus || got.ClaudeSessionID != tt.wantClaude {
				t.Errorf("session status %s claude %s, want %s %s", got.Status, got.ClaudeSessionID, tt.wantStatus, tt.wantClaude)
			}
			if (got.TranscriptPath != "") != tt.wantTransc {
				t.Errorf("transcript %q", got.TranscriptPath)
			}
			e, err := st.Events().Latest(ctx, tt.p.SessionID)
			if err != nil {
				t.Fatalf("event not recorded: %v", err)
			}
			if (e.SessionID == x.ID) != tt.wantLinked || e.Event != tt.p.Event || !e.CreatedAt.Equal(at) {
				t.Errorf("event %+v", e)
			}
		})
	}
}

func TestIngestResumeOfAnotherSession(t *testing.T) {
	st := newStore(t)
	mkSession(t, st, "a", "id-a", store.StatusIdle)
	b := mkSession(t, st, "b", "id-b", store.StatusIdle)
	// Window b resumes a's conversation: the id is taken, so nothing is adopted.
	res, err := Ingest(context.Background(), StoreFrom(st),
		Payload{SessionID: "id-a", Event: SessionStart, Source: "resume"}, "b", t0)
	if err != nil || res.Session != "a" {
		t.Fatalf("got %+v, %v", res, err)
	}
	got, _ := st.Sessions().Get(context.Background(), b.ID)
	if got.ClaudeSessionID != "id-b" {
		t.Errorf("b adopted %s", got.ClaudeSessionID)
	}
}

func TestIngestNoSessionID(t *testing.T) {
	st := newStore(t)
	res, err := Ingest(context.Background(), StoreFrom(st), Payload{Event: Stop}, "", t0)
	if err != nil || res != (Result{}) {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestIngestStoreError(t *testing.T) {
	st := newStore(t)
	_ = st.Close()
	_, err := Ingest(context.Background(), StoreFrom(st), Payload{SessionID: testID, Event: Stop}, "", t0)
	if err == nil || errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want a store error, got %v", err)
	}
}
