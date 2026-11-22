package artifacts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	artfs "github.com/daan3011/forge/internal/artifacts"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// Pins, tags, titles, filters and synced copies (P4.6).

// filters narrow the list; they live for the session only.
type filters struct {
	tag    string // "" = any
	pinned bool
}

func (f filters) any() bool { return f.tag != "" || f.pinned }

func (f filters) match(a store.Artifact) bool {
	if f.pinned && !a.Pinned {
		return false
	}
	return f.tag == "" || slices.Contains(a.Tags, f.tag)
}

// Detach reasons shown once a copy stops syncing.
const (
	reasonArchived = "session archived"
	reasonRemoved  = "worktree removed"
	reasonManual   = "detached"
)

// metaDoneMsg reports a pin / tags / title / detach change; the tab
// rescans and selects want.
type metaDoneMsg struct {
	want string
	text string
	err  error
}

// TargetTab implements tabs.TargetedMsg.
func (metaDoneMsg) TargetTab() string { return Title }

func (t *Tab) metaDone(msg metaDoneMsg) tea.Cmd {
	if msg.err != nil {
		return tea.Batch(errCmd(msg.err), t.Refresh())
	}
	if msg.want != "" {
		t.want = msg.want
	}
	return tea.Batch(success(msg.text), t.Refresh())
}

// displayName is the artifact's title, or its file name.
func displayName(a store.Artifact) string {
	if a.Title != "" {
		return a.Title
	}
	return filepath.Base(a.Path)
}

// metaCmd runs f against the index off the update loop.
func (t *Tab) metaCmd(want, text string, f func(ctx context.Context, idx tabs.ArtifactIndex) error) tea.Cmd {
	if t.index == nil {
		return errCmd(errors.New("no artifact index"))
	}
	idx := t.index
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return metaDoneMsg{want: want, text: text, err: f(ctx, idx)}
	}
}

// togglePin pins or unpins the selected artifact. A worktree file is first
// kept in its project's .forge/artifacts; the pin, title and tags move to
// that copy, which then syncs from the original until detached.
func (t *Tab) togglePin() tea.Cmd {
	a, ok := t.selected()
	if !ok {
		return nil
	}
	name, at := displayName(a), t.now()
	if a.Pinned {
		return t.metaCmd(a.Path, "unpinned "+name, func(ctx context.Context, idx tabs.ArtifactIndex) error {
			if err := idx.SetPinned(ctx, a.Path, a.Project, false); err != nil {
				return err
			}
			if a.Link != nil && a.Link.Active() {
				return idx.Detach(ctx, a.Path, reasonManual, at)
			}
			return nil
		})
	}
	checkout := t.checkout(a.Project)
	if a.SessionID == 0 || artfs.IsKept(a.Path) || checkout == "" {
		return t.metaCmd(a.Path, "pinned "+name, func(ctx context.Context, idx tabs.ArtifactIndex) error {
			return idx.SetPinned(ctx, a.Path, a.Project, true)
		})
	}
	if t.index == nil {
		return errCmd(errors.New("no artifact index"))
	}
	idx, short := t.index, t.shortPath
	return func() tea.Msg {
		dest, _, err := artfs.Keep(a.Path, a.SessionName, checkout)
		if err != nil {
			return metaDoneMsg{err: fmt.Errorf("pin: keep a copy: %w", err)}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// The copy must be indexed before its metadata can show.
		info, err := os.Stat(dest)
		if err != nil {
			return metaDoneMsg{err: fmt.Errorf("pin: %w", err)}
		}
		if err := idx.Sync(ctx, nil, []store.Artifact{{
			Path: dest, Project: a.Project, Type: a.Type, Size: info.Size(),
			CreatedAt: info.ModTime(), ModifiedAt: info.ModTime(),
		}}); err != nil {
			return metaDoneMsg{err: fmt.Errorf("pin: %w", err)}
		}
		err = errors.Join(
			idx.MoveMeta(ctx, a.Path, dest),
			idx.SetPinned(ctx, dest, a.Project, true),
			idx.Link(ctx, store.ArtifactLink{
				CopyPath: dest, SourcePath: a.Path, SessionID: a.SessionID, SessionName: a.SessionName,
				SyncedAt: at,
			}),
		)
		if err != nil {
			return metaDoneMsg{err: fmt.Errorf("pin: %w", err)}
		}
		return metaDoneMsg{want: dest, text: "pinned · copied to " + short(dest)}
	}
}

// editTags asks for the selected artifact's tags, comma separated, with
// completion from every tag in use.
func (t *Tab) editTags() tea.Cmd {
	a, ok := t.selected()
	if !ok {
		return nil
	}
	value := &syncedValue{}
	value.Set(artfs.JoinTags(a.Tags))
	all := t.allTags
	in := huh.NewInput().Key("tags").Title("Tags for " + displayName(a)).
		Description("Comma separated; " + t.keys.HuhKeyMap().Input.AcceptSuggestion.Help().Key + " completes an existing tag").
		Placeholder("design, review").Accessor(value)
	in.SuggestionsFunc(func() []string { return artfs.TagSuggestions(value.load(), all) }, &value.loop)
	return t.openDialog(huh.NewForm(huh.NewGroup(in)), func() tea.Cmd {
		tags := artfs.ParseTags(value.Get())
		if slices.Equal(tags, a.Tags) {
			return nil
		}
		text := "cleared tags"
		if len(tags) > 0 {
			text = "tagged " + hashTags(tags)
		}
		return t.metaCmd(a.Path, text, func(ctx context.Context, idx tabs.ArtifactIndex) error {
			return idx.SetTags(ctx, a.Path, tags)
		})
	})
}

// syncedValue is an input's value that huh's suggestion func can read off
// the update loop: huh hashes and reads loop (update loop only), while the
// func, run as a command, reads the locked copy.
type syncedValue struct {
	loop string

	mu     sync.Mutex
	shared string
}

var _ huh.Accessor[string] = (*syncedValue)(nil)

// Get implements huh.Accessor (update loop).
func (v *syncedValue) Get() string { return v.loop }

// Set implements huh.Accessor (update loop).
func (v *syncedValue) Set(s string) {
	v.loop = s
	v.mu.Lock()
	v.shared = s
	v.mu.Unlock()
}

func (v *syncedValue) load() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.shared
}

// renameSelected asks for the display title; empty (or the file name)
// goes back to the file name. The file itself is not renamed.
func (t *Tab) renameSelected() tea.Cmd {
	a, ok := t.selected()
	if !ok {
		return nil
	}
	value := displayName(a)
	base := filepath.Base(a.Path)
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().Key("title").Title("Rename " + base).
			Description("Display title only; empty shows the file name").
			Value(&value),
	))
	return t.openDialog(form, func() tea.Cmd {
		title := strings.TrimSpace(value)
		if title == base {
			title = ""
		}
		if title == a.Title {
			return nil
		}
		text := "renamed to " + title
		if title == "" {
			text = "showing " + base
		}
		return t.metaCmd(a.Path, text, func(ctx context.Context, idx tabs.ArtifactIndex) error {
			return idx.SetTitle(ctx, a.Path, title)
		})
	})
}

// detachSelected stops syncing the selected copy from its original.
func (t *Tab) detachSelected() tea.Cmd {
	a, ok := t.selected()
	if !ok {
		return nil
	}
	if a.Link == nil || !a.Link.Active() {
		return info(displayName(a) + " isn't synced from a session")
	}
	at := t.now()
	return t.metaCmd(a.Path, "detached "+displayName(a)+" from "+a.Link.SessionName,
		func(ctx context.Context, idx tabs.ArtifactIndex) error {
			return idx.Detach(ctx, a.Path, reasonManual, at)
		})
}

// openFilters asks for a tag and pinned-only.
func (t *Tab) openFilters() tea.Cmd {
	f := t.filt
	opts := []huh.Option[string]{huh.NewOption("Any tag", "")}
	tags := t.allTags
	if f.tag != "" && !slices.Contains(tags, f.tag) {
		tags = append(slices.Clone(tags), f.tag)
	}
	for _, tag := range tags {
		opts = append(opts, huh.NewOption("#"+tag, tag))
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Key("tag").Title("Tag").Options(opts...).Value(&f.tag),
		huh.NewConfirm().Key("pinned").Title("Pinned only?").Affirmative("Pinned").Negative("All").Value(&f.pinned),
	))
	return t.openDialog(form, func() tea.Cmd { t.setFilters(f); return nil })
}

func (t *Tab) togglePinnedOnly() {
	f := t.filt
	f.pinned = !f.pinned
	t.setFilters(f)
}

func (t *Tab) clearFilters() { t.setFilters(filters{}) }

func (t *Tab) setFilters(f filters) {
	if f == t.filt {
		return
	}
	t.filt = f
	t.rebuild()
}

// filterLabel describes the active filters, "" without any.
func (t *Tab) filterLabel() string {
	var parts []string
	if t.filt.tag != "" {
		parts = append(parts, "#"+t.filt.tag)
	}
	if t.filt.pinned {
		parts = append(parts, "pinned only")
	}
	return strings.Join(parts, " · ")
}

func hashTags(tags []string) string {
	out := make([]string, len(tags))
	for i, tag := range tags {
		out[i] = "#" + tag
	}
	return strings.Join(out, " ")
}

// syncLinks refreshes every active pinned copy from its original, and
// detaches those whose session is archived (or gone) or whose original is
// gone; the copy then stays as the final version. Only copies inside one
// of roots (the shown checkouts) are handled; sess are their projects'
// sessions, archived ones included, by id.
func syncLinks(ctx context.Context, idx tabs.ArtifactIndex, sess map[int64]store.Session, roots []string, now time.Time) error {
	links, err := idx.ActiveLinks(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, l := range links {
		if !inside(l.CopyPath, roots) {
			continue
		}
		if s, ok := sess[l.SessionID]; l.SessionID != 0 && (!ok || s.Status == store.StatusArchived) {
			errs = append(errs, idx.Detach(ctx, l.CopyPath, reasonArchived, now))
			continue
		}
		changed, err := artfs.SyncCopy(l.SourcePath, l.CopyPath)
		switch {
		case errors.Is(err, os.ErrNotExist):
			errs = append(errs, idx.Detach(ctx, l.CopyPath, reasonRemoved, now))
		case err != nil:
			errs = append(errs, fmt.Errorf("sync %s: %w", filepath.Base(l.CopyPath), err))
		case changed:
			errs = append(errs, idx.MarkSynced(ctx, l.CopyPath, now))
		}
	}
	return errors.Join(errs...)
}

func inside(p string, roots []string) bool {
	for _, r := range roots {
		if rel, err := filepath.Rel(r, p); err == nil && filepath.IsLocal(rel) {
			return true
		}
	}
	return false
}

// --- dialogs ---------------------------------------------------------------------

// openDialog shows form over the tab; done runs once it is completed.
func (t *Tab) openDialog(form *huh.Form, done func() tea.Cmd) tea.Cmd {
	t.dialog = form.WithTheme(t.theme.Huh()).WithKeyMap(t.keys.HuhKeyMap()).WithShowHelp(false)
	t.onDone = done
	init := t.dialog.Init()
	t.sizeDialog()
	return init
}

func (t *Tab) updateDialog(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		return nil // the app owns the window size; SetSize resizes the form
	}
	_, cmd := t.dialog.Update(msg)
	switch t.dialog.State {
	case huh.StateCompleted:
		done := t.onDone
		t.dialog, t.onDone = nil, nil
		if done == nil {
			return nil
		}
		return done()
	case huh.StateAborted:
		t.dialog, t.onDone = nil, nil
		return nil
	}
	return cmd
}

func (t *Tab) dialogSize() (w, h int) { return max(t.width-4, 10), max(t.height-2, 3) }

func (t *Tab) sizeDialog() {
	if t.dialog == nil {
		return
	}
	w, h := t.dialogSize()
	_, _ = t.dialog.Update(tea.WindowSizeMsg{Width: w, Height: h})
}

func (t *Tab) dialogView() string {
	w, h := t.dialogSize()
	return t.theme.PaneFocused.Render(ui.Box(t.dialog.View(), w, h))
}

// CapturingInput implements tabs.InputCapturer.
func (t *Tab) CapturingInput() bool { return t.dialog != nil }
