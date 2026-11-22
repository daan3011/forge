package artifacts

import (
	"io"
	"os"
	"path/filepath"
)

// SyncCopy refreshes dest from src when src changed (size or modified time
// differ), writing a temp file and renaming it into place, and gives dest
// src's modified time. It reports whether dest was rewritten; a missing src
// is an error (os.ErrNotExist).
func SyncCopy(src, dest string) (changed bool, err error) {
	si, err := os.Stat(src)
	if err != nil {
		return false, err
	}
	if di, err := os.Stat(dest); err == nil && di.Size() == si.Size() && di.ModTime().Equal(si.ModTime()) {
		return false, nil
	}
	in, err := os.Open(src) //nolint:gosec // a linked artifact's original
	if err != nil {
		return false, err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".sync-*")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Chtimes(tmp.Name(), si.ModTime(), si.ModTime()); err != nil {
		return false, err
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return false, err
	}
	return true, nil
}
