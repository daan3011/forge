package projects

import (
	"strings"

	"github.com/daan3011/forge/internal/plane"
)

// PlaneMatch finds the Plane project going with a repo ("owner/name"):
// the one whose identifier or name equals the repo name, ignoring case,
// dashes, underscores, dots and spaces. Archived projects are skipped. ok
// is false for no match or more than one.
func PlaneMatch(repo string, ps []plane.Project) (id string, ok bool) {
	name := repo
	if _, n, found := strings.Cut(repo, "/"); found {
		name = n
	}
	want := normalize(name)
	if want == "" {
		return "", false
	}
	for _, p := range ps {
		if !p.ArchivedAt.IsZero() {
			continue
		}
		if normalize(p.Identifier) != want && normalize(p.Name) != want {
			continue
		}
		if ok {
			return "", false // ambiguous: let the user pick
		}
		id, ok = p.ID, true
	}
	return id, ok
}

func normalize(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '-', '_', '.', ' ':
			return -1
		}
		return r
	}, strings.ToLower(s))
}
