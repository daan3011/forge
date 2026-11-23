package app

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/customcmd"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/sessions/tmux"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// Custom commands (P6.3): config-defined shell commands run on the active
// tab's selection, from their key or the palette.

const (
	// customRunsKept is how many background runs "Command output…" lists.
	customRunsKept = 10
	// groupCustom is the palette group of custom commands.
	groupCustom = "Custom commands"
	// groupOutput is the palette group of the output picker.
	groupOutput = "Command output"
	// windowTimeout bounds opening a tmux window (not the attach).
	windowTimeout = 30 * time.Second
)

// customState is the root model's custom command state.
type customState struct {
	runner customcmd.Runner
	// exec runs an interactive process; tea.ExecProcess unless a test
	// replaces it.
	exec func(*exec.Cmd, tea.ExecCallback) tea.Cmd
	runs []ui.CommandRun // background runs, newest first
}

// WithCommandRunner sets what runs custom commands; the default is
// customcmd.Shell.
func WithCommandRunner(r customcmd.Runner) Option {
	return func(m *Model) { m.custom.runner = r }
}

// customDoneMsg is a finished background run.
type customDoneMsg struct{ run ui.CommandRun }

// customExecMsg is the end of an interactive (exec) run.
type customExecMsg struct {
	name string
	err  error
}

// customWindowMsg is the result of opening a tmux-window command.
type customWindowMsg struct {
	name string
	res  agent.WindowResult
	err  error
}

// customDetachedMsg is the end of looking at a command's tmux window.
type customDetachedMsg struct {
	name string
	err  error
}

// customKeys converts the config to the keymap's form.
func customKeys(cmds []config.CustomCommand) []ui.CustomKey {
	out := make([]ui.CustomKey, len(cmds))
	for i, c := range cmds {
		out[i] = ui.CustomKey{Index: i, Name: c.Name, Key: c.Key, Desc: customDetail(c), Tabs: c.Tabs}
	}
	return out
}

func customDetail(c config.CustomCommand) string {
	if c.Description != "" {
		return c.Description
	}
	return c.Command
}

// activeTitle is the active tab's title, or "".
func (m Model) activeTitle() string {
	if len(m.tabs) == 0 {
		return ""
	}
	return m.tabs[m.active].Title()
}

// customOnTab returns the custom commands offered on the active tab.
func (m Model) customOnTab() []config.CustomCommand {
	var out []config.CustomCommand
	title := m.activeTitle()
	for _, c := range m.cfg.CustomCommands {
		if c.OnTab(title) {
			out = append(out, c)
		}
	}
	return out
}

// customKey returns the custom command bound to msg on the active tab.
func (m Model) customKey(msg tea.KeyPressMsg) (config.CustomCommand, bool) {
	for _, b := range m.keys.CustomFor(m.activeTitle()) {
		if key.Matches(msg, b.Binding) {
			for _, c := range m.cfg.CustomCommands {
				if c.Name == b.Name {
					return c, true
				}
			}
		}
	}
	return config.CustomCommand{}, false
}

// selection is the active tab's selection, with Repo and Path filled from
// the project when the tab has none, and the project it belongs to.
func (m Model) selection() (tabs.Selection, *config.Project) {
	var sel tabs.Selection
	if len(m.tabs) > 0 {
		if s, ok := m.tabs[m.active].(tabs.Selector); ok {
			sel = s.Selection()
		}
	}
	p := m.project
	if sel.Repo != "" && (p == nil || p.Repo != sel.Repo) {
		p = nil
		for i := range m.cfg.Projects {
			if m.cfg.Projects[i].Repo == sel.Repo {
				p = &m.cfg.Projects[i]
				break
			}
		}
	}
	if p != nil {
		if sel.Repo == "" {
			sel.Repo = p.Repo
		}
		if sel.Path == "" {
			sel.Path = p.Path
		}
	}
	return sel, p
}

// runCustom runs c on the active tab's selection.
func (m Model) runCustom(c config.CustomCommand) (Model, tea.Cmd) {
	sel, p := m.selection()
	script, err := customcmd.Expand(c.Command, sel)
	if err != nil {
		return m, m.toast.Show(c.Name+": "+err.Error(), ui.ToastError)
	}
	dir := sel.Worktree
	if dir == "" && p != nil {
		dir = p.Path
	}
	env := customcmd.Env(sel)
	switch c.RunMode() {
	case config.ModeExec:
		name := c.Name
		return m, m.execFn()(m.runner().Cmd(dir, script, env), func(err error) tea.Msg {
			return customExecMsg{name: name, err: err}
		})
	case config.ModeTmuxWindow:
		return m.openCustomWindow(c, sel, p, dir, script, env)
	default:
		return m.startBackground(c.Name, dir, script, env)
	}
}

func (m Model) runner() customcmd.Runner {
	if m.custom.runner == nil {
		return customcmd.Shell{}
	}
	return m.custom.runner
}

func (m Model) execFn() func(*exec.Cmd, tea.ExecCallback) tea.Cmd {
	if m.custom.exec == nil {
		return tea.ExecProcess
	}
	return m.custom.exec
}

func (m Model) startBackground(name, dir, script string, env []string) (Model, tea.Cmd) {
	r, started := m.runner(), m.clock()
	run := func() tea.Msg {
		res := r.Run(context.Background(), dir, script, env)
		return customDoneMsg{run: ui.CommandRun{
			Name: name, Command: script, Dir: dir, Started: started, Took: res.Took,
			Exit: res.Exit, Err: res.Err, Output: string(res.Output), Truncated: res.Truncated,
		}}
	}
	return m, tea.Batch(m.toast.Show("running "+name+"…", ui.ToastInfo), run)
}

func (m Model) handleCustomDone(msg customDoneMsg) (Model, tea.Cmd) {
	r := msg.run
	m.custom.runs = append([]ui.CommandRun{r}, m.custom.runs...)
	if len(m.custom.runs) > customRunsKept {
		m.custom.runs = m.custom.runs[:customRunsKept]
	}
	if r.OK() {
		text := fmt.Sprintf("%s done (%s)", r.Name, roundTook(r.Took))
		return m, func() tea.Msg { return ui.SuccessMsg{Text: text} }
	}
	err := fmt.Errorf("%s %s · %s → Command output", r.Name, failure(r), m.keys.Global.Palette.Help().Key)
	return m, func() tea.Msg { return ui.ErrMsg{Err: err} }
}

// roundTook rounds a run's duration for a toast: "12ms", "1.3s".
func roundTook(d time.Duration) time.Duration {
	if d < time.Second {
		return d.Round(time.Millisecond)
	}
	return d.Round(100 * time.Millisecond)
}

// failure words a failed run for a toast: "exited 3" or the start error.
func failure(r ui.CommandRun) string {
	if r.Err != nil {
		return "failed: " + r.Err.Error()
	}
	return fmt.Sprintf("exited %d", r.Exit)
}

func (m Model) handleCustomExec(msg customExecMsg) (Model, tea.Cmd) {
	m, refresh := m.refresh()
	if msg.err == nil {
		return m, refresh
	}
	var exitErr *exec.ExitError
	text := msg.err.Error()
	if errors.As(msg.err, &exitErr) && exitErr.ExitCode() >= 0 {
		text = fmt.Sprintf("exited %d", exitErr.ExitCode())
	}
	err := fmt.Errorf("%s %s", msg.name, text)
	return m, tea.Batch(refresh, func() tea.Msg { return ui.ErrMsg{Err: err} })
}

// windowName names a command's tmux window after the command and the
// selection, so the same command on two worktrees gets two windows.
func windowName(name string, sel tabs.Selection) string {
	for _, v := range []string{sel.Session, sel.Branch, sel.PR, sel.Issue} {
		if v != "" {
			return "cmd-" + name + "-" + v
		}
	}
	return "cmd-" + name
}

func (m Model) openCustomWindow(c config.CustomCommand, sel tabs.Selection, p *config.Project, dir, script string, env []string) (Model, tea.Cmd) {
	svc := m.sessions
	if svc == nil {
		return m, m.toast.Show(c.Name+": tmux is unavailable", ui.ToastError)
	}
	o := agent.WindowOpts{Name: windowName(c.Name, sel), Dir: dir, Script: script, Env: map[string]string{}, Attach: c.Attaches()}
	if p != nil {
		o.Project = p.Name
	}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		o.Env[k] = v
	}
	name := c.Name
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), windowTimeout)
		defer cancel()
		res, err := svc.OpenWindow(ctx, o)
		return customWindowMsg{name: name, res: res, err: err}
	}
}

// handleCustomWindow shows the window the way the Sessions tab attaches: a
// switched client needs nothing more, a popup of the user's tmux runs while
// forge stays on screen, otherwise tmux attaches in this terminal.
func (m Model) handleCustomWindow(msg customWindowMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		return m, m.toast.Show(fmt.Sprintf("%s: %v", msg.name, msg.err), ui.ToastError)
	}
	a := msg.res.Attachment
	if a == nil {
		t := msg.res.Target
		return m, m.toast.Show(fmt.Sprintf("%s started in tmux window %s:%s", msg.name, t.Session, t.Window), ui.ToastInfo)
	}
	var cmds []tea.Cmd
	if a.Warning != "" {
		cmds = append(cmds, m.toast.Show(a.Warning, ui.ToastInfo))
	}
	name := msg.name
	done := func(err error) tea.Msg { return customDetachedMsg{name: name, err: err} }
	switch {
	case a.Mode == tmux.AttachSwitched || a.Cmd == nil:
	case a.Mode == tmux.AttachPopup:
		c := a.Cmd
		cmds = append(cmds, func() tea.Msg { return done(c.Run()) })
	default:
		cmds = append(cmds, m.execFn()(a.Cmd, done))
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleCustomDetached(msg customDetachedMsg) (Model, tea.Cmd) {
	if msg.err == nil {
		return m, nil
	}
	return m, m.toast.Show(fmt.Sprintf("%s: %v", msg.name, msg.err), ui.ToastError)
}

// customItems are the palette entries of the custom commands on the active
// tab, plus "Command output…" when any command is configured.
func (m Model) customItems() []item {
	var items []item
	for _, c := range m.customOnTab() {
		items = append(items, item{
			id: "custom:" + c.Name, title: c.Name, detail: customDetail(c), group: groupCustom,
			hint: c.Key, search: c.Name + " " + customDetail(c),
			run: func(m Model) (Model, tea.Cmd) { return m.runCustom(c) },
		})
	}
	if len(m.cfg.CustomCommands) > 0 {
		items = append(items, item{
			id: "custom_output", title: "Command output…", group: groupCustom,
			detail: "what background custom commands printed",
			run:    func(m Model) (Model, tea.Cmd) { return m.openOutputs() },
		})
	}
	return items
}

// openOutputs lists the recent background runs, newest first.
func (m Model) openOutputs() (Model, tea.Cmd) {
	if len(m.custom.runs) == 0 {
		return m, m.toast.Show("no command output yet", ui.ToastInfo)
	}
	m.helpOpen, m.outputOpen = false, false
	m.items = nil
	for i, r := range m.custom.runs {
		m.items = append(m.items, item{
			id: fmt.Sprintf("output:%d", i), title: r.Name, group: groupOutput,
			detail: r.Status() + " · " + r.Started.Format("15:04:05"),
			run:    func(m Model) (Model, tea.Cmd) { m.openOutput(r); return m, nil },
		})
	}
	m.overlay = overlayOutputs
	m.palette.Reset()
	m.rerank()
	m.startSlideIn()
	return m, nil
}

// openOutput shows one run's output full screen.
func (m *Model) openOutput(r ui.CommandRun) {
	m.output.SetRun(r)
	m.output.SetSize(m.width, m.height)
	m.helpOpen, m.outputOpen = false, true
}

func (m Model) handleOutputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Input.Cancel, m.keys.Global.Quit):
		m.outputOpen = false
	default:
		m.output.Update(msg)
	}
	return m, nil
}

// customHelp is the help overlay's sections: the custom command keys live
// on the active tab, then the output viewer's keys. Nothing when no
// command is configured.
func (m Model) customHelp() []ui.HelpSection {
	if len(m.cfg.CustomCommands) == 0 {
		return nil
	}
	var out []ui.HelpSection
	var bs []key.Binding
	for _, b := range m.keys.CustomFor(m.activeTitle()) {
		bs = append(bs, b.Binding)
	}
	if len(bs) > 0 {
		out = append(out, ui.HelpSection{Title: "Custom commands", Groups: [][]key.Binding{bs}})
	}
	return append(out, ui.HelpSection{Title: "Command output", Groups: [][]key.Binding{m.keys.OutputHelp()}})
}
