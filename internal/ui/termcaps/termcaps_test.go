package termcaps

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want Protocol
		term string
	}{
		{"kitty TERM", map[string]string{"TERM": "xterm-kitty"}, Kitty, "kitty"},
		{"kitty window id", map[string]string{"TERM": "xterm-256color", "KITTY_WINDOW_ID": "1"}, Kitty, "kitty"},
		{"ghostty program", map[string]string{"TERM_PROGRAM": "ghostty"}, Kitty, "Ghostty"},
		{"ghostty TERM", map[string]string{"TERM": "xterm-ghostty"}, Kitty, "Ghostty"},
		{"ghostty resources", map[string]string{"GHOSTTY_RESOURCES_DIR": "/x"}, Kitty, "Ghostty"},
		{"wezterm", map[string]string{"TERM_PROGRAM": "WezTerm"}, Sixel, "WezTerm"},
		{"iterm2", map[string]string{"TERM_PROGRAM": "iTerm.app"}, Sixel, "iTerm2"},
		{"foot", map[string]string{"TERM": "foot-extra"}, Sixel, "foot"},
		{"konsole", map[string]string{"KONSOLE_VERSION": "230804"}, Sixel, "Konsole"},
		{"windows terminal", map[string]string{"WT_SESSION": "abc"}, Sixel, "Windows Terminal"},
		{"apple terminal", map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, None, "Apple_Terminal"},
		{"bare TERM", map[string]string{"TERM": "xterm-256color"}, None, "xterm-256color"},
		{"nothing", nil, None, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, term := FromEnv(env(tt.env))
			if p != tt.want || term != tt.term {
				t.Fatalf("got %v %q, want %v %q", p, term, tt.want, tt.term)
			}
		})
	}
}

func TestParseMode(t *testing.T) {
	for _, s := range []string{"", "auto", "kitty", "sixel", "chafa", "text", "off"} {
		if _, err := ParseMode(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	if _, err := ParseMode("iterm"); err == nil {
		t.Error("want an error for an unknown mode")
	}
}

func TestResolveModes(t *testing.T) {
	kitty := envTerm{"kitty", Kitty}
	tests := []struct {
		mode      Mode
		t         envTerm
		want      Protocol
		chafa     bool
		off       bool
		reasonHas string
	}{
		{ModeAuto, kitty, Kitty, true, false, ""},
		{ModeAuto, envTerm{name: "Apple_Terminal"}, None, true, false, "Apple_Terminal"},
		{ModeSixel, kitty, Sixel, true, false, ""},
		{ModeKitty, envTerm{name: "x"}, Kitty, true, false, ""},
		{ModeChafa, kitty, None, true, false, "chafa"},
		{ModeText, kitty, None, false, false, "text"},
		{ModeOff, kitty, None, false, true, "off"},
	}
	for _, tt := range tests {
		c := resolve(tt.mode, tt.t, 0, 0)
		if c.Protocol != tt.want || c.Chafa != tt.chafa || c.Off != tt.off || !strings.Contains(c.Reason, tt.reasonHas) {
			t.Errorf("%s/%s: got %+v", tt.mode, tt.t.name, c)
		}
		if c.CellW != DefaultCellW || c.CellH != DefaultCellH {
			t.Errorf("%s: cell %dx%d, want the default", tt.mode, c.CellW, c.CellH)
		}
	}
}

func TestWrap(t *testing.T) {
	seq := "\x1b_Ga=T;AAAA\x1b\\"
	if got := (Caps{}).Wrap(seq); got != seq {
		t.Fatalf("outside tmux: got %q", got)
	}
	got := (Caps{Tmux: true}).Wrap(seq)
	want := "\x1bPtmux;\x1b\x1b_Ga=T;AAAA\x1b\x1b\\\x1b\\"
	if got != want {
		t.Fatalf("tmux: got %q, want %q", got, want)
	}
}

// startMsgs runs the detector's start command and returns the raw output
// it asks for.
func startMsgs(t *testing.T, d *Detector) (raws []string) {
	t.Helper()
	cmd := d.Start()
	if cmd == nil {
		t.Fatal("Start returned nil")
	}
	for _, m := range flatten(cmd) {
		if r, ok := m.(tea.RawMsg); ok {
			raws = append(raws, r.Msg.(string))
		}
	}
	return raws
}

// flatten runs cmd, expanding batches. Timeout ticks (at most
// ReplyTimeout) are dropped: tests send timeoutMsg themselves.
func flatten(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, flatten(c)...)
		}
		return out
	}
	if _, ok := msg.(timeoutMsg); ok {
		return nil
	}
	return []tea.Msg{msg}
}

func TestDetectorNoReplyFallsBackToEnv(t *testing.T) {
	d := NewDetector(ModeAuto, env(map[string]string{"TERM_PROGRAM": "WezTerm"}), nil)
	d.timeout = 0
	raws := startMsgs(t, d)
	if len(raws) != 1 || raws[0] != requestCellSize+"\x1b[c" {
		t.Fatalf("queries = %q", raws)
	}
	caps, changed, handled := d.Update(timeoutMsg{})
	if !changed || !handled || !d.Done() {
		t.Fatal("timeout should finish detection")
	}
	if caps.Protocol != Sixel || caps.CellW != DefaultCellW || caps.CellH != DefaultCellH || caps.Tmux {
		t.Fatalf("caps = %+v", caps)
	}
	// A late reply after the timeout changes nothing but the cell size.
	if _, changed, _ := d.Update(uv.PrimaryDeviceAttributesEvent{62, 4}); changed {
		t.Fatal("a late DA1 should be ignored")
	}
}

func TestDetectorNoReplyUnknownTerminal(t *testing.T) {
	d := NewDetector(ModeAuto, env(map[string]string{"TERM_PROGRAM": "Apple_Terminal"}), nil)
	d.timeout = 0
	startMsgs(t, d)
	caps, _, _ := d.Update(timeoutMsg{})
	if caps.Protocol != None || !caps.Chafa || !strings.Contains(caps.Reason, "Apple_Terminal") {
		t.Fatalf("caps = %+v", caps)
	}
}

func TestDetectorUsesRepliesOutsideTmux(t *testing.T) {
	d := NewDetector(ModeAuto, env(map[string]string{"TERM": "xterm-256color"}), nil)
	d.timeout = 0
	startMsgs(t, d)
	if _, changed, handled := d.Update(uv.CellSizeEvent{Width: 10, Height: 21}); changed || !handled {
		t.Fatal("cell size alone shouldn't finish")
	}
	caps, changed, _ := d.Update(uv.PrimaryDeviceAttributesEvent{62, 4, 22})
	if !changed || caps.Protocol != Sixel || caps.CellW != 10 || caps.CellH != 21 {
		t.Fatalf("caps = %+v", caps)
	}
	// The timeout firing afterwards is a no-op.
	if _, changed, _ := d.Update(timeoutMsg{}); changed {
		t.Fatal("timeout after DA1 changed caps")
	}
	// A resize re-asks the cell size, and a new answer updates it.
	if d.Resized() == nil {
		t.Fatal("Resized should re-query outside tmux")
	}
	caps, changed, _ = d.Update(uv.CellSizeEvent{Width: 12, Height: 25})
	if !changed || caps.CellW != 12 || caps.CellH != 25 {
		t.Fatalf("after resize caps = %+v changed=%v", caps, changed)
	}
}

func TestDetectorDA1WithoutSixel(t *testing.T) {
	d := NewDetector(ModeAuto, env(map[string]string{"TERM": "xterm-256color"}), nil)
	startMsgs(t, d)
	caps, _, _ := d.Update(uv.PrimaryDeviceAttributesEvent{62, 22})
	if caps.Protocol != None {
		t.Fatalf("caps = %+v", caps)
	}
}

type fakeProbe struct {
	info  TmuxInfo
	err   error
	calls int
}

func (f *fakeProbe) Probe(context.Context) (TmuxInfo, error) {
	f.calls++
	return f.info, f.err
}

func tmuxEnv(extra map[string]string) func(string) string {
	m := map[string]string{"TMUX": "/tmp/tmux-501/default,1,0", "TERM": "tmux-256color", "TERM_PROGRAM": "tmux"}
	for k, v := range extra {
		m[k] = v
	}
	return env(m)
}

func TestDetectorTmuxPassthrough(t *testing.T) {
	tests := []struct {
		name string
		info TmuxInfo
		want Protocol
	}{
		{"kitty outside", TmuxInfo{TermName: "xterm-kitty", Passthrough: true, CellW: 9, CellH: 18}, Kitty},
		{"ghostty outside", TmuxInfo{TermName: "xterm-ghostty", Passthrough: true}, Kitty},
		{"wezterm by termtype", TmuxInfo{TermName: "xterm-256color", TermType: "WezTerm 20240203", Passthrough: true}, Sixel},
		{"wezterm by env", TmuxInfo{TermName: "xterm-256color", TermProgram: "WezTerm", Passthrough: true}, Sixel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &fakeProbe{info: tt.info}
			// Stale outer-terminal variables are ignored inside tmux.
			d := NewDetector(ModeAuto, tmuxEnv(map[string]string{"KITTY_WINDOW_ID": "3"}), p)
			if raws := startMsgs(t, d); len(raws) != 0 {
				t.Fatalf("no terminal queries inside tmux, got %q", raws)
			}
			msgs := flatten(d.Start())
			caps, changed, _ := d.Update(msgs[0])
			if !changed || caps.Protocol != tt.want || !caps.Tmux {
				t.Fatalf("caps = %+v", caps)
			}
			if tt.info.CellW > 0 && (caps.CellW != tt.info.CellW || caps.CellH != tt.info.CellH) {
				t.Fatalf("cell = %dx%d", caps.CellW, caps.CellH)
			}
			if got := caps.Wrap("\x1b_Gx\x1b\\"); !strings.HasPrefix(got, "\x1bPtmux;") {
				t.Fatalf("not wrapped: %q", got)
			}
		})
	}
}

func TestDetectorTmuxIgnoresReplies(t *testing.T) {
	p := &fakeProbe{info: TmuxInfo{TermName: "xterm-256color", Passthrough: true}}
	d := NewDetector(ModeAuto, tmuxEnv(nil), p)
	d.Start()
	if _, changed, handled := d.Update(uv.PrimaryDeviceAttributesEvent{62, 4}); changed || !handled {
		t.Fatal("tmux's DA1 must not decide")
	}
	if _, changed, _ := d.Update(uv.CellSizeEvent{Width: 99, Height: 99}); changed {
		t.Fatal("tmux's cell size must not be used")
	}
	caps, _, _ := d.Update(probedMsg{info: p.info})
	if caps.Protocol != None || caps.CellW != DefaultCellW {
		t.Fatalf("caps = %+v", caps)
	}
	if d.Resized() != nil {
		t.Fatal("no cell size query inside tmux")
	}
}

func TestDetectorTmuxPassthroughOff(t *testing.T) {
	d := NewDetector(ModeAuto, tmuxEnv(nil), nil)
	d.Start()
	caps, _, _ := d.Update(probedMsg{info: TmuxInfo{TermName: "xterm-kitty"}})
	if caps.Protocol != None || caps.Tmux || caps.Reason != PassthroughOff || !caps.Chafa {
		t.Fatalf("caps = %+v", caps)
	}
}

func TestDetectorTmuxProbeError(t *testing.T) {
	d := NewDetector(ModeAuto, tmuxEnv(nil), &fakeProbe{err: errors.New("no server")})
	msgs := flatten(d.Start())
	caps, _, _ := d.Update(msgs[0])
	if caps.Protocol != None || !strings.Contains(caps.Reason, "no server") {
		t.Fatalf("caps = %+v", caps)
	}
}

func TestDetectorForcedModesSkipQueries(t *testing.T) {
	for _, m := range []Mode{ModeText, ModeOff, ModeChafa} {
		d := NewDetector(m, env(map[string]string{"TERM": "xterm-kitty"}), nil)
		msgs := flatten(d.Start())
		if len(msgs) != 1 {
			t.Fatalf("%s: msgs = %v", m, msgs)
		}
		if _, ok := msgs[0].(readyMsg); !ok {
			t.Fatalf("%s: want readyMsg, got %T", m, msgs[0])
		}
		caps, changed, _ := d.Update(msgs[0])
		if !changed || caps.Protocol != None {
			t.Fatalf("%s: caps = %+v", m, caps)
		}
	}
}

func TestParseDisplay(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		want    TmuxInfo
		offsetY int
		wantErr bool
	}{
		{
			name:    "bottom status",
			out:     "xterm-kitty\tkitty(0.36.1)\t9\t18\t0\t0\ton\tbottom\ton\n",
			want:    TmuxInfo{TermName: "xterm-kitty", TermType: "kitty(0.36.1)", CellW: 9, CellH: 18, StatusRows: 1, Passthrough: true},
			offsetY: 0,
		},
		{
			name:    "top status, split pane",
			out:     "xterm-256color\tWezTerm 20240203\t\t\t81\t12\ton\ttop\toff\n",
			want:    TmuxInfo{TermName: "xterm-256color", TermType: "WezTerm 20240203", PaneLeft: 81, PaneTop: 12, StatusRows: 1, StatusTop: true},
			offsetY: 13,
		},
		{
			name:    "two-line status on top, passthrough all",
			out:     "xterm-ghostty\t\t10\t20\t0\t0\t2\ttop\tall",
			want:    TmuxInfo{TermName: "xterm-ghostty", CellW: 10, CellH: 20, StatusRows: 2, StatusTop: true, Passthrough: true},
			offsetY: 2,
		},
		{
			name: "status off",
			out:  "xterm\t\t0\t0\t0\t0\toff\ttop\t",
			want: TmuxInfo{TermName: "xterm", StatusTop: true},
		},
		{name: "garbage", out: "nope", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDisplay(tt.out)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if tt.wantErr {
				return
			}
			if got != tt.want {
				t.Fatalf("got %+v\nwant %+v", got, tt.want)
			}
			if got.OffsetY() != tt.offsetY {
				t.Fatalf("OffsetY = %d, want %d", got.OffsetY(), tt.offsetY)
			}
		})
	}
}

func TestFromTmuxOffsets(t *testing.T) {
	c := fromTmux(ModeAuto, TmuxInfo{TermType: "WezTerm 1", PaneLeft: 5, PaneTop: 2, StatusRows: 1, StatusTop: true, Passthrough: true})
	if c.OffsetX != 5 || c.OffsetY != 3 || c.Protocol != Sixel {
		t.Fatalf("caps = %+v", c)
	}
}

func TestExecTmuxProbe(t *testing.T) {
	var calls [][]string
	e := ExecTmux{Run: func(_ context.Context, bin string, args ...string) (string, error) {
		calls = append(calls, append([]string{bin}, args...))
		if args[0] == "display-message" {
			return "xterm-256color\t\t0\t0\t0\t0\ton\tbottom\ton\n", nil
		}
		return "TERM_PROGRAM=WezTerm\n", nil
	}}
	info, err := e.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.TermProgram != "WezTerm" || !info.Passthrough {
		t.Fatalf("info = %+v", info)
	}
	if len(calls) != 2 || calls[0][0] != "tmux" || calls[0][1] != "display-message" || calls[0][3] != tmuxFormat ||
		!slices.Equal(calls[1], []string{"tmux", "show-environment", "-g", "TERM_PROGRAM"}) {
		t.Fatalf("calls = %q", calls)
	}
	if got := parseEnvLine("-TERM_PROGRAM\n", "TERM_PROGRAM"); got != "" {
		t.Fatalf("unset var = %q", got)
	}
}
