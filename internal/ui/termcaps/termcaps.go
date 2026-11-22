// Package termcaps works out which terminal graphics forge can use for
// inline previews (P4.7): the kitty graphics protocol, sixel or neither,
// plus the cell size in pixels so images scale to the pane.
//
// It is the only package that touches ultraviolet (the terminal replies
// Bubble Tea passes through untranslated); everything it hands out is a
// plain Go type.
package termcaps

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Protocol is a terminal graphics protocol.
type Protocol int

// Protocols, best first.
const (
	None  Protocol = iota // no graphics: chafa or text only
	Kitty                 // kitty graphics with Unicode placeholders (kitty, Ghostty)
	Sixel                 // sixel (WezTerm, foot, iTerm2, Konsole, Windows Terminal)
)

func (p Protocol) String() string {
	switch p {
	case Kitty:
		return "kitty"
	case Sixel:
		return "sixel"
	default:
		return "none"
	}
}

// Mode is the `artifacts.preview` setting.
type Mode string

// Preview modes. Auto detects; the others force a path.
const (
	ModeAuto  Mode = "auto"
	ModeKitty Mode = "kitty"
	ModeSixel Mode = "sixel"
	ModeChafa Mode = "chafa"
	ModeText  Mode = "text"
	ModeOff   Mode = "off"
)

// Modes lists the valid modes, for config validation.
var Modes = []Mode{ModeAuto, ModeKitty, ModeSixel, ModeChafa, ModeText, ModeOff}

// Default cell size when the terminal doesn't say: a common 8x16 font.
const (
	DefaultCellW = 8
	DefaultCellH = 16
)

// Caps is what the terminal can show.
type Caps struct {
	Protocol Protocol
	// CellW and CellH are one cell's size in pixels.
	CellW, CellH int
	// Tmux means graphics must go through tmux passthrough (see Wrap).
	Tmux bool
	// OffsetX and OffsetY are where forge's screen starts on the outer
	// terminal (a tmux pane's position); sixel placement adds them.
	OffsetX, OffsetY int
	// Chafa allows the chafa text fallback (false in text mode).
	Chafa bool
	// Off disables previews entirely.
	Off bool
	// Terminal names the terminal detected, "" when unknown.
	Terminal string
	// Reason says why there are no graphics, for the detail pane.
	Reason string
}

// Graphics reports whether images can be drawn as pixels.
func (c Caps) Graphics() bool { return c.Protocol != None && !c.Off }

// Wrap prepares a graphics escape sequence for the terminal: inside tmux
// it goes through passthrough, else unchanged.
func (c Caps) Wrap(seq string) string {
	if c.Tmux && seq != "" {
		return ansi.TmuxPassthrough(seq)
	}
	return seq
}

// withDefaults fills a missing cell size.
func (c Caps) withDefaults() Caps {
	if c.CellW <= 0 || c.CellH <= 0 {
		c.CellW, c.CellH = DefaultCellW, DefaultCellH
	}
	return c
}

// envTerm is what the environment says about the terminal.
type envTerm struct {
	name     string
	protocol Protocol
}

// fromEnv recognises the terminal from its environment variables.
func fromEnv(getenv func(string) string) envTerm {
	term, prog := getenv("TERM"), getenv("TERM_PROGRAM")
	switch {
	case term == "xterm-kitty" || getenv("KITTY_WINDOW_ID") != "":
		return envTerm{"kitty", Kitty}
	case strings.EqualFold(prog, "ghostty") || term == "xterm-ghostty" || getenv("GHOSTTY_RESOURCES_DIR") != "":
		return envTerm{"Ghostty", Kitty}
	case prog == "WezTerm" || getenv("WEZTERM_EXECUTABLE") != "":
		return envTerm{"WezTerm", Sixel}
	case prog == "iTerm.app":
		return envTerm{"iTerm2", Sixel}
	case strings.HasPrefix(term, "foot"):
		return envTerm{"foot", Sixel}
	case term == "mlterm":
		return envTerm{"mlterm", Sixel}
	case getenv("KONSOLE_VERSION") != "":
		return envTerm{"Konsole", Sixel}
	case getenv("WT_SESSION") != "":
		return envTerm{"Windows Terminal", Sixel}
	}
	name := prog
	if name == "" {
		name = term
	}
	return envTerm{name: name}
}

// FromEnv is the protocol and terminal name the environment alone
// suggests (outside tmux).
func FromEnv(getenv func(string) string) (Protocol, string) {
	e := fromEnv(getenv)
	return e.protocol, e.name
}

// InTmux reports whether forge runs inside tmux.
func InTmux(getenv func(string) string) bool { return getenv("TMUX") != "" }

// fromTermID recognises an outer terminal from tmux's client_termname
// (its $TERM) and client_termtype (its XTVERSION / DA2 answer).
func fromTermID(termName, termType, termProgram string) envTerm {
	tt := strings.ToLower(termType)
	switch {
	case termName == "xterm-kitty" || strings.HasPrefix(tt, "kitty"):
		return envTerm{"kitty", Kitty}
	case termName == "xterm-ghostty" || strings.HasPrefix(tt, "ghostty") || strings.EqualFold(termProgram, "ghostty"):
		return envTerm{"Ghostty", Kitty}
	case strings.HasPrefix(tt, "wezterm") || termProgram == "WezTerm":
		return envTerm{"WezTerm", Sixel}
	case strings.HasPrefix(tt, "iterm2") || termProgram == "iTerm.app":
		return envTerm{"iTerm2", Sixel}
	case strings.HasPrefix(termName, "foot") || strings.HasPrefix(tt, "foot"):
		return envTerm{"foot", Sixel}
	case strings.HasPrefix(tt, "konsole"):
		return envTerm{"Konsole", Sixel}
	}
	name := termProgram
	if name == "" {
		name = termName
	}
	return envTerm{name: name}
}

// resolve turns a detected terminal into Caps for mode.
func resolve(mode Mode, t envTerm, cellW, cellH int) Caps {
	c := Caps{Protocol: t.protocol, CellW: cellW, CellH: cellH, Chafa: true, Terminal: t.name}
	switch mode {
	case ModeKitty:
		c.Protocol = Kitty
	case ModeSixel:
		c.Protocol = Sixel
	case ModeChafa:
		c.Protocol = None
		c.Reason = "artifacts.preview is chafa"
	case ModeText:
		c.Protocol, c.Chafa = None, false
		c.Reason = "artifacts.preview is text"
	case ModeOff:
		c.Protocol, c.Chafa, c.Off = None, false, true
		c.Reason = "artifacts.preview is off"
	}
	if c.Protocol == None && c.Reason == "" {
		c.Reason = noGraphics(t.name)
	}
	return c.withDefaults()
}

func noGraphics(term string) string {
	if term == "" {
		return "no graphics support detected in this terminal"
	}
	return fmt.Sprintf("no graphics support in %s", term)
}

// ParseMode reads a config value; "" is auto.
func ParseMode(s string) (Mode, error) {
	if s == "" {
		return ModeAuto, nil
	}
	for _, m := range Modes {
		if string(m) == s {
			return m, nil
		}
	}
	return "", fmt.Errorf("unknown preview mode %q (want auto, kitty, sixel, chafa, text or off)", s)
}
