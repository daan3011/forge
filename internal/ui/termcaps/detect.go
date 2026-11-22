package termcaps

import (
	"context"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// ReplyTimeout is how long the detector waits for the terminal to answer
// before going with the environment alone.
const ReplyTimeout = 150 * time.Millisecond

// probeTimeout bounds the tmux queries.
const probeTimeout = 2 * time.Second

// requestCellSize asks for the cell size in pixels (XTWINOPS 16); the
// reply is CSI 6 ; height ; width t.
const requestCellSize = "\x1b[16t"

// sixelAttr is the DA1 attribute that means sixel graphics.
const sixelAttr = 4

// Detector works out Caps once at startup. Outside tmux it asks the
// terminal for its cell size and device attributes (DA1, which every
// terminal answers, so it also marks the end of the replies) and waits
// up to ReplyTimeout. Inside tmux those replies come from tmux, so it asks
// tmux about its client instead.
type Detector struct {
	mode    Mode
	getenv  func(string) string
	probe   TmuxProbe
	timeout time.Duration

	tmux  bool
	done  bool
	cellW int
	cellH int
	caps  Caps
}

// NewDetector returns a detector for mode (see ParseMode). getenv reads
// the environment; probe is used inside tmux (nil: no graphics in tmux).
func NewDetector(mode Mode, getenv func(string) string, probe TmuxProbe) *Detector {
	if mode == "" {
		mode = ModeAuto
	}
	return &Detector{mode: mode, getenv: getenv, probe: probe, timeout: ReplyTimeout}
}

// timeoutMsg ends the wait for replies.
type timeoutMsg struct{}

// probedMsg is the tmux probe's answer.
type probedMsg struct {
	info TmuxInfo
	err  error
}

// readyMsg finishes at once (modes that need no detection).
type readyMsg struct{}

// Start sends the queries. Every message it leads to goes through Update.
func (d *Detector) Start() tea.Cmd {
	d.tmux = InTmux(d.getenv)
	switch {
	case d.mode == ModeText || d.mode == ModeOff || d.mode == ModeChafa:
		return func() tea.Msg { return readyMsg{} }
	case d.tmux:
		probe := d.probe
		return func() tea.Msg {
			if probe == nil {
				return probedMsg{err: errNoProbe}
			}
			ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
			defer cancel()
			info, err := probe.Probe(ctx)
			return probedMsg{info: info, err: err}
		}
	}
	// One write, so the cell size query is answered before DA1.
	return tea.Batch(
		tea.Raw(requestCellSize+ansi.RequestPrimaryDeviceAttributes),
		tea.Tick(d.timeout, func(time.Time) tea.Msg { return timeoutMsg{} }),
	)
}

// Resized asks for the cell size again (the font size may have changed
// with the window). Only outside tmux and once detection is done.
func (d *Detector) Resized() tea.Cmd {
	if !d.done || d.tmux || d.mode == ModeText || d.mode == ModeOff || d.mode == ModeChafa {
		return nil
	}
	return tea.Raw(requestCellSize)
}

// Caps returns the result so far (the zero Caps until detection is done).
func (d *Detector) Caps() Caps { return d.caps }

// Done reports whether detection has finished.
func (d *Detector) Done() bool { return d.done }

// Update takes a message. handled means it was the detector's; changed
// means Caps has a new value (the first result, or a new cell size).
func (d *Detector) Update(msg tea.Msg) (caps Caps, changed, handled bool) {
	switch msg := msg.(type) {
	case readyMsg:
		return d.finish(resolve(d.mode, fromEnv(d.getenv), 0, 0)), true, true
	case probedMsg:
		if d.done {
			return d.caps, false, true
		}
		if msg.err != nil {
			c := resolve(d.mode, envTerm{name: "tmux"}, 0, 0)
			c.Protocol = None
			c.Reason = "can't ask tmux about the terminal: " + msg.err.Error()
			return d.finish(c), true, true
		}
		return d.finish(fromTmux(d.mode, msg.info)), true, true
	case uv.CellSizeEvent:
		if d.tmux {
			return d.caps, false, true // tmux's own answer: not trusted
		}
		d.cellW, d.cellH = msg.Width, msg.Height
		if d.done && msg.Width > 0 && msg.Height > 0 && (msg.Width != d.caps.CellW || msg.Height != d.caps.CellH) {
			d.caps.CellW, d.caps.CellH = msg.Width, msg.Height
			return d.caps, true, true
		}
		return d.caps, false, true
	case uv.PrimaryDeviceAttributesEvent:
		if d.tmux || d.done {
			return d.caps, false, true
		}
		return d.finish(d.fromReplies(slices.Contains(msg, sixelAttr))), true, true
	case timeoutMsg:
		if d.done {
			return d.caps, false, true
		}
		return d.finish(d.fromReplies(false)), true, true
	}
	return d.caps, false, false
}

// fromReplies combines the environment with the terminal's replies.
func (d *Detector) fromReplies(sixel bool) Caps {
	t := fromEnv(d.getenv)
	if t.protocol == None && sixel {
		t.protocol = Sixel
	}
	return resolve(d.mode, t, d.cellW, d.cellH)
}

func (d *Detector) finish(c Caps) Caps {
	d.done, d.caps = true, c
	return c
}

type probeError string

func (e probeError) Error() string { return string(e) }

const errNoProbe = probeError("no tmux probe")
