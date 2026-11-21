package prs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/tabs/tabkit"
	"github.com/daan3011/forge/internal/ui"
)

// The detail pane (P2.2) shows the selected PR. Its header comes from the
// list row, so it follows the cursor at once; the rest is fetched once the
// cursor has rested for detailDebounce, and cached by PR + updatedAt, in
// memory and in kv_cache.

// detailTab is one of the detail pane's tabs.
type detailTab int

const (
	tabOverview detailTab = iota
	tabChecks
	tabActivity
	numDetailTabs
)

func (d detailTab) String() string {
	switch d {
	case tabChecks:
		return "Checks"
	case tabActivity:
		return "Activity"
	}
	return "Overview"
}

const (
	// detailDebounce is how long the cursor must rest on a PR before its
	// details are fetched.
	detailDebounce = 150 * time.Millisecond
	// detailMemCap is how many details stay in memory.
	detailMemCap = 64
	// detailCachePrefix starts a detail's kv_cache key ("pr-detail:owner/repo#12").
	detailCachePrefix = "pr-detail:"

	detailCacheTimeout = 2 * time.Second
	detailFetchTimeout = 30 * time.Second
)

// detailKey identifies one version of a PR: "owner/repo#12@<updatedAt>".
func detailKey(p github.PR) string {
	return p.ID() + "@" + p.UpdatedAt.UTC().Format(time.RFC3339Nano)
}

// detailEntry is one fetched version of a PR's details and its Markdown,
// rendered for mdWidth (0 = not rendered yet).
type detailEntry struct {
	id, key string
	d       github.PRDetail
	mdWidth int
	body    string
	events  []string // rendered Activity bodies, by index
}

// detailState is the pane's state.
type detailState struct {
	tab    detailTab
	offset int // scroll offset into the tab's content

	want   string // key of the selection last scheduled
	wantID string
	seq    int // debounce generation

	mem      map[string]*detailEntry // by key
	order    []string                // mem keys, oldest first
	latest   map[string]*detailEntry // newest entry per PR id, also stale ones
	inflight map[string]bool         // keys being fetched
	failed   map[string]error        // keys whose last fetch failed

	rendering string // "key@width" being rendered

	links    map[string][]store.Session // linked sessions by PR id
	issues   map[string][]store.Link    // linked Plane work items by PR id (P3.7)
	linksErr map[string]error
}

func newDetailState() detailState {
	return detailState{
		mem: map[string]*detailEntry{}, latest: map[string]*detailEntry{},
		inflight: map[string]bool{}, failed: map[string]error{},
		links: map[string][]store.Session{}, issues: map[string][]store.Link{}, linksErr: map[string]error{},
	}
}

// remember stores e, evicting the oldest entries past detailMemCap.
func (d *detailState) remember(e *detailEntry) {
	if _, ok := d.mem[e.key]; !ok {
		d.order = append(d.order, e.key)
	}
	d.mem[e.key] = e
	d.latest[e.id] = e
	for len(d.order) > detailMemCap {
		old := d.mem[d.order[0]]
		delete(d.mem, d.order[0])
		d.order = d.order[1:]
		if old != nil && d.latest[old.id] == old {
			delete(d.latest, old.id)
		}
	}
}

// entry is the details to show for p: its exact version, else the newest
// one fetched (stale, shown while the new one loads). fresh tells which.
func (d *detailState) entry(p github.PR) (e *detailEntry, fresh bool) {
	if e := d.mem[detailKey(p)]; e != nil {
		return e, true
	}
	return d.latest[p.ID()], false
}

// --- messages ------------------------------------------------------------------

type detailDueMsg struct{ seq int }

type detailLoadedMsg struct {
	id, key  string
	detail   github.PRDetail
	err      error
	cacheErr error
}

type detailRenderedMsg struct {
	key    string
	width  int
	body   string
	events []string
}

type linksLoadedMsg struct {
	id       string
	sessions []store.Session
	issues   []store.Link
	err      error
}

// TargetTab implements tabs.TargetedMsg.
func (detailDueMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (detailLoadedMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (detailRenderedMsg) TargetTab() string { return Title }

// TargetTab implements tabs.TargetedMsg.
func (linksLoadedMsg) TargetTab() string { return Title }

var (
	_ tabs.TargetedMsg = detailDueMsg{}
	_ tabs.TargetedMsg = detailLoadedMsg{}
	_ tabs.TargetedMsg = detailRenderedMsg{}
	_ tabs.TargetedMsg = linksLoadedMsg{}
)

// updateDetail handles the pane's messages; ok is false for other messages.
func (t *Tab) updateDetail(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	switch msg := msg.(type) {
	case detailDueMsg:
		return t.detailDue(msg), true
	case detailLoadedMsg:
		return t.detailLoaded(msg), true
	case detailRenderedMsg:
		t.detailRendered(msg)
		return nil, true
	case linksLoadedMsg:
		t.det.links[msg.id], t.det.linksErr[msg.id] = msg.sessions, msg.err
		t.det.issues[msg.id] = msg.issues
		return nil, true
	}
	return nil, false
}

// --- scheduling ------------------------------------------------------------------

// syncDetail notices a new selection (or a new version of it) and arms
// the debounce. The pane itself needs no fetch to follow the cursor.
func (t *Tab) syncDetail() tea.Cmd {
	d := &t.det
	p, ok := t.selected()
	if !ok {
		d.want, d.wantID = "", ""
		return nil
	}
	k := detailKey(p)
	if k == d.want {
		return nil
	}
	if p.ID() != d.wantID {
		d.offset = 0
	}
	d.want, d.wantID = k, p.ID()
	d.seq++
	seq := d.seq
	return t.tick(detailDebounce, func(time.Time) tea.Msg { return detailDueMsg{seq: seq} })
}

// detailDue runs once the cursor rested: linked sessions always (they
// change without the PR changing), details unless cached or on their way.
func (t *Tab) detailDue(msg detailDueMsg) tea.Cmd {
	d := &t.det
	if msg.seq != d.seq {
		return nil // the cursor moved on
	}
	p, ok := t.selected()
	if !ok || detailKey(p) != d.want {
		return nil
	}
	cmds := []tea.Cmd{t.loadLinks(p.ID())}
	k := d.want
	if d.mem[k] == nil && !d.inflight[k] && t.gh != nil {
		delete(d.failed, k)
		d.inflight[k] = true
		cmds = append(cmds, t.loadDetail(p))
	}
	return tea.Batch(cmds...)
}

// cachedDetail is a detail in kv_cache, with the list's updatedAt it was
// fetched for (GitHub's may already be newer).
type cachedDetail struct {
	For    time.Time       `json:"for"`
	Detail github.PRDetail `json:"detail"`
}

// loadDetail reads p's details from kv_cache when they are for p's
// updatedAt, else fetches and caches them. It runs off the update loop.
func (t *Tab) loadDetail(p github.PR) tea.Cmd {
	gh, kv := t.gh, t.store
	id, key, updated := p.ID(), detailKey(p), p.UpdatedAt
	repo, number := p.Repo, p.Number
	return func() tea.Msg {
		msg := detailLoadedMsg{id: id, key: key}
		ck := detailCachePrefix + id
		if kv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), detailCacheTimeout)
			e, err := kv.Get(ctx, ck)
			cancel()
			var c cachedDetail
			if err == nil && json.Unmarshal(e.Value, &c) == nil && c.For.Equal(updated) {
				msg.detail = c.Detail
				return msg
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), detailFetchTimeout)
		defer cancel()
		msg.detail, msg.err = gh.PRDetail(ctx, repo, number)
		if msg.err != nil || kv == nil {
			return msg
		}
		b, err := json.Marshal(cachedDetail{For: updated, Detail: msg.detail})
		if err == nil {
			cctx, ccancel := context.WithTimeout(context.Background(), detailCacheTimeout)
			err = kv.Put(cctx, ck, tabkit.CacheKind, "", b, 0)
			ccancel()
		}
		if err != nil {
			msg.cacheErr = fmt.Errorf("prs detail cache: %w", err)
		}
		return msg
	}
}

func (t *Tab) detailLoaded(msg detailLoadedMsg) tea.Cmd {
	d := &t.det
	delete(d.inflight, msg.key)
	if msg.err != nil {
		d.failed[msg.key] = msg.err
		if msg.key != d.want {
			return nil // nobody is looking at it any more
		}
		err := fmt.Errorf("prs: %s: %w", msg.id, msg.err)
		if github.IsAuthError(msg.err) {
			err = errors.New("GitHub: not logged in; run `gh auth login`")
		}
		return errCmd(err)
	}
	d.remember(&detailEntry{id: msg.id, key: msg.key, d: msg.detail})
	if msg.cacheErr != nil {
		return errCmd(msg.cacheErr)
	}
	return nil
}

// loadLinks reads the forge sessions (P1.9) and Plane work items (P3.7)
// linked to the PR.
func (t *Tab) loadLinks(id string) tea.Cmd {
	api := t.sessions
	if api == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), detailCacheTimeout)
		defer cancel()
		ss, err := api.LinkedSessions(ctx, store.KindGitHubPR, id)
		is, ierr := api.PRIssues(ctx, id)
		return linksLoadedMsg{id: id, sessions: ss, issues: is, err: errors.Join(err, ierr)}
	}
}

// --- Markdown ----------------------------------------------------------------------

// detailWidth is the width the pane's content is laid out at.
func (t *Tab) detailWidth() int {
	w, _ := t.split.RightSize()
	return w
}

// ensureRendered renders the shown entry's Markdown for the pane's width
// in the background; until then the view shows it as plain text.
func (t *Tab) ensureRendered() tea.Cmd {
	d := &t.det
	p, ok := t.selected()
	w := t.detailWidth()
	if !ok || w <= 0 {
		return nil
	}
	e, _ := d.entry(p)
	if e == nil || e.mdWidth == w {
		return nil
	}
	job := fmt.Sprintf("%s@%d", e.key, w)
	if d.rendering == job {
		return nil
	}
	d.rendering = job
	key, style := e.key, t.theme.GlamourStyle()
	body := e.d.Body
	events := make([]string, len(e.d.Activity))
	for i, ev := range e.d.Activity {
		events[i] = ev.Body
	}
	return func() tea.Msg {
		out := ui.RenderMarkdown(style, w, append([]string{body}, events...))
		return detailRenderedMsg{key: key, width: w, body: out[0], events: out[1:]}
	}
}

func (t *Tab) detailRendered(msg detailRenderedMsg) {
	d := &t.det
	if d.rendering == fmt.Sprintf("%s@%d", msg.key, msg.width) {
		d.rendering = ""
	}
	if msg.width != t.detailWidth() {
		return // resized meanwhile; the next update renders again
	}
	e := d.mem[msg.key]
	if e == nil {
		for _, l := range d.latest {
			if l.key == msg.key {
				e = l
			}
		}
	}
	if e == nil {
		return
	}
	e.mdWidth, e.body, e.events = msg.width, msg.body, msg.events
}

// --- tabs & scrolling ----------------------------------------------------------------

func (t *Tab) setDetailTab(tab detailTab) {
	t.det.tab = (tab%numDetailTabs + numDetailTabs) % numDetailTabs
	t.det.offset = 0
}

// scrollDetail moves the pane by half its height; the view clamps it.
func (t *Tab) scrollDetail(dir int) {
	_, h := t.split.RightSize()
	t.det.offset = max(t.det.offset+dir*max(h/2, 1), 0)
}

// failedFor is the error of the last fetch of p's version, if it failed.
func (t *Tab) failedFor(p github.PR) error {
	return t.det.failed[detailKey(p)]
}

// linkedFor is the sessions linked to PR id; loaded is false until the
// first load for it finished.
func (t *Tab) linkedFor(id string) (ss []store.Session, loaded bool, err error) {
	ss, loaded = t.det.links[id]
	err = t.det.linksErr[id]
	return ss, loaded || err != nil, err
}

func sortedSessions(ss []store.Session) []store.Session {
	out := slices.Clone(ss)
	slices.SortStableFunc(out, func(a, b store.Session) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out
}
