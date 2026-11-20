// Package tmuxtest provides an in-memory tmux.Driver for tests and a
// conformance suite that both the fake and the real driver must pass.
package tmuxtest

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"

	"github.com/daan3011/forge/internal/sessions/tmux"
)

// Fake is an in-memory tmux.Driver. Windows don't run anything: tests set
// their output with SetOutput and end them with Exit. Safe for concurrent
// use.
type Fake struct {
	// SwitchInPlace makes Attach behave as if forge ran inside a client of
	// its own tmux server: it records the switch and returns a nil command.
	SwitchInPlace bool
	// Popup makes Attach behave as if forge ran inside the user's own tmux:
	// it returns an AttachPopup whose command runs `true`, with Warning.
	Popup   bool
	Warning string
	// OnStart, if set, is called for every new window with its command; it
	// returns the window's output and whether its process exits at once
	// (e.g. a program that refuses its arguments).
	OnStart func(cmd []string) (output string, exited bool)

	mu       sync.Mutex
	sessions map[string][]*fakeWindow // in creation order
	nextID   int
	attached []tmux.Target
	watched  map[tmux.Target]bool
}

type fakeWindow struct {
	tmux.Window
	Dir    string
	Cmd    []string
	Env    map[string]string
	output string
	sent   []string
	pasted []string
}

var _ tmux.Driver = (*Fake)(nil)

// NewFake returns an empty fake server.
func NewFake() *Fake {
	return &Fake{sessions: map[string][]*fakeWindow{}}
}

func (f *Fake) find(t tmux.Target) (*fakeWindow, int) {
	for i, w := range f.sessions[t.Session] {
		if w.Name == t.Window {
			return w, i
		}
	}
	return nil, -1
}

func (f *Fake) mustFind(t tmux.Target) (*fakeWindow, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	w, _ := f.find(t)
	if w == nil {
		return nil, fmt.Errorf("%w: %s", tmux.ErrNotFound, t)
	}
	return w, nil
}

// EnsureSession implements tmux.Driver.
func (f *Fake) EnsureSession(_ context.Context, project string) (string, error) {
	name := tmux.SanitizeName(project)
	if name == "" {
		return "", fmt.Errorf("%w: project %q", tmux.ErrInvalidName, project)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sessions[name]; !ok {
		f.sessions[name] = nil
	}
	return name, nil
}

// NewWindow implements tmux.Driver.
func (f *Fake) NewWindow(_ context.Context, session, name, dir string, cmd []string, env map[string]string) (tmux.Target, error) {
	t := tmux.Target{Session: session, Window: tmux.SanitizeName(name)}
	if err := t.Validate(); err != nil {
		return tmux.Target{}, err
	}
	for k := range env {
		if k == "" || strings.ContainsAny(k, "=\x00") {
			return tmux.Target{}, fmt.Errorf("tmux: invalid environment variable name %q", k)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ws, ok := f.sessions[session]
	if !ok {
		return tmux.Target{}, fmt.Errorf("%w: session %s", tmux.ErrNotFound, session)
	}
	if w, _ := f.find(t); w != nil {
		return tmux.Target{}, fmt.Errorf("%w: %s", tmux.ErrWindowExists, t)
	}
	f.nextID++
	command := "bash"
	if len(cmd) > 0 {
		command = cmd[0]
	}
	w := &fakeWindow{
		Window: tmux.Window{ID: fmt.Sprintf("@%d", f.nextID), Index: len(ws) + 1, Name: t.Window, Command: command},
		Dir:    dir,
		Cmd:    slices.Clone(cmd),
		Env:    env,
	}
	if f.OnStart != nil {
		var exited bool
		w.output, exited = f.OnStart(slices.Clone(cmd))
		if exited {
			w.Dead, w.ExitStatus = true, 1
		}
	}
	f.sessions[session] = append(ws, w)
	return t, nil
}

// ListWindows implements tmux.Driver.
func (f *Fake) ListWindows(_ context.Context, session string) ([]tmux.Window, error) {
	if err := tmux.CheckName(session); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []tmux.Window
	for _, w := range f.sessions[session] {
		out = append(out, w.Window)
	}
	return out, nil
}

// WindowAlive implements tmux.Driver.
func (f *Fake) WindowAlive(_ context.Context, t tmux.Target) (bool, error) {
	if err := t.Validate(); err != nil {
		return false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	w, _ := f.find(t)
	return w != nil && !w.Dead, nil
}

// KillWindow implements tmux.Driver.
func (f *Fake) KillWindow(_ context.Context, t tmux.Target) error {
	if err := t.Validate(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, i := f.find(t); i >= 0 {
		f.sessions[t.Session] = slices.Delete(f.sessions[t.Session], i, i+1)
	}
	return nil
}

// WindowWatched implements tmux.Driver: true after SetWatched(t, true),
// while the window exists.
func (f *Fake) WindowWatched(_ context.Context, t tmux.Target) (bool, error) {
	if err := t.Validate(); err != nil {
		return false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	w, _ := f.find(t)
	return w != nil && f.watched[t], nil
}

// SetWatched marks the window as looked at (a client is attached to it) or
// not.
func (f *Fake) SetWatched(t tmux.Target, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.watched == nil {
		f.watched = map[tmux.Target]bool{}
	}
	f.watched[t] = on
}

// SendKeys implements tmux.Driver.
func (f *Fake) SendKeys(_ context.Context, t tmux.Target, text string, enter bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, err := f.mustFind(t)
	if err != nil {
		return err
	}
	if enter {
		text += "\n"
	}
	w.sent = append(w.sent, text)
	return nil
}

// PasteText implements tmux.Driver.
func (f *Fake) PasteText(_ context.Context, t tmux.Target, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, err := f.mustFind(t)
	if err != nil {
		return err
	}
	w.pasted = append(w.pasted, text)
	return nil
}

// Pasted returns every text PasteText pasted into t, in order (each was
// followed by Enter).
func (f *Fake) Pasted(t tmux.Target) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if w, _ := f.find(t); w != nil {
		return slices.Clone(w.pasted)
	}
	return nil
}

// CapturePane implements tmux.Driver.
func (f *Fake) CapturePane(_ context.Context, t tmux.Target, lines int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, err := f.mustFind(t)
	if err != nil {
		return "", err
	}
	return tmux.LastLines(w.output, lines), nil
}

// Attach implements tmux.Driver. Outside SwitchInPlace it returns a command
// running `true`, so it returns at once (an instant detach).
func (f *Fake) Attach(ctx context.Context, t tmux.Target) (tmux.Attachment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.mustFind(t); err != nil {
		return tmux.Attachment{}, err
	}
	f.attached = append(f.attached, t)
	switch {
	case f.SwitchInPlace:
		return tmux.Attachment{Mode: tmux.AttachSwitched}, nil
	case f.Popup:
		cmd := exec.CommandContext(context.WithoutCancel(ctx), "true")
		return tmux.Attachment{Mode: tmux.AttachPopup, Cmd: cmd, Warning: f.Warning}, nil
	}
	return tmux.Attachment{Mode: tmux.AttachExec, Cmd: exec.CommandContext(context.WithoutCancel(ctx), "true")}, nil
}

// ListAll implements tmux.Driver.
func (f *Fake) ListAll(context.Context) ([]tmux.Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, 0, len(f.sessions))
	for s := range f.sessions {
		names = append(names, s)
	}
	slices.Sort(names)
	var out []tmux.Target
	for _, s := range names {
		for _, w := range f.sessions[s] {
			out = append(out, tmux.Target{Session: s, Window: w.Name})
		}
	}
	return out, nil
}

// KillServer implements tmux.Driver: every session and window goes away.
func (f *Fake) KillServer(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = map[string][]*fakeWindow{}
	return nil
}

// SetOutput sets what CapturePane returns for t. Unknown targets are ignored.
func (f *Fake) SetOutput(t tmux.Target, s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if w, _ := f.find(t); w != nil {
		w.output = s
	}
}

// Exit marks t's process as exited; the window stays (remain-on-exit).
func (f *Fake) Exit(t tmux.Target, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if w, _ := f.find(t); w != nil {
		w.Dead, w.ExitStatus = true, status
	}
}

// Sent returns everything SendKeys typed into t; Enter shows as "\n".
func (f *Fake) Sent(t tmux.Target) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if w, _ := f.find(t); w != nil {
		return slices.Clone(w.sent)
	}
	return nil
}

// Started returns the dir, argv and env t was created with.
func (f *Fake) Started(t tmux.Target) (dir string, cmd []string, env map[string]string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, _ := f.find(t)
	if w == nil {
		return "", nil, nil, false
	}
	return w.Dir, slices.Clone(w.Cmd), w.Env, true
}

// Attached returns every target Attach was called for, in order.
func (f *Fake) Attached() []tmux.Target {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.attached)
}
