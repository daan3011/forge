package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const id = "0b5f3c3e-8a1d-4c2b-9f00-1234567890ab"

func TestNewSessionID(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		got, err := NewSessionID()
		if err != nil {
			t.Fatal(err)
		}
		if !ValidSessionID(got) {
			t.Fatalf("invalid uuid %q", got)
		}
		if got[14] != '4' || !strings.ContainsRune("89ab", rune(got[19])) {
			t.Errorf("%q is not a v4 RFC 4122 uuid", got)
		}
		if seen[got] {
			t.Fatalf("duplicate %q", got)
		}
		seen[got] = true
	}
}

func TestArgv(t *testing.T) {
	tests := []struct {
		name    string
		l       Launch
		want    []string
		wantErr bool
	}{
		{
			name: "no prompt",
			l:    Launch{SessionID: id},
			want: []string{"sh", "-c", "exec 'claude' --session-id " + id},
		},
		{
			name: "prompt file",
			l:    Launch{SessionID: id, PromptFile: "/data/sessions/fix-ci/prompt.md"},
			want: []string{"sh", "-c", "exec 'claude' --session-id " + id + ` -- "$(cat '/data/sessions/fix-ci/prompt.md')"`},
		},
		{
			name: "paths with spaces and quotes",
			l:    Launch{Bin: "/opt/my tools/claude", SessionID: id, PromptFile: "/tmp/it's here/p.md"},
			want: []string{"sh", "-c", `exec '/opt/my tools/claude' --session-id ` + id + ` -- "$(cat '/tmp/it'\''s here/p.md')"`},
		},
		{
			name: "resume ignores the prompt file",
			l:    Launch{SessionID: id, Resume: true, PromptFile: "/data/p.md"},
			want: []string{"sh", "-c", "exec 'claude' --resume " + id},
		},
		{
			name: "mcp config",
			l:    Launch{SessionID: id, PromptFile: "/d/p.md", MCPConfig: "/data/sessions/fix ci/mcp.json"},
			want: []string{"sh", "-c", "exec 'claude' --mcp-config '/data/sessions/fix ci/mcp.json' --session-id " + id + ` -- "$(cat '/d/p.md')"`},
		},
		{
			name: "mcp config on resume",
			l:    Launch{SessionID: id, Resume: true, MCPConfig: "/d/mcp.json"},
			want: []string{"sh", "-c", "exec 'claude' --mcp-config '/d/mcp.json' --resume " + id},
		},
		{name: "bad id", l: Launch{SessionID: "x; rm -rf /"}, wantErr: true},
		{name: "bad id on resume", l: Launch{SessionID: "x; rm -rf /", Resume: true}, wantErr: true},
		{name: "uppercase id", l: Launch{SessionID: strings.ToUpper(id)}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.l.Argv()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Argv = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Argv =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

// TestArgvDeliversPromptVerbatim runs the generated command through a real
// sh with a stand-in claude that records its arguments, and checks a prompt
// full of shell metacharacters arrives as one untouched argument.
func TestArgvDeliversPromptVerbatim(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := filepath.Join(t.TempDir(), "it's a dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got := filepath.Join(dir, "args")
	bin := filepath.Join(dir, "fake claude")
	script := "#!/bin/sh\n" +
		`printf '%s\0' "$@" > "$FORGE_ARGS_OUT"` + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	prompts := map[string]string{
		"quotes":     `He said "don't" and 'left'`,
		"newlines":   "line one\nline two\n\nline four",
		"vars":       "home is $HOME, user ${USER}, missing $NOPE_X and $(id)",
		"backticks":  "run `whoami` then `rm -rf /`",
		"everything": "a \"b\" 'c' $d ${e} `f` $(g) \\ ; | & > < * ? [x] ~ # !\n\ttabbed\n-- --flag -n",
		"trailing":   "keep inner\n\nbut trailing newlines go\n\n\n",
	}
	for name, prompt := range prompts {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(dir, name+" prompt.md")
			if err := os.WriteFile(file, []byte(prompt), 0o600); err != nil {
				t.Fatal(err)
			}
			argv, err := Launch{Bin: bin, SessionID: id, PromptFile: file}.Argv()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), argv[0], argv[1:]...)
			cmd.Env = append(os.Environ(), "FORGE_ARGS_OUT="+got)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			raw, err := os.ReadFile(got)
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
			want := []string{"--session-id", id, "--", strings.TrimRight(prompt, "\n")}
			if !reflect.DeepEqual(args, want) {
				t.Errorf("args =\n%q\nwant\n%q", args, want)
			}
		})
	}
}
