package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// call is one recorded invocation.
type call struct {
	bin   string
	args  []string
	stdin string
}

// reply is a canned result for invocations whose args contain sub.
type reply struct {
	sub    string // matched against the space-joined args
	out    string
	stderr string // non-empty => failure
}

// recorder is a runner that records calls and answers from replies (first
// match wins; unmatched calls succeed with no output).
type recorder struct {
	calls   []call
	replies []reply
}

func (r *recorder) run(_ context.Context, bin string, args []string, stdin string) (string, error) {
	r.calls = append(r.calls, call{bin, slices.Clone(args), stdin})
	joined := strings.Join(args, " ")
	for _, rp := range r.replies {
		if strings.Contains(joined, rp.sub) {
			if rp.stderr != "" {
				return "", &Error{Args: args, Stderr: rp.stderr, Err: errors.New("exit status 1")}
			}
			return rp.out, nil
		}
	}
	return "", nil
}

func newRec(replies ...reply) (*Exec, *recorder) {
	r := &recorder{replies: replies}
	env := map[string]string{}
	return New(withRunner(r), WithGetenv(func(k string) string { return env[k] })), r
}

func (r *recorder) argv() [][]string {
	out := make([][]string, len(r.calls))
	for i, c := range r.calls {
		out[i] = c.args
	}
	return out
}

func TestEveryCallUsesForgeSocketAndConfig(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		opts []Option
		conf string
	}{
		{"configured", []Option{WithConfig("/home/me/.config/forge/tmux.conf")}, "/home/me/.config/forge/tmux.conf"},
		{"default never reads ~/.tmux.conf", nil, os.DevNull},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{replies: []reply{
				{sub: "has-session", stderr: "can't find session: p"},
				{sub: "list-windows -t", out: "@1\t2\tw\t0\t\tbash\n"},
			}}
			e := New(append(tc.opts, withRunner(r), WithGetenv(func(string) string { return "" }))...)
			tg := Target{Session: "p", Window: "w"}
			_, _ = e.EnsureSession(ctx, "p")
			_, _ = e.NewWindow(ctx, "p", "w2", "/tmp", []string{"bash"}, nil)
			_, _ = e.ListWindows(ctx, "p")
			_, _ = e.ListAll(ctx)
			_, _ = e.WindowAlive(ctx, tg)
			_ = e.KillWindow(ctx, tg)
			_ = e.SendKeys(ctx, tg, "hi", true)
			_ = e.PasteText(ctx, tg, "hi")
			_, _ = e.CapturePane(ctx, tg, 5)
			_, _ = e.Version(ctx)
			_ = e.KillServer(ctx)
			a, err := e.Attach(ctx, tg)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.calls) < 12 {
				t.Fatalf("only %d calls recorded", len(r.calls))
			}
			want := []string{"-L", "forge", "-f", tc.conf}
			for _, c := range r.calls {
				if c.bin != "tmux" || len(c.args) < 4 || !reflect.DeepEqual(c.args[:4], want) {
					t.Errorf("call without %q: %s %q", want, c.bin, c.args)
				}
			}
			if !reflect.DeepEqual(a.Cmd.Args[1:5], want) {
				t.Errorf("attach cmd = %q", a.Cmd.Args)
			}
		})
	}
}

func TestEnsureSession(t *testing.T) {
	ctx := context.Background()

	t.Run("exists", func(t *testing.T) {
		e, r := newRec()
		name, err := e.EnsureSession(ctx, "my proj")
		if err != nil || name != "my-proj" {
			t.Fatalf("= %q, %v", name, err)
		}
		want := [][]string{{"-L", "forge", "-f", "/dev/null", "has-session", "-t", "=my-proj:"}}
		if !reflect.DeepEqual(r.argv(), want) {
			t.Errorf("calls = %q", r.argv())
		}
	})

	t.Run("creates with options", func(t *testing.T) {
		e, r := newRec(reply{sub: "has-session", stderr: "no server running on /tmp/tmux-501/forge"})
		if _, err := e.EnsureSession(ctx, "proj"); err != nil {
			t.Fatal(err)
		}
		want := []string{
			"-L", "forge", "-f", "/dev/null", "new-session", "-d", "-s", "proj", "-n", "_forge", ";",
			"set-option", "-gw", "remain-on-exit", "on", ";",
			"set-option", "-gw", "allow-rename", "off", ";",
			"set-option", "-gw", "automatic-rename", "off",
		}
		if len(r.calls) != 2 || !reflect.DeepEqual(r.calls[1].args, want) {
			t.Errorf("calls = %q", r.argv())
		}
	})

	t.Run("race is fine", func(t *testing.T) {
		e, _ := newRec(
			reply{sub: "has-session", stderr: "can't find session: proj"},
			reply{sub: "new-session", stderr: "duplicate session: proj"},
		)
		if _, err := e.EnsureSession(ctx, "proj"); err != nil {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("other errors surface", func(t *testing.T) {
		e, _ := newRec(reply{sub: "has-session", stderr: "server exited unexpectedly"})
		if _, err := e.EnsureSession(ctx, "proj"); err == nil {
			t.Error("want error")
		}
	})

	t.Run("config flag", func(t *testing.T) {
		r := &recorder{}
		e := New(withRunner(r), WithConfig("/dev/null"), WithSocket("forge-test"))
		_, _ = e.EnsureSession(ctx, "p")
		if got := r.calls[0].args[:4]; !reflect.DeepEqual(got, []string{"-L", "forge-test", "-f", "/dev/null"}) {
			t.Errorf("global flags = %q", got)
		}
	})
}

func TestNewWindowArgs(t *testing.T) {
	ctx := context.Background()
	e, r := newRec()
	tg, err := e.NewWindow(ctx, "proj", "Fix: login", "/work/tree",
		[]string{"claude", "--session-id", "a b"}, map[string]string{"Z": "1", "A": "x=y"})
	if err != nil {
		t.Fatal(err)
	}
	if tg != (Target{Session: "proj", Window: "Fix-login"}) {
		t.Errorf("target = %+v", tg)
	}
	want := []string{
		"-L", "forge", "-f", "/dev/null", "new-window", "-d", "-t", "=proj:", "-n", "Fix-login", "-P", "-F", "#{window_id}",
		"-c", "/work/tree", "-e", "A=x=y", "-e", "Z=1", "--", "claude", "--session-id", "a b",
	}
	if len(r.calls) != 2 || !reflect.DeepEqual(r.calls[1].args, want) {
		t.Errorf("calls = %q", r.argv())
	}
}

func TestNewWindowDefaultShellAndExists(t *testing.T) {
	ctx := context.Background()
	e, r := newRec()
	if _, err := e.NewWindow(ctx, "proj", "w", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	last := r.calls[len(r.calls)-1].args
	if slices.Contains(last, "--") || slices.Contains(last, "-c") {
		t.Errorf("args = %q; want no -c and no command", last)
	}

	e, r = newRec(reply{sub: "list-windows", out: "@3\t2\tw\t0\t\tbash\n"})
	if _, err := e.NewWindow(ctx, "proj", "w", "", nil, nil); !errors.Is(err, ErrWindowExists) {
		t.Errorf("err = %v, want ErrWindowExists", err)
	}
	if len(r.calls) != 1 {
		t.Errorf("new-window ran despite existing window: %q", r.argv())
	}
}

func TestSendKeysArgs(t *testing.T) {
	ctx := context.Background()
	tg := Target{Session: "p", Window: "w"}
	tests := []struct {
		text  string
		enter bool
		want  []string
	}{
		{"-n ; echo", false, []string{"-L", "forge", "-f", "/dev/null", "send-keys", "-t", "=p:=w", "-l", "--", "-n ; echo"}},
		{"ls", true, []string{"-L", "forge", "-f", "/dev/null", "send-keys", "-t", "=p:=w", "-l", "--", "ls", ";", "send-keys", "-t", "=p:=w", "Enter"}},
	}
	for _, tt := range tests {
		e, r := newRec()
		if err := e.SendKeys(ctx, tg, tt.text, tt.enter); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(r.calls[0].args, tt.want) {
			t.Errorf("args = %q, want %q", r.calls[0].args, tt.want)
		}
	}
}

func TestCapturePane(t *testing.T) {
	ctx := context.Background()
	tg := Target{Session: "p", Window: "w"}
	e, r := newRec(reply{sub: "capture-pane", out: "old\n$ echo hi\nhi\n$\n\n\n"})
	got, err := e.CapturePane(ctx, tg, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got != "$ echo hi\nhi\n$" {
		t.Errorf("got %q", got)
	}
	want := []string{"-L", "forge", "-f", "/dev/null", "capture-pane", "-p", "-J", "-t", "=p:=w", "-S", "-3"}
	if !reflect.DeepEqual(r.calls[0].args, want) {
		t.Errorf("args = %q", r.calls[0].args)
	}

	e, r = newRec()
	_, _ = e.CapturePane(ctx, tg, 0)
	if slices.Contains(r.calls[0].args, "-S") {
		t.Errorf("visible-only capture has -S: %q", r.calls[0].args)
	}
}

func TestKillWindow(t *testing.T) {
	ctx := context.Background()
	tg := Target{Session: "p", Window: "w"}
	e, _ := newRec(reply{sub: "kill-window", stderr: "can't find window: w"})
	if err := e.KillWindow(ctx, tg); err != nil {
		t.Errorf("missing window: %v", err)
	}
	e, _ = newRec(reply{sub: "kill-window", stderr: "permission denied"})
	if err := e.KillWindow(ctx, tg); err == nil {
		t.Error("want error")
	}
}

func TestWindowAlive(t *testing.T) {
	ctx := context.Background()
	out := "@1\t2\tlive\t0\t\tbash\n@2\t3\tdead\t1\t0\tbash\n"
	e, _ := newRec(reply{sub: "list-windows", out: out})
	for name, want := range map[string]bool{"live": true, "dead": false, "absent": false} {
		got, err := e.WindowAlive(ctx, Target{Session: "p", Window: name})
		if err != nil || got != want {
			t.Errorf("WindowAlive(%s) = %v, %v; want %v", name, got, err, want)
		}
	}
	e, _ = newRec(reply{sub: "list-windows", stderr: "can't find session: p"})
	if got, err := e.WindowAlive(ctx, Target{Session: "p", Window: "live"}); err != nil || got {
		t.Errorf("missing session = %v, %v", got, err)
	}
}

func TestAttach(t *testing.T) {
	ctx := context.Background()
	tg := Target{Session: "proj", Window: "agent"}
	list := reply{sub: "list-windows", out: "@1\t2\tagent\t0\t\tclaude\n"}
	const (
		userTmux  = "/private/tmp/tmux-501/default,1,0"
		forgeTmux = "/private/tmp/tmux-501/forge,9,0"
	)
	newAt := func(tmuxEnv string, replies ...reply) (*Exec, *recorder) {
		r := &recorder{replies: append([]reply{list}, replies...)}
		return New(withRunner(r), WithConfig("/cfg dir/it's tmux.conf"),
			WithGetenv(func(string) string { return tmuxEnv })), r
	}
	noTmuxEnv := func(t *testing.T, env []string) {
		t.Helper()
		for _, kv := range env {
			if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
				t.Errorf("env keeps %s", kv)
			}
		}
	}

	t.Run("no TMUX execs attach", func(t *testing.T) {
		t.Setenv("TMUX", userTmux) // forge's own env; must not leak into the attach
		t.Setenv("TMUX_PANE", "%1")
		e, r := newAt("")
		a, err := e.Attach(ctx, tg)
		if err != nil || a.Mode != AttachExec || a.Cmd == nil {
			t.Fatalf("= %+v, %v", a, err)
		}
		want := []string{
			"tmux", "-L", "forge", "-f", "/cfg dir/it's tmux.conf",
			"select-window", "-t", "=proj:=agent", ";", "attach-session", "-t", "=proj:",
		}
		if !reflect.DeepEqual(a.Cmd.Args, want) {
			t.Errorf("args = %q", a.Cmd.Args)
		}
		noTmuxEnv(t, a.Cmd.Env)
		if a.Cmd.Stdin != nil || a.Cmd.Stdout != nil {
			t.Error("stdio should be left for tea.ExecProcess")
		}
		if len(r.calls) != 1 {
			t.Errorf("ran tmux beyond the window check: %q", r.argv())
		}
	})

	t.Run("cmd outlives a cancelled ctx", func(t *testing.T) {
		r := &recorder{replies: []reply{list}}
		e := New(withRunner(r), WithBinary("true"), WithGetenv(func(string) string { return "" }))
		c, cancel := context.WithCancel(ctx)
		a, err := e.Attach(c, tg)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Cmd.Run(); err != nil {
			t.Errorf("attach cmd killed by cancelled ctx: %v", err)
		}
	})

	t.Run("forge socket switches client", func(t *testing.T) {
		e, r := newAt(forgeTmux)
		a, err := e.Attach(ctx, tg)
		if err != nil || a.Mode != AttachSwitched || a.Cmd != nil {
			t.Fatalf("= %+v, %v; want switched", a, err)
		}
		want := []string{
			"-L", "forge", "-f", "/cfg dir/it's tmux.conf",
			"select-window", "-t", "=proj:=agent", ";", "switch-client", "-t", "=proj:",
		}
		if len(r.calls) != 2 || !reflect.DeepEqual(r.calls[1].args, want) {
			t.Errorf("calls = %q", r.argv())
		}
	})

	t.Run("other tmux opens a popup", func(t *testing.T) {
		e, r := newAt(userTmux, reply{sub: "-V", out: "tmux 3.8\n"}, reply{sub: "list-keys", stderr: "unknown key: M-d"})
		a, err := e.Attach(ctx, tg)
		if err != nil || a.Mode != AttachPopup || a.Cmd == nil {
			t.Fatalf("= %+v, %v; want popup", a, err)
		}
		args := a.Cmd.Args
		if slices.Contains(args, "-L") {
			t.Errorf("popup must go to the outer tmux, not -L: %q", args)
		}
		if want := []string{"tmux", "display-popup", "-E", "-w", "90%", "-h", "90%"}; !reflect.DeepEqual(args[:len(want)], want) {
			t.Errorf("args = %q", args)
		}
		script := args[len(args)-1]
		if !strings.HasPrefix(script, "env -u TMUX -u TMUX_PANE '") {
			t.Errorf("script = %q", script)
		}
		for _, sub := range []string{
			`'-L' 'forge' '-f' '/cfg dir/it'\''s tmux.conf'`,
			`'select-window' '-t' '=proj:=agent' ';' 'attach-session' '-t' '=proj:'`,
		} {
			if !strings.Contains(script, sub) {
				t.Errorf("script %q lacks %q", script, sub)
			}
		}
		if a.Cmd.Env != nil {
			t.Error("popup needs the inherited $TMUX to reach the outer server")
		}
		if a.Warning != "" {
			t.Errorf("warning = %q", a.Warning)
		}
		if got := r.calls[len(r.calls)-1].args; !reflect.DeepEqual(got, []string{"list-keys", "-T", "root", "M-d"}) {
			t.Errorf("outer check = %q", got)
		}
	})

	t.Run("popup script survives sh", func(t *testing.T) {
		e, _ := newAt(userTmux, reply{sub: "-V", out: "tmux 3.8"})
		a, err := e.Attach(ctx, tg)
		if err != nil {
			t.Fatal(err)
		}
		// Echo the words sh sees after "env -u TMUX -u TMUX_PANE".
		words := strings.TrimPrefix(a.Cmd.Args[len(a.Cmd.Args)-1], "env -u TMUX -u TMUX_PANE ")
		out, err := exec.CommandContext(ctx, "sh", "-c", `printf '%s\n' `+words).Output()
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
		want := []string{
			"-L", "forge", "-f", "/cfg dir/it's tmux.conf",
			"select-window", "-t", "=proj:=agent", ";", "attach-session", "-t", "=proj:",
		}
		if len(got) != len(want)+1 || !reflect.DeepEqual(got[1:], want) {
			t.Errorf("sh saw %q", got)
		}
	})

	t.Run("outer M-d binding warns", func(t *testing.T) {
		tests := []struct {
			name string
			keys reply
			want string
		}{
			{"bound", reply{sub: "list-keys", out: "bind-key -T root M-d detach-client\n"}, OuterMdWarning},
			{"success, empty output", reply{sub: "list-keys", out: ""}, ""},
			{"success, blank lines", reply{sub: "list-keys", out: "\n\n"}, ""},
			{"list-keys fails", reply{sub: "list-keys", stderr: "unknown key: M-d"}, ""},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				e, _ := newAt(userTmux, reply{sub: "-V", out: "tmux 3.4"}, tt.keys)
				a, err := e.Attach(ctx, tg)
				if err != nil || a.Mode != AttachPopup {
					t.Fatalf("= %+v, %v", a, err)
				}
				if a.Warning != tt.want {
					t.Errorf("warning = %q, want %q", a.Warning, tt.want)
				}
			})
		}
	})

	t.Run("M-d check only on the popup path", func(t *testing.T) {
		for _, env := range []string{"", forgeTmux} {
			e, r := newAt(env, reply{sub: "list-keys", out: "bind-key -T root M-d detach-client"})
			a, _ := e.Attach(ctx, tg)
			if a.Warning != "" {
				t.Errorf("TMUX=%q: warning %q", env, a.Warning)
			}
			for _, c := range r.calls {
				if slices.Contains(c.args, "list-keys") || slices.Contains(c.args, "-V") {
					t.Errorf("TMUX=%q ran %q", env, c.args)
				}
			}
		}
	})

	t.Run("old tmux nests with TMUX unset", func(t *testing.T) {
		e, _ := newAt(userTmux, reply{sub: "-V", out: "tmux 3.1c"})
		a, err := e.Attach(ctx, tg)
		if err != nil || a.Mode != AttachExec || a.Cmd == nil {
			t.Fatalf("= %+v, %v; want exec", a, err)
		}
		if !slices.Contains(a.Cmd.Args, "attach-session") || !slices.Contains(a.Cmd.Args, "-f") {
			t.Errorf("args = %q", a.Cmd.Args)
		}
		noTmuxEnv(t, a.Cmd.Env)
	})

	t.Run("missing window", func(t *testing.T) {
		e, _ := newRec()
		if _, err := e.Attach(ctx, Target{Session: "proj", Window: "nope"}); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestAttachMode(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		popupOK bool
		want    AttachMode
	}{
		{"no tmux", "", true, AttachExec},
		{"forge socket", "/tmp/tmux-501/forge,1,0", true, AttachSwitched},
		{"other socket", "/tmp/tmux-501/default,1,0", true, AttachPopup},
		{"other socket, old tmux", "/tmp/tmux-501/default,1,0", false, AttachExec},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asked := false
			got := attachMode(tt.env, "forge", func() bool { asked = true; return tt.popupOK })
			if got != tt.want {
				t.Errorf("= %v, want %v", got, tt.want)
			}
			if asked != (tt.want == AttachPopup || tt.name == "other socket, old tmux") {
				t.Errorf("popupOK asked = %v", asked)
			}
		})
	}
}

func TestInvalidTargetsNeverRunTmux(t *testing.T) {
	ctx := context.Background()
	bad := Target{Session: "p", Window: "a:b"}
	e, r := newRec()
	_, _ = e.WindowAlive(ctx, bad)
	_ = e.KillWindow(ctx, bad)
	_ = e.SendKeys(ctx, bad, "x", true)
	_ = e.PasteText(ctx, bad, "x")
	_, _ = e.CapturePane(ctx, bad, 1)
	_, _ = e.Attach(ctx, bad)
	_, _ = e.ListWindows(ctx, "p.q")
	_, _ = e.NewWindow(ctx, "p q", "w", "", nil, nil)
	_, _ = e.NewWindow(ctx, "p", "w", "", nil, map[string]string{"": "x"})
	if len(r.calls) != 0 {
		t.Errorf("ran tmux for invalid input: %q", r.argv())
	}
}

func TestPasteText(t *testing.T) {
	ctx := context.Background()
	tg := Target{Session: "p", Window: "w"}
	list := reply{sub: "list-windows", out: "@1\t2\tw\t0\t\tbash\n"}
	text := "line one\nsay \"hi\" $HOME `x` ; -n"

	e, r := newRec(list)
	if err := e.PasteText(ctx, tg, text); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("calls = %q", r.argv())
	}
	got := r.calls[1]
	if got.stdin != text {
		t.Errorf("stdin = %q, want %q", got.stdin, text)
	}
	if slices.Contains(got.args, text) {
		t.Errorf("text leaked into argv: %q", got.args)
	}
	buf := got.args[6]
	if !strings.HasPrefix(buf, "forge-paste-") {
		t.Fatalf("buffer name = %q", buf)
	}
	want := []string{
		"-L", "forge", "-f", "/dev/null", "load-buffer", "-b", buf, "-", ";",
		"paste-buffer", "-p", "-d", "-b", buf, "-t", "=p:=w", ";",
		"send-keys", "-t", "=p:=w", "Enter",
	}
	if !reflect.DeepEqual(got.args, want) {
		t.Errorf("args = %q\nwant  %q", got.args, want)
	}

	// Missing window: nothing is loaded.
	e, r = newRec(list)
	if err := e.PasteText(ctx, Target{Session: "p", Window: "gone"}, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if len(r.calls) != 1 {
		t.Errorf("calls = %q", r.argv())
	}

	// A failed paste deletes the buffer it loaded.
	e, r = newRec(list, reply{sub: "paste-buffer", stderr: "boom"})
	if err := e.PasteText(ctx, tg, "x"); err == nil {
		t.Fatal("want error")
	}
	last := r.calls[len(r.calls)-1].args
	if !slices.Contains(last, "delete-buffer") {
		t.Errorf("no cleanup, last call %q", last)
	}
}

func TestWindowWatched(t *testing.T) {
	ctx := context.Background()
	out := "agent\t1\t1\nother\t0\t1\n"
	e, r := newRec(reply{sub: "list-windows", out: out})
	for name, want := range map[string]bool{"agent": true, "other": false, "absent": false} {
		got, err := e.WindowWatched(ctx, Target{Session: "p", Window: name})
		if err != nil || got != want {
			t.Errorf("WindowWatched(%s) = %v, %v; want %v", name, got, err, want)
		}
	}
	if args := strings.Join(r.calls[0].args, " "); !strings.Contains(args, "list-windows -t =p: -F #{window_name}\t#{window_active}\t#{session_attached}") {
		t.Errorf("args = %q", args)
	}
	e, _ = newRec(reply{sub: "list-windows", out: "agent\t1\t0\n"})
	if got, err := e.WindowWatched(ctx, Target{Session: "p", Window: "agent"}); err != nil || got {
		t.Errorf("active window, no client = %v, %v; want false", got, err)
	}
	e, _ = newRec(reply{sub: "list-windows", stderr: "can't find session: p"})
	if got, err := e.WindowWatched(ctx, Target{Session: "p", Window: "agent"}); err != nil || got {
		t.Errorf("missing session = %v, %v", got, err)
	}
	if _, err := e.WindowWatched(ctx, Target{Session: "p", Window: "a:b"}); !errors.Is(err, ErrInvalidName) {
		t.Errorf("invalid target err = %v", err)
	}
}
