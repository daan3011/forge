// Package transcript parses Claude Code session transcripts (the JSONL files
// under ~/.claude/projects/) into a readable list of entries plus a summary.
//
// The format is undocumented and changes between Claude Code versions, so the
// parser is defensive: it only understands the "user", "assistant" and
// "system" line types, and skips anything else (and any malformed line).
//
// Things learned from real files (Claude Code v2.1.x):
//   - One API message is written as several lines, one content block per
//     line (thinking, text, tool_use). They share message.id, and each line
//     repeats the same message.usage, so usage must be counted once per id.
//   - A user line's message.content is either the prompt (a string, possibly
//     wrapped in <command-name>/<local-command-stdout> tags) or an array of
//     tool_result blocks answering earlier tool_use blocks.
//   - isSidechain lines belong to subagents; isMeta lines are injected
//     context the person never typed.
package transcript

import (
	"bytes"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/daan3011/forge/internal/store"
)

// Kind is the kind of an Entry.
type Kind int

// Entry kinds.
const (
	// User is a prompt the person typed.
	User Kind = iota
	// Assistant is assistant prose (markdown).
	Assistant
	// Tool is a tool call, with its result once it arrives.
	Tool
	// Notice is a one-line event: a slash command, a compaction, ...
	Notice
)

// Entry is one item of the transcript as the viewer shows it.
type Entry struct {
	Kind Kind
	Time time.Time
	// Text is the prompt, the assistant markdown or the notice.
	Text string

	// Tool calls only.
	ToolID    string
	ToolName  string
	Summary   string          // one-line description of the call
	Input     json.RawMessage // the raw tool input
	Result    string          // the tool result text; "" until it arrives
	HasResult bool
	IsError   bool
}

// toolResultMax caps how much of a tool result is kept per call; the viewer
// only shows the first lines anyway.
const toolResultMax = 16 << 10

// line is the subset of a transcript line the parser reads.
type line struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	Timestamp        time.Time       `json:"timestamp"`
	IsSidechain      bool            `json:"isSidechain"`
	IsMeta           bool            `json:"isMeta"`
	IsCompactSummary bool            `json:"isCompactSummary"`
	Content          json.RawMessage `json:"content"` // system lines
	Message          *message        `json:"message"`
}

type message struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Usage   *usage          `json:"usage"`
}

type usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// Parser turns transcript lines into entries. Feed it lines in file order;
// it keeps the state needed to pair tool results with their calls and to
// count usage once per message, so it can be fed incrementally.
type Parser struct {
	entries []Entry
	// tools maps a tool_use id to its entry index.
	tools map[string]int
	// usage is the last usage seen per assistant message id.
	usage map[string]usage
	// lastText is the entry index of the last assistant text block per
	// message id, so consecutive text blocks of one message merge.
	lastText map[string]int
	files    map[string]struct{}
	last     time.Time
	chars    int64
	skipped  int
}

// NewParser returns an empty Parser.
func NewParser() *Parser {
	return &Parser{
		tools:    map[string]int{},
		usage:    map[string]usage{},
		lastText: map[string]int{},
		files:    map[string]struct{}{},
	}
}

// Skipped is how many lines could not be decoded.
func (p *Parser) Skipped() int { return p.skipped }

// Feed parses one JSONL line. Blank, malformed and unknown lines are ignored.
func (p *Parser) Feed(raw []byte) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return
	}
	var l line
	if err := json.Unmarshal(raw, &l); err != nil {
		p.skipped++
		return
	}
	if l.IsSidechain {
		return
	}
	switch l.Type {
	case "user":
		p.user(l)
	case "assistant":
		p.assistant(l)
	case "system":
		p.system(l)
	default:
		return
	}
	if l.Timestamp.After(p.last) {
		p.last = l.Timestamp
	}
}

func (p *Parser) add(e Entry) {
	p.chars += int64(len(e.Text) + len(e.Input) + len(e.Result))
	p.entries = append(p.entries, e)
}

func (p *Parser) user(l line) {
	if l.Message == nil || len(l.Message.Content) == 0 {
		return
	}
	var s string
	if json.Unmarshal(l.Message.Content, &s) == nil {
		if l.IsMeta {
			return
		}
		if l.IsCompactSummary {
			p.add(Entry{Kind: Notice, Time: l.Timestamp, Text: "conversation compacted"})
			return
		}
		p.prompt(l.Timestamp, s)
		return
	}
	var blocks []block
	if json.Unmarshal(l.Message.Content, &blocks) != nil {
		p.skipped++
		return
	}
	var texts []string
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			p.toolResult(b)
		case "text":
			texts = append(texts, b.Text)
		}
	}
	if len(texts) > 0 && !l.IsMeta {
		p.prompt(l.Timestamp, strings.Join(texts, "\n"))
	}
}

var (
	commandNameRe = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
	commandArgsRe = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
	wrapperRe     = regexp.MustCompile(`(?s)^\s*<(local-command-stdout|local-command-stderr|local-command-caveat|system-reminder|bash-stdout|bash-stderr)>`)
	tagRe         = regexp.MustCompile(`</?[a-z][a-z-]*>`)
)

// prompt adds a user prompt, turning slash-command wrappers into notices.
func (p *Parser) prompt(t time.Time, s string) {
	if m := commandNameRe.FindStringSubmatch(s); m != nil {
		text := strings.TrimSpace(m[1])
		if a := commandArgsRe.FindStringSubmatch(s); a != nil && strings.TrimSpace(a[1]) != "" {
			text += " " + strings.TrimSpace(a[1])
		}
		p.add(Entry{Kind: Notice, Time: t, Text: text})
		return
	}
	if m := wrapperRe.FindStringSubmatch(s); m != nil {
		if m[1] == "local-command-stdout" {
			if out := strings.TrimSpace(tagRe.ReplaceAllString(s, "")); out != "" {
				p.add(Entry{Kind: Notice, Time: t, Text: firstLine(out)})
			}
		}
		return
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	p.add(Entry{Kind: User, Time: t, Text: s})
}

func (p *Parser) toolResult(b block) {
	i, ok := p.tools[b.ToolUseID]
	if !ok {
		return
	}
	e := &p.entries[i]
	text := blockText(b.Content)
	if len(text) > toolResultMax {
		text = text[:toolResultMax]
	}
	p.chars += int64(len(text) - len(e.Result))
	e.Result = text
	e.HasResult = true
	e.IsError = b.IsError
}

// blockText flattens a tool_result content: a string, or an array of blocks
// of which only the text ones are kept.
func blockText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "image":
			parts = append(parts, "[image]")
		}
	}
	return strings.Join(parts, "\n")
}

func (p *Parser) assistant(l line) {
	m := l.Message
	if m == nil {
		return
	}
	if m.Usage != nil && m.ID != "" {
		p.usage[m.ID] = *m.Usage
	}
	var blocks []block
	if json.Unmarshal(m.Content, &blocks) != nil {
		var s string
		if json.Unmarshal(m.Content, &s) == nil && strings.TrimSpace(s) != "" {
			p.add(Entry{Kind: Assistant, Time: l.Timestamp, Text: s})
		}
		return
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			p.assistantText(m.ID, l.Timestamp, b.Text)
		case "tool_use":
			delete(p.lastText, m.ID)
			if _, dup := p.tools[b.ID]; dup && b.ID != "" {
				continue
			}
			p.add(Entry{
				Kind:     Tool,
				Time:     l.Timestamp,
				ToolID:   b.ID,
				ToolName: b.Name,
				Summary:  toolSummary(b.Name, b.Input),
				Input:    b.Input,
			})
			if b.ID != "" {
				p.tools[b.ID] = len(p.entries) - 1
			}
			if f := touchedFile(b.Name, b.Input); f != "" {
				p.files[f] = struct{}{}
			}
		}
	}
}

// assistantText adds a text block, appending it to the previous one when
// both belong to the same message with nothing in between.
func (p *Parser) assistantText(id string, t time.Time, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if i, ok := p.lastText[id]; ok && id != "" && i == len(p.entries)-1 {
		p.entries[i].Text += "\n\n" + text
		p.chars += int64(len(text))
		return
	}
	p.add(Entry{Kind: Assistant, Time: t, Text: text})
	if id != "" {
		p.lastText[id] = len(p.entries) - 1
	}
}

func (p *Parser) system(l line) {
	switch l.Subtype {
	case "compact_boundary":
		p.add(Entry{Kind: Notice, Time: l.Timestamp, Text: "conversation compacted"})
	case "informational":
		var s string
		if json.Unmarshal(l.Content, &s) == nil && strings.TrimSpace(s) != "" {
			p.add(Entry{Kind: Notice, Time: l.Timestamp, Text: firstLine(strings.TrimSpace(s))})
		}
	}
}

// toolSummary is the one-line description of a tool call.
func toolSummary(name string, input json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(input, &in) != nil {
		return ""
	}
	str := func(k string) string {
		s, _ := in[k].(string)
		return firstLine(strings.TrimSpace(s))
	}
	var keys []string
	switch name {
	case "Bash":
		keys = []string{"description", "command"}
	case "Edit", "MultiEdit", "Write", "Read", "NotebookEdit":
		keys = []string{"file_path", "notebook_path"}
	case "Grep", "Glob":
		keys = []string{"pattern"}
	case "Agent", "Task":
		keys = []string{"description", "prompt"}
	case "WebFetch":
		keys = []string{"url"}
	case "WebSearch":
		keys = []string{"query"}
	}
	for _, k := range keys {
		if s := str(k); s != "" {
			return s
		}
	}
	// Otherwise the first string field, in key order for stable output.
	ks := make([]string, 0, len(in))
	for k := range in {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		if s := str(k); s != "" {
			return s
		}
	}
	return ""
}

// touchedFile is the file an editing tool call writes, or "".
func touchedFile(name string, input json.RawMessage) string {
	switch name {
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
	default:
		return ""
	}
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	if json.Unmarshal(input, &in) != nil {
		return ""
	}
	if in.FilePath != "" {
		return in.FilePath
	}
	return in.NotebookPath
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

// Summary describes a whole transcript.
type Summary struct {
	// Messages counts prompts and assistant replies (not tool calls).
	Messages int
	// ToolCalls counts tool calls.
	ToolCalls int
	// FilesTouched are the files written by Edit/Write calls, sorted.
	FilesTouched []string
	LastActivity time.Time
	Tokens       store.Tokens
	// Estimated is true when the transcript had no usage fields and Tokens
	// is a chars/4 estimate (all in Input).
	Estimated bool
}

// Summary computes the summary of everything fed so far.
func (p *Parser) Summary() Summary {
	s := Summary{LastActivity: p.last}
	for _, e := range p.entries {
		switch e.Kind {
		case User, Assistant:
			s.Messages++
		case Tool:
			s.ToolCalls++
		}
	}
	for f := range p.files {
		s.FilesTouched = append(s.FilesTouched, f)
	}
	sort.Strings(s.FilesTouched)
	for _, u := range p.usage {
		s.Tokens.Input += u.InputTokens
		s.Tokens.Output += u.OutputTokens
		s.Tokens.CacheRead += u.CacheReadInputTokens
		s.Tokens.CacheWrite += u.CacheCreationInputTokens
	}
	s.Tokens.Messages = int64(len(p.usage))
	if s.Tokens.Total() == 0 && p.chars > 0 {
		s.Tokens = store.Tokens{Input: p.chars / 4}
		s.Estimated = true
	}
	return s
}

// Entries returns a copy of the entries parsed so far.
func (p *Parser) Entries() []Entry {
	out := make([]Entry, len(p.entries))
	copy(out, p.entries)
	return out
}
