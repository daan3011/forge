package sessions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/sessions/transcript"
	"github.com/daan3011/forge/internal/store"
)

// summaryReplies is how many of the last assistant replies an archive
// summary quotes.
const summaryReplies = 3

// Archive moves x to the archive (P1.10) with a summary built from its
// transcript and links. Its notes, worktree and tmux window are left alone;
// removing them is Kill with RemoveWorktree. It returns the archived row.
func (s *Service) Archive(ctx context.Context, x store.Session) (store.Session, error) {
	x, err := s.c.Store.Get(ctx, x.ID)
	if err != nil {
		return store.Session{}, err
	}
	if x.Archived() {
		return store.Session{}, fmt.Errorf("%w: %s", ErrArchived, x.Name)
	}
	if err := s.archive(ctx, x); err != nil {
		return store.Session{}, err
	}
	return s.c.Store.Get(ctx, x.ID)
}

func (s *Service) archive(ctx context.Context, x store.Session) error {
	sum, err := s.Summarize(ctx, x)
	if err != nil {
		return err
	}
	return s.c.Store.Archive(ctx, x.ID, sum, s.c.Now())
}

// Summarize is the archive summary of x: its issue and PR links with their
// states, then transcript.Brief (stats, files touched, last replies). A
// missing or unreadable transcript is noted, not an error.
func (s *Service) Summarize(ctx context.Context, x store.Session) (string, error) {
	links, err := s.c.Store.LinksFor(ctx, store.SessionRef(x.ID))
	if err != nil {
		return "", err
	}
	var lines []string
	for _, kind := range []store.LinkKind{store.KindGitHubPR, store.KindPlaneIssue} {
		label := "PR"
		if kind == store.KindPlaneIssue {
			label = "issue"
		}
		for _, l := range LinksOf(links, kind) {
			line := label + " " + l.Other.ID
			if l.State != "" {
				line += " " + l.State
			}
			if l.Title != "" {
				line += " — " + l.Title
			}
			lines = append(lines, line)
		}
	}
	tr, err := s.Transcript(ctx, x)
	switch {
	case errors.Is(err, transcript.ErrNoTranscript):
		lines = append(lines, "no transcript")
	case err != nil:
		lines = append(lines, "transcript unreadable: "+err.Error())
	default:
		lines = append(lines, transcript.Brief(tr, x.Worktree, summaryReplies))
	}
	return strings.Join(lines, "\n"), nil
}

// AutoArchive archives every active session of the project (all projects
// when "") whose PRs are all merged or closed, and returns their rows. Sessions
// without PRs, restored from the archive (AutoArchiveOff), or with an agent
// working or waiting are left alone; busy ones are picked up by a later
// call. Failures for one session are joined into err; the others still go.
func (s *Service) AutoArchive(ctx context.Context, project string) ([]Row, error) {
	active := false
	xs, err := s.c.Store.List(ctx, store.SessionFilter{Project: project, Archived: &active})
	if err != nil {
		return nil, err
	}
	var (
		out  []Row
		errs []error
	)
	for _, x := range xs {
		if x.AutoArchiveOff || x.Status == store.StatusWorking || x.Status == store.StatusWaiting {
			continue
		}
		links, err := s.c.Store.LinksFor(ctx, store.SessionRef(x.ID))
		if err != nil {
			return out, err
		}
		if !PRsFinished(links) {
			continue
		}
		if err := s.archive(ctx, x); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", x.Name, err))
			continue
		}
		x, err = s.c.Store.Get(ctx, x.ID)
		if err != nil {
			return out, err
		}
		out = append(out, newRow(x, links))
	}
	return out, errors.Join(errs...)
}

// PRsFinished reports whether links has at least one PR and every PR is
// merged or closed.
func PRsFinished(links []store.Link) bool {
	prs := LinksOf(links, store.KindGitHubPR)
	for _, l := range prs {
		if l.State != github.StateMerged && l.State != github.StateClosed {
			return false
		}
	}
	return len(prs) > 0
}

// ArchiveReason describes why a session with these links was archived:
// "PR #12 merged", "PRs #12 merged, #14 closed", or "" without PRs.
func ArchiveReason(links []store.Link) string {
	prs := LinksOf(links, store.KindGitHubPR)
	if len(prs) == 0 {
		return ""
	}
	parts := make([]string, len(prs))
	for i, l := range prs {
		parts[i] = ShortRef(l.Other)
		if l.State != "" {
			parts[i] += " " + l.State
		}
	}
	label := "PR "
	if len(prs) > 1 {
		label = "PRs "
	}
	return label + strings.Join(parts, ", ")
}

// Unarchive moves x back to the active list. It is never archived
// automatically again. If its worktree was removed it comes back dead, and
// resuming it recreates the worktree from its branch.
func (s *Service) Unarchive(ctx context.Context, x store.Session) error {
	if !x.Archived() {
		return fmt.Errorf("sessions: %s is not archived", x.Name)
	}
	return s.c.Store.Unarchive(ctx, x.ID)
}
