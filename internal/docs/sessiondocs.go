package docs

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// DocStatus is how a session worktree's doc differs from the main checkout.
type DocStatus string

// Doc statuses.
const (
	DocNew     DocStatus = "new"     // not in the main checkout
	DocChanged DocStatus = "changed" // differs from the main checkout's copy
)

// SessionDoc is a doc in a session worktree that the main checkout doesn't
// have, or has differently (P6.5).
type SessionDoc struct {
	Rel     string // slash-separated, relative to the docs root
	Path    string // absolute, in the worktree
	ModTime time.Time
	Status  DocStatus
}

// Diverged lists the docs under wtRoot (a session worktree's docs folder)
// that are missing from mainRoot or differ from their copy there, in tree
// order. Hidden files and folders are skipped as Scan skips them; a
// missing wtRoot has none.
func Diverged(mainRoot, wtRoot string) ([]SessionDoc, error) {
	tree, err := Scan(wtRoot)
	if err != nil {
		return nil, err
	}
	var out []SessionDoc
	var walk func(n *Node) error
	walk = func(n *Node) error {
		if !n.IsDir {
			st, err := compare(n.Path, filepath.Join(mainRoot, filepath.FromSlash(n.Rel)))
			if err != nil {
				return err
			}
			if st != "" {
				out = append(out, SessionDoc{Rel: n.Rel, Path: n.Path, ModTime: n.ModTime, Status: st})
			}
			return nil
		}
		for _, c := range n.Children {
			if err := walk(c); err != nil {
				return err
			}
		}
		return nil
	}
	return out, walk(tree)
}

// compare says how the worktree doc at wt differs from main ("" when it
// doesn't).
func compare(wt, main string) (DocStatus, error) {
	mi, err := os.Stat(main)
	if errors.Is(err, fs.ErrNotExist) {
		return DocNew, nil
	}
	if err != nil {
		return "", err
	}
	wi, err := os.Stat(wt)
	if err != nil {
		return "", err
	}
	if mi.Size() != wi.Size() {
		return DocChanged, nil
	}
	a, err := os.ReadFile(wt)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(main)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(a, b) {
		return DocChanged, nil
	}
	return "", nil
}
