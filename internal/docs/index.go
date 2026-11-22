package docs

import (
	"os"
	"sort"
	"strings"
)

// The search index (P4.2) holds every doc's title, headings and lines in
// memory. It is small (personal docs), so a rebuild after each change is
// cheaper than keeping it in sync incrementally.

// MaxIndexBytes caps how much of a file is indexed; bigger files are found
// by title only.
const MaxIndexBytes = 1 << 20

// Heading is an ATX heading ("## Rollback") in a doc.
type Heading struct {
	Text  string
	Level int
	Line  int // 0-based line number
}

// Doc is one indexed markdown file.
type Doc struct {
	Root     string // project name
	Path     string // absolute path
	Rel      string // slash-separated path relative to the docs root
	Title    string // first H1, else the file name without .md
	Headings []Heading
	Lines    []string
}

// IndexRoot is one scanned docs folder to index.
type IndexRoot struct {
	Name string
	Tree *Node
}

// Index is an in-memory full-text index of docs.
type Index struct {
	docs []Doc
}

// MatchKind ranks a hit: title matches beat heading matches beat body
// matches.
type MatchKind int

// Match kinds, best first.
const (
	MatchTitle MatchKind = iota
	MatchHeading
	MatchBody
)

// Hit is a doc matching a query, at its best match.
type Hit struct {
	Doc  *Doc
	Kind MatchKind
	// Heading is the heading to jump to: the matched heading, or the nearest
	// one above a body match ("" when there is none).
	Heading string
	// Snippet is the matched line (title, heading text or trimmed body
	// line); the match is Snippet[MatchAt:MatchAt+MatchLen].
	Snippet  string
	MatchAt  int
	MatchLen int
	count    int // occurrences in the doc, to rank within a kind
}

// BuildIndex reads every doc under the roots' trees.
func BuildIndex(roots []IndexRoot) *Index {
	ix := &Index{}
	for _, r := range roots {
		ix.add(r.Name, r.Tree)
	}
	return ix
}

func (ix *Index) add(root string, n *Node) {
	if n == nil {
		return
	}
	if n.IsDir {
		for _, c := range n.Children {
			ix.add(root, c)
		}
		return
	}
	var text string
	if info, err := os.Stat(n.Path); err == nil && info.Size() <= MaxIndexBytes {
		if b, err := os.ReadFile(n.Path); err == nil {
			text = string(b)
		}
	}
	ix.docs = append(ix.docs, ParseDoc(root, n.Path, n.Rel, text))
}

// ParseDoc indexes text as the doc at path.
func ParseDoc(root, path, rel, text string) Doc {
	d := Doc{Root: root, Path: path, Rel: rel}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if text != "" {
		d.Lines = strings.Split(text, "\n")
	}
	// Front matter isn't searched; its lines stay (blank) so line numbers
	// still match the file.
	for i := range min(FrontMatterLines(text), len(d.Lines)) {
		d.Lines[i] = ""
	}
	fence := ""
	for i, l := range d.Lines {
		t := strings.TrimSpace(l)
		if f := fenceMark(t); f != "" {
			switch {
			case fence == "":
				fence = f
			case strings.HasPrefix(t, fence) && strings.TrimLeft(t, fence[:1]) == "":
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		if h, ok := parseHeading(l); ok {
			h.Line = i
			d.Headings = append(d.Headings, h)
			if h.Level == 1 && d.Title == "" {
				d.Title = h.Text
			}
		}
	}
	if d.Title == "" {
		name := rel
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		if len(name) > 3 && strings.EqualFold(name[len(name)-3:], ".md") {
			name = name[:len(name)-3]
		}
		d.Title = name
	}
	return d
}

// fenceMark returns the fence (``` or ~~~, at least three) that line opens
// or closes, or "".
func fenceMark(line string) string {
	for _, c := range []string{"`", "~"} {
		if strings.HasPrefix(line, c+c+c) {
			n := len(line) - len(strings.TrimLeft(line, c))
			return strings.Repeat(c, n)
		}
	}
	return ""
}

// parseHeading parses an ATX heading: up to three spaces, 1-6 '#', then a
// space (or nothing), optional closing '#'s.
func parseHeading(line string) (Heading, bool) {
	s := line
	for range 3 {
		s = strings.TrimPrefix(s, " ")
	}
	n := len(s) - len(strings.TrimLeft(s, "#"))
	if n < 1 || n > 6 {
		return Heading{}, false
	}
	rest := s[n:]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return Heading{}, false
	}
	rest = strings.TrimSpace(rest)
	if trimmed := strings.TrimRight(rest, "#"); trimmed != rest && (trimmed == "" || strings.HasSuffix(trimmed, " ")) {
		rest = strings.TrimSpace(trimmed)
	}
	if rest == "" {
		return Heading{}, false
	}
	return Heading{Text: rest, Level: n}, true
}

// Docs returns every indexed doc.
func (ix *Index) Docs() []Doc {
	if ix == nil {
		return nil
	}
	return ix.docs
}

// Len is the number of indexed docs.
func (ix *Index) Len() int {
	if ix == nil {
		return 0
	}
	return len(ix.docs)
}

// Search finds docs containing q (case-insensitive, as one phrase), at most
// one hit per doc, ranked by match kind, then occurrences, then project and
// path. limit <= 0 means no limit.
func (ix *Index) Search(q string, limit int) []Hit {
	lq := strings.ToLower(strings.TrimSpace(q))
	if ix == nil || lq == "" {
		return nil
	}
	var hits []Hit
	for i := range ix.docs {
		if h, ok := match(&ix.docs[i], lq); ok {
			hits = append(hits, h)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		switch {
		case a.Kind != b.Kind:
			return a.Kind < b.Kind
		case a.count != b.count:
			return a.count > b.count
		case a.Doc.Root != b.Doc.Root:
			return a.Doc.Root < b.Doc.Root
		}
		return a.Doc.Rel < b.Doc.Rel
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// match finds d's best match for the lower-cased query lq.
func match(d *Doc, lq string) (Hit, bool) {
	h := Hit{Doc: d}
	count := 0
	for _, l := range d.Lines {
		count += strings.Count(strings.ToLower(l), lq)
	}
	titleAt := index(d.Title, lq)
	if titleAt >= 0 && count == 0 {
		count = 1 // a file name match
	}
	if count == 0 {
		return Hit{}, false
	}
	h.count = count
	if titleAt >= 0 {
		h.Kind, h.Snippet, h.MatchAt, h.MatchLen = MatchTitle, d.Title, titleAt, len(lq)
		for _, hd := range d.Headings {
			if hd.Level == 1 && hd.Text == d.Title {
				h.Heading = hd.Text
				break
			}
		}
		return h, true
	}
	for _, hd := range d.Headings {
		if at := index(hd.Text, lq); at >= 0 {
			h.Kind, h.Heading, h.Snippet, h.MatchAt, h.MatchLen = MatchHeading, hd.Text, hd.Text, at, len(lq)
			return h, true
		}
	}
	heads := 0
	for i, l := range d.Lines {
		for heads < len(d.Headings) && d.Headings[heads].Line <= i {
			heads++
		}
		t := strings.TrimSpace(l)
		at := index(t, lq)
		if at < 0 {
			continue
		}
		h.Kind, h.Snippet, h.MatchAt, h.MatchLen = MatchBody, t, at, len(lq)
		if heads > 0 {
			h.Heading = d.Headings[heads-1].Text
		}
		return h, true
	}
	// Only a case fold that changes byte lengths gets here: report the doc
	// without a usable snippet position.
	h.Kind, h.Snippet = MatchBody, d.Title
	return h, true
}

// index is the byte offset of lq in s, case-insensitively, or -1. When
// lower-casing changes s's length the offset can't be mapped back, so a
// match is reported at 0 with the caller highlighting what it can.
func index(s, lq string) int {
	low := strings.ToLower(s)
	i := strings.Index(low, lq)
	if i < 0 || len(low) == len(s) {
		return i
	}
	return 0
}
