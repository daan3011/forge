package termcaps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// TmuxInfo is what tmux knows about the client forge is shown in. Inside
// tmux the terminal replies come from tmux itself, so this is how forge
// learns about the real (outer) terminal.
type TmuxInfo struct {
	TermName    string // client_termname: the outer $TERM
	TermType    string // client_termtype: the outer XTVERSION / DA2 answer
	TermProgram string // TERM_PROGRAM in tmux's global environment
	CellW       int    // client_cell_width (tmux 3.4+; 0 when unknown)
	CellH       int    // client_cell_height
	PaneLeft    int    // pane_left
	PaneTop     int    // pane_top
	StatusRows  int    // status line rows (0 when off)
	StatusTop   bool   // status-position is top
	Passthrough bool   // allow-passthrough is on (or all) for forge's pane
}

// OffsetY is the outer terminal row of the pane's first row.
func (i TmuxInfo) OffsetY() int {
	if i.StatusTop {
		return i.PaneTop + i.StatusRows
	}
	return i.PaneTop
}

// TmuxProbe asks the tmux server forge runs in about its client.
type TmuxProbe interface {
	Probe(ctx context.Context) (TmuxInfo, error)
}

// tmuxFormat is the display-message format ExecTmux reads; parseDisplay
// splits it.
const tmuxFormat = "#{client_termname}\t#{client_termtype}\t#{client_cell_width}\t#{client_cell_height}" +
	"\t#{pane_left}\t#{pane_top}\t#{status}\t#{status-position}\t#{allow-passthrough}"

// ExecTmux runs plain `tmux` (no -L), so it talks to the server named by
// $TMUX: the one forge is shown in. Argv only, never a shell.
type ExecTmux struct {
	// Run executes bin with args and returns stdout; exec when nil.
	Run func(ctx context.Context, bin string, args ...string) (string, error)
}

var _ TmuxProbe = ExecTmux{}

func (e ExecTmux) run(ctx context.Context, args ...string) (string, error) {
	if e.Run != nil {
		return e.Run(ctx, "tmux", args...)
	}
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, "tmux", args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return "", fmt.Errorf("tmux %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("tmux %s: %w", args[0], err)
	}
	return out.String(), nil
}

// Probe implements TmuxProbe.
func (e ExecTmux) Probe(ctx context.Context) (TmuxInfo, error) {
	out, err := e.run(ctx, "display-message", "-p", tmuxFormat)
	if err != nil {
		return TmuxInfo{}, err
	}
	info, err := parseDisplay(out)
	if err != nil {
		return TmuxInfo{}, err
	}
	// Best effort: the outer TERM_PROGRAM when tmux's server started from
	// that terminal.
	if env, err := e.run(ctx, "show-environment", "-g", "TERM_PROGRAM"); err == nil {
		info.TermProgram = parseEnvLine(env, "TERM_PROGRAM")
	}
	return info, nil
}

// parseDisplay reads tmuxFormat's output.
func parseDisplay(out string) (TmuxInfo, error) {
	f := strings.Split(strings.TrimRight(out, "\r\n"), "\t")
	if len(f) != 9 {
		return TmuxInfo{}, errors.New("tmux: unexpected display-message output")
	}
	atoi := func(s string) int {
		n, _ := strconv.Atoi(strings.TrimSpace(s))
		return max(n, 0)
	}
	info := TmuxInfo{
		TermName: f[0], TermType: f[1],
		CellW: atoi(f[2]), CellH: atoi(f[3]),
		PaneLeft: atoi(f[4]), PaneTop: atoi(f[5]),
		StatusTop:   f[7] == "top",
		Passthrough: f[8] == "on" || f[8] == "all" || f[8] == "1",
	}
	switch f[6] {
	case "on":
		info.StatusRows = 1
	case "off", "":
	default:
		info.StatusRows = atoi(f[6]) // "2" to "5"
	}
	return info, nil
}

// parseEnvLine reads "NAME=value" from show-environment ("-NAME" means
// unset).
func parseEnvLine(out, name string) string {
	line := strings.TrimSpace(out)
	if v, ok := strings.CutPrefix(line, name+"="); ok {
		return v
	}
	return ""
}

// PassthroughOff is the reason shown when tmux would swallow graphics.
const PassthroughOff = "tmux allow-passthrough is off — run `tmux set -g allow-passthrough on`"

// fromTmux turns a probe into Caps for mode.
func fromTmux(mode Mode, info TmuxInfo) Caps {
	c := resolve(mode, fromTermID(info.TermName, info.TermType, info.TermProgram), info.CellW, info.CellH)
	c.OffsetX, c.OffsetY = info.PaneLeft, info.OffsetY()
	if c.Protocol == None {
		return c
	}
	if !info.Passthrough {
		c.Protocol, c.Reason = None, PassthroughOff
		return c
	}
	c.Tmux = true
	return c
}
