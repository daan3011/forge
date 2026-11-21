package capture

import (
	"regexp"
	"slices"
	"strings"

	"github.com/daan3011/forge/internal/plane"
)

// Candidates are the token values kind k can take in Plane project
// project, in the order completion offers them: for projects the given
// one first, then those linked to forge, then the rest; @me before the
// members; labels by name.
func (c Catalog) Candidates(k Kind, project string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		if v != "" && !seen[norm(v)] {
			seen[norm(v)] = true
			out = append(out, v)
		}
	}
	switch k {
	case Project:
		ps := slices.Clone(c.Projects)
		rank := func(p plane.Project) int {
			switch {
			case p.ID == project:
				return 0
			case len(c.Aliases[p.ID]) > 0:
				return 1
			}
			return 2
		}
		slices.SortStableFunc(ps, func(a, b plane.Project) int { return rank(a) - rank(b) })
		for _, p := range ps {
			if p.ArchivedAt.IsZero() {
				add(projectWord(p))
			}
		}
		for _, p := range ps {
			if p.ArchivedAt.IsZero() {
				for _, a := range c.Aliases[p.ID] {
					add(tokenText(a))
				}
			}
		}
	case Priority:
		for _, p := range priorities {
			add(string(p))
		}
	case Person:
		add("me")
		for _, m := range c.Details[project].Members {
			if m.Member != nil && m.MemberID != c.Me.ID && !isIntakeBot(*m.Member) {
				add(tokenText(userWord(*m.Member)))
			}
		}
	case Label:
		ls := slices.Clone(c.Details[project].Labels)
		slices.SortFunc(ls, func(a, b plane.Label) int { return strings.Compare(norm(a.Name), norm(b.Name)) })
		for _, l := range ls {
			add(tokenText(l.Name))
		}
	}
	return out
}

// intakeBot is the display name Plane gives a project's intake bot, the
// only way the API tells it apart from people.
var intakeBot = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}-intake$`)

// isIntakeBot reports whether u is a project's intake bot; completion
// leaves it out (it still resolves when typed).
func isIntakeBot(u plane.User) bool { return intakeBot.MatchString(u.DisplayName) }

func userWord(u plane.User) string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	if local, _, ok := strings.Cut(u.Email, "@"); ok {
		return local
	}
	return u.FirstName
}

// Completion is what the token under the cursor can become.
type Completion struct {
	Kind       Kind
	Start, End int      // the token's span, sigil included, to replace
	Prefix     string   // what is typed after the sigil
	Candidates []string // token values (without the sigil), best first
}

// Complete offers values for the token under byte offset cursor in s:
// candidates starting with what's typed, then those containing it. +label
// and @person complete in the line's #project, else defaultProject. ok is
// false when the cursor isn't in a token.
func (c Catalog) Complete(s string, cursor int, defaultProject string) (Completion, bool) {
	cursor = min(max(cursor, 0), len(s))
	var cur word
	found := false
	for _, w := range words(s) {
		if w.start <= cursor && cursor <= w.end {
			cur, found = w, true
			break
		}
	}
	if !found || kindOf(cur.text[0]) == 0 {
		return Completion{}, false
	}
	if cursor == cur.start {
		return Completion{}, false // before the sigil
	}
	k := kindOf(cur.text[0])
	comp := Completion{Kind: k, Start: cur.start, End: cur.end, Prefix: s[cur.start+1 : cursor]}

	project := defaultProject
	if k == Person || k == Label {
		d := Parse(s[:cur.start] + s[cur.end:])
		if t, ok := d.last(Project); ok {
			if id, ok := c.findProject(t.Value); ok {
				project = id
			}
		}
	}
	comp.Candidates = filter(c.Candidates(k, project), comp.Prefix)
	return comp, true
}

// filter keeps the candidates that start with prefix (folded), then those
// that contain it, each in their given order.
func filter(cands []string, prefix string) []string {
	p := norm(prefix)
	var starts, contains []string
	for _, c := range cands {
		n := norm(c)
		switch {
		case strings.HasPrefix(n, p):
			starts = append(starts, c)
		case strings.Contains(n, p):
			contains = append(contains, c)
		}
	}
	return append(starts, contains...)
}

// closest is the candidate a mistyped value most likely meant: one it is
// a prefix of, else one containing it, else the nearest by edit distance
// (transpositions count once): within 1 for short values, else within 2
// or a third of its length.
func closest(v string, cands []string) string {
	if got := filter(cands, v); len(got) > 0 {
		return got[0]
	}
	n := []rune(norm(v))
	limit := 1
	if len(n) > 3 {
		limit = max(2, len(n)/3)
	}
	best, bestD := "", limit+1
	for _, c := range cands {
		if d := distance(n, []rune(norm(c))); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

// distance is the optimal string alignment distance between a and b.
func distance(a, b []rune) int {
	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(a)][len(b)]
}
