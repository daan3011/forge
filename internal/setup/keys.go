package setup

import (
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/sessions/tmux"
)

// KeyResult is how a test key press arrived.
type KeyResult int

// The results.
const (
	// KeyOK: the expected key with the expected modifiers.
	KeyOK KeyResult = iota
	// KeyComposed: the terminal turned Option+key into a character (∂ for
	// Option+d on a US Mac layout): Option is not acting as Meta/Alt.
	KeyComposed
	// KeyNoMod: the key arrived without its modifier.
	KeyNoMod
	// KeyOther: some other key.
	KeyOther
)

// TmuxKey is a key in tmux notation, parsed: "M-d" is Alt with code 'd'.
type TmuxKey struct {
	Mod  tea.KeyMod
	Code rune
	// Name is the tmux spelling of the key itself when it isn't a single
	// character ("F10", "Enter").
	Name string
}

// ParseTmuxKey parses tmux key notation (M- Alt, C- Ctrl, S- Shift).
func ParseTmuxKey(k string) TmuxKey {
	var out TmuxKey
	for len(k) > 2 && k[1] == '-' && strings.ContainsRune("MCS", rune(k[0])) {
		switch k[0] {
		case 'M':
			out.Mod |= tea.ModAlt
		case 'C':
			out.Mod |= tea.ModCtrl
		case 'S':
			out.Mod |= tea.ModShift
		}
		k = k[2:]
	}
	if r, n := utf8.DecodeRuneInString(k); n == len(k) {
		out.Code = r
	} else {
		out.Name = k
	}
	return out
}

// UsesAlt reports whether the tmux key needs Alt, i.e. whether Option-as-
// Meta matters on a Mac.
func UsesAlt(k string) bool { return ParseTmuxKey(k).Mod&tea.ModAlt != 0 }

// macOption maps what a US Mac layout types for Option+<key> back to the
// key. Dead keys (e, i, n, u, `) send nothing on their own and aren't here.
var macOption = map[rune]rune{
	'å': 'a', '∫': 'b', 'ç': 'c', '∂': 'd', 'ƒ': 'f', '©': 'g', '˙': 'h',
	'∆': 'j', '˚': 'k', '¬': 'l', 'µ': 'm', 'ø': 'o', 'π': 'p', 'œ': 'q',
	'®': 'r', 'ß': 's', '†': 't', '√': 'v', '∑': 'w', '≈': 'x', '¥': 'y',
	'Ω': 'z', '“': '[', '‘': ']', '¡': '1', '™': '2', '£': '3', '¢': '4',
}

// ClassifyKey tells whether msg is the expected tmux key (as tmux would
// see it from this terminal), and if not, how it went wrong.
func ClassifyKey(expected string, msg tea.KeyPressMsg) KeyResult {
	want := ParseTmuxKey(expected)
	k := msg.Key()
	mods := k.Mod &^ (tea.ModCapsLock | tea.ModNumLock | tea.ModScrollLock)
	code := unicode.ToLower(k.Code)
	if want.Name != "" {
		if strings.EqualFold(msg.String(), keyString(want)) {
			return KeyOK
		}
		return KeyOther
	}
	wantCode := unicode.ToLower(want.Code)
	switch {
	case code == wantCode && mods == want.Mod:
		return KeyOK
	case want.Mod&tea.ModAlt != 0 && composedFrom(k) == wantCode:
		return KeyComposed
	case code == wantCode && mods != want.Mod:
		return KeyNoMod
	}
	return KeyOther
}

// composedFrom returns the key a macOS Option character came from, or 0.
func composedFrom(k tea.Key) rune {
	r := k.Code
	if k.Text != "" {
		r, _ = utf8.DecodeRuneInString(k.Text)
	}
	return macOption[r]
}

// keyString spells a named key the way tea.Key.String does ("alt+f10").
func keyString(k TmuxKey) string {
	var parts []string
	if k.Mod&tea.ModCtrl != 0 {
		parts = append(parts, "ctrl")
	}
	if k.Mod&tea.ModAlt != 0 {
		parts = append(parts, "alt")
	}
	if k.Mod&tea.ModShift != 0 {
		parts = append(parts, "shift")
	}
	return strings.Join(append(parts, strings.ToLower(k.Name)), "+")
}

// Terminal is a terminal emulator setup knows how to fix.
type Terminal int

// The terminals.
const (
	TermUnknown Terminal = iota
	TermITerm2
	TermAppleTerminal
	TermGhostty
	TermWezTerm
	TermKitty
)

func (t Terminal) String() string {
	return [...]string{"your terminal", "iTerm2", "Terminal.app", "Ghostty", "WezTerm", "Kitty"}[t]
}

// DetectTerminal guesses the terminal from the environment. Inside tmux
// TERM_PROGRAM says "tmux", so the variables terminals set on their own
// (and tmux passes on) are checked too.
func DetectTerminal(getenv func(string) string) Terminal {
	switch getenv("TERM_PROGRAM") {
	case "iTerm.app":
		return TermITerm2
	case "Apple_Terminal":
		return TermAppleTerminal
	case "ghostty":
		return TermGhostty
	case "WezTerm":
		return TermWezTerm
	}
	switch {
	case getenv("TERM") == "xterm-kitty" || getenv("KITTY_WINDOW_ID") != "":
		return TermKitty
	case getenv("TERM") == "xterm-ghostty" || getenv("GHOSTTY_RESOURCES_DIR") != "":
		return TermGhostty
	case getenv("WEZTERM_EXECUTABLE") != "" || getenv("WEZTERM_PANE") != "":
		return TermWezTerm
	case getenv("LC_TERMINAL") == "iTerm2" || getenv("ITERM_SESSION_ID") != "":
		return TermITerm2
	}
	return TermUnknown
}

// OptionAsMetaHelp explains how to make Option send Alt in t, with exact
// menu paths or config lines.
func OptionAsMetaHelp(t Terminal) []string {
	switch t {
	case TermITerm2:
		return []string{
			"iTerm2 → Settings… → Profiles → Keys → General:",
			`set "Left Option key" to "Esc+" (and "Right Option key" too if you use that one).`,
		}
	case TermAppleTerminal:
		return []string{
			"Terminal → Settings… → Profiles → (your profile) → Keyboard:",
			`tick "Use Option as Meta key".`,
		}
	case TermGhostty:
		return []string{
			"Add this line to ~/.config/ghostty/config:",
			"  macos-option-as-alt = true",
			"then Ghostty → Reload Configuration (cmd+shift+,).",
		}
	case TermWezTerm:
		return []string{
			"Add these lines to ~/.wezterm.lua (before `return config`):",
			"  config.send_composed_key_when_left_alt_is_pressed = false",
			"  config.send_composed_key_when_right_alt_is_pressed = false",
			"WezTerm reloads its config on save.",
		}
	case TermKitty:
		return []string{
			"Add this line to ~/.config/kitty/kitty.conf:",
			"  macos_option_as_alt yes",
			"then reload the config (ctrl+cmd+,).",
		}
	}
	return []string{
		"Your terminal turns Option+key into a special character instead of sending Alt.",
		`Look for a setting called "Option as Meta", "Option as Alt" or "Esc+" in its preferences.`,
	}
}

// KeyAdvice is what the key test says about res for the expected key.
// Option-as-Meta help only appears for keys that use Alt.
func KeyAdvice(res KeyResult, expected string, t Terminal) []string {
	label := tmux.KeyLabel(expected)
	switch res {
	case KeyOK:
		return []string{label + " arrives as " + label + ": agent windows will take you back to forge."}
	case KeyComposed:
		return append([]string{
			label + " arrived as a character, not " + label + ": " + t.String() + " isn't sending Option as Alt (Meta).",
		}, OptionAsMetaHelp(t)...)
	case KeyNoMod:
		lines := []string{"The key arrived without its modifier, so tmux won't see " + label + "."}
		if UsesAlt(expected) {
			lines = append(lines, OptionAsMetaHelp(t)...)
		}
		return lines
	}
	return []string{"That was a different key. Press " + label + " (esc skips the test)."}
}
