// Package live carries "something changed" pings from `forge hook` to a
// running forge TUI over a unix socket (P1.4). The database is the source
// of truth; a ping only tells the TUI to show it now.
package live

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/daan3011/forge/internal/config"
)

// Version is the ping format version.
const Version = 1

// SendTimeout bounds a whole Send: a hook must never hang on a stuck TUI.
const SendTimeout = 250 * time.Millisecond

// readTimeout bounds reading one ping from a connection.
const readTimeout = time.Second

// buffer is how many pings may queue before new ones are dropped (the next
// list refresh catches up anyway).
const buffer = 64

// ErrRunning means another forge TUI is already listening on the socket.
var ErrRunning = errors.New("live: another forge is listening")

// Ping says a session's hook event was recorded.
type Ping struct {
	V               int    `json:"v"`
	Session         string `json:"session,omitempty"` // forge session name; "" if unknown
	ClaudeSessionID string `json:"claude_session_id,omitempty"`
	Event           string `json:"event"`
	Status          string `json:"status,omitempty"` // status after the event; "" if unknown
	Changed         bool   `json:"changed,omitempty"`
}

// DefaultPath is $XDG_DATA_HOME/forge/forge.sock, or
// ~/.local/share/forge/forge.sock.
func DefaultPath() string {
	return filepath.Join(config.DataHome(), "forge", "forge.sock")
}

// Sender delivers pings; `forge hook` takes one so tests can fake it.
type Sender interface {
	// Send reports whether a TUI received p; no TUI running is not an
	// error.
	Send(ctx context.Context, p Ping) (delivered bool, err error)
}

// Socket is the Sender for the socket at Path.
type Socket struct{ Path string }

// Send implements Sender. No TUI running (no socket, or a stale one) is
// false, nil.
func (s Socket) Send(ctx context.Context, p Ping) (bool, error) {
	return Send(ctx, s.Path, p)
}

// Send writes p to the TUI listening at path, giving up after SendTimeout,
// and reports whether it was delivered. It returns false, nil when nothing
// is listening.
func Send(ctx context.Context, path string, p Ping) (bool, error) {
	if p.V == 0 {
		p.V = Version
	}
	ctx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		if notListening(err) {
			return false, nil
		}
		return false, fmt.Errorf("live: %w", err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetWriteDeadline(dl)
	}
	b, err := json.Marshal(p)
	if err != nil {
		return false, err
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return false, fmt.Errorf("live: %w", err)
	}
	return true, nil
}

func notListening(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, syscall.ECONNREFUSED)
}

// Listener receives pings on a unix socket.
type Listener struct {
	path  string
	ln    net.Listener
	pings chan Ping
	done  chan struct{}
	wg    sync.WaitGroup
	once  sync.Once
}

// Listen starts listening at path. A socket file left behind by a forge
// that died is replaced; a live one returns ErrRunning. The socket is only
// accessible to the user. ctx only bounds the setup.
func Listen(ctx context.Context, path string) (*Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("live: %w", err)
	}
	if _, err := os.Lstat(path); err == nil {
		d := net.Dialer{Timeout: SendTimeout}
		conn, err := d.DialContext(ctx, "unix", path)
		if err == nil {
			_ = conn.Close()
			return nil, ErrRunning
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("live: remove stale socket: %w", err)
		}
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("live: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("live: %w", err)
	}
	l := &Listener{path: path, ln: ln, pings: make(chan Ping, buffer), done: make(chan struct{})}
	l.wg.Add(1)
	go l.accept()
	return l, nil
}

// Path is the socket path.
func (l *Listener) Path() string { return l.path }

// Pings delivers received pings. It is closed by Close.
func (l *Listener) Pings() <-chan Ping { return l.pings }

func (l *Listener) accept() {
	defer l.wg.Done()
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			select {
			case <-l.done:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		l.wg.Add(1)
		go l.read(conn)
	}
}

func (l *Listener) read(conn net.Conn) {
	defer l.wg.Done()
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 1024), 64<<10)
	for sc.Scan() {
		var p Ping
		if json.Unmarshal(sc.Bytes(), &p) != nil || p.V != Version {
			continue
		}
		select {
		case <-l.done:
			return
		case l.pings <- p:
		default: // full: drop; the next refresh shows it anyway
		}
	}
}

// Close stops listening, removes the socket file and closes Pings.
func (l *Listener) Close() error {
	var err error
	l.once.Do(func() {
		close(l.done)
		err = l.ln.Close()
		l.wg.Wait()
		close(l.pings)
		if rmErr := os.Remove(l.path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			err = errors.Join(err, rmErr)
		}
	})
	return err
}
