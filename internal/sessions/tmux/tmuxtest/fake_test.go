package tmuxtest

import (
	"context"
	"reflect"
	"testing"

	"github.com/daan3011/forge/internal/sessions/tmux"
)

func TestFakeConformance(t *testing.T) {
	RunConformance(t, NewFake(), []string{"sleep", "600"})
}

func TestFakeHelpers(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	s, _ := f.EnsureSession(ctx, "proj")
	tg, err := f.NewWindow(ctx, s, "agent", "/wt", []string{"claude"}, map[string]string{"K": "v"})
	if err != nil {
		t.Fatal(err)
	}

	dir, cmd, env, ok := f.Started(tg)
	if !ok || dir != "/wt" || !reflect.DeepEqual(cmd, []string{"claude"}) || env["K"] != "v" {
		t.Errorf("Started = %q %q %v %v", dir, cmd, env, ok)
	}

	f.SetOutput(tg, "one\ntwo\nthree\n\n")
	if got, _ := f.CapturePane(ctx, tg, 2); got != "two\nthree" {
		t.Errorf("CapturePane = %q", got)
	}

	_ = f.SendKeys(ctx, tg, "hi", true)
	_ = f.SendKeys(ctx, tg, "x", false)
	if got := f.Sent(tg); !reflect.DeepEqual(got, []string{"hi\n", "x"}) {
		t.Errorf("Sent = %q", got)
	}

	f.Exit(tg, 2)
	if alive, _ := f.WindowAlive(ctx, tg); alive {
		t.Error("alive after Exit")
	}
	ws, _ := f.ListWindows(ctx, s)
	if len(ws) != 1 || !ws[0].Dead || ws[0].ExitStatus != 2 {
		t.Errorf("ListWindows after Exit = %+v", ws)
	}

	a, err := f.Attach(ctx, tg)
	cmdRun := a.Cmd
	if err != nil || cmdRun == nil {
		t.Errorf("Attach = %v, %v", cmdRun, err)
	}
	f.SwitchInPlace = true
	if a, err := f.Attach(ctx, tg); err != nil || a.Cmd != nil || a.Mode != tmux.AttachSwitched {
		t.Errorf("Attach in place = %+v, %v", a, err)
	}
	if got := f.Attached(); !reflect.DeepEqual(got, []tmux.Target{tg, tg}) {
		t.Errorf("Attached = %v", got)
	}
}
