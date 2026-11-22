package artifacts

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	artfs "github.com/daan3011/forge/internal/artifacts"
	"github.com/daan3011/forge/internal/clipboard"
	"github.com/daan3011/forge/internal/ui"
)

// openSelected opens the artifact in its default app (`open` / xdg-open).
func (t *Tab) openSelected() tea.Cmd {
	a, ok := t.selected()
	if !ok {
		return nil
	}
	if t.files == nil {
		return errCmd(errors.New("can't open files here"))
	}
	f, path := t.files, a.Path
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := f.OpenFile(ctx, path); err != nil {
			return ui.ErrMsg{Err: fmt.Errorf("open %s: %w", filepath.Base(path), err)}
		}
		return ui.InfoMsg{Text: "opened " + filepath.Base(path)}
	}
}

// copyPath copies the artifact's path, through the terminal (OSC 52) when
// no copy tool is installed.
func (t *Tab) copyPath() tea.Cmd {
	a, ok := t.selected()
	if !ok {
		return nil
	}
	path := a.Path
	if t.clip == nil {
		return func() tea.Msg { return clipboardFallbackMsg{text: path} }
	}
	c := t.clip
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := c.Copy(ctx, path)
		switch {
		case errors.Is(err, clipboard.ErrNoTool):
			return clipboardFallbackMsg{text: path}
		case err != nil:
			return ui.ErrMsg{Err: fmt.Errorf("copy path: %w", err)}
		}
		return ui.InfoMsg{Text: "copied " + path}
	}
}

// checkout is the main checkout of the named project.
func (t *Tab) checkout(project string) string {
	for _, p := range t.cfg.Projects {
		if p.Name == project {
			return p.Path
		}
	}
	return ""
}

// keepSelected copies the artifact into its project's .forge/artifacts so
// it survives the worktree's removal.
func (t *Tab) keepSelected() tea.Cmd {
	a, ok := t.selected()
	if !ok {
		return nil
	}
	if artfs.IsKept(a.Path) {
		return info(filepath.Base(a.Path) + " is already kept")
	}
	checkout := t.checkout(a.Project)
	src, session := a.Path, a.SessionName
	return func() tea.Msg {
		dest, wrote, err := artfs.Keep(src, session, checkout)
		return keptMsg{dest: dest, wrote: wrote, err: err}
	}
}

func (t *Tab) kept(msg keptMsg) tea.Cmd {
	switch {
	case errors.Is(msg.err, artfs.ErrNoCheckout):
		return errCmd(errors.New("keep: this artifact's project has no checkout"))
	case msg.err != nil:
		return errCmd(fmt.Errorf("keep: %w", msg.err))
	case !msg.wrote:
		return info("already kept as " + t.shortPath(msg.dest))
	}
	return tea.Batch(success("kept as "+t.shortPath(msg.dest)), t.Refresh())
}

// shortPath shows dest relative to its project's checkout when it is in
// one.
func (t *Tab) shortPath(dest string) string {
	for _, p := range t.cfg.Projects {
		if p.Path == "" {
			continue
		}
		if rel, err := filepath.Rel(p.Path, dest); err == nil && filepath.IsLocal(rel) {
			return filepath.ToSlash(rel)
		}
	}
	return dest
}
