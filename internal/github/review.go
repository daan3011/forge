package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
)

// Review events, as the REST reviews endpoint names them (P2.4).
const (
	ReviewEventApprove        = "APPROVE"
	ReviewEventRequestChanges = "REQUEST_CHANGES"
	ReviewEventComment        = "COMMENT"
)

// maxDiffBytes caps a fetched diff; GitHub refuses much bigger ones anyway.
const maxDiffBytes = 20 << 20

// ErrDiffTooLarge means GitHub won't serve the PR's diff through the API.
var ErrDiffTooLarge = errors.New("diff too large for the GitHub API")

// PRReviewer reads a PR's diff and reviews it (P2.4). repo is "owner/name".
type PRReviewer interface {
	// Diff fetches the PR's unified diff.
	Diff(ctx context.Context, repo string, number int) (string, error)
	// SubmitReview submits a review with event (a ReviewEvent* constant).
	SubmitReview(ctx context.Context, repo string, number int, event, body string) error
}

var _ PRReviewer = (*Client)(nil)

// diffClient is a REST client that asks for diffs instead of JSON; go-gh
// only sets headers per client.
func (c *Client) diffClient() (*api.RESTClient, error) {
	c.diffOnce.Do(func() {
		opts := c.options()
		h := make(map[string]string, len(opts.Headers)+1)
		for k, v := range opts.Headers {
			h[k] = v
		}
		h["Accept"] = "application/vnd.github.v3.diff"
		opts.Headers = h
		c.diffC, c.diffErr = api.NewRESTClient(opts)
	})
	return c.diffC, c.diffErr
}

// Diff implements PRReviewer.
func (c *Client) Diff(ctx context.Context, repo string, number int) (string, error) {
	if _, _, err := splitRepo(repo, number); err != nil {
		return "", err
	}
	r, err := c.diffClient()
	if err != nil {
		return "", fmt.Errorf("github: %w", err)
	}
	resp, err := r.RequestWithContext(ctx, http.MethodGet, fmt.Sprintf("repos/%s/pulls/%d", repo, number), nil)
	if err != nil {
		var he *api.HTTPError
		if errors.As(err, &he) && he.StatusCode == http.StatusNotAcceptable {
			return "", fmt.Errorf("github: %s#%d: %w", repo, number, ErrDiffTooLarge)
		}
		return "", fmt.Errorf("github: %s#%d: %w", repo, number, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxDiffBytes+1))
	if err != nil {
		return "", fmt.Errorf("github: %s#%d: %w", repo, number, err)
	}
	if len(b) > maxDiffBytes {
		return "", fmt.Errorf("github: %s#%d: %w", repo, number, ErrDiffTooLarge)
	}
	return string(b), nil
}

func checkEvent(event string) error {
	switch event {
	case ReviewEventApprove, ReviewEventRequestChanges, ReviewEventComment:
		return nil
	}
	return fmt.Errorf("github: unknown review event %q", event)
}

// SubmitReview implements PRReviewer.
func (c *Client) SubmitReview(ctx context.Context, repo string, number int, event, body string) error {
	if err := checkEvent(event); err != nil {
		return err
	}
	if event != ReviewEventApprove && strings.TrimSpace(body) == "" {
		return errors.New("github: a review that requests changes or comments needs a body")
	}
	payload := map[string]string{"event": event}
	if body != "" {
		payload["body"] = body
	}
	return c.restDo(ctx, http.MethodPost, repo, number, "reviews", payload)
}

// restDo sends body as JSON to repos/<repo>/pulls/<n>/<path>.
func (c *Client) restDo(ctx context.Context, method, repo string, number int, path string, body any) error {
	if _, _, err := splitRepo(repo, number); err != nil {
		return err
	}
	r, err := c.rest()
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("repos/%s/pulls/%d/%s", repo, number, path)
	if err := r.DoWithContext(ctx, method, url, bytes.NewReader(b), nil); err != nil {
		return fmt.Errorf("github: %s#%d: %w", repo, number, err)
	}
	return nil
}

// --- parsing -------------------------------------------------------------------

// File statuses in a diff.
const (
	FileModified = "modified"
	FileAdded    = "added"
	FileDeleted  = "deleted"
	FileRenamed  = "renamed"
	FileBinary   = "binary"
)

// DiffFile is one file of a unified diff.
type DiffFile struct {
	OldPath, NewPath string
	Status           string
	Additions        int
	Deletions        int
	// Text is the file's part of the diff, from its "diff --git" line on,
	// without the trailing newline.
	Text string
}

// Path is the file's current path (the old one for a deleted file).
func (f DiffFile) Path() string {
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

// ParseDiff splits a unified (git) diff into its files.
func ParseDiff(diff string) []DiffFile {
	var (
		files []DiffFile
		cur   *DiffFile
		lines []string
		inHdr bool // before the first hunk of cur
	)
	flush := func() {
		if cur == nil {
			return
		}
		cur.Text = strings.Join(lines, "\n")
		files = append(files, *cur)
		cur, lines = nil, nil
	}
	for _, line := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			cur = &DiffFile{Status: FileModified}
			cur.OldPath, cur.NewPath = gitPaths(strings.TrimPrefix(line, "diff --git "))
			inHdr = true
			lines = append(lines, line)
			continue
		}
		if cur == nil {
			continue // anything before the first file
		}
		lines = append(lines, line)
		if inHdr {
			switch {
			case strings.HasPrefix(line, "@@"):
				inHdr = false
			case strings.HasPrefix(line, "new file mode"):
				cur.Status = FileAdded
			case strings.HasPrefix(line, "deleted file mode"):
				cur.Status = FileDeleted
			case strings.HasPrefix(line, "rename from "):
				cur.Status, cur.OldPath = FileRenamed, strings.TrimPrefix(line, "rename from ")
			case strings.HasPrefix(line, "rename to "):
				cur.Status, cur.NewPath = FileRenamed, strings.TrimPrefix(line, "rename to ")
			case strings.HasPrefix(line, "Binary files ") || line == "GIT binary patch":
				cur.Status = FileBinary
			case strings.HasPrefix(line, "--- "):
				if p := diffPath(line[4:]); p != "" {
					cur.OldPath = p
				}
			case strings.HasPrefix(line, "+++ "):
				if p := diffPath(line[4:]); p != "" {
					cur.NewPath = p
				}
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			cur.Additions++
		case strings.HasPrefix(line, "-"):
			cur.Deletions++
		}
	}
	flush()
	for i := range files {
		switch files[i].Status {
		case FileAdded:
			files[i].OldPath = ""
		case FileDeleted:
			files[i].NewPath = ""
		}
	}
	return files
}

// gitPaths reads "a/x b/y" from a diff --git line. Paths with spaces are
// ambiguous there; the ---/+++ or rename lines correct them later.
func gitPaths(s string) (oldPath, newPath string) {
	if strings.HasPrefix(s, `"`) {
		return "", "" // quoted paths: wait for the ---/+++ lines
	}
	if i := strings.Index(s, " b/"); strings.HasPrefix(s, "a/") && i > 0 {
		return s[2:i], s[i+3:]
	}
	return s, s
}

// diffPath reads a ---/+++ path: "" for /dev/null, without a/ or b/.
func diffPath(s string) string {
	s, _, _ = strings.Cut(s, "\t")
	s = strings.Trim(s, `"`)
	if s == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(s, "a/") || strings.HasPrefix(s, "b/") {
		return s[2:]
	}
	return s
}
