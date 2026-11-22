package preview

import (
	"bufio"
	"html"
	"io"
	"regexp"
	"strings"
)

// HTMLReadLimit is how much of an HTML file the summary reads.
const HTMLReadLimit = 512 << 10

var (
	reTitle    = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reComment  = regexp.MustCompile(`(?s)<!--.*?(-->|$)`)
	reHidden   = regexp.MustCompile(`(?is)<(script|style|noscript|template|svg|head|title)\b[^>]*>.*?(</(script|style|noscript|template|svg|head|title)\s*>|$)`)
	reBlockTag = regexp.MustCompile(`(?i)</?(p|div|br|li|ul|ol|h[1-6]|tr|td|th|table|section|article|header|footer|nav|main|aside|pre|blockquote|figure|figcaption|form|label|button|hr|dt|dd)\b[^>]*>`)
	reTag      = regexp.MustCompile(`(?s)<[^>]*>`)
	reSpace    = regexp.MustCompile(`[ \t\r\f\v\x{00a0}]+`)
)

// ExtractHTML reads up to limit bytes of an HTML document and returns its
// title and visible text, one line per block, whitespace collapsed and
// blank lines dropped. It is a summary, not a parser: scripts, styles,
// the head and comments are dropped and every other tag is stripped.
func ExtractHTML(r io.Reader, limit int64) (title string, lines []string, err error) {
	data, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return "", nil, err //nolint:wrapcheck
	}
	s := string(data)
	if m := reTitle.FindStringSubmatch(s); m != nil {
		title = collapse(html.UnescapeString(reTag.ReplaceAllString(m[1], "")))
	}
	s = reComment.ReplaceAllString(s, "")
	s = reHidden.ReplaceAllString(s, "\n")
	s = reBlockTag.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 0, 64<<10), len(s)+1)
	for sc.Scan() {
		if l := collapse(sc.Text()); l != "" {
			lines = append(lines, l)
		}
	}
	return title, lines, nil
}

func collapse(s string) string {
	return strings.TrimSpace(reSpace.ReplaceAllString(strings.ReplaceAll(s, "\n", " "), " "))
}
