package worktree

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCopyFiles(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, ".env.local"), "SECRET=1")
	writeFile(t, filepath.Join(src, ".env.test"), "T=1")
	writeFile(t, filepath.Join(src, "config/dev.json"), "{}")
	writeFile(t, filepath.Join(src, "config/nested/x.json"), "[]")
	writeFile(t, filepath.Join(src, "README.md"), "source")
	writeFile(t, filepath.Join(dst, "README.md"), "tracked")
	if err := os.Symlink(filepath.Join(src, ".env.local"), filepath.Join(src, "config/link")); err != nil {
		t.Fatal(err)
	}

	copied, skipped, err := copyFiles(src, dst, []string{".env*", "config", "README.md", "missing.txt"})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(copied)
	wantCopied := []string{".env.local", ".env.test", "config/dev.json", "config/nested/x.json"}
	if !slices.Equal(copied, wantCopied) {
		t.Errorf("copied = %v, want %v", copied, wantCopied)
	}
	if !slices.Equal(skipped, []string{"README.md", "missing.txt"}) {
		t.Errorf("skipped = %v", skipped)
	}
	if got := readFile(t, filepath.Join(dst, "README.md")); got != "tracked" {
		t.Errorf("README.md overwritten: %q", got)
	}
	if got := readFile(t, filepath.Join(dst, ".env.local")); got != "SECRET=1" {
		t.Errorf(".env.local = %q", got)
	}
	if info, err := os.Stat(filepath.Join(dst, ".env.local")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode not preserved: %v %v", info, err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "config/link")); !os.IsNotExist(err) {
		t.Errorf("symlink inside dir should be skipped, got err %v", err)
	}
}

func TestCopyFilesRejectsEscapes(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	for _, pat := range []string{"../x", "a/../../x", "/etc/passwd"} {
		if _, _, err := copyFiles(src, dst, []string{pat}); err == nil {
			t.Errorf("copyFiles(%q): want error", pat)
		}
	}
}
