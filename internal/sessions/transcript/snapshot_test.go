package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memPersister struct {
	m    map[string][]byte
	proj map[string]string
	puts int
}

func newMem() *memPersister { return &memPersister{m: map[string][]byte{}, proj: map[string]string{}} }

func (p *memPersister) Get(k string) ([]byte, bool) { b, ok := p.m[k]; return b, ok }
func (p *memPersister) Put(k, project string, v []byte) {
	p.m[k], p.proj[k] = v, project
	p.puts++
}

func TestCachePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	prompt := func(s string) string { return `{"type":"user","message":{"content":"` + s + `"}}` + "\n" }
	tool := `{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/a.go"}}],"usage":{"input_tokens":5,"output_tokens":7}}}` + "\n"
	result := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}` + "\n"
	write := func(s string, flag int) {
		t.Helper()
		f, err := os.OpenFile(path, flag|os.O_WRONLY|os.O_CREATE, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(s); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	texts := func(tr Transcript) string {
		var out []string
		for _, e := range tr.Entries {
			out = append(out, e.Text+e.ToolName+e.Result)
		}
		return strings.Join(out, ",")
	}

	mem := newMem()
	write(prompt("a")+tool, os.O_TRUNC)
	var err error
	if _, err = NewCache(mem).Load(path, "forge"); err != nil {
		t.Fatal(err)
	}
	if mem.puts != 1 || mem.proj[KeyPrefix+path] != "forge" {
		t.Fatalf("puts = %d, project = %q", mem.puts, mem.proj[KeyPrefix+path])
	}

	// Mark the snapshot so a restore is visible: a reparse would say "a".
	p, snap, ok := decodeSnapshot(mem.m[KeyPrefix+path])
	if !ok {
		t.Fatal("snapshot did not decode")
	}
	p.entries[0].Text = "A"
	if mem.m[KeyPrefix+path], err = encodeSnapshot(p, snap.Offset, snap.Head, snap.HeadLen); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func()
		want   string
	}{
		// A fresh cache (a restart) resumes from the snapshot: the tool
		// result pairs with the call parsed before the restart.
		{"restored", func() { write(result, os.O_APPEND) }, "A,Editok"},
		// Different first bytes: the file was replaced.
		{"replaced", func() { write(prompt("b")+tool+result, os.O_TRUNC) }, "b,Editok"},
		{"truncated", func() { write(prompt("c"), os.O_TRUNC) }, "c"},
	}
	for _, tt := range tests {
		tt.mutate()
		tr, err := NewCache(mem).Load(path, "forge")
		if err != nil {
			t.Fatal(err)
		}
		if got := texts(tr); got != tt.want {
			t.Errorf("%s: entries = %q, want %q", tt.name, got, tt.want)
		}
	}

	// A corrupt snapshot is ignored.
	mem.m[KeyPrefix+path] = []byte("junk")
	if tr, err := NewCache(mem).Load(path, ""); err != nil || texts(tr) != "c" {
		t.Fatalf("corrupt snapshot: %q, %v", texts(tr), err)
	}

	// Reset drops in-memory state.
	c := NewCache(nil)
	if _, err := c.Load(path, ""); err != nil {
		t.Fatal(err)
	}
	c.Reset()
	if len(c.files) != 0 {
		t.Fatal("Reset kept state")
	}
}
