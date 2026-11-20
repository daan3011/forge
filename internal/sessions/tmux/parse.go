package tmux

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// windowFormat is the list-windows -F format parseWindows reads.
const windowFormat = "#{window_id}\t#{window_index}\t#{window_name}\t#{pane_dead}\t#{pane_dead_status}\t#{pane_current_command}"

// parseWindows reads list-windows output in windowFormat, dropping the
// placeholder window.
func parseWindows(out string) ([]Window, error) {
	var ws []Window
	for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "\t", 6)
		if len(f) != 6 {
			return nil, fmt.Errorf("tmux: list-windows: unexpected line %q", line)
		}
		idx, err := strconv.Atoi(f[1])
		if err != nil {
			return nil, fmt.Errorf("tmux: list-windows: bad index in %q: %w", line, err)
		}
		w := Window{ID: f[0], Index: idx, Name: f[2], Dead: f[3] == "1", Command: f[5]}
		if f[4] != "" {
			if w.ExitStatus, err = strconv.Atoi(f[4]); err != nil {
				return nil, fmt.Errorf("tmux: list-windows: bad exit status in %q: %w", line, err)
			}
		}
		if w.Name == PlaceholderWindow {
			continue
		}
		ws = append(ws, w)
	}
	return ws, nil
}

// allWindowsFormat is the list-windows -a -F format parseTargets reads.
const allWindowsFormat = "#{session_name}\t#{window_name}"

// parseTargets reads list-windows -a output in allWindowsFormat, dropping
// placeholder windows.
func parseTargets(out string) ([]Target, error) {
	var ts []Target
	for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		sess, win, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("tmux: list-windows -a: unexpected line %q", line)
		}
		if win == PlaceholderWindow {
			continue
		}
		ts = append(ts, Target{Session: sess, Window: win})
	}
	return ts, nil
}

// Version is a tmux major.minor version.
type Version struct{ Major, Minor int }

// AtLeast reports whether v >= major.minor.
func (v Version) AtLeast(major, minor int) bool {
	return v.Major > major || v.Major == major && v.Minor >= minor
}

func (v Version) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

var versionNumRe = regexp.MustCompile(`(\d+)\.(\d+)`)

// parseVersion reads `tmux -V` output ("tmux 3.8", "tmux 3.2a",
// "tmux next-3.6"); ok is false when it holds no N.N.
func parseVersion(out string) (v Version, ok bool) {
	m := versionNumRe.FindStringSubmatch(out)
	if m == nil {
		return Version{}, false
	}
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	return v, true
}

// onSocket reports whether $TMUX (value "socketpath,pid,session") names a
// server on the given socket name.
func onSocket(tmuxEnv, socket string) bool {
	if tmuxEnv == "" {
		return false
	}
	path, _, _ := strings.Cut(tmuxEnv, ",")
	return path != "" && filepath.Base(path) == socket
}

// Error is a failed tmux invocation.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("tmux %s: %s", strings.Join(e.Args, " "), msg)
}

func (e *Error) Unwrap() error { return e.Err }

// Is makes errors.Is(err, ErrNotFound) true for missing sessions, windows
// and servers.
func (e *Error) Is(target error) bool {
	return target == ErrNotFound && notFound(e.Stderr)
}

func notFound(stderr string) bool {
	for _, s := range []string{"can't find", "no server running", "error connecting to"} {
		if strings.Contains(strings.ToLower(stderr), s) {
			return true
		}
	}
	return false
}

func isDuplicateSession(err error) bool {
	var te *Error
	return errors.As(err, &te) && strings.Contains(te.Stderr, "duplicate session")
}
