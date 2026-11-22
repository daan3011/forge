package docs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// TrashDir is the folder under a docs root that deleted docs move to.
const TrashDir = ".trash"

// ErrExists is returned when a create or rename would overwrite a file.
var ErrExists = errors.New("a doc with that name already exists")

// CleanRel validates a doc name typed by the user ("runbooks/deploy") and
// returns it slash-separated, cleaned and ending in .md.
func CleanRel(name string) (string, error) {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	if name == "" {
		return "", errors.New("name is empty")
	}
	if strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return "", errors.New("name must be relative to the docs folder")
	}
	for _, part := range strings.Split(name, "/") {
		switch {
		case part == "..":
			return "", errors.New("name must stay inside the docs folder")
		case part == ".":
		case strings.HasPrefix(part, "."):
			return "", fmt.Errorf("%q: names starting with a dot are hidden", part)
		}
	}
	rel := path.Clean(name)
	if rel == "." || strings.HasSuffix(name, "/") {
		return "", errors.New("name needs a file name, not just a folder")
	}
	if !strings.EqualFold(path.Ext(rel), ".md") {
		rel += ".md"
	}
	return rel, nil
}

// Create writes a new doc at root/name (see CleanRel) with body, creating
// folders as needed. It returns the doc's absolute path.
func Create(root, name, body string) (string, error) {
	rel, err := CleanRel(name)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("%s: %w", rel, ErrExists)
	}
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		return "", err
	}
	return abs, f.Close()
}

// Rename moves the doc at abs (under root) to root/name, creating folders
// as needed and never overwriting. It returns the new absolute path.
func Rename(root, abs, name string) (string, error) {
	if _, err := relTo(root, abs); err != nil {
		return "", err
	}
	rel, err := CleanRel(name)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(root, filepath.FromSlash(rel))
	if dst == abs {
		return abs, nil
	}
	if _, err := os.Lstat(dst); err == nil {
		return "", fmt.Errorf("%s: %w", rel, ErrExists)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(abs, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// Trash moves the doc at abs to root/.trash, keeping its relative path. A
// name already in the trash gets a -YYYYMMDD-HHMMSS suffix (from now). It
// returns where the doc went.
func Trash(root, abs string, now time.Time) (string, error) {
	rel, err := relTo(root, abs)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(root, TrashDir, rel)
	if _, err := os.Lstat(dst); err == nil {
		ext := filepath.Ext(dst)
		dst = strings.TrimSuffix(dst, ext) + now.Format("-20060102-150405") + ext
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(abs, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// relTo returns abs relative to root, refusing paths outside it.
func relTo(root, abs string) (string, error) {
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is not inside %s", abs, root)
	}
	return rel, nil
}
