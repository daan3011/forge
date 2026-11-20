package tmux

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// call is one recorded invocation.
type call struct {
	bin  string
	args []string
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

func (r *recorder) run(_ context.Context, bin string, args []string) (string, error) {
	r.calls = append(r.calls, call{bin, slices.Clone(args)})
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

func TestEveryCallUsesForgeSocket(t *testing.T) {
	ctx := context.Background()
	e, r := newRec(reply{sub: "has-session", stderr: "can't find session: p"})
	tg := Target{Session: "p", Window: "w"}
	_, _ = e.EnsureSession(ctx, "p")
	_, _ = e.NewWindow(ctx, "p", "w", "/tmp", []string{"bash"}, nil)
	_, _ = e.ListWindows(ctx, "p")
	_, _ = e.WindowAlive(ctx, tg)
	_ = e.KillWindow(ctx, tg)
	_ = e.SendKeys(ctx, tg, "hi", true)
	_, _ = e.CapturePane(ctx, tg, 5)
	_, _ = e.Attach(ctx, tg)
	_ = e.KillServer(ctx)
	if len(r.calls) == 0 {
		t.Fatal("no calls recorded")
	}
	for _, c := range r.calls {
		if c.bin != "tmux" || len(c.args) < 2 || c.args[0] != "-L" || c.args[1] != "forge" {
			t.Errorf("call without -L forge: %s %v", c.bin, c.args)
		}
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
		want := [][]string{{"-L", "forge", "has-session", "-t", "=my-proj:"}}
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
			"-L", "forge", "new-session", "-d", "-s", "proj", "-n", "_forge", ";",
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
		"-L", "forge", "new-window", "-d", "-t", "=proj:", "-n", "Fix-login", "-P", "-F", "#{window_id}",
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
		{"-n ; echo", false, []string{"-L", "forge", "send-keys", "-t", "=p:=w", "-l", "--", "-n ; echo"}},
		{"ls", true, []string{"-L", "forge", "send-keys", "-t", "=p:=w", "-l", "--", "ls", ";", "send-keys", "-t", "=p:=w", "Enter"}},
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
	want := []string{"-L", "forge", "capture-pane", "-p", "-J", "-t", "=p:=w", "-S", "-3"}
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

	t.Run("outside tmux returns exec cmd", func(t *testing.T) {
		t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
		t.Setenv("TMUX_PANE", "%1")
		r := &recorder{replies: []reply{list}}
		e := New(withRunner(r), WithGetenv(func(string) string { return "" }))
		cmd, err := e.Attach(ctx, tg)
		if err != nil || cmd == nil {
			t.Fatalf("= %v, %v", cmd, err)
		}
		want := []string{"tmux", "-L", "forge", "select-window", "-t", "=proj:=agent", ";", "attach-session", "-t", "=proj:"}
		if !reflect.DeepEqual(cmd.Args, want) {
			t.Errorf("args = %q", cmd.Args)
		}
		for _, kv := range cmd.Env {
			if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
				t.Errorf("env keeps %s", kv)
			}
		}
		if cmd.Stdin != nil || cmd.Stdout != nil {
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
		cmd, err := e.Attach(c, tg)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Run(); err != nil {
			t.Errorf("attach cmd killed by cancelled ctx: %v", err)
		}
	})

	t.Run("inside user's tmux still execs", func(t *testing.T) {
		r := &recorder{replies: []reply{list}}
		e := New(withRunner(r), WithGetenv(func(string) string { return "/tmp/tmux-501/default,1,0" }))
		cmd, err := e.Attach(ctx, tg)
		if err != nil || cmd == nil {
			t.Fatalf("= %v, %v", cmd, err)
		}
	})

	t.Run("on forge socket switches client", func(t *testing.T) {
		r := &recorder{replies: []reply{list}}
		e := New(withRunner(r), WithGetenv(func(string) string { return "/private/tmp/tmux-501/forge,9,0" }))
		cmd, err := e.Attach(ctx, tg)
		if err != nil || cmd != nil {
			t.Fatalf("= %v, %v; want nil cmd", cmd, err)
		}
		want := []string{"-L", "forge", "select-window", "-t", "=proj:=agent", ";", "switch-client", "-t", "=proj:"}
		if len(r.calls) != 2 || !reflect.DeepEqual(r.calls[1].args, want) {
			t.Errorf("calls = %q", r.argv())
		}
	})

	t.Run("missing window", func(t *testing.T) {
		e, _ := newRec()
		if _, err := e.Attach(ctx, Target{Session: "proj", Window: "nope"}); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestInvalidTargetsNeverRunTmux(t *testing.T) {
	ctx := context.Background()
	bad := Target{Session: "p", Window: "a:b"}
	e, r := newRec()
	_, _ = e.WindowAlive(ctx, bad)
	_ = e.KillWindow(ctx, bad)
	_ = e.SendKeys(ctx, bad, "x", true)
	_, _ = e.CapturePane(ctx, bad, 1)
	_, _ = e.Attach(ctx, bad)
	_, _ = e.ListWindows(ctx, "p.q")
	_, _ = e.NewWindow(ctx, "p q", "w", "", nil, nil)
	_, _ = e.NewWindow(ctx, "p", "w", "", nil, map[string]string{"": "x"})
	if len(r.calls) != 0 {
		t.Errorf("ran tmux for invalid input: %q", r.argv())
	}
}
