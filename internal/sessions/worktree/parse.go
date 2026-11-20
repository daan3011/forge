package worktree

import (
	"fmt"
	"strings"
)

// parseList parses `git worktree list --porcelain -z`: attribute fields
// each terminated by NUL, records ended by an empty field.
func parseList(out string) ([]Worktree, error) {
	var (
		res []Worktree
		cur *Worktree
	)
	flush := func() {
		if cur != nil {
			res = append(res, *cur)
			cur = nil
		}
	}
	for field := range strings.SplitSeq(out, "\x00") {
		if field == "" {
			flush()
			continue
		}
		key, val, _ := strings.Cut(field, " ")
		if key == "worktree" {
			flush()
			cur = &Worktree{Path: val}
			continue
		}
		if cur == nil {
			return nil, fmt.Errorf("worktree list: %q before any worktree line", field)
		}
		switch key {
		case "HEAD":
			cur.Head = val
		case "branch":
			cur.Branch = strings.TrimPrefix(val, "refs/heads/")
		case "bare":
			cur.Bare = true
		case "detached":
			cur.Detached = true
		case "locked":
			cur.Locked = true
		case "prunable":
			cur.Prunable = true
		}
	}
	flush()
	return res, nil
}
