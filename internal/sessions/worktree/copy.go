package worktree

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// copyFiles copies files matching patterns (globs relative to src) into the
// same relative paths under dst. Files already in dst (tracked files) are
// never overwritten. Top-level matches follow symlinks; symlinks inside a
// matched directory are skipped. It returns copied paths and skipped
// patterns/paths, relative to dst.
func copyFiles(src, dst string, patterns []string) (copied, skipped []string, err error) {
	for _, pat := range patterns {
		if filepath.IsAbs(pat) || slices.Contains(strings.Split(filepath.ToSlash(pat), "/"), "..") {
			return copied, skipped, fmt.Errorf("pattern %q must be relative and stay inside the project", pat)
		}
		matches, err := filepath.Glob(filepath.Join(src, pat))
		if err != nil {
			return copied, skipped, fmt.Errorf("pattern %q: %w", pat, err)
		}
		if len(matches) == 0 {
			skipped = append(skipped, pat)
			continue
		}
		for _, m := range matches {
			rel, err := filepath.Rel(src, m)
			if err != nil {
				return copied, skipped, err
			}
			if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
				skipped = append(skipped, rel)
				continue
			}
			info, err := os.Stat(m)
			if err != nil {
				return copied, skipped, err
			}
			if !info.IsDir() {
				ok, err := copyFile(m, filepath.Join(dst, rel), info.Mode())
				if err != nil {
					return copied, skipped, err
				}
				if ok {
					copied = append(copied, rel)
				} else {
					skipped = append(skipped, rel)
				}
				continue
			}
			err = filepath.WalkDir(m, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() || !d.Type().IsRegular() {
					return nil
				}
				r, err := filepath.Rel(src, p)
				if err != nil {
					return err
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				ok, err := copyFile(p, filepath.Join(dst, r), info.Mode())
				if ok {
					copied = append(copied, r)
				} else if err == nil {
					skipped = append(skipped, r)
				}
				return err
			})
			if err != nil {
				return copied, skipped, err
			}
		}
	}
	return copied, skipped, nil
}

// copyFile copies a regular file to dst unless dst exists. It reports
// whether it copied.
func copyFile(src, dst string, mode fs.FileMode) (ok bool, err error) {
	if !mode.IsRegular() {
		return false, nil
	}
	in, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return false, err
	}
	return true, out.Close()
}
