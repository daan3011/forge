package context

import (
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
)

// editSelected opens the selected file in $EDITOR, creating a missing one
// (an empty CLAUDE.local.md, a missing import) first.
func (t *Tab) editSelected() tea.Cmd {
	f, ok := t.selected()
	if !ok {
		return nil
	}
	if f.Exists {
		return t.edit(f.Path)
	}
	path := f.Path
	return func() tea.Msg {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return createdMsg{path: path, err: err}
		}
		fh, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			err = fh.Close()
		}
		return createdMsg{path: path, err: err}
	}
}
