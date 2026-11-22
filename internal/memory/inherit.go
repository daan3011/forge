package memory

import (
	"path/filepath"
)

// Cell says how a worktree has one memory file.
type Cell int

// Cells.
const (
	Absent  Cell = iota // the worktree doesn't load it
	Shared              // the same file outside the checkout (user, managed, auto memory)
	Same                // its own copy, identical to the first worktree that has it
	Differs             // its own copy, with different content
)

// Tree is one checkout (the main one or a worktree) and the files Resolve
// found for it.
type Tree struct {
	Root  string
	Label string
	Files []File
}

// InheritRow is one memory file across every tree. Path is the file in
// the first tree that has it; Rel is its path relative to that tree's root,
// or "" for files outside every checkout.
type InheritRow struct {
	Path   string
	Rel    string
	Source Source
	Cells  []Cell
}

// Inheritance lines up the launch files of trees: a file inside a checkout
// is matched by its path relative to the checkout, any other file by its
// absolute path.
func Inheritance(trees []Tree) []InheritRow {
	var rows []InheritRow
	index := map[string]int{}
	firstHash := map[string]string{}
	for ti, t := range trees {
		for _, f := range t.Files {
			if !f.Counts() {
				continue
			}
			id, rel := f.Path, ""
			if within(t.Root, f.Path) {
				if r, err := filepath.Rel(t.Root, f.Path); err == nil {
					id, rel = "rel:"+r, r
				}
			}
			i, ok := index[id]
			if !ok {
				i = len(rows)
				index[id] = i
				rows = append(rows, InheritRow{Path: f.Path, Rel: rel, Source: f.Source, Cells: make([]Cell, len(trees))})
				firstHash[id] = f.Hash
			}
			switch {
			case rel == "":
				rows[i].Cells[ti] = Shared
			case f.Hash == firstHash[id]:
				rows[i].Cells[ti] = Same
			default:
				rows[i].Cells[ti] = Differs
			}
		}
	}
	return rows
}
