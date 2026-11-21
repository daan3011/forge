package plane

import (
	"html"
	"regexp"
	"strings"
)

var (
	blockEndRe = regexp.MustCompile(`(?i)<br\s*/?>|</(p|div|h[1-6]|li|blockquote|pre|tr)>`)
	listItemRe = regexp.MustCompile(`(?i)<li[^>]*>`)
	tagRe      = regexp.MustCompile(`<[^>]*>`)
	blankRe    = regexp.MustCompile(`\n{3,}`)
)

// HTMLToText turns Plane's description/comment HTML into plain text for
// prompts and terminals: block ends become newlines, list items "- ",
// other tags are dropped and entities decoded.
func HTMLToText(s string) string {
	s = blockEndRe.ReplaceAllString(s, "\n")
	s = listItemRe.ReplaceAllString(s, "- ")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t ")
	}
	s = strings.Join(lines, "\n")
	return strings.TrimSpace(blankRe.ReplaceAllString(s, "\n\n"))
}
