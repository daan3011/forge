package docs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Sending docs to an agent (P4.3): each doc becomes an @path mention
// relative to the session's worktree. Docs inside the repo keep their path
// (the worktree has the same layout); docs outside it are copied into
// CopyDir, which is git-ignored. When the worktree's copy of an in-repo doc
// differs from the source, the branch's file is left alone and the source
// goes into CopyDir too, mentioned with a note saying which is which.

// CopyDir is where docs from outside the repo are copied in a worktree,
// slash-separated and relative to it. It is not config.DefaultDocsDir, so
// copies never mix with (or git-ignore) a project's own docs.
const CopyDir = ".forge/attached"

// Notes written after the mention of a doc whose worktree version differs.
const (
	NewerNote    = "(newer version than on this branch)"
	DivergedNote = "(version from the main checkout; this branch has its own changes to this file)"
)

// Attachment is a doc handed to an agent session.
type Attachment struct {
	Src string // absolute path of the doc
	Rel string // slash-separated path relative to the worktree
	// Copy is set for docs that go into CopyDir: from outside the repo, or
	// differing from the branch's version (Newer / Diverged).
	Copy bool
	// Newer: the branch has the doc unchanged since it left its base, so
	// the source is the newer version.
	Newer bool
	// Diverged: the branch changed the doc itself (or that couldn't be
	// told), so the source is just the main checkout's version.
	Diverged bool
}

// Plan maps srcs (absolute doc paths) to worktree paths for a repo whose
// main checkout is repoRoot. Duplicates are dropped; copied names that
// clash get -2, -3, … before the extension.
func Plan(repoRoot string, srcs []string) []Attachment {
	var out []Attachment
	seenSrc := map[string]bool{}
	taken := map[string]bool{}
	for _, src := range srcs {
		src = filepath.Clean(src)
		if seenSrc[src] {
			continue
		}
		seenSrc[src] = true
		if rel, ok := inside(repoRoot, src); ok {
			taken[rel] = true
			out = append(out, Attachment{Src: src, Rel: rel})
			continue
		}
		rel := uniqueRel(taken, CopyDir+"/"+filepath.Base(src))
		out = append(out, Attachment{Src: src, Rel: rel, Copy: true})
	}
	return out
}

// uniqueRel returns rel, or rel with -2, -3, … before the extension, so
// that it isn't in taken; the result is added to taken.
func uniqueRel(taken map[string]bool, rel string) string {
	ext := path.Ext(rel)
	stem := strings.TrimSuffix(rel, ext)
	out := rel
	for n := 2; taken[out]; n++ {
		out = fmt.Sprintf("%s-%d%s", stem, n, ext)
	}
	taken[out] = true
	return out
}

// inside returns src relative to root (slash-separated) when src is in it.
func inside(root, src string) (string, bool) {
	if root == "" {
		return "", false
	}
	rel, err := filepath.Rel(filepath.Clean(root), src)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// ChangedFunc reports whether the session's branch changed the file at rel
// (relative to the worktree) since it left its base, committed or not.
type ChangedFunc func(rel string) (bool, error)

// Materialize puts the attachments into worktree and returns them as they
// ended up, with how many went into CopyDir:
//
//   - Copy docs are (re)copied into CopyDir.
//   - An in-repo doc the worktree lacks (an uncommitted draft) is copied to
//     its own path.
//   - An in-repo doc the worktree has with the same content is left alone.
//   - One it has with different content is left alone too; the source goes
//     to CopyDir/<Rel> instead, marked Newer when changed says the branch
//     didn't touch the file, else Diverged (also when changed fails). A nil
//     changed counts as untouched.
//
// A file the branch has is never overwritten.
func Materialize(worktree string, as []Attachment, changed ChangedFunc) (out []Attachment, copied int, err error) {
	if worktree == "" {
		return nil, 0, errors.New("no worktree")
	}
	taken := map[string]bool{}
	for _, a := range as {
		taken[a.Rel] = true
	}
	out = make([]Attachment, 0, len(as))
	for _, a := range as {
		if !a.Copy {
			b, rerr := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(a.Rel)))
			switch {
			case errors.Is(rerr, fs.ErrNotExist):
			case rerr != nil:
				return out, copied, rerr
			default:
				src, err := os.ReadFile(a.Src)
				if err != nil {
					return out, copied, fmt.Errorf("read %s: %w", filepath.Base(a.Src), err)
				}
				if bytes.Equal(src, b) {
					out = append(out, a)
					continue
				}
				a = divergent(a, taken, changed)
			}
		}
		if err := copyFile(a.Src, filepath.Join(worktree, filepath.FromSlash(a.Rel))); err != nil {
			return out, copied, fmt.Errorf("copy %s: %w", filepath.Base(a.Src), err)
		}
		if a.Copy {
			copied++
		}
		out = append(out, a)
	}
	return out, copied, nil
}

// divergent moves a, whose worktree version differs from the source, into
// CopyDir and says which version is newer.
func divergent(a Attachment, taken map[string]bool, changed ChangedFunc) Attachment {
	branchChanged := false
	if changed != nil {
		var err error
		if branchChanged, err = changed(a.Rel); err != nil {
			branchChanged = true // can't tell: don't claim the source is newer
		}
	}
	a.Rel = uniqueRel(taken, CopyDir+"/"+a.Rel)
	a.Copy, a.Newer, a.Diverged = true, !branchChanged, branchChanged
	return a
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// mention is a's bare file mention: @docs/a.md, or @"docs/with space.md".
func (a Attachment) mention() string {
	if strings.ContainsAny(a.Rel, " \t") {
		return `@"` + a.Rel + `"`
	}
	return "@" + a.Rel
}

// Mention is a's file mention, followed by a note when the worktree has a
// different version of the doc.
func (a Attachment) Mention() string {
	switch {
	case a.Newer:
		return a.mention() + " " + NewerNote
	case a.Diverged:
		return a.mention() + " " + DivergedNote
	}
	return a.mention()
}

// Mentions returns the attachments' mentions separated by spaces.
func Mentions(as []Attachment) string {
	parts := make([]string, len(as))
	for i, a := range as {
		parts[i] = a.Mention()
	}
	return strings.Join(parts, " ")
}

// Rewrite updates a prompt rendered from before (Plan's attachments) to
// after (what Materialize did): each mention of a doc that moved or got a
// note is replaced, as a whole token, by its new mention.
func Rewrite(prompt string, before, after []Attachment) string {
	for i := range min(len(before), len(after)) {
		old, upd := before[i].Mention(), after[i].Mention()
		if old != upd {
			prompt = replaceToken(prompt, old, upd)
		}
	}
	return prompt
}

// replaceToken replaces old in s where it stands alone: at the start or
// after whitespace, and followed by the end, whitespace or punctuation
// that ends a sentence (not more path, as in @a.md.bak).
func replaceToken(s, old, upd string) string {
	var b strings.Builder
	from := 0 // s[:from] is written
	for at := 0; ; {
		i := strings.Index(s[at:], old)
		if i < 0 {
			break
		}
		i += at
		end := i + len(old)
		if startsToken(s, i) && endsToken(s, end) {
			b.WriteString(s[from:i])
			b.WriteString(upd)
			from = end
		}
		at = end
	}
	b.WriteString(s[from:])
	return b.String()
}

func startsToken(s string, i int) bool { return i == 0 || isSpace(s[i-1]) }

func endsToken(s string, end int) bool {
	if end == len(s) || isSpace(s[end]) {
		return true
	}
	switch s[end] {
	case ',', ';', ':', ')', '!', '?':
		return true
	case '.':
		return end+1 == len(s) || isSpace(s[end+1])
	}
	return false
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
