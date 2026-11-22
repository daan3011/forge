package transcript

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"time"
)

// snapshotVersion is bumped whenever the parser state changes shape, so an
// old snapshot is ignored (and the file reparsed) instead of misread.
const snapshotVersion = 1

// headLen is how much of the start of a file identifies it: a replaced
// transcript has a different first line.
const headLen = 1 << 10

// snapshot is the persisted state of one file in the Cache (P5.3).
type snapshot struct {
	Version int   `json:"v"`
	Offset  int64 `json:"offset"`
	// Head hashes the first HeadLen bytes of the file.
	Head    uint64 `json:"head"`
	HeadLen int64  `json:"head_len"`

	Entries  []Entry             `json:"entries"`
	Tools    map[string]int      `json:"tools"`
	Usage    map[string]usage    `json:"usage"`
	LastText map[string]int      `json:"last_text"`
	Files    map[string]struct{} `json:"files"`
	Last     time.Time           `json:"last"`
	Chars    int64               `json:"chars"`
	Skipped  int                 `json:"skipped"`
}

func encodeSnapshot(p *Parser, offset int64, head uint64, headN int64) ([]byte, error) {
	s := snapshot{
		Version: snapshotVersion, Offset: offset, Head: head, HeadLen: headN,
		Entries: p.entries, Tools: p.tools, Usage: p.usage, LastText: p.lastText,
		Files: p.files, Last: p.last, Chars: p.chars, Skipped: p.skipped,
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(s); err != nil {
		return nil, fmt.Errorf("encode transcript snapshot: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("encode transcript snapshot: %w", err)
	}
	return buf.Bytes(), nil
}

// decodeSnapshot restores a parser; ok is false for anything unusable.
func decodeSnapshot(b []byte) (p *Parser, s snapshot, ok bool) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, s, false
	}
	defer zr.Close()
	if err := json.NewDecoder(io.LimitReader(zr, 1<<30)).Decode(&s); err != nil || s.Version != snapshotVersion {
		return nil, s, false
	}
	p = NewParser()
	p.entries, p.last, p.chars, p.skipped = s.Entries, s.Last, s.Chars, s.Skipped
	for k, v := range s.Tools {
		p.tools[k] = v
	}
	for k, v := range s.Usage {
		p.usage[k] = v
	}
	for k, v := range s.LastText {
		p.lastText[k] = v
	}
	for k := range s.Files {
		p.files[k] = struct{}{}
	}
	return p, s, true
}

// headHash hashes the first n bytes of r.
func headHash(r io.ReaderAt, n int64) uint64 {
	buf := make([]byte, n)
	m, _ := r.ReadAt(buf, 0)
	h := fnv.New64a()
	_, _ = h.Write(buf[:m])
	return h.Sum64()
}
