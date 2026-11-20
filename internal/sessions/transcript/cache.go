package transcript

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

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
// what was appended since. It is safe for concurrent use.
type Cache struct {
	mu    sync.Mutex
	files map[string]*fileState
}

type fileState struct {
	mu     sync.Mutex
	info   os.FileInfo
	offset int64
	parser *Parser
}

// NewCache returns an empty Cache.
func NewCache() *Cache { return &Cache{files: map[string]*fileState{}} }

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

// Load parses path, reading only bytes appended since the previous Load. If
// the file was replaced or truncated it starts over. A trailing line without
// a newline (still being written) is left for the next Load.
func (c *Cache) Load(path string) (Transcript, error) {
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
	st.info = fi

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
