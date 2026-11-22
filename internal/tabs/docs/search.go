package docs

import (
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	docfs "github.com/daan3011/forge/internal/docs"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// Docs search (P4.2): `/` searches the in-memory index of the shown roots
// (every project, or the active one). Results replace the tree while a
// query is set; moving through them previews the doc in the detail pane,
// scrolled to the matching heading, and enter jumps there.

// searchLimit caps the results shown.
const searchLimit = 200

// hitHeight is the lines a result takes: the title line and the snippet.
const hitHeight = 2

// searching reports whether results replace the tree.
func (t *Tab) searching() bool { return t.search.Visible() }

func (t *Tab) startSearch() tea.Cmd {
	t.focusDetail = false
	t.search.Start()
	return nil
}

func (t *Tab) clearSearch() {
	t.search.Clear()
	t.hits, t.hitCursor, t.hitTop = nil, 0, 0
}

// runSearch reruns the query, keeping the cursor on keepPath if it is
// still a result, and previews the selected hit.
func (t *Tab) runSearch(keepPath string) {
	t.hits = t.index.Search(t.search.Value(), searchLimit)
	cur := 0
	for i, h := range t.hits {
		if h.Doc.Path == keepPath {
			cur = i
			break
		}
	}
	t.setHit(cur)
}

func (t *Tab) currentHit() (docfs.Hit, bool) {
	if t.hitCursor < 0 || t.hitCursor >= len(t.hits) {
		return docfs.Hit{}, false
	}
	return t.hits[t.hitCursor], true
}

// setHit moves the result cursor and previews that doc.
func (t *Tab) setHit(i int) {
	if len(t.hits) == 0 {
		t.hitCursor, t.hitTop = 0, 0
		return
	}
	t.hitCursor = min(max(i, 0), len(t.hits)-1)
	visible := t.hitsVisible()
	if t.hitCursor < t.hitTop {
		t.hitTop = t.hitCursor
	}
	if t.hitCursor >= t.hitTop+visible {
		t.hitTop = t.hitCursor - visible + 1
	}
	t.hitTop = max(min(t.hitTop, len(t.hits)-visible), 0)
	h := t.hits[t.hitCursor]
	t.selectDoc(h.Doc.Path, h.Heading)
}

// hitsVisible is how many results fit above the filter line.
func (t *Tab) hitsVisible() int {
	return max((t.treeHeight()-1)/hitHeight, 1)
}

// jumpToHit leaves the search on the selected result, focused on its
// heading.
func (t *Tab) jumpToHit() {
	h, ok := t.currentHit()
	if !ok {
		return
	}
	t.clearSearch()
	t.selectDoc(h.Doc.Path, h.Heading)
	t.focusDetail = true
}

// searchKey handles a key while results are shown; ok is false for keys
// left to the tab's other handlers (edit, rename, scroll the detail).
func (t *Tab) searchKey(kp tea.KeyPressMsg) (handled bool) {
	in, l := t.keys.Input, t.keys.List
	if t.search.Active() {
		switch {
		case key.Matches(kp, in.Cancel):
			t.clearSearch()
		case key.Matches(kp, in.Accept):
			t.search.Stop()
			if t.search.Value() == "" {
				t.clearSearch()
			}
		case key.Matches(kp, in.Next):
			t.setHit(t.hitCursor + 1)
		case key.Matches(kp, in.Prev):
			t.setHit(t.hitCursor - 1)
		default:
			if t.search.Update(kp) {
				t.hitTop = 0
				t.runSearch("")
			}
		}
		return true
	}
	page := max(t.hitsVisible()/2, 1)
	switch {
	case key.Matches(kp, l.Filter):
		t.search.Start()
	case key.Matches(kp, in.Cancel):
		t.clearSearch()
	case key.Matches(kp, t.keys.Docs.Open):
		t.jumpToHit()
	case key.Matches(kp, l.Up):
		t.setHit(t.hitCursor - 1)
	case key.Matches(kp, l.Down):
		t.setHit(t.hitCursor + 1)
	case key.Matches(kp, l.Top):
		t.setHit(0)
	case key.Matches(kp, l.Bottom):
		t.setHit(len(t.hits))
	case key.Matches(kp, l.PageDown):
		t.setHit(t.hitCursor + page)
	case key.Matches(kp, l.PageUp):
		t.setHit(t.hitCursor - page)
	default:
		return false
	}
	return true
}

// --- view ------------------------------------------------------------------------

// searchView renders the results and the filter line at w x h.
func (t *Tab) searchView(w, h int) string {
	th := t.theme
	q := strings.TrimSpace(t.search.Value())
	var body []string
	switch {
	case !t.scanned:
		body = []string{th.Hint.Render("Indexing…")}
	case q == "":
		scope := "all projects"
		if !t.all() && len(t.roots) > 0 {
			scope = t.roots[0].name
		}
		body = []string{th.Hint.Render(ansi.Truncate("Search "+docCount(t.index.Len())+" in "+scope, w, "…"))}
	case len(t.hits) == 0:
		body = []string{th.TableEmpty.Render(ansi.Wrap("No matches for “"+q+"”", w, ""))}
	default:
		end := min(t.hitTop+t.hitsVisible(), len(t.hits))
		for i := t.hitTop; i < end; i++ {
			body = append(body, t.hitView(t.hits[i], q, i == t.hitCursor, w)...)
		}
	}
	room := max(h-1, 0)
	if len(body) > room {
		body = body[:room]
	}
	for len(body) < room {
		body = append(body, "")
	}
	return strings.Join(append(body, t.search.View(w)), "\n")
}

// hitView is a result's title line and snippet line.
func (t *Tab) hitView(hit docfs.Hit, q string, selected bool, w int) []string {
	th := t.theme
	d := hit.Doc
	where := d.Rel
	if t.all() {
		where = d.Root + " · " + d.Rel
	}
	// The title keeps at least half the row; a long location loses its
	// start ("…oks/release.md").
	if room, ww := w-min(ansi.StringWidth(d.Title), w/2)-1, ansi.StringWidth(where); ww > room {
		if room > 4 {
			where = "…" + ansi.TruncateLeft(where, ww-room+1, "")
		} else {
			where = ""
		}
	}
	space := w - ansi.StringWidth(where) - 1
	if where == "" {
		space = w
	}
	title := ansi.Truncate(d.Title, space, "…")
	pad := max(w-ansi.StringWidth(title)-ansi.StringWidth(where), 0)

	prefix := "  "
	if hit.Kind == docfs.MatchHeading {
		prefix = "  § "
	}
	snip := snippet(hit.Snippet, hit.MatchAt, max(w-ansi.StringWidth(prefix), 1))

	if selected {
		return []string{
			th.TableSelected.Render(title + strings.Repeat(" ", pad) + where),
			th.TableSelected.Render(prefix) + ui.Highlight(snip, q, th.TableSelected, th.SearchMatch) +
				th.TableSelected.Render(strings.Repeat(" ", max(w-ansi.StringWidth(prefix+snip), 0))),
		}
	}
	return []string{
		th.TableRow.Render(title+strings.Repeat(" ", pad)) + th.Hint.Render(where),
		th.Hint.Render(prefix) + ui.Highlight(snip, q, th.Hint, th.SearchMatch),
	}
}

// snippet cuts s to w cells around the match at byte offset at, so the
// match stays visible: "…text around the match…".
func snippet(s string, at, w int) string {
	if ansi.StringWidth(s) <= w {
		return s
	}
	at = min(max(at, 0), len(s))
	start := 0
	if lead := ansi.StringWidth(s[:at]); lead > w/3 {
		// Drop cells from the front until the match sits a third in.
		drop := lead - w/3
		for start < at && drop > 0 {
			r, n := utf8.DecodeRuneInString(s[start:])
			drop -= max(ansi.StringWidth(string(r)), 1)
			start += n
		}
	}
	if start > 0 {
		s = "…" + s[start:]
	}
	return ansi.Truncate(s, w, "…")
}

// --- palette ---------------------------------------------------------------------

// EntityKind is the palette group docs are listed under.
const EntityKind = "Doc"

// Entities implements tabs.EntityProvider: every indexed doc, matched by
// title, path and headings.
func (t *Tab) Entities() []tabs.Entity {
	docs := t.index.Docs()
	out := make([]tabs.Entity, 0, len(docs))
	for _, d := range docs {
		detail := d.Rel
		if t.all() {
			detail = d.Root + " · " + d.Rel
		}
		heads := make([]string, 0, len(d.Headings))
		for _, h := range d.Headings {
			heads = append(heads, h.Text)
		}
		out = append(out, tabs.Entity{
			Kind: EntityKind, ID: d.Path, Title: d.Title, Detail: detail,
			Search: strings.TrimSpace(d.Rel + " " + strings.Join(heads, " ")),
		})
	}
	return out
}

// Select implements tabs.EntityProvider: it drops any search and opens the
// doc in the detail pane.
func (t *Tab) Select(id string) tea.Cmd {
	t.clearSearch()
	t.selectDoc(id, "")
	t.focusDetail = true
	return nil
}
