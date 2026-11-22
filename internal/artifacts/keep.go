package artifacts

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrAlreadyKept means the artifact already lives in the keep folder.
var ErrAlreadyKept = errors.New("artifacts: already kept")

// ErrNoCheckout means the artifact's project has no checkout to keep it in.
var ErrNoCheckout = errors.New("artifacts: no project checkout to keep it in")

// Keep copies the artifact at src into checkout's .forge/artifacts folder,
// as <session>/<name> (or <name> without a session), so it survives the
// worktree's removal. A name taken by a different file gets -2, -3, ...; an
// identical copy already there is reused. It returns the copy's path and
// whether a new file was written.
func Keep(src, session, checkout string) (dest string, wrote bool, err error) {
	if checkout == "" {
		return "", false, ErrNoCheckout
	}
	dir := filepath.Join(checkout, filepath.FromSlash(KeptDir))
	if rel, err := filepath.Rel(dir, src); err == nil && !strings.HasPrefix(rel, "..") {
		return src, false, ErrAlreadyKept
	}
	data, err := os.ReadFile(src) //nolint:gosec // a path from the scan
	if err != nil {
		return "", false, err
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", false, err
	}
	if session != "" {
		dir = filepath.Join(dir, safeName(session))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, err
	}
	base := filepath.Base(src)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; ; i++ {
		name := base
		if i > 1 {
			name = stem + "-" + strconv.Itoa(i) + ext
		}
		dest = filepath.Join(dir, name)
		existing, err := os.ReadFile(dest) //nolint:gosec // inside the keep folder
		switch {
		case err == nil && bytes.Equal(existing, data):
			return dest, false, nil
		case err == nil:
			continue // taken by a different file
		case !errors.Is(err, os.ErrNotExist):
			return "", false, err
		}
		if err := writeNew(dest, data); errors.Is(err, os.ErrExist) {
			continue // created in the meantime
		} else if err != nil {
			return "", false, err
		}
		_ = os.Chtimes(dest, info.ModTime(), info.ModTime())
		return dest, true, nil
	}
}

// writeNew writes data to a temp file next to dest and links it into
// place, failing with os.ErrExist if dest appeared meanwhile.
func writeNew(dest string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".keep-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := io.Copy(tmp, bytes.NewReader(data)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), dest); err != nil {
		if errors.Is(err, os.ErrExist) {
			return err
		}
		return fmt.Errorf("keep %s: %w", dest, err)
	}
	return nil
}

// safeName makes a session name usable as one folder name.
func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return '-'
		}
		return r
	}, s)
	if s == "." || s == ".." || strings.HasPrefix(s, ".") {
		s = "_" + s
	}
	return s
}
