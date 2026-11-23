package customcmd

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestExpand(t *testing.T) {
	v := Vars{Repo: "o/r", PR: "12", Branch: "it's/feat", Worktree: "/wt/a b", Issue: "PROJ-3", Session: "fix", Path: "/p"}
	tests := []struct {
		name, tmpl, want string
		missing          string
	}{
		{"worktree", "code {worktree}", "code '/wt/a b'", ""},
		{"quote in value", "git log {branch}", `git log 'it'\''s/feat'`, ""},
		{"repeat", "{pr} {pr}", "'12' '12'", ""},
		{"all", "{repo}{pr}{branch}{worktree}{issue}{session}{path}", `'o/r''12''it'\''s/feat''/wt/a b''PROJ-3''fix''/p'`, ""},
		{"env var untouched", "echo ${HOME} $HOME ${repo}", "echo ${HOME} $HOME ${repo}", ""},
		{"brace expansion untouched", "echo {a,b} {} {nope}", "echo {a,b} {} {nope}", ""},
		{"unclosed", "echo {repo", "echo {repo", ""},
		{"no vars", "make test", "make test", ""},
		{"missing", "open {worktree}", "", "worktree"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := v
			if tt.missing != "" {
				in = Vars{}
			}
			got, err := Expand(tt.tmpl, in)
			if tt.missing != "" {
				var me MissingError
				if !errors.As(err, &me) || me.Var != tt.missing || !strings.Contains(err.Error(), "{"+tt.missing+"}") {
					t.Fatalf("err = %v, want missing %s", err, tt.missing)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("Expand = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestEnv(t *testing.T) {
	got := Env(Vars{Repo: "o/r", Worktree: "/wt"})
	if want := []string{"FORGE_REPO=o/r", "FORGE_WORKTREE=/wt"}; !slices.Equal(got, want) {
		t.Fatalf("Env = %v, want %v", got, want)
	}
}

func TestShellRun(t *testing.T) {
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)
	tests := []struct {
		name   string
		script string
		exit   int
		out    string
	}{
		{"ok in dir with env", "pwd; echo $FORGE_PR", 0, dir + "\n7\n"},
		{"exit code and stderr", "echo oops >&2; exit 3", 3, "oops\n"},
		{"no stdin", "cat; echo done", 0, "done\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Shell{}.Run(context.Background(), dir, tt.script, Env(Vars{PR: "7"}))
			if r.Err != nil || r.Exit != tt.exit || string(r.Output) != tt.out {
				t.Fatalf("Run = exit %d err %v out %q; want %d %q", r.Exit, r.Err, r.Output, tt.exit, tt.out)
			}
		})
	}
}

func TestShellRunBadDir(t *testing.T) {
	r := Shell{}.Run(context.Background(), filepath.Join(t.TempDir(), "gone"), "true", nil)
	if r.Err == nil || r.Exit != -1 {
		t.Fatalf("Run = %+v, want an error", r)
	}
}

func TestShellCmd(t *testing.T) {
	c := Shell{}.Cmd("/tmp", "echo hi", []string{"FORGE_PR=1"})
	if c.Dir != "/tmp" || !slices.Equal(c.Args, []string{"sh", "-c", "echo hi"}) || !slices.Contains(c.Env, "FORGE_PR=1") || len(c.Env) <= 1 {
		t.Fatalf("Cmd = %v dir %s env %d", c.Args, c.Dir, len(c.Env))
	}
}

func TestTailBuffer(t *testing.T) {
	b := &tailBuffer{max: 5}
	_, _ = b.Write([]byte("abc"))
	if string(b.Bytes()) != "abc" || b.dropped {
		t.Fatalf("got %q", b.Bytes())
	}
	_, _ = b.Write([]byte("defg"))
	if string(b.Bytes()) != "cdefg" || !b.dropped {
		t.Fatalf("got %q", b.Bytes())
	}
	_, _ = b.Write([]byte("0123456789"))
	if string(b.Bytes()) != "56789" {
		t.Fatalf("got %q", b.Bytes())
	}
}
