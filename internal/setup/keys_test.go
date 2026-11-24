package setup

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func press(code rune, mod tea.KeyMod, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: mod, Text: text}
}

func TestParseTmuxKey(t *testing.T) {
	tests := []struct {
		in   string
		want TmuxKey
	}{
		{"M-d", TmuxKey{Mod: tea.ModAlt, Code: 'd'}},
		{"C-M-x", TmuxKey{Mod: tea.ModAlt | tea.ModCtrl, Code: 'x'}},
		{"F10", TmuxKey{Name: "F10"}},
		{"M-[", TmuxKey{Mod: tea.ModAlt, Code: '['}},
		{"q", TmuxKey{Code: 'q'}},
	}
	for _, tt := range tests {
		if got := ParseTmuxKey(tt.in); got != tt.want {
			t.Errorf("ParseTmuxKey(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
	if !UsesAlt("M-d") || UsesAlt("C-]") || UsesAlt("F10") {
		t.Error("UsesAlt")
	}
}

func TestClassifyKey(t *testing.T) {
	tests := []struct {
		name     string
		expected string
		msg      tea.KeyPressMsg
		want     KeyResult
	}{
		{"alt+d", "M-d", press('d', tea.ModAlt, ""), KeyOK},
		{"alt+d with caps lock", "M-d", press('d', tea.ModAlt|tea.ModCapsLock, ""), KeyOK},
		{"option composes ∂", "M-d", press('∂', 0, "∂"), KeyComposed},
		{"option composes via text only", "M-d", press(0, 0, "∂"), KeyComposed},
		{"plain d", "M-d", press('d', 0, "d"), KeyNoMod},
		{"other key", "M-d", press('x', tea.ModAlt, ""), KeyOther},
		{"enter", "M-d", press(tea.KeyEnter, 0, ""), KeyOther},
		{"configured alt+q", "M-q", press('q', tea.ModAlt, ""), KeyOK},
		{"configured alt+q composes œ", "M-q", press('œ', 0, "œ"), KeyComposed},
		{"alt+d when alt+q expected", "M-q", press('d', tea.ModAlt, ""), KeyOther},
		{"ctrl key", "C-]", press(']', tea.ModCtrl, ""), KeyOK},
		{"∂ for a ctrl key is just other", "C-d", press('∂', 0, "∂"), KeyOther},
		{"named key", "F10", press(tea.KeyF10, 0, ""), KeyOK},
		{"named key wrong", "F10", press(tea.KeyF9, 0, ""), KeyOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyKey(tt.expected, tt.msg); got != tt.want {
				t.Errorf("ClassifyKey(%q, %v) = %v, want %v", tt.expected, tt.msg, got, tt.want)
			}
		})
	}
}

func TestDetectTerminal(t *testing.T) {
	tests := []struct {
		env  map[string]string
		want Terminal
	}{
		{map[string]string{"TERM_PROGRAM": "iTerm.app"}, TermITerm2},
		{map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, TermAppleTerminal},
		{map[string]string{"TERM_PROGRAM": "ghostty"}, TermGhostty},
		{map[string]string{"TERM_PROGRAM": "WezTerm"}, TermWezTerm},
		{map[string]string{"TERM": "xterm-kitty"}, TermKitty},
		{map[string]string{"TERM_PROGRAM": "tmux", "KITTY_WINDOW_ID": "1"}, TermKitty},
		{map[string]string{"TERM_PROGRAM": "tmux", "LC_TERMINAL": "iTerm2"}, TermITerm2},
		{map[string]string{"TERM_PROGRAM": "tmux", "GHOSTTY_RESOURCES_DIR": "/x"}, TermGhostty},
		{map[string]string{"TERM_PROGRAM": "tmux", "WEZTERM_PANE": "0"}, TermWezTerm},
		{map[string]string{"TERM_PROGRAM": "vscode"}, TermUnknown},
	}
	for _, tt := range tests {
		if got := DetectTerminal(func(k string) string { return tt.env[k] }); got != tt.want {
			t.Errorf("DetectTerminal(%v) = %v, want %v", tt.env, got, tt.want)
		}
	}
}

func TestOptionAsMetaHelp(t *testing.T) {
	want := map[Terminal]string{
		TermITerm2:        "Left Option key",
		TermAppleTerminal: "Use Option as Meta key",
		TermGhostty:       "macos-option-as-alt = true",
		TermWezTerm:       "send_composed_key_when_left_alt_is_pressed = false",
		TermKitty:         "macos_option_as_alt yes",
		TermUnknown:       "Option as Meta",
	}
	for term, s := range want {
		if got := strings.Join(OptionAsMetaHelp(term), "\n"); !strings.Contains(got, s) {
			t.Errorf("%v help lacks %q:\n%s", term, s, got)
		}
	}
}

func TestKeyAdvice(t *testing.T) {
	ok := strings.Join(KeyAdvice(KeyOK, "M-d", TermITerm2), "\n")
	if strings.Contains(ok, "Option") || !strings.Contains(ok, "Alt+d") {
		t.Errorf("ok advice: %s", ok)
	}
	composed := strings.Join(KeyAdvice(KeyComposed, "M-d", TermGhostty), "\n")
	if !strings.Contains(composed, "Ghostty") || !strings.Contains(composed, "macos-option-as-alt") {
		t.Errorf("composed advice: %s", composed)
	}
	// A configured key without Alt never gets Option-as-Meta help.
	noAlt := strings.Join(KeyAdvice(KeyNoMod, "C-]", TermITerm2), "\n")
	if strings.Contains(noAlt, "Option") {
		t.Errorf("Option help for a Ctrl key: %s", noAlt)
	}
	other := strings.Join(KeyAdvice(KeyOther, "M-q", TermKitty), "\n")
	if !strings.Contains(other, "Press Alt+q") {
		t.Errorf("other advice: %s", other)
	}
}
