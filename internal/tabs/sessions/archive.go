package sessions

// This file holds the session archive (P1.10): automatic archiving after a
// PR sync, manual archive / restore with A, and the cleanup of an archived
// session's worktree and tmux window (sessions.archive_cleanup).

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/config"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
)

type archivedMsg struct {
	row agent.Row // as it was listed, for the links
	res store.Session
	err error
}

type restoredMsg struct {
	name string
	err  error
}

func (archivedMsg) TargetTab() string { return Title }
func (restoredMsg) TargetTab() string { return Title }

// archiveOrRestore is A: archive the selected session, or restore it in the
// archived view.
func (t *Tab) archiveOrRestore() tea.Cmd {
	r, ok := t.current()
	if !ok || t.svc == nil {
		return nil
	}
	svc, x := t.svc, r.Session
	t.busy++
	if r.Archived() {
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
			defer cancel()
			return restoredMsg{name: x.Name, err: svc.Unarchive(ctx, x)}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
		defer cancel()
		res, err := svc.Archive(ctx, x)
		return archivedMsg{row: r, res: res, err: err}
	}
}

func (t *Tab) archivedOne(msg archivedMsg) tea.Cmd {
	t.busy--
	refresh := t.forceRefresh()
	if msg.err != nil {
		return tea.Batch(refresh, errCmd(fmt.Errorf("archive %s: %w", msg.row.Name, msg.err)))
	}
	r := msg.row
	r.Session = msg.res
	return tea.Batch(refresh, success(msg.res.Name+" archived"), t.cleanup([]agent.Row{r}))
}

func (t *Tab) restored(msg restoredMsg) tea.Cmd {
	t.busy--
	if msg.err != nil {
		return tea.Batch(t.forceRefresh(), errCmd(fmt.Errorf("restore %s: %w", msg.name, msg.err)))
	}
	if t.archived {
		t.archived = false
		t.resetList()
	}
	t.pending = msg.name
	return tea.Batch(t.forceRefresh(), success(msg.name+" restored"))
}

// autoArchived reports the sessions AutoArchive moved to the archive and
// starts their cleanup.
func (t *Tab) autoArchived(rows []agent.Row) tea.Cmd {
	if len(rows) == 0 {
		return nil
	}
	var text string
	if len(rows) == 1 {
		text = rows[0].Name + " archived"
		if why := agent.ArchiveReason(rows[0].LinkList); why != "" {
			text += " — " + why
		}
	} else {
		names := make([]string, len(rows))
		for i, r := range rows {
			names[i] = r.Name
		}
		text = fmt.Sprintf("%d sessions archived (PRs done): %s", len(rows), strings.Join(names, ", "))
	}
	return tea.Batch(t.forceRefresh(), success(text), t.cleanup(rows))
}

// needsCleanup reports whether an archived session still has a tmux window
// or a worktree forge could remove.
func needsCleanup(x store.Session) bool {
	if x.Status == store.StatusArchived {
		return false // worktree already removed
	}
	return x.TmuxWindow != "" || (x.Worktree != "" && !x.WorktreeExternal)
}

// cleanup removes the worktree and tmux window of freshly archived
// sessions, or asks first, as sessions.archive_cleanup says.
func (t *Tab) cleanup(rows []agent.Row) tea.Cmd {
	var todo []agent.Row
	for _, r := range rows {
		if needsCleanup(r.Session) {
			todo = append(todo, r)
		}
	}
	switch t.cfg.Sessions.ArchiveCleanup {
	case config.CleanupNever:
		return nil
	case config.CleanupAlways:
		cmds := make([]tea.Cmd, len(todo))
		for i, r := range todo {
			// Never forced: a dirty worktree still asks (see killed).
			cmds[i] = t.kill(r.Session, agent.KillOpts{RemoveWorktree: true})
		}
		return tea.Batch(cmds...)
	}
	t.cleanupQueue = append(t.cleanupQueue, todo...)
	return t.nextCleanupAsk()
}

// nextCleanupAsk opens the next queued cleanup question, once nothing else
// (a dialog, the transcript viewer, the filter) has the screen.
func (t *Tab) nextCleanupAsk() tea.Cmd {
	if len(t.cleanupQueue) == 0 || t.dialog != nil || t.transcript != nil || t.filter.Active() {
		return nil
	}
	r := t.cleanupQueue[0]
	t.cleanupQueue = t.cleanupQueue[1:]
	return t.askCleanup(r, true)
}

// openCleanup is x in the archived view.
func (t *Tab) openCleanup() tea.Cmd {
	r, ok := t.current()
	if !ok || t.svc == nil {
		return nil
	}
	if !needsCleanup(r.Session) {
		return info(r.Name + " has no worktree or tmux window left to remove")
	}
	return t.askCleanup(r, false)
}

const (
	cleanRemove       = "remove"
	cleanRemoveBranch = "remove-branch"
	cleanKeep         = "keep"
)

// askCleanup asks whether to remove r's worktree and tmux window. queued
// questions that get pushed aside by another dialog go back in the queue.
func (t *Tab) askCleanup(r agent.Row, queued bool) tea.Cmd {
	choice := cleanRemove
	var opts []huh.Option[string]
	if r.WorktreeExternal || r.Worktree == "" {
		opts = append(opts, huh.NewOption("Close its tmux window (the worktree isn't forge's; it stays)", cleanRemove))
	} else {
		opts = append(opts, huh.NewOption("Remove worktree and tmux window (keep branch)", cleanRemove))
		if agent.CanDeleteBranch(r.Session) {
			opts = append(opts, huh.NewOption("Remove worktree, tmux window and branch "+r.Branch+" (unpushed commits are lost)", cleanRemoveBranch))
		}
	}
	opts = append(opts, huh.NewOption("Keep them", cleanKeep))
	title := r.Name + " is archived"
	if why := agent.ArchiveReason(r.LinkList); why != "" {
		title += " (" + why + ")"
	}
	f := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Key("cleanup").
			Title(title + ". Clean up?").
			Description("the summary, notes and links stay in the archive").
			Options(opts...).Value(&choice),
	)).WithTheme(t.theme.Huh()).WithKeyMap(t.keys.HuhKeyMap()).WithShowHelp(false)
	x := r.Session
	cmd := t.openDialog(r.Name, f, func() tea.Cmd {
		switch choice {
		case cleanRemove:
			return t.kill(x, agent.KillOpts{RemoveWorktree: true})
		case cleanRemoveBranch:
			return t.kill(x, agent.KillOpts{RemoveWorktree: true, DeleteBranch: true})
		}
		return nil
	})
	if queued {
		t.dialog.displaced = func() { t.cleanupQueue = append([]agent.Row{r}, t.cleanupQueue...) }
	}
	return cmd
}

// archiveLine is the detail pane's "archived:" value.
func (t *Tab) archiveLine(r agent.Row) string {
	s := r.ArchivedAt.Format("2006-01-02 15:04")
	if why := agent.ArchiveReason(r.LinkList); why != "" {
		s += " · " + why
	}
	if needsCleanup(r.Session) {
		s += " · worktree kept"
	}
	return s
}

// archiveBody replaces the output preview of an archived session: its
// summary and notes, wrapped to width.
func (t *Tab) archiveBody(r agent.Row, width int) []string {
	th := t.theme
	sum := r.Summary
	if strings.TrimSpace(sum) == "" {
		sum = "(no summary)"
	}
	out := []string{th.PreviewRule.Render(rule("summary", width)), th.Body.Render(wrapText(sum, width))}
	if strings.TrimSpace(r.Notes) != "" {
		out = append(out, "", th.PreviewRule.Render(rule("notes", width)), th.Body.Render(wrapText(r.Notes, width)))
	}
	return out
}
