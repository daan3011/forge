package tmux

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/daan3011/forge/internal/shell"
)

// runner runs one tmux invocation (global flags included in args), feeding
// it stdin when non-empty, and returns its stdout.
type runner interface {
	run(ctx context.Context, bin string, args []string, stdin string) (string, error)
}

type execRunner struct{}

func (execRunner) run(ctx context.Context, bin string, args []string, stdin string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	if err := cmd.Run(); err != nil {
		return "", &Error{Args: args, Stderr: stderr.String(), Err: err}
	}
	return stdout.String(), nil
}

// Exec is the Driver backed by the tmux binary.
type Exec struct {
	bin    string
	socket string
	config string
	getenv func(string) string
	r      runner
}

var _ Driver = (*Exec)(nil)

// Option configures Exec.
type Option func(*Exec)

// WithSocket overrides the socket name (default "forge"). Tests use a
// throwaway name; never pass "default".
func WithSocket(name string) Option { return func(e *Exec) { e.socket = name } }

// WithConfig sets the config file passed as -f to every invocation; it is
// read by whichever call starts the server. Without it forge passes
// /dev/null, so tmux never falls back to ~/.tmux.conf or /etc/tmux.conf.
func WithConfig(path string) Option { return func(e *Exec) { e.config = path } }

// WithBinary overrides the tmux executable (default "tmux" on $PATH).
func WithBinary(path string) Option { return func(e *Exec) { e.bin = path } }

// WithGetenv overrides how $TMUX is read.
func WithGetenv(f func(string) string) Option { return func(e *Exec) { e.getenv = f } }

func withRunner(r runner) Option { return func(e *Exec) { e.r = r } }

// New returns an Exec driver on the forge socket.
func New(opts ...Option) *Exec {
	e := &Exec{bin: "tmux", socket: DefaultSocket, getenv: os.Getenv, r: execRunner{}}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Socket is the socket name every call uses.
func (e *Exec) Socket() string { return e.socket }

// Config is the config file forge's server is started with.
func (e *Exec) Config() string {
	if e.config == "" {
		return os.DevNull
	}
	return e.config
}

// base is the flags that start every invocation on forge's server: its own
// socket and its own config file, so the server never reads ~/.tmux.conf.
// Every command for forge's server is built from it.
func (e *Exec) base() []string {
	return []string{"-L", e.socket, "-f", e.Config()}
}

func (e *Exec) tmux(ctx context.Context, args ...string) (string, error) {
	return e.r.run(ctx, e.bin, append(e.base(), args...), "")
}

// tmuxIn is tmux with stdin.
func (e *Exec) tmuxIn(ctx context.Context, stdin string, args ...string) (string, error) {
	return e.r.run(ctx, e.bin, append(e.base(), args...), stdin)
}

// outer runs tmux against the server named by $TMUX: the user's own tmux,
// when forge runs inside it. It is the one exception to base(): only the
// popup path uses it, and the command shown in that popup is built from
// base() again.
func (e *Exec) outer(ctx context.Context, args ...string) (string, error) {
	return e.r.run(ctx, e.bin, args, "")
}

// Version is the tmux version (`tmux -V`). Output it can't parse counts as
// a new version.
func (e *Exec) Version(ctx context.Context) (Version, error) {
	out, err := e.tmux(ctx, "-V")
	if err != nil {
		return Version{}, err
	}
	if v, ok := parseVersion(out); ok {
		return v, nil
	}
	return Version{Major: 99}, nil
}

// ListAll implements Driver.
func (e *Exec) ListAll(ctx context.Context) ([]Target, error) {
	out, err := e.tmux(ctx, "list-windows", "-a", "-F", allWindowsFormat)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseTargets(out)
}

// KillServer implements Driver.
func (e *Exec) KillServer(ctx context.Context) error {
	if _, err := e.tmux(ctx, "kill-server"); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// EnsureSession implements Driver.
func (e *Exec) EnsureSession(ctx context.Context, project string) (string, error) {
	name := SanitizeName(project)
	if name == "" {
		return "", fmt.Errorf("%w: project %q", ErrInvalidName, project)
	}
	exists, err := e.hasSession(ctx, name)
	if err != nil || exists {
		return name, err
	}
	_, err = e.tmux(ctx,
		"new-session", "-d", "-s", name, "-n", PlaceholderWindow, ";",
		// forge's own server: keep exited panes readable and names stable.
		"set-option", "-gw", "remain-on-exit", "on", ";",
		"set-option", "-gw", "allow-rename", "off", ";",
		"set-option", "-gw", "automatic-rename", "off",
	)
	if err != nil && !isDuplicateSession(err) {
		return "", err
	}
	return name, nil
}

func (e *Exec) hasSession(ctx context.Context, name string) (bool, error) {
	_, err := e.tmux(ctx, "has-session", "-t", sessionTarget(name))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNotFound):
		return false, nil
	default:
		return false, err
	}
}

// NewWindow implements Driver.
func (e *Exec) NewWindow(ctx context.Context, session, name, dir string, cmd []string, env map[string]string) (Target, error) {
	t := Target{Session: session, Window: SanitizeName(name)}
	if err := t.Validate(); err != nil {
		return Target{}, err
	}
	args := []string{"new-window", "-d", "-t", sessionTarget(session), "-n", t.Window, "-P", "-F", "#{window_id}"}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	envArgs, err := envFlags(env)
	if err != nil {
		return Target{}, err
	}
	args = append(args, envArgs...)
	if len(cmd) > 0 {
		args = append(args, "--")
		args = append(args, cmd...)
	}

	ws, err := e.ListWindows(ctx, session)
	if err != nil {
		return Target{}, err
	}
	if slices.ContainsFunc(ws, func(w Window) bool { return w.Name == t.Window }) {
		return Target{}, fmt.Errorf("%w: %s", ErrWindowExists, t)
	}
	if _, err := e.tmux(ctx, args...); err != nil {
		return Target{}, err
	}
	return t, nil
}

// envFlags turns env into sorted -e KEY=VALUE flags.
func envFlags(env map[string]string) ([]string, error) {
	keys := make([]string, 0, len(env))
	for k := range env {
		if k == "" || strings.ContainsAny(k, "=\x00") {
			return nil, fmt.Errorf("tmux: invalid environment variable name %q", k)
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		out = append(out, "-e", k+"="+env[k])
	}
	return out, nil
}

// ListWindows implements Driver.
func (e *Exec) ListWindows(ctx context.Context, session string) ([]Window, error) {
	if err := CheckName(session); err != nil {
		return nil, err
	}
	out, err := e.tmux(ctx, "list-windows", "-t", sessionTarget(session), "-F", windowFormat)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseWindows(out)
}

func (e *Exec) window(ctx context.Context, t Target) (Window, bool, error) {
	if err := t.Validate(); err != nil {
		return Window{}, false, err
	}
	ws, err := e.ListWindows(ctx, t.Session)
	if err != nil {
		return Window{}, false, err
	}
	for _, w := range ws {
		if w.Name == t.Window {
			return w, true, nil
		}
	}
	return Window{}, false, nil
}

// WindowAlive implements Driver.
func (e *Exec) WindowAlive(ctx context.Context, t Target) (bool, error) {
	w, ok, err := e.window(ctx, t)
	return ok && !w.Dead, err
}

// KillWindow implements Driver.
func (e *Exec) KillWindow(ctx context.Context, t Target) error {
	if err := t.Validate(); err != nil {
		return err
	}
	if _, err := e.tmux(ctx, "kill-window", "-t", t.String()); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// SendKeys implements Driver.
func (e *Exec) SendKeys(ctx context.Context, t Target, text string, enter bool) error {
	if err := t.Validate(); err != nil {
		return err
	}
	args := []string{"send-keys", "-t", t.String(), "-l", "--", text}
	if enter {
		args = append(args, ";", "send-keys", "-t", t.String(), "Enter")
	}
	_, err := e.tmux(ctx, args...)
	return err
}

// PasteText implements Driver. The text goes through a uniquely named paste
// buffer (load-buffer from stdin, so it never appears in argv) and is pasted
// with -p so an app that asked for bracketed paste receives it as one paste;
// -d deletes the buffer afterwards.
func (e *Exec) PasteText(ctx context.Context, t Target, text string) error {
	if _, ok, err := e.window(ctx, t); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, t)
	}
	if text == "" {
		_, err := e.tmux(ctx, "send-keys", "-t", t.String(), "Enter")
		return err
	}
	buf, err := bufferName()
	if err != nil {
		return err
	}
	_, err = e.tmuxIn(ctx, text,
		"load-buffer", "-b", buf, "-", ";",
		"paste-buffer", "-p", "-d", "-b", buf, "-t", t.String(), ";",
		"send-keys", "-t", t.String(), "Enter")
	if err != nil {
		// A failed paste leaves the buffer behind; best-effort cleanup.
		_, _ = e.tmux(context.WithoutCancel(ctx), "delete-buffer", "-b", buf)
	}
	return err
}

// bufferName is a fresh paste buffer name.
func bufferName() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("tmux: paste buffer name: %w", err)
	}
	return "forge-paste-" + hex.EncodeToString(b[:]), nil
}

// CapturePane implements Driver.
func (e *Exec) CapturePane(ctx context.Context, t Target, lines int) (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	args := []string{"capture-pane", "-p", "-J", "-t", t.String()}
	if lines > 0 {
		args = append(args, "-S", "-"+strconv.Itoa(lines))
	}
	out, err := e.tmux(ctx, args...)
	if err != nil {
		return "", err
	}
	return LastLines(out, lines), nil
}

// popupMinVersion is the first tmux with display-popup.
var popupMinVersion = Version{Major: 3, Minor: 2}

// OuterMdWarning is Attachment.Warning when the outer tmux binds M-d itself.
const OuterMdWarning = "your ~/.tmux.conf binds M-d; your tmux will catch it before forge's popup. " +
	"Remove that binding (e.g. `unbind -n M-d`) and reload your tmux config."

// attachMode decides how to show a window: switch the client when forge runs
// inside its own server, a popup of the outer tmux when inside another one
// that supports it, otherwise exec an attach in the current terminal.
// popupOK is only called in the outer-tmux case.
func attachMode(tmuxEnv, socket string, popupOK func() bool) AttachMode {
	switch {
	case onSocket(tmuxEnv, socket):
		return AttachSwitched
	case tmuxEnv != "" && popupOK():
		return AttachPopup
	default:
		return AttachExec
	}
}

// Attach implements Driver.
func (e *Exec) Attach(ctx context.Context, t Target) (Attachment, error) {
	_, ok, err := e.window(ctx, t)
	if err != nil {
		return Attachment{}, err
	}
	if !ok {
		return Attachment{}, fmt.Errorf("%w: %s", ErrNotFound, t)
	}
	popupOK := func() bool {
		v, err := e.Version(ctx)
		return err == nil && v.AtLeast(popupMinVersion.Major, popupMinVersion.Minor)
	}
	switch attachMode(e.getenv("TMUX"), e.socket, popupOK) {
	case AttachSwitched:
		_, err := e.tmux(ctx, "select-window", "-t", t.String(), ";", "switch-client", "-t", sessionTarget(t.Session))
		return Attachment{Mode: AttachSwitched}, err
	case AttachPopup:
		return e.popup(ctx, t), nil
	default:
		return Attachment{Mode: AttachExec, Cmd: e.attachCmd(ctx, t)}, nil
	}
}

// attachArgs selects t and attaches to its session (after base()).
func (e *Exec) attachArgs(t Target) []string {
	return append(e.base(), "select-window", "-t", t.String(), ";", "attach-session", "-t", sessionTarget(t.Session))
}

// attachCmd attaches in the current terminal, for tea.ExecProcess.
func (e *Exec) attachCmd(ctx context.Context, t Target) *exec.Cmd {
	// ctx bounds the lookup, not the attach: it lasts until detach.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), e.bin, e.attachArgs(t)...)
	// From inside the user's own tmux (only on tmux < 3.2, else it's a popup)
	// attaching would be refused as nesting; forge's server is a different
	// one, so drop the markers.
	cmd.Env = withoutTmuxEnv(os.Environ())
	return cmd
}

// popup shows t in a display-popup of the outer tmux. display-popup blocks
// until the popup closes, so the caller runs Cmd in the background while
// forge stays on screen behind it.
func (e *Exec) popup(ctx context.Context, t Target) Attachment {
	bin := e.bin
	if p, err := exec.LookPath(bin); err == nil {
		bin = p
	}
	script := "env -u TMUX -u TMUX_PANE " + shell.Join(append([]string{bin}, e.attachArgs(t)...)...)
	// The popup belongs to the outer server, so this command is the one place
	// that keeps $TMUX and has no -L.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), e.bin, "display-popup", "-E", "-w", "90%", "-h", "90%", script)
	a := Attachment{Mode: AttachPopup, Cmd: cmd}
	if e.outerBindsMd(ctx) {
		a.Warning = OuterMdWarning
	}
	return a
}

// outerBindsMd reports whether the outer tmux binds M-d in its root table,
// which would catch the key before the popup's forge client sees it.
func (e *Exec) outerBindsMd(ctx context.Context) bool {
	out, err := e.outer(ctx, "list-keys", "-T", "root", "M-d")
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "M-d") {
			return true
		}
	}
	return false
}

func withoutTmuxEnv(env []string) []string {
	return slices.DeleteFunc(env, func(kv string) bool {
		return strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=")
	})
}
