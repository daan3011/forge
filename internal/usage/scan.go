package usage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/store"
)

// Other is the project of usage that matches no forge project.
const Other = "other"

// headLen is how much of the start of a file identifies it: a replaced
// transcript has a different first line.
const headLen = 1 << 10

// Ledger is the usage store; *store.Usage satisfies it.
type Ledger interface {
	UsageFile(ctx context.Context, path string) (store.UsageFile, bool, error)
	Ingest(ctx context.Context, f store.UsageFile, msgs []store.UsageMessage) error
	ForgetFile(ctx context.Context, path string) error
	LedgerGroups(ctx context.Context) ([]store.LedgerGroup, error)
	ReplaceRollups(ctx context.Context, rows []store.UsageRow) error
	Rows(ctx context.Context, since time.Time, claudeSessionID string) ([]store.UsageRow, error)
}

// SessionLister lists forge's sessions; *store.Sessions satisfies it.
type SessionLister interface {
	List(ctx context.Context, f store.SessionFilter) ([]store.Session, error)
}

// ScanResult describes one scan.
type ScanResult struct {
	Files    int // transcript files found
	Read     int // files with new bytes read
	Messages int // usage lines ingested (before dedupe)
	Bad      int // malformed lines skipped
}

// transcriptFile is one file found under the projects dir.
type transcriptFile struct {
	path    string
	session string // the Claude Code session it belongs to
	size    int64
}

// pending is what a scan read from one file, ingested afterwards.
type pending struct {
	state store.UsageFile
	msgs  []store.UsageMessage
	first time.Time // earliest message, for the ingest order
}

// findFiles lists dir/*/*.jsonl (sessions) and dir/*/*/subagents/*.jsonl
// (their subagents, which count towards the parent session).
func findFiles(dir string) ([]transcriptFile, error) {
	if dir == "" {
		return nil, nil
	}
	var out []transcriptFile
	add := func(pattern string, session func(path string) string) error {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			return fmt.Errorf("usage: list transcripts: %w", err)
		}
		for _, m := range matches {
			fi, err := os.Stat(m)
			if err != nil || !fi.Mode().IsRegular() {
				continue
			}
			out = append(out, transcriptFile{path: m, session: session(m), size: fi.Size()})
		}
		return nil
	}
	if err := add(filepath.Join("*", "*.jsonl"), func(p string) string {
		return strings.TrimSuffix(filepath.Base(p), ".jsonl")
	}); err != nil {
		return nil, err
	}
	if err := add(filepath.Join("*", "*", "subagents", "*.jsonl"), func(p string) string {
		return filepath.Base(filepath.Dir(filepath.Dir(p)))
	}); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

// readFile reads what f gained since its stored state. ok is false when
// nothing changed. A file that shrank or whose first bytes changed was
// replaced: its old messages are forgotten and it is read from the start.
func readFile(ctx context.Context, l Ledger, f transcriptFile, res *ScanResult) (p pending, ok bool, err error) {
	st, known, err := l.UsageFile(ctx, f.path)
	if err != nil {
		return pending{}, false, err
	}
	if known && f.size == st.Size {
		return pending{}, false, nil
	}
	fh, err := os.Open(f.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return pending{}, false, nil
		}
		return pending{}, false, fmt.Errorf("usage: open transcript: %w", err)
	}
	defer fh.Close()
	offset := int64(0)
	if known {
		if f.size < st.Offset || headHash(fh, st.HeadLen) != st.Head {
			if err := l.ForgetFile(ctx, f.path); err != nil {
				return pending{}, false, err
			}
		} else {
			offset = st.Offset
		}
	}
	if _, err := fh.Seek(offset, io.SeekStart); err != nil {
		return pending{}, false, fmt.Errorf("usage: seek transcript: %w", err)
	}
	buf, err := io.ReadAll(io.LimitReader(fh, f.size-offset))
	if err != nil {
		return pending{}, false, fmt.Errorf("usage: read transcript: %w", err)
	}
	// Only complete lines; a trailing partial line is read next time.
	end := bytes.LastIndexByte(buf, '\n') + 1
	for rest := buf[:end]; len(rest) > 0; {
		i := bytes.IndexByte(rest, '\n')
		m, good, bad := ParseLine(rest[:i])
		rest = rest[i+1:]
		if bad {
			res.Bad++
		}
		if !good {
			continue
		}
		p.msgs = append(p.msgs, store.UsageMessage{
			Key: m.Key, ClaudeSessionID: f.session, Cwd: m.Cwd, Model: m.Model, Time: m.Time, Tokens: m.Tokens,
		})
		if p.first.IsZero() || m.Time.Before(p.first) {
			p.first = m.Time
		}
	}
	newOffset := offset + int64(end)
	n := min(newOffset, headLen)
	p.state = store.UsageFile{
		Path: f.path, Size: f.size, Offset: newOffset, Head: headHash(fh, n), HeadLen: n,
	}
	if known && offset > 0 {
		// Already-read files go first: their messages predate new files.
		p.first = time.Time{}
	}
	return p, true, nil
}

// headHash hashes the first n bytes of r.
func headHash(r io.ReaderAt, n int64) uint64 {
	buf := make([]byte, n)
	m, _ := r.ReadAt(buf, 0)
	h := fnv.New64a()
	_, _ = h.Write(buf[:m])
	return h.Sum64()
}

// Resolver maps usage to a forge project.
type Resolver struct {
	sessions map[string]store.Session // by Claude session id
	projects []config.Project         // with a path, longest first
}

// NewResolver builds a Resolver from forge's sessions and projects.
func NewResolver(sessions []store.Session, projects []config.Project) Resolver {
	r := Resolver{sessions: make(map[string]store.Session, len(sessions))}
	for _, x := range sessions {
		r.sessions[x.ClaudeSessionID] = x
	}
	for _, p := range projects {
		if p.Path != "" && p.Name != "" {
			r.projects = append(r.projects, p)
		}
	}
	sort.SliceStable(r.projects, func(i, j int) bool { return len(r.projects[i].Path) > len(r.projects[j].Path) })
	return r
}

// Project is the forge session's project, else the project whose checkout
// holds cwd, else Other.
func (r Resolver) Project(claudeSessionID, cwd string) string {
	if x, ok := r.sessions[claudeSessionID]; ok && x.Project != "" {
		return x.Project
	}
	cwd = filepath.Clean(cwd)
	for _, p := range r.projects {
		root := filepath.Clean(p.Path)
		if cwd == root || strings.HasPrefix(cwd, root+string(filepath.Separator)) {
			return p.Name
		}
	}
	return Other
}

// Session returns the forge session with this Claude session id.
func (r Resolver) Session(claudeSessionID string) (store.Session, bool) {
	x, ok := r.sessions[claudeSessionID]
	return x, ok
}
