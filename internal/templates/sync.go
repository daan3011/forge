package templates

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ToolsSection is the "forge tools" section (P6.5) every default template
// ends with: it tells the agent forge's MCP tools exist and when to use
// them. It starts with ToolsMarker.
//
//go:embed tools.md
var ToolsSection string

// ToolsMarker opens ToolsSection; a template containing it is up to date.
const ToolsMarker = "<!-- forge:mcp-tools"

// InsertBeforeTools adds text (a paragraph) to prompt: just before the
// forge tools section when prompt has one, else at the end.
func InsertBeforeTools(prompt, text string) string {
	text = strings.TrimRight(text, "\n") + "\n"
	i := strings.Index(prompt, ToolsMarker)
	if i < 0 {
		return strings.TrimRight(prompt, "\n") + "\n\n" + text
	}
	head := strings.TrimRight(prompt[:i], "\n")
	if head == "" {
		return text + "\n" + prompt[i:]
	}
	return head + "\n\n" + text + "\n" + prompt[i:]
}

// Change is a template Sync appended the tools section to.
type Change struct {
	Path  string
	Added string // the text appended
}

// Sync appends ToolsSection to every template in dir (*.md) that lacks
// ToolsMarker; templates that have it are left alone, so running it again
// changes nothing. With dryRun nothing is written. A missing dir has
// nothing to sync. Files that can't be read or written are reported
// together; the others are still synced.
func Sync(dir string, dryRun bool) ([]Change, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("templates: %w", err)
	}
	var (
		out  []Change
		errs []error
	)
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Name(), err))
			continue
		}
		if strings.Contains(string(data), ToolsMarker) {
			continue
		}
		added := ToolsSection
		switch {
		case len(data) == 0:
		case strings.HasSuffix(string(data), "\n\n"):
		case strings.HasSuffix(string(data), "\n"):
			added = "\n" + added
		default:
			added = "\n\n" + added
		}
		if !dryRun {
			if err := replaceFile(path, append(data, added...)); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", e.Name(), err))
				continue
			}
		}
		out = append(out, Change{Path: path, Added: added})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, errors.Join(errs...)
}

// Diff renders c as unified-diff-style added lines.
func (c Change) Diff() string {
	var b strings.Builder
	fmt.Fprintf(&b, "+++ %s\n", c.Path)
	for _, l := range strings.Split(strings.TrimSuffix(c.Added, "\n"), "\n") {
		b.WriteString("+" + l + "\n")
	}
	return b.String()
}

// replaceFile writes data to path atomically, keeping its mode.
func replaceFile(path string, data []byte) error {
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmpl-*.md")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	_, werr := f.Write(data)
	if err := errors.Join(werr, f.Close(), os.Chmod(tmp, mode)); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
