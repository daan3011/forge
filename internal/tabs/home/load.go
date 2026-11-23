package home

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/notes"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs/tabkit"
)

const (
	// loadTimeout bounds one source's load.
	loadTimeout = 30 * time.Second
	// noteLines is how many lines of today's note the side panel keeps.
	noteLines = 12
	// reviewsQuery and mineQuery are the two PR searches Home runs.
	reviewsQuery = "is:open review-requested:@me"
	mineQuery    = "is:open author:@me"
)

// planeExpand is what Plane items are listed with (the state for "in
// progress first").
var planeExpand = []string{"state", "labels", "assignees"}

// openGroups are the Plane state groups of unfinished work.
var openGroups = []plane.StateGroup{plane.GroupTriage, plane.GroupBacklog, plane.GroupUnstarted, plane.GroupStarted}

// loadState tracks one source.
type loadState struct {
	loaded   bool // holds data (cached or fetched)
	fetching bool
	err      error // the last load failed
	at       time.Time
}

// settled reports whether the source has finished its first load, either
// way.
func (s loadState) settled() bool { return s.loaded || s.err != nil }

// sessionsMsg carries the sessions list.
type sessionsMsg struct {
	gen  int
	rows []sessions.Row
	err  error
	at   time.Time
}

// planeMsg carries my Plane items in the current cycles.
type planeMsg struct {
	gen   int
	me    plane.User
	items []plane.WorkItem
	err   error
	at    time.Time
}

// pinsMsg carries the pinned artifacts.
type pinsMsg struct {
	gen  int
	pins []store.Artifact
	err  error
}

// noteMsg carries the first lines of today's note; none when it doesn't
// exist.
type noteMsg struct {
	gen   int
	lines []string
	err   error
}

// TargetTab implements tabs.TargetedMsg for every load result, so they
// reach Home while it is hidden.
func (sessionsMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (planeMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (pinsMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (noteMsg) TargetTab() string { return Title }

// projectName is the sessions / artifacts project filter ("" = all).
func (t *Tab) projectName() string {
	if t.project == nil {
		return ""
	}
	return t.project.Name
}

// prKey is the kv_cache key of one of Home's PR searches.
func (t *Tab) prKey(which string) string { return tabkit.CacheKey(Title, t.project) + ":" + which }

func (t *Tab) loadSessions() tea.Cmd {
	if t.svc == nil || t.agents.fetching {
		return nil
	}
	t.agents.fetching = true
	svc, gen, project, now := t.svc, t.gen, t.projectName(), t.now
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		rows, err := svc.List(ctx, project, false)
		return sessionsMsg{gen: gen, rows: rows, err: err, at: now()}
	}
}

// loadPRs loads both PR searches cache-first.
func (t *Tab) loadPRs() tea.Cmd {
	var cmds []tea.Cmd
	repo := ""
	if t.project != nil {
		repo = t.project.Repo
		if repo == "" {
			return nil // a project without a repo has no PRs
		}
	}
	for _, s := range []*prSearch{&t.reviews, &t.mine} {
		k := t.prKey(s.which)
		if s.cacheAsked != k {
			s.cacheAsked = k
			cmds = append(cmds, tabkit.LoadCached(t.store, Title, k))
		}
		if s.fetching || t.gh == nil {
			continue
		}
		s.fetching = true
		gh, q := t.gh, github.BuildQuery(s.query, repo)
		cmds = append(cmds, tabkit.FetchAndCache(t.store, tabkit.Fetch{
			Tab: Title, Key: k, Project: t.project, Now: t.now,
			Run: func(ctx context.Context) ([]byte, error) {
				res, err := gh.SearchPRs(ctx, q, github.SectionLimit)
				if err != nil {
					return nil, err
				}
				return json.Marshal(res)
			},
		}))
	}
	return tea.Batch(cmds...)
}

// planeProjects are the Plane project ids to look at: the active
// project's, or every configured project's when all are shown.
func (t *Tab) planeProjects() []string {
	if t.project != nil {
		if t.project.PlaneProjectID == "" {
			return nil
		}
		return []string{t.project.PlaneProjectID}
	}
	return notes.PlaneProjects(t.cfg.Projects)
}

// loadPlane finds me (cached first), then each project's current cycle and
// my open items in it.
func (t *Tab) loadPlane() tea.Cmd {
	ids := t.planeProjects()
	if t.plane == nil || len(ids) == 0 || t.issues.fetching {
		return nil
	}
	t.issues.fetching = true
	api, gen, me, now := t.plane, t.gen, t.me, t.now
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		if me.ID == "" {
			if cr, ok := api.(plane.CacheReader); ok {
				me, _ = cr.CachedMe(ctx)
			}
		}
		if me.ID == "" {
			u, err := api.Me(ctx)
			if err != nil {
				return planeMsg{gen: gen, err: err, at: now()}
			}
			me = u
		}
		items, err := cycleItems(ctx, api, ids, me.ID)
		return planeMsg{gen: gen, me: me, items: items, err: err, at: now()}
	}
}

// cycleItems lists my open items in each project's current cycle. A
// project that fails doesn't hide the others; the first error is returned
// with what loaded.
func cycleItems(ctx context.Context, api plane.API, projects []string, me string) ([]plane.WorkItem, error) {
	var out []plane.WorkItem
	var errs []error
	for _, id := range projects {
		c, ok, err := api.CurrentCycle(ctx, id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ok {
			continue
		}
		items, err := api.WorkItems(ctx, id, plane.WorkItemFilter{
			CycleID: c.ID, AssigneeIDs: []string{me}, StateGroups: openGroups, Expand: planeExpand,
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, items...)
	}
	if len(errs) > 0 {
		return out, errs[0] // the client's errors start with "plane:"
	}
	return out, nil
}

func (t *Tab) loadPins() tea.Cmd {
	if t.arts == nil {
		return nil
	}
	idx, gen, project := t.arts, t.gen, t.projectName()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		all, err := idx.List(ctx, project)
		var pins []store.Artifact
		for _, a := range all {
			if a.Pinned {
				pins = append(pins, a)
			}
		}
		return pinsMsg{gen: gen, pins: pins, err: err}
	}
}

// notePath is today's note.
func (t *Tab) notePath() string {
	return notes.Path(t.notesDir, notes.Day(t.now()))
}

func (t *Tab) loadNote() tea.Cmd {
	if t.notesDir == "" {
		return nil
	}
	path, gen := t.notePath(), t.gen
	return func() tea.Msg {
		lines, err := readHead(path, noteLines)
		if errors.Is(err, fs.ErrNotExist) {
			err = nil
		}
		return noteMsg{gen: gen, lines: lines, err: err}
	}
}

// readHead reads up to n lines of path, without trailing blank lines.
func readHead(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var lines []string
	sc := bufio.NewScanner(f)
	for len(lines) < n && sc.Scan() {
		lines = append(lines, strings.TrimRight(sc.Text(), " \t\r"))
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, sc.Err()
}
