package docs

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// The decision log (P4.4): architecture decisions as numbered markdown
// files, NNNN-slug.md, in the decisions folder of a docs root. Each has
// YAML front matter with its date and status.

// DecisionsDir is the folder under a docs root that holds decisions.
const DecisionsDir = "decisions"

// Decision statuses.
const (
	StatusAccepted   = "accepted"
	StatusSuperseded = "superseded"
)

// Front matter keys.
const (
	metaDate         = "date"
	metaStatus       = "status"
	metaSupersedes   = "supersedes"
	metaSupersededBy = "superseded_by"
)

// metaOrder is the order known front matter keys are written in; others
// follow by name.
var metaOrder = []string{metaDate, metaStatus, metaSupersedes, metaSupersededBy}

// slugMax caps a decision's slug.
const slugMax = 60

// DecisionInput is what the decision form asks for. Title and Decision
// are required.
type DecisionInput struct {
	Title        string
	Context      string
	Decision     string
	Alternatives string
	Consequences string
}

// Validate reports a missing title or decision.
func (in DecisionInput) Validate() error {
	switch {
	case strings.TrimSpace(in.Title) == "":
		return errors.New("a decision needs a title")
	case strings.TrimSpace(in.Decision) == "":
		return errors.New("a decision needs the decision itself")
	}
	return nil
}

// Decision is one logged decision.
type Decision struct {
	Number       int
	Title        string // without the "0007." prefix
	Status       string
	Date         string
	Supersedes   string // file name of the decision this one replaces
	SupersededBy string // file name of the decision that replaced this one
	Path         string // absolute path
	Rel          string // slash-separated path relative to the docs root
}

// File is the decision's file name ("0007-use-sqlite.md").
func (d Decision) File() string { return filepath.Base(d.Path) }

// Label is "0007. Use SQLite".
func (d Decision) Label() string { return fmt.Sprintf("%04d. %s", d.Number, d.Title) }

// Superseded reports whether a newer decision replaced this one.
func (d Decision) Superseded() bool { return d.Status == StatusSuperseded }

// Slug turns a title into a file-name part: lower case, runs of anything
// but letters and digits become "-", at most slugMax long.
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if len(s) > slugMax {
		s = strings.TrimRight(s[:slugMax], "-")
	}
	if s == "" {
		return "decision"
	}
	return s
}

// decisionNumber reads the NNNN prefix of a decision file name.
func decisionNumber(name string) (int, bool) {
	if !IsDoc(name) {
		return 0, false
	}
	digits, _, ok := strings.Cut(name, "-")
	if !ok || len(digits) < 4 {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// NextNumber is one more than the highest decision number under root.
func NextNumber(root string) int {
	entries, err := os.ReadDir(filepath.Join(root, DecisionsDir))
	if err != nil {
		return 1
	}
	top := 0
	for _, e := range entries {
		if n, ok := decisionNumber(e.Name()); ok && !e.IsDir() {
			top = max(top, n)
		}
	}
	return top + 1
}

// RenderDecision is the markdown of a new decision; empty optional
// sections are left out.
func RenderDecision(n int, in DecisionInput, now time.Time) string {
	var b strings.Builder
	writeMeta(&b, map[string]string{metaDate: now.Format("2006-01-02"), metaStatus: StatusAccepted})
	fmt.Fprintf(&b, "# %04d. %s\n", n, strings.TrimSpace(in.Title))
	for _, s := range []struct{ head, text string }{
		{"Context", in.Context},
		{"Decision", in.Decision},
		{"Alternatives considered", in.Alternatives},
		{"Consequences", in.Consequences},
	} {
		if t := strings.TrimSpace(s.text); t != "" {
			fmt.Fprintf(&b, "\n## %s\n\n%s\n", s.head, t)
		}
	}
	return b.String()
}

// NewDecision writes in as the next numbered decision under root. A
// number taken in the meantime moves on to the next one.
func NewDecision(root string, in DecisionInput, now time.Time) (Decision, error) {
	if err := in.Validate(); err != nil {
		return Decision{}, err
	}
	n := NextNumber(root)
	slug := Slug(in.Title)
	for range 5 {
		rel := fmt.Sprintf("%s/%04d-%s.md", DecisionsDir, n, slug)
		p, err := Create(root, rel, RenderDecision(n, in, now))
		if errors.Is(err, ErrExists) {
			n = max(n+1, NextNumber(root))
			continue
		}
		if err != nil {
			return Decision{}, err
		}
		return Decision{
			Number: n, Title: strings.TrimSpace(in.Title), Status: StatusAccepted,
			Date: now.Format("2006-01-02"), Path: p, Rel: rel,
		}, nil
	}
	return Decision{}, fmt.Errorf("couldn't find a free decision number in %s", filepath.Join(root, DecisionsDir))
}

// SplitFrontMatter splits a leading "---" YAML block off src. Scalar
// values come back as strings; src without (valid) front matter is
// returned whole with nil meta.
func SplitFrontMatter(src []byte) (map[string]string, []byte) {
	text := bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(text, []byte("---\n")) {
		return nil, src
	}
	rest := text[4:]
	var block, body []byte
	switch {
	case bytes.HasPrefix(rest, []byte("---\n")):
		body = rest[4:]
	case bytes.Equal(rest, []byte("---")):
	default:
		end := bytes.Index(rest, []byte("\n---\n"))
		if end < 0 {
			if !bytes.HasSuffix(rest, []byte("\n---")) {
				return nil, src
			}
			end = len(rest) - 4
			block = rest[:end]
		} else {
			block, body = rest[:end], rest[end+5:]
		}
	}
	raw := map[string]any{}
	if err := yaml.Unmarshal(block, &raw); err != nil {
		return nil, src
	}
	meta := make(map[string]string, len(raw))
	for k, v := range raw {
		switch v := v.(type) {
		case nil:
			meta[k] = ""
		case time.Time:
			meta[k] = v.Format("2006-01-02")
		case string, bool, int, float64:
			meta[k] = fmt.Sprint(v)
		}
	}
	return meta, body
}

// FrontMatterLines is how many lines of text the front matter takes (0
// without one), so line numbers of the body can be kept.
func FrontMatterLines(text string) int {
	meta, body := SplitFrontMatter([]byte(text))
	if meta == nil {
		return 0
	}
	norm := strings.ReplaceAll(text, "\r\n", "\n")
	return strings.Count(norm[:len(norm)-len(body)], "\n")
}

// metaKeys are meta's keys, known ones first, then by name.
func metaKeys(meta map[string]string) []string {
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	rank := func(k string) int {
		for i, o := range metaOrder {
			if o == k {
				return i
			}
		}
		return len(metaOrder)
	}
	sort.Slice(keys, func(i, j int) bool {
		ri, rj := rank(keys[i]), rank(keys[j])
		if ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	return keys
}

// writeMeta writes meta as a front matter block, known keys first.
func writeMeta(b *strings.Builder, meta map[string]string) {
	b.WriteString("---\n")
	for _, k := range metaKeys(meta) {
		fmt.Fprintf(b, "%s: %s\n", k, metaValue(meta[k]))
	}
	b.WriteString("---\n")
}

// FormatMeta is front matter as one line, "date: 2026-11-22 · status:
// accepted", for showing above a rendered doc.
func FormatMeta(meta map[string]string) string {
	parts := make([]string, 0, len(meta))
	for _, k := range metaKeys(meta) {
		if meta[k] != "" {
			parts = append(parts, strings.ReplaceAll(k, "_", " ")+": "+meta[k])
		}
	}
	return strings.Join(parts, " · ")
}

// metaValue writes plain values (dates, file names, words) bare and
// anything else YAML-quoted.
func metaValue(v string) string {
	safe := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._/-", r)
	}
	plain := v != "" && strings.IndexFunc(v, func(r rune) bool { return !safe(r) }) < 0
	if plain && !strings.HasPrefix(v, "-") && !strings.HasPrefix(v, ".") {
		return v
	}
	b, _ := yaml.Marshal(v)
	return strings.TrimSpace(string(b))
}

// ReadDecision parses the decision file at path (rel is relative to the
// docs root).
func ReadDecision(path, rel string) (Decision, error) {
	n, ok := decisionNumber(filepath.Base(path))
	if !ok {
		return Decision{}, fmt.Errorf("%s: not a decision file (NNNN-name.md)", filepath.Base(path))
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return Decision{}, err
	}
	meta, body := SplitFrontMatter(src)
	d := Decision{
		Number: n, Path: path, Rel: rel,
		Status: meta[metaStatus], Date: meta[metaDate],
		Supersedes: meta[metaSupersedes], SupersededBy: meta[metaSupersededBy],
	}
	if d.Status == "" {
		d.Status = StatusAccepted
	}
	for l := range strings.SplitSeq(string(body), "\n") {
		if h, ok := parseHeading(l); ok && h.Level == 1 {
			d.Title = stripNumber(h.Text)
			break
		}
	}
	if d.Title == "" {
		_, name, _ := strings.Cut(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), "-")
		d.Title = TitleFrom(name)
	}
	return d, nil
}

// stripNumber drops a leading "0007." or "7:" from a heading.
func stripNumber(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i == len(s) || !strings.ContainsRune(".:", rune(s[i])) {
		return s
	}
	if t := strings.TrimSpace(s[i+1:]); t != "" {
		return t
	}
	return s
}

// ListDecisions reads the decisions under root, newest (highest number)
// first. A missing folder has none; unreadable files are skipped.
func ListDecisions(root string) ([]Decision, error) {
	dir := filepath.Join(root, DecisionsDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Decision
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, ok := decisionNumber(e.Name()); !ok {
			continue
		}
		d, err := ReadDecision(filepath.Join(dir, e.Name()), DecisionsDir+"/"+e.Name())
		if err != nil {
			continue // removed while listing
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Number != out[j].Number {
			return out[i].Number > out[j].Number
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// Supersede marks old as superseded by newer: old's front matter gets
// status superseded and superseded_by, and a "Superseded by" link under
// its title; newer's gets supersedes.
func Supersede(old, newer Decision) error {
	switch {
	case old.Path == newer.Path:
		return errors.New("a decision can't supersede itself")
	case filepath.Dir(old.Path) != filepath.Dir(newer.Path):
		return errors.New("decisions must be in the same project")
	case newer.Number < old.Number:
		return fmt.Errorf("%04d is older than %04d: only a newer decision can supersede", newer.Number, old.Number)
	case newer.Superseded():
		return fmt.Errorf("%s is itself superseded", newer.Label())
	}
	link := fmt.Sprintf("> Superseded by [%s](%s)", newer.Label(), newer.File())
	err := rewriteDecision(old.Path, func(meta map[string]string, body string) string {
		meta[metaStatus] = StatusSuperseded
		meta[metaSupersededBy] = newer.File()
		return insertAfterTitle(dropSupersededLine(body), link)
	})
	if err != nil {
		return err
	}
	return rewriteDecision(newer.Path, func(meta map[string]string, body string) string {
		meta[metaSupersedes] = old.File()
		return body
	})
}

// rewriteDecision rewrites the front matter and body of the file at path,
// through a temporary file.
func rewriteDecision(path string, edit func(meta map[string]string, body string) string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	meta, body := SplitFrontMatter(src)
	if meta == nil {
		meta = map[string]string{}
	}
	newBody := edit(meta, string(body))
	var b strings.Builder
	writeMeta(&b, meta)
	b.WriteString(newBody)
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".decision-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(b.String()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// dropSupersededLine removes an earlier "> Superseded by" line (and the
// blank line after it), so superseding again replaces the link.
func dropSupersededLine(body string) string {
	lines := strings.Split(body, "\n")
	out := lines[:0]
	skipBlank := false
	for _, l := range lines {
		if strings.HasPrefix(l, "> Superseded by ") {
			skipBlank = true
			continue
		}
		if skipBlank && strings.TrimSpace(l) == "" {
			skipBlank = false
			continue
		}
		skipBlank = false
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// insertAfterTitle puts line, as its own paragraph, after the first H1 (at
// the top when there is none).
func insertAfterTitle(body, line string) string {
	lines := strings.Split(body, "\n")
	at := 0
	for i, l := range lines {
		if h, ok := parseHeading(l); ok && h.Level == 1 {
			at = i + 1
			break
		}
	}
	add := []string{line, ""}
	if at > 0 {
		add = []string{"", line}
		if at < len(lines) && strings.TrimSpace(lines[at]) != "" {
			add = append(add, "")
		}
	}
	out := make([]string, 0, len(lines)+len(add))
	out = append(out, lines[:at]...)
	out = append(out, add...)
	out = append(out, lines[at:]...)
	return strings.Join(out, "\n")
}

// ClaudeMDNote is the line added to a project's CLAUDE.md, with %s the
// decisions folder relative to the repo.
const ClaudeMDNote = "- Architecture decisions are logged in %s/ (newest first). " +
	"Read them before changing the design; log a new decision (`forge adr`) instead of silently contradicting one."

// EnsureClaudeMD adds ClaudeMDNote to repo/CLAUDE.md (creating it) unless
// the file already mentions the decisions folder. It reports whether it
// added the line. A decisions folder outside the repo is left alone.
func EnsureClaudeMD(repo, decisionsDir string) (bool, error) {
	if repo == "" {
		return false, nil
	}
	rel, err := filepath.Rel(repo, decisionsDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, nil
	}
	rel = filepath.ToSlash(rel)
	path := filepath.Join(repo, "CLAUDE.md")
	src, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if bytes.Contains(src, []byte(rel)) {
		return false, nil
	}
	var b strings.Builder
	if len(src) > 0 {
		if !bytes.HasSuffix(src, []byte("\n")) {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, ClaudeMDNote+"\n", rel)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return false, err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close()
		return false, err
	}
	return true, f.Close()
}

// Logged is the result of LogDecision.
type Logged struct {
	Decision
	// Noted reports that the CLAUDE.md line was added just now.
	Noted bool
	// NoteErr is a failure to update CLAUDE.md; the decision is still
	// written.
	NoteErr error
}

// LogDecision writes a new decision under docsRoot and points the repo's
// CLAUDE.md at the decision log.
func LogDecision(docsRoot, repo string, in DecisionInput, now time.Time) (Logged, error) {
	d, err := NewDecision(docsRoot, in, now)
	if err != nil {
		return Logged{}, err
	}
	noted, noteErr := EnsureClaudeMD(repo, filepath.Join(docsRoot, DecisionsDir))
	return Logged{Decision: d, Noted: noted, NoteErr: noteErr}, nil
}
