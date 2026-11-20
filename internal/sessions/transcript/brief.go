package transcript

import (
	"fmt"
	"strings"
)

// briefReplyMax caps each reply quoted in a Brief, in runes.
const briefReplyMax = 240

// Brief is a short plain-text summary of a transcript for the session
// archive (P1.10): a stats line with the files touched (relative to
// worktree), then the last n assistant replies, one line each.
func Brief(t Transcript, worktree string, n int) string {
	s := t.Summary
	stats := []string{plural(s.Messages, "message"), plural(s.ToolCalls, "tool call")}
	if k := len(s.FilesTouched); k > 0 {
		stats = append(stats, fmt.Sprintf("%s: %s", plural(k, "file"), ShortFiles(s.FilesTouched, worktree, 5)))
	}
	lines := []string{strings.Join(stats, " · ")}
	var replies []string
	for i := len(t.Entries) - 1; i >= 0 && len(replies) < n; i-- {
		if e := t.Entries[i]; e.Kind == Assistant {
			if r := oneLine(e.Text, briefReplyMax); r != "" {
				replies = append(replies, r)
			}
		}
	}
	if len(replies) > 0 {
		lines = append(lines, "Last replies:")
		for i := len(replies) - 1; i >= 0; i-- { // oldest first
			lines = append(lines, "- "+replies[i])
		}
	}
	return strings.Join(lines, "\n")
}

// ShortFiles lists files relative to root, at most shown, then "+N".
func ShortFiles(files []string, root string, shown int) string {
	var out []string
	prefix := strings.TrimSuffix(root, "/") + "/"
	for i, f := range files {
		if i == shown {
			out = append(out, fmt.Sprintf("+%d", len(files)-shown))
			break
		}
		if root != "" && strings.HasPrefix(f, prefix) {
			f = strings.TrimPrefix(f, prefix)
		}
		out = append(out, f)
	}
	return strings.Join(out, ", ")
}

// oneLine collapses whitespace and cuts s to limit runes with an ellipsis.
func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > limit {
		s = strings.TrimSpace(string(r[:limit-1])) + "…"
	}
	return s
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
