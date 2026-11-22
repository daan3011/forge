package memory

import (
	"strings"
)

// Imports returns the @path imports in a memory file, in order, without
// duplicates. Like Claude Code it ignores fenced code blocks and code spans,
// needs the @ at the start of a line or after whitespace, reads "\ " as a
// space, and skips quoted paths ("@'x'", "@\"x\"").
func Imports(text string) []string {
	text = stripCode(strings.ReplaceAll(text, "\r\n", "\n"))
	var out []string
	seen := map[string]bool{}
	for i := 0; i < len(text); i++ {
		if text[i] != '@' || (i > 0 && !isSpace(text[i-1])) {
			continue
		}
		p, n := readPath(text[i+1:])
		i += n
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// readPath reads an import path after "@" and how many bytes it used.
func readPath(s string) (string, int) {
	if s == "" || isSpace(s[0]) || s[0] == '"' || s[0] == '\'' || s[0] == '@' {
		return "", 0
	}
	var b strings.Builder
	i := 0
	for i < len(s) && !isSpace(s[i]) {
		if s[i] == '\\' && i+1 < len(s) && s[i+1] == ' ' {
			b.WriteByte(' ')
			i += 2
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	// Sentence punctuation after a path isn't part of it.
	p := strings.TrimRight(b.String(), ".,;:!?)]}>")
	return p, i
}

// stripCode blanks fenced code blocks and inline code spans, keeping
// newlines so the rest of the text keeps its shape.
func stripCode(text string) string {
	lines := strings.Split(text, "\n")
	fence := ""
	for i, l := range lines {
		t := strings.TrimLeft(l, " ")
		if fence != "" {
			if strings.HasPrefix(t, fence) && strings.Trim(t, fence[:1]+" \t") == "" {
				fence = ""
			}
			lines[i] = ""
			continue
		}
		if f := fenceOf(t); f != "" && len(l)-len(t) < 4 {
			fence = f
			lines[i] = ""
			continue
		}
		lines[i] = stripSpans(l)
	}
	return strings.Join(lines, "\n")
}

// fenceOf returns the opening fence (``` or ~~~, possibly longer) at the
// start of a line.
func fenceOf(t string) string {
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(t) && t[n] == c {
			n++
		}
		if n >= 3 {
			return t[:n]
		}
	}
	return ""
}

// stripSpans blanks `code` spans: a run of backticks up to the next run of
// the same length.
func stripSpans(l string) string {
	b := []byte(l)
	for i := 0; i < len(b); {
		if b[i] != '`' {
			i++
			continue
		}
		n := 0
		for i+n < len(b) && b[i+n] == '`' {
			n++
		}
		end := closing(b, i+n, n)
		if end < 0 {
			i += n
			continue
		}
		for j := i; j < end+n; j++ {
			b[j] = ' '
		}
		i = end + n
	}
	return string(b)
}

func closing(b []byte, from, n int) int {
	for i := from; i < len(b); {
		if b[i] != '`' {
			i++
			continue
		}
		m := 0
		for i+m < len(b) && b[i+m] == '`' {
			m++
		}
		if m == n {
			return i
		}
		i += m
	}
	return -1
}
