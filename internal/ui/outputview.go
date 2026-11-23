package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// CommandRun is one finished background custom command (P6.3), as the
// output viewer shows it.
type CommandRun struct {
	Name    string
	Command string // the expanded command line
	Dir     string
	Started time.Time
	Took    time.Duration
	Exit    int
	Err     error // the command couldn't start or died of a signal
	Output  string
	// Truncated says the start of the output was dropped.
	Truncated bool
}

// Status is a short outcome: "exit 0", "exit 3" or the start error.
func (r CommandRun) Status() string {
	if r.Err != nil {
		return r.Err.Error()
	}
	return fmt.Sprintf("exit %d", r.Exit)
}

// OK reports whether the run started and exited 0.
func (r CommandRun) OK() bool { return r.Err == nil && r.Exit == 0 }

// OutputView is the full-screen viewer of a custom command's output.
type OutputView struct {
	theme  *Theme
	keys   *KeyMap
	run    CommandRun
	lines  []string
	width  int
	height int
	offset int
}

// outputHeader is the rows above the output: title, meta, blank.
const outputHeader = 3

// NewOutputView returns an empty viewer.
func NewOutputView(theme *Theme, keys *KeyMap) OutputView {
	return OutputView{theme: theme, keys: keys}
}

// SetRun shows run, scrolled to the end (where errors usually are).
func (o *OutputView) SetRun(r CommandRun) {
	o.run = r
	text := strings.TrimRight(strings.ReplaceAll(r.Output, "\r\n", "\n"), "\n")
	o.lines = nil
	if r.Truncated {
		o.lines = append(o.lines, o.theme.OutputMeta.Render("… (earlier output dropped)"))
	}
	if text == "" {
		o.lines = append(o.lines, o.theme.OutputMeta.Render("(no output)"))
	} else {
		for l := range strings.SplitSeq(text, "\n") {
			// Strip escape sequences: colours from tools would leak and
			// cursor movement would break the layout.
			o.lines = append(o.lines, strings.ReplaceAll(ansi.Strip(l), "\t", "    "))
		}
	}
	o.offset = len(o.lines)
	o.clamp()
}

// Run is the run shown.
func (o *OutputView) Run() CommandRun { return o.run }

// SetSize sets the viewer size (the whole screen).
func (o *OutputView) SetSize(width, height int) {
	o.width, o.height = max(width, 0), max(height, 0)
	o.clamp()
}

func (o *OutputView) bodyHeight() int { return max(o.height-outputHeader-1, 0) } // + footer

func (o *OutputView) clamp() {
	o.offset = min(max(o.offset, 0), max(len(o.lines)-o.bodyHeight(), 0))
}

// Update scrolls on list movement keys.
func (o *OutputView) Update(msg tea.KeyPressMsg) {
	l := o.keys.List
	half := max(o.bodyHeight()/2, 1)
	switch {
	case key.Matches(msg, l.Up):
		o.offset--
	case key.Matches(msg, l.Down):
		o.offset++
	case key.Matches(msg, l.PageUp):
		o.offset -= half
	case key.Matches(msg, l.PageDown):
		o.offset += half
	case key.Matches(msg, l.Top):
		o.offset = 0
	case key.Matches(msg, l.Bottom):
		o.offset = len(o.lines)
	}
	o.clamp()
}

// View renders the viewer at exactly its size.
func (o *OutputView) View() string {
	if o.width == 0 || o.height == 0 {
		return ""
	}
	t, r := o.theme, o.run
	status := t.OutputOK.Render(r.Status())
	if !r.OK() {
		status = t.OutputFail.Render(r.Status())
	}
	out := make([]string, 0, o.height)
	out = append(out,
		fit(t.Wordmark.Render("forge")+t.HelpTitle.Render(r.Name)+"  "+status, o.width),
		fit(" "+t.OutputMeta.Render(fmt.Sprintf("%s · %s · in %s · $ %s",
			r.Started.Format("15:04:05"), r.Took.Round(time.Millisecond), r.Dir, r.Command)), o.width),
		"",
	)
	body := o.bodyHeight()
	for i := range body {
		line := ""
		if j := o.offset + i; j < len(o.lines) {
			line = " " + t.OutputText.Render(o.lines[j])
		}
		out = append(out, fit(line, o.width))
	}
	out = append(out, fit(" "+HelpLine(t, o.keys.OutputHelp()), o.width))
	return Box(strings.Join(out[:o.height], "\n"), o.width, o.height)
}
