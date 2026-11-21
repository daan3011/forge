package plane

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

var (
	tokenRe = regexp.MustCompile(`(?s)<!--.*?-->|<(/?)([a-zA-Z][a-zA-Z0-9-]*)([^>]*?)(/?)>`)
	attrRe  = regexp.MustCompile(`([a-zA-Z_:][a-zA-Z0-9_:.-]*)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+)))?`)
	spaceRe = regexp.MustCompile(`\s+`)
	langRe  = regexp.MustCompile(`(?:^|\s)language-([A-Za-z0-9_+-]+)`)
)

// HTMLToMarkdown turns Plane's description/comment HTML (tiptap output)
// into Markdown for Glamour: headings, paragraphs, emphasis, code, code
// blocks, links, images, nested and task lists, quotes and rules. Unknown
// tags are dropped and their text kept.
func HTMLToMarkdown(s string) string {
	c := mdConverter{}
	pos := 0
	for _, m := range tokenRe.FindAllStringSubmatchIndex(s, -1) {
		c.text(s[pos:m[0]])
		pos = m[1]
		if m[4] < 0 { // a comment
			continue
		}
		closing := m[3] > m[2]
		name := strings.ToLower(s[m[4]:m[5]])
		attrs := parseAttrs(s[m[6]:m[7]])
		selfClosing := m[9] > m[8]
		if closing {
			c.close(name)
		} else {
			c.open(name, attrs)
			if selfClosing {
				c.close(name)
			}
		}
	}
	c.text(s[pos:])
	return tidyMarkdown(c.out.String())
}

type mdList struct {
	ordered bool
	n       int
}

type mdConverter struct {
	out     strings.Builder
	pending int // line breaks owed before the next text
	lists   []mdList
	liFresh bool // a list item just started: its first block adds no break
	quote   int
	pre     bool
	fence   bool   // a code block started; its opening fence isn't written yet
	lang    string // the code block's language
	hrefs   []string
}

// prefix starts every line: quote markers and list indentation.
func (c *mdConverter) prefix() string {
	p := strings.Repeat("> ", c.quote)
	if n := len(c.lists); n > 0 {
		p += strings.Repeat("  ", n)
	}
	return p
}

// brk asks for n line breaks (1 = new line, 2 = blank line) before the next
// text; breaks never pile up beyond that.
func (c *mdConverter) brk(n int) {
	if c.out.Len() == 0 {
		return
	}
	c.pending = max(c.pending, n)
}

func (c *mdConverter) flush(pfx string) {
	if c.out.Len() == 0 {
		c.out.WriteString(pfx)
		return
	}
	for i := 0; i < c.pending; i++ {
		c.out.WriteString("\n")
		if i < c.pending-1 {
			c.out.WriteString(strings.TrimRight(strings.Repeat("> ", c.quote), " "))
		}
	}
	if c.pending > 0 {
		c.out.WriteString(pfx)
	}
	c.pending = 0
}

// raw writes markup at the current position.
func (c *mdConverter) raw(s string) {
	c.flush(c.prefix())
	c.out.WriteString(s)
	c.liFresh = false
}

func (c *mdConverter) text(s string) {
	if s == "" {
		return
	}
	s = html.UnescapeString(s)
	if c.pre {
		c.openFence()
		c.out.WriteString(s)
		return
	}
	s = strings.ReplaceAll(spaceRe.ReplaceAllString(s, " "), " ", " ")
	if c.pending > 0 || c.out.Len() == 0 || strings.HasSuffix(c.out.String(), "\n") {
		s = strings.TrimLeft(s, " ")
	}
	if s == "" {
		return
	}
	c.raw(s)
}

func (c *mdConverter) block() {
	if c.liFresh {
		return
	}
	if len(c.lists) > 0 {
		c.brk(1)
		return
	}
	c.brk(2)
}

func (c *mdConverter) open(name string, attrs map[string]string) {
	switch name {
	case "p", "div":
		c.block()
	case "br":
		if c.pre {
			c.openFence()
			c.out.WriteString("\n")
		} else {
			c.brk(1)
		}
	case "h1", "h2", "h3", "h4", "h5", "h6":
		c.brk(2)
		n, _ := strconv.Atoi(name[1:])
		c.raw(strings.Repeat("#", n) + " ")
	case "strong", "b":
		c.raw("**")
	case "em", "i":
		c.raw("*")
	case "s", "del", "strike":
		c.raw("~~")
	case "code":
		if !c.pre {
			c.raw("`")
		} else if m := langRe.FindStringSubmatch(attrs["class"]); m != nil && c.fence {
			c.lang = m[1]
		}
	case "pre":
		c.brk(2)
		c.pre, c.fence, c.lang = true, true, ""
	case "a":
		c.hrefs = append(c.hrefs, attrs["href"])
		if attrs["href"] != "" {
			c.raw("[")
		}
	case "img":
		if src := attrs["src"]; src != "" {
			c.raw("![" + attrs["alt"] + "](" + src + ")")
		}
	case "ul", "ol":
		if len(c.lists) == 0 {
			c.brk(2)
		} else {
			c.brk(1)
		}
		c.lists = append(c.lists, mdList{ordered: name == "ol"})
	case "li":
		c.brk(1)
		marker := "- "
		if n := len(c.lists); n > 0 {
			l := &c.lists[n-1]
			l.n++
			if l.ordered {
				marker = strconv.Itoa(l.n) + ". "
			}
		}
		if v, ok := attrs["data-checked"]; ok {
			if v == "true" {
				marker = "- [x] "
			} else {
				marker = "- [ ] "
			}
		}
		indent := strings.Repeat("  ", max(len(c.lists)-1, 0))
		c.flush(strings.Repeat("> ", c.quote) + indent)
		if c.out.Len() > 0 && !strings.HasSuffix(c.out.String(), "\n") && !strings.HasSuffix(c.out.String(), indent) {
			c.out.WriteString("\n" + strings.Repeat("> ", c.quote) + indent)
		}
		c.out.WriteString(marker)
		c.liFresh = true
	case "blockquote":
		c.brk(2)
		c.quote++
	case "hr":
		c.brk(2)
		c.raw("---")
		c.brk(2)
	case "tr":
		c.brk(1)
	}
}

func (c *mdConverter) close(name string) {
	switch name {
	case "p", "div":
		c.liFresh = false
		c.block()
	case "h1", "h2", "h3", "h4", "h5", "h6":
		c.brk(2)
	case "strong", "b":
		c.raw("**")
	case "em", "i":
		c.raw("*")
	case "s", "del", "strike":
		c.raw("~~")
	case "code":
		if !c.pre {
			c.raw("`")
		}
	case "pre":
		c.openFence()
		c.pre = false
		out := strings.TrimRight(c.out.String(), "\n")
		c.out.Reset()
		c.out.WriteString(out + "\n" + c.prefix() + "```")
		c.brk(2)
	case "a":
		if n := len(c.hrefs); n > 0 {
			if href := c.hrefs[n-1]; href != "" {
				c.raw("](" + href + ")")
			}
			c.hrefs = c.hrefs[:n-1]
		}
	case "ul", "ol":
		if n := len(c.lists); n > 0 {
			c.lists = c.lists[:n-1]
		}
		c.liFresh = false
		if len(c.lists) == 0 {
			c.brk(2)
		} else {
			c.brk(1)
		}
	case "li":
		c.liFresh = false
		c.brk(1)
	case "blockquote":
		c.quote = max(c.quote-1, 0)
		c.brk(2)
	case "td", "th":
		c.raw(" ")
	case "tr":
		c.brk(1)
	}
}

func parseAttrs(s string) map[string]string {
	out := map[string]string{}
	for _, m := range attrRe.FindAllStringSubmatch(s, -1) {
		v := m[2] + m[3] + m[4]
		out[strings.ToLower(m[1])] = html.UnescapeString(v)
	}
	return out
}

// openFence writes a code block's opening fence before its first text.
func (c *mdConverter) openFence() {
	if !c.fence {
		return
	}
	c.fence = false
	c.raw("```" + c.lang + "\n")
}

// tidyMarkdown drops trailing spaces and runs of blank lines.
func tidyMarkdown(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	s = strings.Join(lines, "\n")
	return strings.TrimSpace(blankRe.ReplaceAllString(s, "\n\n"))
}
