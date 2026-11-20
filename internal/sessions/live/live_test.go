package live

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sockPath is a socket path short enough for macOS's 104-byte limit
// (t.TempDir paths are not).
func sockPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "forge-live")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "forge.sock")
}

func listen(t *testing.T, path string) *Listener {
	t.Helper()
	l, err := Listen(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func recv(t *testing.T, l *Listener) Ping {
	t.Helper()
	select {
	case p := <-l.Pings():
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("no ping received")
		return Ping{}
	}
}

func TestSendWithoutListener(t *testing.T) {
	path := sockPath(t)
	if ok, err := Send(context.Background(), path, Ping{Event: "Stop"}); err != nil || ok {
		t.Errorf("no socket: %v, %v", ok, err)
	}
	// A stale socket file (nothing accepting) is not an error either.
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close()
	if ok, err := Send(context.Background(), path, Ping{Event: "Stop"}); err != nil || ok {
		t.Errorf("stale socket: %v, %v", ok, err)
	}
}

func TestSendReceive(t *testing.T) {
	path := sockPath(t)
	l := listen(t, path)
	want := Ping{Session: "fix", ClaudeSessionID: "id", Event: "PreToolUse", Status: "working", Changed: true}
	if ok, err := (Socket{Path: path}).Send(context.Background(), want); err != nil || !ok {
		t.Fatalf("Send = %v, %v; want delivered", ok, err)
	}
	want.V = Version
	if got := recv(t, l); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("socket mode %v, %v", fi.Mode(), err)
	}
}

func TestListenIgnoresJunk(t *testing.T) {
	path := sockPath(t)
	l := listen(t, path)
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write([]byte("garbage\n{\"v\":99,\"event\":\"x\"}\n{\"v\":1,\"event\":\"Stop\"}\n"))
	_ = conn.Close()
	if got := recv(t, l); got.Event != "Stop" {
		t.Errorf("got %+v", got)
	}
}

func TestListenReclaimsStaleSocket(t *testing.T) {
	path := sockPath(t)
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close() // a forge that died: the file stays
	l := listen(t, path)
	if _, err := Send(context.Background(), path, Ping{Event: "Stop"}); err != nil {
		t.Fatal(err)
	}
	recv(t, l)
}

func TestListenSecondInstance(t *testing.T) {
	path := sockPath(t)
	listen(t, path)
	if _, err := Listen(context.Background(), path); !errors.Is(err, ErrRunning) {
		t.Fatalf("second Listen: %v, want ErrRunning", err)
	}
}

func TestCloseRemovesSocket(t *testing.T) {
	path := sockPath(t)
	l, err := Listen(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket still there: %v", err)
	}
	if _, ok := <-l.Pings(); ok {
		t.Error("Pings not closed")
	}
	if err := l.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestFullBufferDrops(t *testing.T) {
	path := sockPath(t)
	l := listen(t, path)
	for range buffer + 10 {
		if _, err := Send(context.Background(), path, Ping{Event: "PreToolUse"}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.After(2 * time.Second)
	for len(l.Pings()) < buffer {
		select {
		case <-deadline:
			t.Fatalf("only %d pings queued", len(l.Pings()))
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := l.Close(); err != nil { // must not block on the full channel
		t.Fatal(err)
	}
}
