// Package customcmd expands and runs the user's custom commands (P6.3):
// selection variables are substituted shell-quoted, and the result runs with
// sh -c, either captured in the background or attached to the terminal.
package customcmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/daan3011/forge/internal/shell"
)

// Vars are the values a command can use, taken from the current selection.
// An empty field means the selection has no such value.
type Vars struct {
	Repo     string
	PR       string
	Branch   string
	Worktree string
	Issue    string
	Session  string
	Path     string
}

// Names are the variable names, as written in a command between braces.
var Names = []string{"repo", "pr", "branch", "worktree", "issue", "session", "path"}

// Get returns the value of the named variable and whether the name is one
// of Names.
func (v Vars) Get(name string) (string, bool) {
	switch name {
	case "repo":
		return v.Repo, true
	case "pr":
		return v.PR, true
	case "branch":
		return v.Branch, true
	case "worktree":
		return v.Worktree, true
	case "issue":
		return v.Issue, true
	case "session":
		return v.Session, true
	case "path":
		return v.Path, true
	}
	return "", false
}

// MissingError is returned by Expand when the command uses a variable the
// selection doesn't have.
type MissingError struct{ Var string }

func (e MissingError) Error() string {
	return fmt.Sprintf("needs {%s}, which the selection doesn't have", e.Var)
}

// Expand replaces every {name} of Names in tmpl with the shell-quoted value.
// Other braces are left alone: ${VAR} (a "{" right after "$") and shell
// brace expansion like {a,b}.
func Expand(tmpl string, v Vars) (string, error) {
	var b strings.Builder
	for i := 0; i < len(tmpl); {
		c := tmpl[i]
		if c != '{' || (i > 0 && tmpl[i-1] == '$') {
			b.WriteByte(c)
			i++
			continue
		}
		end := strings.IndexByte(tmpl[i:], '}')
		if end < 0 {
			b.WriteString(tmpl[i:])
			break
		}
		name := tmpl[i+1 : i+end]
		val, known := v.Get(name)
		if !known {
			b.WriteByte(c)
			i++
			continue
		}
		if val == "" {
			return "", MissingError{Var: name}
		}
		b.WriteString(shell.Quote(val))
		i += end + 1
	}
	return b.String(), nil
}

// Env returns the variables as FORGE_<NAME> environment entries, sorted;
// empty values are left out.
func Env(v Vars) []string {
	var env []string
	for _, n := range Names {
		if val, _ := v.Get(n); val != "" {
			env = append(env, "FORGE_"+strings.ToUpper(n)+"="+val)
		}
	}
	sort.Strings(env)
	return env
}

// MaxOutput caps the output Run keeps; beyond it the start is dropped.
const MaxOutput = 1 << 20

// Result is how a background run went.
type Result struct {
	// Output is stdout and stderr combined, at most MaxOutput bytes (the
	// tail); Truncated says whether anything was dropped.
	Output    []byte
	Truncated bool
	// Exit is the exit status; -1 when the command didn't run or was
	// killed by a signal.
	Exit int
	// Err is set when the command couldn't start (or died of a signal);
	// a non-zero exit alone is not an error.
	Err  error
	Took time.Duration
}

// Runner runs custom commands. Shell is the real one; tests fake it.
type Runner interface {
	// Run runs script with sh -c in dir and waits, capturing its output.
	Run(ctx context.Context, dir, script string, env []string) Result
	// Cmd builds the process for an interactive run (tea.ExecProcess);
	// its stdio is left unset.
	Cmd(dir, script string, env []string) *exec.Cmd
}

// Shell runs commands with /bin/sh.
type Shell struct{}

var _ Runner = Shell{}

// Cmd implements Runner.
func (Shell) Cmd(dir, script string, env []string) *exec.Cmd {
	return command(context.Background(), dir, script, env)
}

func command(ctx context.Context, dir, script string, env []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "sh", "-c", script) //nolint:gosec // the user's own command
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	return cmd
}

// Run implements Runner.
func (Shell) Run(ctx context.Context, dir, script string, env []string) Result {
	cmd := command(ctx, dir, script, env)
	out := &tailBuffer{max: MaxOutput}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Stdin = nil // /dev/null: nothing can prompt
	start := time.Now()
	err := cmd.Run()
	r := Result{Output: out.Bytes(), Truncated: out.dropped, Took: time.Since(start)}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr) && exitErr.ExitCode() >= 0:
		r.Exit = exitErr.ExitCode()
	default:
		r.Exit, r.Err = -1, err
	}
	return r
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	buf     bytes.Buffer
	max     int
	dropped bool
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) >= t.max {
		t.buf.Reset()
		p = p[len(p)-t.max:]
		t.dropped = true
	} else if over := t.buf.Len() + len(p) - t.max; over > 0 {
		t.buf.Next(over)
		t.dropped = true
	}
	t.buf.Write(p)
	return n, nil
}

func (t *tailBuffer) Bytes() []byte { return bytes.Clone(t.buf.Bytes()) }
