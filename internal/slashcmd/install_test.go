package slashcmd

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/daan3011/forge/internal/testutil"
)

func skill(name, body string) Command {
	return Command{Name: name, Source: SourceBuiltin, Content: []byte("---\nname: " + name + "\n---\n" + body)}
}

func skillPath(root, name string) string {
	return filepath.Join(root, ".claude", "skills", name, "SKILL.md")
}

func rel(name string) string { return ".claude/skills/" + name + "/SKILL.md" }

func TestInstall(t *testing.T) {
	testutil.IsolateHome(t)
	root := filepath.Join(t.TempDir(), "forge")
	vals := map[string]string{"session": "s1"}

	// Fresh install.
	res, err := Install(root, []Command{skill("forge-a", "a {session}"), skill("forge-b", "b")}, vals)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Written, []string{rel("forge-a"), rel("forge-b")}) {
		t.Fatalf("written = %v", res.Written)
	}
	if got := string(mustRead(t, skillPath(root, "forge-a"))); got != "---\nname: forge-a\n---\na s1" {
		t.Fatalf("forge-a = %q", got)
	}

	// Idempotent: nothing written the second time.
	res, err = Install(root, []Command{skill("forge-a", "a {session}"), skill("forge-b", "b")}, vals)
	if err != nil || len(res.Written)+len(res.Removed)+len(res.Kept) != 0 {
		t.Fatalf("re-run: %+v %v", res, err)
	}

	// A foreign file in the root, and one forge wrote that was edited.
	foreign := skillPath(root, "mine")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath(root, "forge-b"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}

	// forge-a changes, forge-b and forge-a… are dropped, mine is wanted
	// but foreign.
	res, err = Install(root, []Command{skill("forge-a", "a2"), skill("mine", "ours")}, vals)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Written, []string{rel("forge-a")}) {
		t.Errorf("written = %v", res.Written)
	}
	if !reflect.DeepEqual(res.Kept, []string{rel("forge-b"), rel("mine")}) {
		t.Errorf("kept = %v", res.Kept)
	}
	if string(mustRead(t, foreign)) != "mine" || string(mustRead(t, skillPath(root, "forge-b"))) != "edited" {
		t.Error("a file forge doesn't own was changed")
	}

	// The edited file goes back to what forge wrote: now it can be removed.
	restore := []byte("---\nname: forge-b\n---\nb")
	if err := os.WriteFile(skillPath(root, "forge-b"), restore, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = Install(root, []Command{skill("forge-a", "a2")}, vals)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Removed, []string{rel("forge-b")}) {
		t.Errorf("removed = %v", res.Removed)
	}
	if _, err := os.Stat(filepath.Dir(skillPath(root, "forge-b"))); !os.IsNotExist(err) {
		t.Error("empty skill folder left behind")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Error("foreign skill removed")
	}
}

func TestInstallRemovedTemplate(t *testing.T) {
	testutil.IsolateHome(t)
	root := t.TempDir()
	tplDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tplDir, "explore.md"), []byte("Explore {repo}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := Catalog{TemplatesDir: tplDir}
	if _, err := c.Sync(root, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(skillPath(root, "forge-tpl-explore")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(tplDir, "explore.md")); err != nil {
		t.Fatal(err)
	}
	res, err := c.Sync(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Removed, []string{rel("forge-tpl-explore")}) {
		t.Errorf("removed = %v", res.Removed)
	}
	if _, err := os.Stat(skillPath(root, "fix-ci")); err != nil {
		t.Error("built-ins missing")
	}
}

func TestShadow(t *testing.T) {
	tests := []struct {
		name  string
		setup func(home, repo string)
		want  Shadower
	}{
		{"none", func(string, string) {}, ""},
		{"repo skill", func(_, repo string) { mkdir(t, repo, ".claude/skills/fix-ci") }, ShadowRepo},
		{"repo command", func(_, repo string) { touch(t, repo, ".claude/commands/fix-ci.md") }, ShadowRepo},
		{"personal skill", func(home, _ string) { mkdir(t, home, ".claude/skills/fix-ci") }, ShadowPersonal},
		{"personal command", func(home, _ string) { touch(t, home, ".claude/commands/fix-ci.md") }, ShadowPersonal},
		{"repo beats personal", func(home, repo string) {
			mkdir(t, home, ".claude/skills/fix-ci")
			touch(t, repo, ".claude/commands/fix-ci.md")
		}, ShadowRepo},
		{"other names don't count", func(home, repo string) {
			mkdir(t, repo, ".claude/skills/fix")
			touch(t, home, ".claude/commands/fix-ci.txt")
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := testutil.IsolateHome(t)
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			repo := t.TempDir()
			tt.setup(home, repo)
			before := tree(t, filepath.Join(home, ".claude"))
			got, ok := Shadow(repo, "fix-ci")
			if got != tt.want || ok != (tt.want != "") {
				t.Errorf("Shadow = %q %v, want %q", got, ok, tt.want)
			}
			c := Catalog{}
			sts, _ := c.Status(repo)
			for _, s := range sts {
				if s.Name == "fix-ci" && s.Shadow != tt.want {
					t.Errorf("Status shadow = %q", s.Shadow)
				}
			}
			if after := tree(t, filepath.Join(home, ".claude")); !reflect.DeepEqual(before, after) {
				t.Errorf("~/.claude changed: %v → %v", before, after)
			}
		})
	}
	if got := ShadowRepo.Badge("fix-ci"); got != "shadowed by repo — use /forge:fix-ci" {
		t.Errorf("badge %q", got)
	}
	if got := ShadowPersonal.Badge("fix-ci"); got != "shadowed by your personal skill — use /forge:fix-ci" {
		t.Errorf("badge %q", got)
	}
}

func TestShadowedStillInstalled(t *testing.T) {
	home := testutil.IsolateHome(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	mkdir(t, home, ".claude/skills/fix-ci")
	root := t.TempDir()
	if _, err := (Catalog{}).Sync(root, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(skillPath(root, "fix-ci")); err != nil {
		t.Error("a shadowed skill must still be synced (reachable as /forge:fix-ci)")
	}
}

func mkdir(t *testing.T, base, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(base, rel), 0o755); err != nil {
		t.Fatal(err)
	}
}

func touch(t *testing.T, base, rel string) {
	t.Helper()
	p := filepath.Join(base, rel)
	mkdir(t, filepath.Dir(p), "")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// tree lists every path under dir.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err == nil {
			out = append(out, p)
		}
		return nil
	})
	return out
}
