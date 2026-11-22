package docs

import (
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
// CopyDir, which is git-ignored.

// CopyDir is where docs from outside the repo are copied in a worktree,
// slash-separated and relative to it. It is not config.DefaultDocsDir, so
// copies never mix with (or git-ignore) a project's own docs.
const CopyDir = ".forge/attached"

// Attachment is a doc handed to an agent session.
type Attachment struct {
	Src string // absolute path of the doc
	Rel string // slash-separated path relative to the worktree
	// Copy is set for docs outside the repo: they go into CopyDir.
	Copy bool
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
		base := filepath.Base(src)
		ext := path.Ext(base)
		stem := strings.TrimSuffix(base, ext)
		rel := CopyDir + "/" + base
		for n := 2; taken[rel]; n++ {
			rel = fmt.Sprintf("%s/%s-%d%s", CopyDir, stem, n, ext)
		}
		taken[rel] = true
		out = append(out, Attachment{Src: src, Rel: rel, Copy: true})
	}
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

// Materialize puts the attachments into worktree: copied docs always (they
// are refreshed), in-repo docs only when the worktree lacks them (e.g. an
// uncommitted draft). A file the branch already has is never overwritten.
// It returns how many docs went into CopyDir.
func Materialize(worktree string, as []Attachment) (copied int, err error) {
	if worktree == "" {
		return 0, errors.New("no worktree")
	}
	for _, a := range as {
		dst := filepath.Join(worktree, filepath.FromSlash(a.Rel))
		if !a.Copy {
			if _, err := os.Stat(dst); err == nil {
				continue
			} else if !errors.Is(err, fs.ErrNotExist) {
				return copied, err
			}
		}
		if err := copyFile(a.Src, dst); err != nil {
			return copied, fmt.Errorf("copy %s: %w", filepath.Base(a.Src), err)
		}
		if a.Copy {
			copied++
		}
	}
	return copied, nil
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

// Mentions returns the attachments as Claude Code file mentions separated
// by spaces: @docs/a.md @"docs/with space.md".
func Mentions(as []Attachment) string {
	parts := make([]string, 0, len(as))
	for _, a := range as {
		if strings.ContainsAny(a.Rel, " \t") {
			parts = append(parts, `@"`+a.Rel+`"`)
		} else {
			parts = append(parts, "@"+a.Rel)
		}
	}
	return strings.Join(parts, " ")
}
