package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/docs"
	"github.com/daan3011/forge/internal/store"
)

// Size limits for docs.
const (
	maxDocWrite = 512 << 10
	maxDocRead  = 256 << 10
)

// docsRoot is the session worktree's .forge/docs: the only place docs
// tools read or write.
func (s *Server) docsRoot() (string, error) {
	wt, err := s.worktree()
	if err != nil {
		return "", err
	}
	return filepath.Join(wt, filepath.FromSlash(config.DefaultDocsDir)), nil
}

// docPath validates an agent-given doc path and returns it relative
// (slash-separated, ending in .md) and absolute. Besides docs.CleanRel's
// checks (relative, no "..", no hidden parts) no symlink may sit anywhere
// between the worktree and the file, so a link can't lead outside
// .forge/docs.
func (s *Server) docPath(name string) (rel, abs string, err error) {
	root, err := s.docsRoot()
	if err != nil {
		return "", "", err
	}
	rel, err = docs.CleanRel(name)
	if err != nil {
		return "", "", fmt.Errorf("path %q: %w (paths are relative to .forge/docs, e.g. notes/retries.md)", name, err)
	}
	abs = filepath.Join(root, filepath.FromSlash(rel))
	if err := s.noSymlinks(abs); err != nil {
		return "", "", fmt.Errorf("path %q: %w", name, err)
	}
	return rel, abs, nil
}

// noSymlinks checks that no existing component of target below the session
// worktree (.forge and .forge/docs included) is a symlink.
func (s *Server) noSymlinks(target string) error {
	wt, err := s.worktree()
	if err != nil {
		return err
	}
	return noSymlinksBelow(wt, target)
}

// noSymlinksBelow checks that no existing component of target below base
// (base itself is trusted) is a symlink.
func noSymlinksBelow(base, target string) error {
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("outside the session's docs folder")
	}
	cur := base
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return errors.New("goes through a symlink; docs tools only follow real folders inside .forge/docs")
		}
	}
	return nil
}

// WriteDocIn is write_doc's input.
type WriteDocIn struct {
	Path    string `json:"path" jsonschema:"Doc path relative to .forge/docs, e.g. 'notes/plane-retries.md' (.md is added if missing)."`
	Content string `json:"content" jsonschema:"Markdown content."`
	Mode    string `json:"mode,omitempty" jsonschema:"create (default: fails if the doc exists), overwrite (replace it) or append (add to the end, creating it if needed)."`
}

// DocOut describes a written or read doc.
type DocOut struct {
	Path    string `json:"path" jsonschema:"Relative to .forge/docs."`
	Bytes   int    `json:"bytes"`
	Message string `json:"message,omitempty"`
}

func (s *Server) writeDoc(ctx context.Context, _ *mcp.CallToolRequest, in WriteDocIn) (*mcp.CallToolResult, DocOut, error) {
	rel, abs, err := s.docPath(in.Path)
	if err != nil {
		return nil, DocOut{}, err
	}
	if len(in.Content) > maxDocWrite {
		return nil, DocOut{}, fmt.Errorf("content is %d bytes; the limit is %d", len(in.Content), maxDocWrite)
	}
	root, _ := s.docsRoot()
	var verb string
	switch in.Mode {
	case "", "create":
		if _, err := docs.Create(root, rel, in.Content); err != nil {
			if errors.Is(err, docs.ErrExists) {
				return nil, DocOut{}, fmt.Errorf("%s already exists; read_doc it, then use mode overwrite or append", rel)
			}
			return nil, DocOut{}, err
		}
		verb = "created"
	case "overwrite":
		if err := writeFile(abs, []byte(in.Content)); err != nil {
			return nil, DocOut{}, err
		}
		verb = "overwrote"
	case "append":
		if err := appendFile(abs, in.Content); err != nil {
			return nil, DocOut{}, err
		}
		verb = "appended to"
	default:
		return nil, DocOut{}, fmt.Errorf("mode %q: use create, overwrite or append", in.Mode)
	}
	if err := s.attribute(ctx, store.DocRef(abs), store.LinkMeta{Title: rel}); err != nil {
		return nil, DocOut{}, err
	}
	return nil, DocOut{
		Path: rel, Bytes: len(in.Content),
		Message: verb + " .forge/docs/" + rel + " in this session's worktree; commit it with your changes so it reaches the main checkout.",
	}, nil
}

func writeFile(abs string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(abs), ".doc-*.md")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	_, werr := f.Write(data)
	if err := errors.Join(werr, f.Close(), os.Chmod(tmp, 0o644)); err != nil {
		return err
	}
	return os.Rename(tmp, abs)
}

func appendFile(abs, content string) error {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	old, err := os.ReadFile(abs)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if len(old)+len(content) > maxDocWrite {
		return fmt.Errorf("the doc would grow past %d bytes", maxDocWrite)
	}
	f, err := os.OpenFile(abs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	sep := ""
	if len(old) > 0 && !strings.HasSuffix(string(old), "\n") {
		sep = "\n"
	}
	_, werr := f.WriteString(sep + content)
	return errors.Join(werr, f.Close())
}

// ReadDocIn is read_doc's input.
type ReadDocIn struct {
	Path string `json:"path" jsonschema:"Doc path relative to .forge/docs, as list_docs shows it."`
}

// ReadDocOut is read_doc's result.
type ReadDocOut struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

func (s *Server) readDoc(_ context.Context, _ *mcp.CallToolRequest, in ReadDocIn) (*mcp.CallToolResult, ReadDocOut, error) {
	rel, abs, err := s.docPath(in.Path)
	if err != nil {
		return nil, ReadDocOut{}, err
	}
	b, err := os.ReadFile(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ReadDocOut{}, fmt.Errorf("no doc %s; list_docs shows what exists", rel)
	}
	if err != nil {
		return nil, ReadDocOut{}, err
	}
	out := ReadDocOut{Path: rel}
	if len(b) > maxDocRead {
		b, out.Truncated = b[:maxDocRead], true
	}
	out.Content = string(b)
	return nil, out, nil
}

// ListDocsIn is list_docs' input (none).
type ListDocsIn struct{}

// DocEntry is one doc in list_docs.
type DocEntry struct {
	Path     string `json:"path"`
	Modified string `json:"modified"`
}

// ListDocsOut is list_docs' result.
type ListDocsOut struct {
	Root string     `json:"root" jsonschema:"Absolute path of the session's .forge/docs."`
	Docs []DocEntry `json:"docs"`
}

func (s *Server) listDocs(_ context.Context, _ *mcp.CallToolRequest, _ ListDocsIn) (*mcp.CallToolResult, ListDocsOut, error) {
	root, err := s.docsRoot()
	if err != nil {
		return nil, ListDocsOut{}, err
	}
	if err := s.noSymlinks(root); err != nil {
		return nil, ListDocsOut{}, err
	}
	tree, err := docs.Scan(root)
	if err != nil {
		return nil, ListDocsOut{}, err
	}
	out := ListDocsOut{Root: root, Docs: []DocEntry{}}
	var walk func(n *docs.Node)
	walk = func(n *docs.Node) {
		if !n.IsDir {
			out.Docs = append(out.Docs, DocEntry{Path: n.Rel, Modified: n.ModTime.Format("2006-01-02 15:04")})
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(tree)
	return nil, out, nil
}

// LogDecisionIn is log_decision's input.
type LogDecisionIn struct {
	Title        string `json:"title" jsonschema:"The decision in a few words, e.g. 'Retry Plane requests with exponential backoff'."`
	Context      string `json:"context,omitempty" jsonschema:"Markdown: the forces and constraints that led here."`
	Decision     string `json:"decision" jsonschema:"Markdown: what was decided."`
	Alternatives string `json:"alternatives,omitempty" jsonschema:"Markdown: options considered and why not."`
	Consequences string `json:"consequences,omitempty" jsonschema:"Markdown: what follows, good and bad."`
}

// LogDecisionOut is log_decision's result.
type LogDecisionOut struct {
	Number int    `json:"number"`
	Path   string `json:"path" jsonschema:"Relative to .forge/docs."`
}

func (s *Server) logDecision(ctx context.Context, _ *mcp.CallToolRequest, in LogDecisionIn) (*mcp.CallToolResult, LogDecisionOut, error) {
	root, err := s.docsRoot()
	if err != nil {
		return nil, LogDecisionOut{}, err
	}
	dir := filepath.Join(root, filepath.FromSlash(docs.DecisionsDir))
	if err := s.noSymlinks(dir); err != nil {
		return nil, LogDecisionOut{}, err
	}
	di := docs.DecisionInput{
		Title: in.Title, Context: in.Context, Decision: in.Decision,
		Alternatives: in.Alternatives, Consequences: in.Consequences,
	}
	if err := di.Validate(); err != nil {
		return nil, LogDecisionOut{}, err
	}
	// NewDecision, not LogDecision: that also edits CLAUDE.md, which is
	// outside .forge/docs.
	d, err := docs.NewDecision(root, di, s.d.Now())
	if err != nil {
		return nil, LogDecisionOut{}, err
	}
	if err := s.attribute(ctx, store.DocRef(d.Path), store.LinkMeta{Title: d.Rel}); err != nil {
		return nil, LogDecisionOut{}, err
	}
	return nil, LogDecisionOut{Number: d.Number, Path: d.Rel}, nil
}

func (s *Server) addDocTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "write_doc",
		Description: "Write a markdown doc into this project's docs folder (.forge/docs in the session worktree): design notes, " +
			"runbooks, investigation write-ups worth keeping. Paths are relative to .forge/docs; nothing outside it can be written. " +
			"The user sees it in forge's Docs tab under 'From sessions'.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false)},
	}, s.writeDoc)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "read_doc",
		Description: "Read a doc from this project's .forge/docs (path as list_docs shows it).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.readDoc)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_docs",
		Description: "List the markdown docs in this project's .forge/docs, including the decision log.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.listDocs)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "log_decision",
		Description: "Record an architecture or design decision you made (an ADR) in the project's decision log under .forge/docs/decisions. " +
			"Use it when you chose between real alternatives, so the reasoning outlives the session.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false)},
	}, s.logDecision)
}
