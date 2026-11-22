package transcript

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/daan3011/forge/internal/sessions/claude"
	"github.com/daan3011/forge/internal/store"
)

// Transcript is a parsed snapshot of one transcript file. It shares nothing
// with the Cache, so the UI can hold on to it.
type Transcript struct {
	Path    string
	Entries []Entry
	Summary Summary
}

// ErrNoTranscript means a session has no transcript file (yet): Claude Code
// only writes one once the conversation has a message.
var ErrNoTranscript = errors.New("no transcript yet")

// Locate finds the transcript of x: the path the hooks reported if that file
// exists, otherwise <projectsDir>/*/<claude session id>.jsonl.
func Locate(x store.Session, projectsDir string) (string, error) {
	if x.TranscriptPath != "" {
		if fi, err := os.Stat(x.TranscriptPath); err == nil && fi.Mode().IsRegular() {
			return x.TranscriptPath, nil
		}
	}
	if p, ok := claude.FindTranscript(projectsDir, x.ClaudeSessionID); ok {
		return p, nil
	}
	return "", ErrNoTranscript
}

// Cache parses transcript files incrementally: it remembers, per file, how
// far it has read and the parser state, so loading a file again only reads
// what was appended since. With a Persister the state also survives a
// restart (P5.3). It is safe for concurrent use.
type Cache struct {
	mu    sync.Mutex
	files map[string]*fileState
	store Persister
}

// Persister keeps parser snapshots between runs. Errors are not fatal: a
// missing snapshot only means the file is parsed from the start.
type Persister interface {
	Get(key string) ([]byte, bool)
	Put(key, project string, v []byte)
}

// KeyPrefix starts every persisted snapshot key; the rest is the path.
const KeyPrefix = "transcript:"

// CacheKind is the kv_cache kind of persisted snapshots.
const CacheKind = "transcript"

type fileState struct {
	mu       sync.Mutex
	info     os.FileInfo
	offset   int64
	parser   *Parser
	restored bool // a snapshot was looked up
}

// NewCache returns an empty Cache; store may be nil.
func NewCache(store Persister) *Cache {
	return &Cache{files: map[string]*fileState{}, store: store}
}

// Reset forgets every in-memory parser state, e.g. after the persisted
// snapshots were cleared.
func (c *Cache) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.files = map[string]*fileState{}
}

func (c *Cache) state(path string) *fileState {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.files[path]
	if !ok {
		st = &fileState{parser: NewParser()}
		c.files[path] = st
	}
	return st
}

// Load parses path, reading only bytes appended since the previous Load (or
// the persisted snapshot). If the file was replaced or truncated it starts
// over. A trailing line without a newline (still being written) is left for
// the next Load. project tags the persisted snapshot.
func (c *Cache) Load(path, project string) (Transcript, error) {
	st := c.state(path)
	st.mu.Lock()
	defer st.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Transcript{}, ErrNoTranscript
		}
		return Transcript{}, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Transcript{}, fmt.Errorf("stat transcript: %w", err)
	}
	if st.info != nil && (!os.SameFile(st.info, fi) || fi.Size() < st.offset) {
		st.offset = 0
		st.parser = NewParser()
	}
	if st.info == nil && !st.restored {
		st.restored = true
		c.restore(st, path, f, fi.Size())
	}
	st.info = fi

	before := st.offset
	if fi.Size() > st.offset {
		if _, err := f.Seek(st.offset, io.SeekStart); err != nil {
			return Transcript{}, fmt.Errorf("seek transcript: %w", err)
		}
		buf, err := io.ReadAll(io.LimitReader(f, fi.Size()-st.offset))
		if err != nil {
			return Transcript{}, fmt.Errorf("read transcript: %w", err)
		}
		st.offset += feedLines(st.parser, buf)
	}
	if st.offset != before {
		c.persist(st, path, project, f)
	}
	return Transcript{Path: path, Entries: st.parser.Entries(), Summary: st.parser.Summary()}, nil
}

// feedLines feeds every complete line of buf to p and returns how many bytes
// it consumed (up to and including the last newline).
func feedLines(p *Parser, buf []byte) int64 {
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		return 0
	}
	rest := buf[:end+1]
	for len(rest) > 0 {
		i := bytes.IndexByte(rest, '\n')
		p.Feed(rest[:i])
		rest = rest[i+1:]
	}
	return int64(end + 1)
}

// restore loads the persisted snapshot of path into st when it still
// matches the file: not shorter than what was read, same first bytes.
func (c *Cache) restore(st *fileState, path string, f *os.File, size int64) {
	if c.store == nil {
		return
	}
	b, ok := c.store.Get(KeyPrefix + path)
	if !ok {
		return
	}
	p, snap, ok := decodeSnapshot(b)
	if !ok || snap.Offset > size || snap.HeadLen > size || headHash(f, snap.HeadLen) != snap.Head {
		return
	}
	st.parser, st.offset = p, snap.Offset
}

func (c *Cache) persist(st *fileState, path, project string, f *os.File) {
	if c.store == nil {
		return
	}
	n := min(st.offset, headLen)
	b, err := encodeSnapshot(st.parser, st.offset, headHash(f, n), n)
	if err != nil {
		return
	}
	c.store.Put(KeyPrefix+path, project, b)
}

// storeTimeout bounds one snapshot read or write.
const storeTimeout = 5 * time.Second

type kvPersister struct{ c *store.Cache }

// StorePersister keeps snapshots in forge's kv_cache (kind CacheKind).
func StorePersister(c *store.Cache) Persister { return kvPersister{c} }

func (p kvPersister) Get(key string) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	e, err := p.c.Get(ctx, key)
	return e.Value, err == nil
}

func (p kvPersister) Put(key, project string, v []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	_ = p.c.Put(ctx, key, CacheKind, project, v, 0)
}
