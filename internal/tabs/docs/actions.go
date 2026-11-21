package docs

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	docfs "github.com/daan3011/forge/internal/docs"
)

// errNoProject is shown when an action needs a docs folder and none is
// configured.
var errNoProject = errors.New("no project configured: add one under projects: in the config")

// selectedDoc returns the selected doc row; false on a folder or nothing.
func (t *Tab) selectedDoc() (row, bool) {
	r, ok := t.selected()
	if !ok || r.node.IsDir {
		return row{}, false
	}
	return r, true
}

// editSelected opens the selected doc in $EDITOR.
func (t *Tab) editSelected() tea.Cmd {
	r, ok := t.selectedDoc()
	if !ok {
		return nil
	}
	return t.edit(r.node.Path)
}

// newTarget is where a new doc goes by default: the selection's root and
// folder ("runbooks/").
func (t *Tab) newTarget() (rootIdx int, prefix string) {
	r, ok := t.selected()
	if !ok {
		return 0, ""
	}
	switch {
	case r.top:
		return r.root, ""
	case r.node.IsDir:
		return r.root, r.node.Rel + "/"
	}
	if dir := path.Dir(r.node.Rel); dir != "." {
		return r.root, dir + "/"
	}
	return r.root, ""
}

type newForm struct {
	root     int
	name     string
	template string
}

// newDoc asks for a name, a template and (with every project shown) the
// project, then creates the doc and opens it in $EDITOR.
func (t *Tab) newDoc() tea.Cmd {
	if len(t.roots) == 0 {
		return errCmd(errNoProject)
	}
	idx, prefix := t.newTarget()
	f := &newForm{root: idx, name: prefix, template: docfs.Templates[0]}
	var fields []huh.Field
	if len(t.roots) > 1 {
		opts := make([]huh.Option[int], len(t.roots))
		for i, r := range t.roots {
			opts[i] = huh.NewOption(r.name, i)
		}
		fields = append(fields, huh.NewSelect[int]().Key("project").Title("Project").Options(opts...).Value(&f.root))
	}
	tplOpts := make([]huh.Option[string], len(docfs.Templates))
	for i, name := range docfs.Templates {
		tplOpts[i] = huh.NewOption(name, name)
	}
	fields = append(fields,
		huh.NewInput().Key("name").Title("Name").
			Description("Relative to the docs folder; folders are created, .md is added").
			Placeholder("runbooks/deploy").Value(&f.name).
			Validate(func(s string) error { _, err := docfs.CleanRel(s); return err }),
		huh.NewSelect[string]().Key("template").Title("Template").Options(tplOpts...).Value(&f.template),
	)
	return t.openDialog(huh.NewForm(huh.NewGroup(fields...).Title("New doc")), func() tea.Cmd {
		return t.create(f)
	})
}

func (t *Tab) create(f *newForm) tea.Cmd {
	if f.root < 0 || f.root >= len(t.roots) {
		return nil
	}
	rootPath, now := t.roots[f.root].path, t.now()
	return func() tea.Msg {
		rel, err := docfs.CleanRel(f.name)
		if err != nil {
			return opDoneMsg{err: err}
		}
		body := docfs.Template(f.template, docfs.TitleFrom(rel), now)
		p, err := docfs.Create(rootPath, rel, body)
		if err != nil {
			return opDoneMsg{err: err}
		}
		return opDoneMsg{path: p, text: "created " + rel, openEd: true, reveal: true}
	}
}

// renameSelected asks for a new name (which may move the doc to another
// folder) and renames it.
func (t *Tab) renameSelected() tea.Cmd {
	r, ok := t.selectedDoc()
	if !ok {
		return nil
	}
	name := r.node.Rel
	rootPath, src := t.roots[r.root].path, r.node.Path
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().Key("name").Title("Rename " + r.node.Rel).
			Description("A path with folders moves the doc").
			Value(&name).
			Validate(func(s string) error { _, err := docfs.CleanRel(s); return err }),
	))
	return t.openDialog(form, func() tea.Cmd {
		return func() tea.Msg {
			p, err := docfs.Rename(rootPath, src, name)
			if err != nil {
				return opDoneMsg{err: err}
			}
			rel, _ := filepath.Rel(rootPath, p)
			return opDoneMsg{path: p, text: "renamed to " + filepath.ToSlash(rel), reveal: true}
		}
	})
}

// deleteSelected asks, then moves the selected doc to the root's .trash.
func (t *Tab) deleteSelected() tea.Cmd {
	r, ok := t.selectedDoc()
	if !ok {
		return nil
	}
	rootPath, src, rel, now := t.roots[r.root].path, r.node.Path, r.node.Rel, t.now()
	confirm := false
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Key("confirm").
			Title(fmt.Sprintf("Move %s to %s?", rel, docfs.TrashDir)).
			Description(strings.TrimSuffix(rootPath, string(filepath.Separator)) + string(filepath.Separator) + docfs.TrashDir).
			Affirmative("Delete").Negative("Cancel").Value(&confirm),
	))
	return t.openDialog(form, func() tea.Cmd {
		if !confirm {
			return nil
		}
		return func() tea.Msg {
			if _, err := docfs.Trash(rootPath, src, now); err != nil {
				return opDoneMsg{err: err}
			}
			return opDoneMsg{text: "moved " + rel + " to " + docfs.TrashDir, trashed: true}
		}
	})
}

// opDone selects the result of a create / rename, rescans, and opens a new
// doc in $EDITOR.
func (t *Tab) opDone(msg opDoneMsg) tea.Cmd {
	if msg.err != nil {
		return errCmd(msg.err)
	}
	if msg.path != "" {
		t.want = msg.path
		if msg.reveal {
			t.reveal(msg.path)
		}
	}
	if msg.trashed {
		t.focusDetail = false
	}
	cmds := []tea.Cmd{t.Refresh(), success(msg.text)}
	if msg.openEd {
		cmds = append(cmds, t.edit(msg.path))
	}
	return tea.Batch(cmds...)
}
