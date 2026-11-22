package docs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Node is a folder or markdown doc under a docs root.
type Node struct {
	Name     string // base name ("deploy.md", "runbooks")
	Path     string // absolute path
	Rel      string // slash-separated path relative to the root; "" for the root
	IsDir    bool
	ModTime  time.Time
	Children []*Node // folders first, then docs, each by name
}

// Docs counts the docs under n (n itself when it is a doc).
func (n *Node) Docs() int {
	if !n.IsDir {
		return 1
	}
	c := 0
	for _, ch := range n.Children {
		c += ch.Docs()
	}
	return c
}

// IsDoc reports whether name is a markdown file forge shows.
func IsDoc(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".md") && !hidden(name)
}

func hidden(name string) bool { return strings.HasPrefix(name, ".") }

// Scan walks root and returns its tree of markdown docs. Dotfiles and
// dot-folders (such as .trash) are skipped, as are folders without docs. A
// missing root is an empty tree.
func Scan(root string) (*Node, error) {
	n := &Node{Name: filepath.Base(root), Path: root, IsDir: true}
	info, err := os.Stat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return n, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &fs.PathError{Op: "scan", Path: root, Err: errors.New("not a directory")}
	}
	n.ModTime = info.ModTime()
	if err := scanDir(n, ""); err != nil {
		return nil, err
	}
	return n, nil
}

func scanDir(n *Node, rel string) error {
	entries, err := os.ReadDir(n.Path)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if hidden(name) {
			continue
		}
		childRel := name
		if rel != "" {
			childRel = rel + "/" + name
		}
		child := &Node{Name: name, Path: filepath.Join(n.Path, name), Rel: childRel}
		info, err := e.Info()
		if err != nil {
			continue // removed while scanning
		}
		child.ModTime = info.ModTime()
		switch {
		case e.IsDir():
			child.IsDir = true
			if err := scanDir(child, childRel); err != nil {
				return err
			}
			if len(child.Children) == 0 {
				continue
			}
		case info.Mode().IsRegular() && IsDoc(name):
		default:
			continue
		}
		n.Children = append(n.Children, child)
	}
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return nil
}
