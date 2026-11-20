package sessions

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/refresh"
	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
)

// Session <-> issue/PR links (P1.9): starting a session from a source,
// turning refs into links, PR sync, the detail pane's link lines and the
// open-in-browser keys.

const (
	syncTimeout = 30 * time.Second
	openTimeout = 10 * time.Second
)

// Default templates for a session started from an issue or a PR.
var sourceTemplates = map[store.LinkKind]string{
	store.KindPlaneIssue: "implement-issue",
	store.KindGitHubPR:   "review-pr",
}

type syncedMsg struct {
	changed bool
	err     error
}

type openedMsg struct {
	url string
	err error
}

func (syncedMsg) TargetTab() string { return Title }
func (openedMsg) TargetTab() string { return Title }

// --- start from a source -----------------------------------------------------

// StartSession implements tabs.SessionStarter: the new-session form,
// pre-filled from src, for a session linked to it.
func (t *Tab) StartSession(src tabs.Source) tea.Cmd {
	if t.dialog != nil {
		return info("finish the open dialog first")
	}
	return t.openNewWith(t.sourceRequest(src))
}

// sourceRequest turns src into a new-session request.
func (t *Tab) sourceRequest(src tabs.Source) NewRequest {
	o := agent.StartOpts{
		Name:     t.uniqueName(SourceName(src)),
		Base:     src.Base,
		Branch:   src.Branch,
		Template: src.Template,
	}
	if p, ok := projectForRepo(t.cfg.Projects, src.Repo); ok {
		o.Project = p.Name
	} else if t.project != nil {
		o.Project = t.project.Name
	}
	if o.Template == "" {
		// templatesLoaded falls back to the default when it doesn't exist.
		o.Template = sourceTemplates[src.Kind]
	}
	url := src.URL
	if url == "" {
		url = t.refURL(src.Kind, src.ID)
	}
	o.Links = []agent.LinkSpec{{
		To:   store.Ref{Kind: src.Kind, ID: src.ID},
		Meta: store.LinkMeta{URL: url, Title: src.Title, State: src.State},
	}}
	// The source's title fills {issue_title} / {pr_title}, so that much is
	// known even when no context provider can fetch the item.
	vars := maps.Clone(src.Vars)
	if v, ok := titleVars[src.Kind]; ok && src.Title != "" {
		if vars == nil {
			vars = map[string]string{}
		}
		if _, set := vars[v]; !set {
			vars[v] = src.Title
		}
	}
	return NewRequest{Opts: o, Refs: map[store.LinkKind]string{src.Kind: src.ID}, Vars: vars}
}

var titleVars = map[store.LinkKind]string{
	store.KindPlaneIssue: "issue_title",
	store.KindGitHubPR:   "pr_title",
}

var nameJunk = regexp.MustCompile(`[^a-z0-9_-]+`)

// SourceName is the session name for a source: the lower-cased issue key
// ("proj-123") or "pr-<number>", cut to a valid session name.
func SourceName(src tabs.Source) string {
	if src.Kind == store.KindGitHubPR {
		n := src.Number
		if n == 0 {
			_, num, _ := strings.Cut(src.ID, "#")
			n, _ = strconv.Atoi(num)
		}
		if n > 0 {
			return "pr-" + strconv.Itoa(n)
		}
	}
	name := strings.Trim(nameJunk.ReplaceAllString(strings.ToLower(src.ID), "-"), "-_")
	if len(name) > 56 { // room for a "-NN" suffix within 64
		name = strings.TrimRight(name[:56], "-_")
	}
	if name == "" {
		return "session"
	}
	return name
}

// uniqueName adds -2, -3, ... while a listed session has the name. The
// service still rejects names taken in other projects or the archive.
func (t *Tab) uniqueName(name string) string {
	if _, taken := t.rows[name]; !taken {
		return name
	}
	for i := 2; ; i++ {
		n := fmt.Sprintf("%s-%d", name, i)
		if _, taken := t.rows[n]; !taken {
			return n
		}
	}
}

func projectForRepo(ps []config.Project, repo string) (config.Project, bool) {
	if repo == "" {
		return config.Project{}, false
	}
	for _, p := range ps {
		if strings.EqualFold(p.Repo, repo) && p.Path != "" {
			return p, true
		}
	}
	return config.Project{}, false
}

// --- refs typed into the form ------------------------------------------------

var prRefRe = regexp.MustCompile(`^(?:([\w.-]+/[\w.-]+))?#?(\d+)$`)

// normalizeRef turns what was typed into a link id: "owner/repo#12" for a
// PR ("#12" and "12" use the project's repo), the trimmed key for an issue.
// ok is false when a PR ref can't be understood.
func normalizeRef(kind store.LinkKind, ref, repo string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}
	if kind != store.KindGitHubPR {
		return strings.ToUpper(ref), true
	}
	if u, ok := strings.CutPrefix(ref, "https://github.com/"); ok {
		// https://github.com/owner/repo/pull/12
		parts := strings.Split(strings.Trim(u, "/"), "/")
		if len(parts) >= 4 && parts[2] == "pull" {
			ref = parts[0] + "/" + parts[1] + "#" + parts[3]
		}
	}
	m := prRefRe.FindStringSubmatch(ref)
	if m == nil {
		return "", false
	}
	if m[1] != "" {
		repo = m[1]
	}
	if repo == "" {
		return "", false
	}
	return repo + "#" + m[2], true
}

func (t *Tab) refURL(kind store.LinkKind, id string) string { return RefURL(t.cfg, kind, id) }

// RefURL is the web URL of a link id ("owner/repo#12", "PROJ-123"), ""
// when unknown (issues need plane.base_url and plane.workspace_slug).
func RefURL(cfg *config.Config, kind store.LinkKind, id string) string {
	switch kind {
	case store.KindGitHubPR:
		repo, num, ok := strings.Cut(id, "#")
		n, err := strconv.Atoi(num)
		if !ok || err != nil {
			return ""
		}
		return github.PullURL(repo, n)
	case store.KindPlaneIssue:
		return cfg.Plane.IssueURL(id)
	}
	return ""
}

func (t *Tab) withRefLinks(o agent.StartOpts, refs map[store.LinkKind]string) agent.StartOpts {
	return WithRefLinks(t.cfg, o, refs)
}

// WithRefLinks adds a link for every ref in refs ("#12", "owner/repo#12",
// a PR URL, an issue key) that o doesn't link yet, so issues and PRs typed
// into the template form or given to `forge new` are linked too.
func WithRefLinks(cfg *config.Config, o agent.StartOpts, refs map[store.LinkKind]string) agent.StartOpts {
	repo := ""
	for _, p := range cfg.Projects {
		if p.Name == o.Project {
			repo = p.Repo
		}
	}
	has := map[store.LinkKind]bool{}
	for _, l := range o.Links {
		has[l.To.Kind] = true
	}
	for _, kind := range []store.LinkKind{store.KindPlaneIssue, store.KindGitHubPR} {
		if has[kind] {
			continue
		}
		id, ok := normalizeRef(kind, refs[kind], repo)
		if !ok {
			continue
		}
		o.Links = append(o.Links, agent.LinkSpec{
			To: store.Ref{Kind: kind, ID: id}, Meta: store.LinkMeta{URL: RefURL(cfg, kind, id)},
		})
	}
	return o
}

// --- branch checked out elsewhere ---------------------------------------------

const (
	inUseGo     = "go"
	inUseHere   = "here"
	inUseCancel = "cancel"
)

// branchInUse asks what to do when the new session's branch is checked out
// in another worktree: go to the forge session there, or run the new
// session in that worktree (forge then never removes it or its branch).
func (t *Tab) branchInUse(o agent.StartOpts, e *agent.BranchInUseError) tea.Cmd {
	choice := inUseCancel
	var opts []huh.Option[string]
	desc := "git can't check out one branch in two worktrees"
	if e.Session != "" {
		opts = append(opts, huh.NewOption("Go to session "+e.Session, inUseGo))
	} else {
		opts = append(opts, huh.NewOption("Start the session in "+e.Path, inUseHere))
		desc += "; in that worktree forge never removes the directory or deletes the branch"
	}
	opts = append(opts, huh.NewOption("Cancel", inUseCancel))
	choice = opts[0].Value
	f := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Key("in_use").
			Title(fmt.Sprintf("Branch %s is already checked out at %s", e.Branch, e.Path)).
			Description(desc).Options(opts...).Value(&choice),
	)).WithTheme(t.theme.Huh()).WithKeyMap(t.keys.HuhKeyMap()).WithShowHelp(false)
	return t.openDialog(o.Name, f, func() tea.Cmd {
		switch choice {
		case inUseGo:
			return t.goToSession(e.Session)
		case inUseHere:
			o.Worktree = e.Path
			return t.start(o)
		}
		return nil
	})
}

// goToSession selects and attaches to a session, if it is listed.
func (t *Tab) goToSession(name string) tea.Cmd {
	if _, ok := t.rows[name]; !ok {
		return info("session " + name + " isn't in this list — switch project or view to find it")
	}
	return t.AttachSession(name)
}

// --- PR sync -------------------------------------------------------------------

// RefreshSource implements tabs.SourceRefresher: the list on Sessions
// ticks, PR links on GitHub ticks (so GitHub's rate-limit backoff applies).
func (t *Tab) RefreshSource(src refresh.Source) tea.Cmd {
	if src == refresh.GitHub {
		return t.syncPRs()
	}
	return t.Refresh()
}

// syncPRs links sessions to PRs opened from their branches. One sync runs
// at a time.
func (t *Tab) syncPRs() tea.Cmd {
	if t.svc == nil || t.syncing {
		return nil
	}
	t.syncing = true
	svc, project := t.svc, ""
	if t.project != nil {
		project = t.project.Name
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
		defer cancel()
		changed, err := svc.SyncPRs(ctx, project)
		return syncedMsg{changed: changed, err: err}
	}
}

func (t *Tab) synced(msg syncedMsg) tea.Cmd {
	t.syncing = false
	var cmds []tea.Cmd
	if msg.changed {
		cmds = append(cmds, t.liveRefresh())
	}
	if msg.err != nil && !t.syncErrShown {
		// Once per run: a missing `gh auth` would otherwise toast every tick.
		t.syncErrShown = true
		cmds = append(cmds, info("couldn't look up PRs for sessions: "+msg.err.Error()))
	}
	return tea.Batch(cmds...)
}

// --- detail pane & open keys ------------------------------------------------------

// linkLines are the detail pane's issue and PR lines.
func (t *Tab) linkLines(r agent.Row) []string {
	th := t.theme
	line := func(label string, kind store.LinkKind) string {
		ls := agent.LinksOf(r.LinkList, kind)
		if len(ls) == 0 {
			return th.DetailLabel.Render(label+": ") + th.Body.Render("—")
		}
		l := ls[0]
		s := th.DetailLabel.Render(label+": ") + th.Body.Render(agent.ShortRef(l.Other))
		if l.Title != "" {
			s += " " + th.Body.Render(l.Title)
		}
		if l.State != "" {
			s += th.Hint.Render(" · ") + th.LinkState(l.State).Render(l.State)
		}
		if len(ls) > 1 {
			s += th.Hint.Render(fmt.Sprintf(" (+%d more)", len(ls)-1))
		}
		return s
	}
	return []string{line("issue", store.KindPlaneIssue), line("pr", store.KindGitHubPR)}
}

// openLinked opens the selected session's link of kind in the browser; for
// a PR with none linked it falls back to the issue. More than one link of
// the kind asks which.
func (t *Tab) openLinked(kind store.LinkKind) tea.Cmd {
	r, ok := t.current()
	if !ok {
		return nil
	}
	ls := agent.LinksOf(r.LinkList, kind)
	what := "issue"
	if kind == store.KindGitHubPR {
		what = "PR or issue"
		if len(ls) == 0 {
			ls = agent.LinksOf(r.LinkList, store.KindPlaneIssue)
		}
	}
	if len(ls) == 0 {
		return info(r.Name + " has no linked " + what)
	}
	if len(ls) == 1 {
		return t.openLink(ls[0])
	}
	choice := 0
	opts := make([]huh.Option[int], len(ls))
	for i, l := range ls {
		label := agent.ShortRef(l.Other)
		if l.Title != "" {
			label += "  " + l.Title
		}
		if l.State != "" {
			label += "  · " + l.State
		}
		opts[i] = huh.NewOption(label, i)
	}
	f := huh.NewForm(huh.NewGroup(
		huh.NewSelect[int]().Key("link").Title("Open which link of " + r.Name + "?").
			Options(opts...).Value(&choice),
	)).WithTheme(t.theme.Huh()).WithKeyMap(t.keys.HuhKeyMap()).WithShowHelp(false)
	return t.openDialog(r.Name, f, func() tea.Cmd { return t.openLink(ls[choice]) })
}

func (t *Tab) openLink(l store.Link) tea.Cmd {
	url := l.URL
	if url == "" {
		url = t.refURL(l.Other.Kind, l.Other.ID)
	}
	if url == "" {
		return info("no URL known for " + l.Other.ID + " (set plane.base_url and plane.workspace_slug for issues)")
	}
	if t.browser == nil {
		return errCmd(errors.New("no browser opener configured"))
	}
	b := t.browser
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
		defer cancel()
		return openedMsg{url: url, err: b.Open(ctx, url)}
	}
}

func (t *Tab) opened(msg openedMsg) tea.Cmd {
	if msg.err != nil {
		return errCmd(fmt.Errorf("open %s: %w", msg.url, msg.err))
	}
	return info("opened " + msg.url)
}
