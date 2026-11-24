package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Set is one value Merge writes: Key is a dotted path into the config
// ("plane.workspace_slug", "project_roots"), Value any YAML-encodable value
// (string, bool, int, []string).
type Set struct {
	Key   string
	Value any
}

// Merge writes sets into the config file at path, keeping everything else
// (comments included) as it is. Keys that are missing, or only present as a
// commented-out line, are added at the end of their section (a new section
// at the end of the file). The result is
// validated like Load before anything is written, and the file is replaced
// atomically. A missing file starts from the default template.
func Merge(path string, sets ...Set) error {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		data = Template()
	case err != nil:
		return fmt.Errorf("config: %w", err)
	}
	out, err := MergeBytes(data, sets...)
	if err != nil {
		return err
	}
	if _, err := parse(path, out); err != nil {
		return fmt.Errorf("config: the merged file would be invalid: %w", err)
	}
	return writeAtomic(path, out)
}

// MergeBytes is Merge on the file's contents. It edits the text in place:
// only the lines of the keys it sets change, so comments, blank lines and
// indentation elsewhere stay exactly as they were.
func MergeBytes(data []byte, sets ...Set) ([]byte, error) {
	for _, s := range sets {
		var err error
		if data, err = setText(data, strings.Split(s.Key, "."), s.Value); err != nil {
			return nil, fmt.Errorf("config: %s: %w", s.Key, err)
		}
	}
	return data, nil
}

// setText sets the value at keys in the YAML text data.
func setText(data []byte, keys []string, v any) ([]byte, error) {
	if slices.Contains(keys, "") {
		return nil, errors.New("empty key")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	if len(doc.Content) == 0 { // empty or comments only: append at the end
		return appendLines(lines, trimTrailingBlank(lines), render(keys, v, 0, 0)), nil
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return nil, errors.New("the top level is not a mapping")
	}
	for depth, k := range keys {
		key, val := lookup(m, k)
		last := depth == len(keys)-1
		switch {
		case key == nil:
			// Add the rest of the path to this section.
			indent, at := 0, len(lines)
			if depth > 0 {
				indent, at = sectionIndent(m), lastLine(m)
			}
			if depth == 0 {
				at = trimTrailingBlank(lines)
			}
			return appendLines(lines, at, render(keys[depth:], v, indent, styleOf(nil))), nil
		case last:
			indent := key.Column - 1
			end := max(lastLine(val), key.Line)
			block := render(keys[depth:depth+1], v, indent, styleOf(val))
			// Keep a comment at the end of a one-line value.
			if end == key.Line && val.LineComment != "" && len(block) == 1 {
				block[0] += " " + val.LineComment
			}
			out := append(append(slices.Clone(lines[:key.Line-1]), block...), lines[end:]...)
			return []byte(strings.Join(out, "\n")), nil
		case val.Kind == yaml.MappingNode && len(val.Content) > 0:
			m = val
		case val.Kind == yaml.ScalarNode && (val.Tag == "!!null" || val.Value == ""):
			// "projects:" with only comments under it: add right below the key.
			return appendLines(lines, key.Line, render(keys[depth+1:], v, key.Column-1+2, styleOf(nil))), nil
		default:
			return nil, fmt.Errorf("%s is not a section", k)
		}
	}
	return data, nil
}

func lookup(m *yaml.Node, k string) (key, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == k {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

// sectionIndent is the indentation of the keys in mapping m.
func sectionIndent(m *yaml.Node) int {
	if len(m.Content) == 0 {
		return 0
	}
	return m.Content[0].Column - 1
}

// lastLine is the last line (1-based) holding part of n.
func lastLine(n *yaml.Node) int {
	if n == nil {
		return 0
	}
	l := n.Line
	if n.Kind == yaml.ScalarNode && (n.Style&(yaml.LiteralStyle|yaml.FoldedStyle)) != 0 {
		l += strings.Count(strings.TrimRight(n.Value, "\n"), "\n") + 1
	}
	for _, c := range n.Content {
		l = max(l, lastLine(c))
	}
	return l
}

func trimTrailingBlank(lines []string) int {
	n := len(lines)
	for n > 0 && strings.TrimSpace(lines[n-1]) == "" {
		n--
	}
	return n
}

// appendLines inserts block after the first at lines.
func appendLines(lines []string, at int, block []string) []byte {
	out := append(append(slices.Clone(lines[:at]), block...), lines[at:]...)
	s := strings.Join(out, "\n")
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return []byte(s)
}

// styleOf is the style a new value keeps from the old one: a flow list
// stays a flow list.
func styleOf(old *yaml.Node) yaml.Style {
	if old != nil && old.Kind == yaml.SequenceNode {
		return old.Style & yaml.FlowStyle
	}
	return 0
}

// render is "k1:\n  k2: v" for keys, indented by indent spaces.
func render(keys []string, v any, indent int, seqStyle yaml.Style) []string {
	val := &yaml.Node{}
	if err := val.Encode(v); err != nil {
		val = &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprint(v)}
	}
	if val.Kind == yaml.SequenceNode {
		val.Style = seqStyle
	}
	for i := len(keys) - 1; i >= 0; i-- {
		val = &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{{Kind: yaml.ScalarNode, Value: keys[i]}, val}}
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	_ = enc.Encode(val)
	_ = enc.Close()
	pad := strings.Repeat(" ", indent)
	out := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	for i := range out {
		out[i] = pad + out[i]
	}
	return out
}

// writeAtomic replaces path with data through a temp file in the same
// folder, keeping the old file's permissions.
func writeAtomic(path string, data []byte) error {
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("config: %w", err)
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return fmt.Errorf("config: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}
