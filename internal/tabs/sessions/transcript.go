package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	glamouransi "charm.land/glamour/v2/ansi"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/sessions/transcript"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/ui/anim"
)

// transcriptPoll is how often an open transcript is re-read. Only appended
// bytes are parsed, so this is cheap.
const transcriptPoll = 2 * time.Second

// toolDetailLines caps an expanded tool call's input and result.
const toolDetailLines = 40

// transcriptView is the full-screen transcript viewer (P1.6). It takes over
// the tab while open.
type transcriptView struct {
	id      int // tells this viewer's messages from an earlier one's
	name    string
	session store.Session

	tr      transcript.Transcript
	loaded  bool
	loading bool

	cursor   int
	expanded map[int]bool
	allOpen  bool
	follow   bool // keep the newest entry selected and in view

	vp     viewport.Model
	starts []int // first content line of each entry

	// Glamour output per assistant entry, for mdWidth; mdSrc is the text
	// each was rendered from (merged text blocks can grow).
	md        map[int]string
	mdSrc     map[int]string
	mdWidth   int
	rendering bool
}

type transcriptLoadedMsg struct {
	id   int
	poll int // Tab.transcriptPoll at request time; schedules the next poll
	tr   transcript.Transcript
	err  error
}

type transcriptPollMsg struct{ id, poll int }

type transcriptRenderedMsg struct {
	id    int
	width int
	md    map[int]string
	src   map[int]string
}

func (transcriptLoadedMsg) TargetTab() string   { return Title }
func (transcriptPollMsg) TargetTab() string     { return Title }
func (transcriptRenderedMsg) TargetTab() string { return Title }

// openTranscript opens the viewer on the selected session.
func (t *Tab) openTranscript() tea.Cmd {
	if t.svc == nil {
		return errCmd(errUnavailable)
	}
	r, ok := t.current()
	if !ok {
		return nil
	}
	t.transcriptID++
	t.transcriptPoll++
	t.transcript = &transcriptView{
		id:       t.transcriptID,
		name:     r.Name,
		session:  r.Session,
		loading:  true,
		follow:   true,
		expanded: map[int]bool{},
		md:       map[int]string{},
		mdSrc:    map[int]string{},
		vp:       viewport.New(),
	}
	t.layoutTranscript()
	return t.loadTranscript()
}

func (t *Tab) closeTranscript() tea.Cmd {
	if t.transcript == nil {
		return nil
	}
	t.transcript = nil
	t.transcriptPoll++
	return t.selectionChanged()
}

// loadTranscript reads the open transcript in the background.
func (t *Tab) loadTranscript() tea.Cmd {
	v := t.transcript
	if v == nil {
		return nil
	}
	x := v.session
	if r, ok := t.rows[v.name]; ok {
		x = r.Session // the hooks may have reported a new transcript path
	}
	svc, id, poll := t.svc, v.id, t.transcriptPoll
	return func() tea.Msg {
		tr, err := svc.Transcript(context.Background(), x)
		return transcriptLoadedMsg{id: id, poll: poll, tr: tr, err: err}
	}
}

func (t *Tab) transcriptLoaded(msg transcriptLoadedMsg) tea.Cmd {
	v := t.transcript
	if v == nil || msg.id != v.id {
		return nil
	}
	v.loading = false
	if msg.err != nil {
		if !v.loaded {
			t.transcript = nil
			t.transcriptPoll++
			if errors.Is(msg.err, transcript.ErrNoTranscript) {
				return info(v.name + ": no transcript yet")
			}
			return errCmd(fmt.Errorf("transcript: %w", msg.err))
		}
		return t.nextTranscriptPoll(msg.poll) // keep showing what we have
	}
	v.loaded = true
	v.tr = msg.tr
	if v.follow || v.cursor >= len(v.tr.Entries) {
		v.cursor = max(len(v.tr.Entries)-1, 0)
	}
	t.layoutTranscript()
	return tea.Batch(t.renderMarkdown(), t.nextTranscriptPoll(msg.poll))
}

func (t *Tab) nextTranscriptPoll(poll int) tea.Cmd {
	v := t.transcript
	if v == nil || poll != t.transcriptPoll || !t.visible {
		return nil
	}
	id := v.id
	return t.tick(transcriptPoll, func(time.Time) tea.Msg { return transcriptPollMsg{id: id, poll: poll} })
}

func (t *Tab) transcriptPolled(msg transcriptPollMsg) tea.Cmd {
	v := t.transcript
	if v == nil || msg.id != v.id || msg.poll != t.transcriptPoll || !t.visible {
		return nil
	}
	return t.loadTranscript()
}

// transcriptVisible restarts polling when the tab is shown again, and stops
// it (by dropping the in-flight tick) when hidden.
func (t *Tab) transcriptVisible(v bool) tea.Cmd {
	if t.transcript == nil {
		return nil
	}
	t.transcriptPoll++
	if !v {
		return nil
	}
	return t.loadTranscript()
}

// renderMarkdown renders, off the update loop, the assistant entries whose
// Glamour output is missing or stale.
func (t *Tab) renderMarkdown() tea.Cmd {
	v := t.transcript
	if v == nil || v.rendering {
		return nil
	}
	width := t.transcriptTextWidth()
	if width != v.mdWidth {
		v.md, v.mdSrc, v.mdWidth = map[int]string{}, map[int]string{}, width
	}
	todo := map[int]string{}
	for i, e := range v.tr.Entries {
		if e.Kind == transcript.Assistant && v.mdSrc[i] != e.Text {
			todo[i] = e.Text
		}
	}
	if len(todo) == 0 {
		return nil
	}
	v.rendering = true
	style, id := t.theme.GlamourStyle(), v.id
	return func() tea.Msg {
		return transcriptRenderedMsg{id: id, width: width, md: renderMarkdown(style, width, todo), src: todo}
	}
}

// renderMarkdown renders each text with Glamour; entries Glamour fails on
// are left out (the viewer shows them as plain text).
func renderMarkdown(style glamouransi.StyleConfig, width int, texts map[int]string) map[int]string {
	out := make(map[int]string, len(texts))
	r, err := glamour.NewTermRenderer(glamour.WithStyles(style), glamour.WithWordWrap(width))
	if err != nil {
		return out
	}
	for i, s := range texts {
		md, err := r.Render(s)
		if err != nil {
			continue
		}
		out[i] = strings.Trim(md, "\n")
	}
	return out
}

func (t *Tab) markdownRendered(msg transcriptRenderedMsg) tea.Cmd {
	v := t.transcript
	if v == nil || msg.id != v.id {
		return nil
	}
	v.rendering = false
	if msg.width == v.mdWidth {
		for i, md := range msg.md {
			v.md[i] = md
		}
		for i, src := range msg.src {
			v.mdSrc[i] = src
		}
		t.layoutTranscript()
	}
	return t.renderMarkdown() // the width or the entries changed meanwhile
}

func (t *Tab) updateTranscript(kp tea.KeyPressMsg) tea.Cmd {
	v := t.transcript
	k := t.keys.Transcript
	n := len(v.tr.Entries)
	half := max(v.vp.Height()/2, 1)
	switch {
	case key.Matches(kp, k.Close):
		return t.closeTranscript()
	case key.Matches(kp, k.Up):
		v.cursor, v.follow = max(v.cursor-1, 0), false
	case key.Matches(kp, k.Down):
		v.cursor = min(v.cursor+1, max(n-1, 0))
		v.follow = v.cursor == n-1
	case key.Matches(kp, k.Top):
		v.cursor, v.follow = 0, false
	case key.Matches(kp, k.Bottom):
		v.cursor, v.follow = max(n-1, 0), true
	case key.Matches(kp, k.PageUp):
		v.cursor, v.follow = v.entryAt(v.vp.YOffset()-half), false
		v.vp.SetYOffset(v.vp.YOffset() - half)
		t.layoutTranscript()
		return nil
	case key.Matches(kp, k.PageDown):
		v.cursor = v.entryAt(v.vp.YOffset() + half)
		v.follow = v.cursor == n-1
		v.vp.SetYOffset(v.vp.YOffset() + half)
		t.layoutTranscript()
		return nil
	case key.Matches(kp, k.Toggle):
		return t.transcriptToggle()
	case key.Matches(kp, k.ToggleAll):
		return t.transcriptToggleAll()
	case key.Matches(kp, k.Follow):
		return t.transcriptFollow()
	default:
		return nil
	}
	t.relayoutTranscript()
	return nil
}

func (t *Tab) relayoutTranscript() {
	t.layoutTranscript()
	t.scrollToCursor()
}

// transcriptToggle expands or collapses the tool call under the cursor.
func (t *Tab) transcriptToggle() tea.Cmd {
	v := t.transcript
	if v == nil {
		return nil
	}
	if v.cursor < len(v.tr.Entries) && v.tr.Entries[v.cursor].Kind == transcript.Tool {
		v.expanded[v.cursor] = !v.isOpen(v.cursor)
	}
	t.relayoutTranscript()
	return nil
}

// transcriptToggleAll expands or collapses every tool call.
func (t *Tab) transcriptToggleAll() tea.Cmd {
	v := t.transcript
	if v == nil {
		return nil
	}
	v.allOpen = !v.allOpen
	v.expanded = map[int]bool{}
	t.relayoutTranscript()
	return nil
}

// transcriptFollow toggles keeping the newest entry selected and in view.
func (t *Tab) transcriptFollow() tea.Cmd {
	v := t.transcript
	if v == nil {
		return nil
	}
	v.follow = !v.follow
	if v.follow {
		v.cursor = max(len(v.tr.Entries)-1, 0)
	}
	t.relayoutTranscript()
	return nil
}

// transcriptCommands are the viewer's palette commands, offered while it is
// open.
func (t *Tab) transcriptCommands() []tabs.Command {
	k := t.keys.Transcript
	return []tabs.Command{
		{ID: "transcript_toggle", Title: "Transcript: expand / collapse tool call", Key: k.Toggle, Run: t.transcriptToggle},
		{ID: "transcript_toggle_all", Title: "Transcript: expand / collapse all tool calls", Key: k.ToggleAll, Run: t.transcriptToggleAll},
		{ID: "transcript_follow", Title: "Transcript: follow new entries", Key: k.Follow, Run: t.transcriptFollow},
		{ID: "transcript_close", Title: "Transcript: close", Key: k.Close, Run: t.closeTranscript},
	}
}

// entryAt is the index of the entry shown at content line y.
func (v *transcriptView) entryAt(y int) int {
	i := 0
	for j, s := range v.starts {
		if s > y {
			break
		}
		i = j
	}
	return i
}

func (v *transcriptView) isOpen(i int) bool {
	if open, ok := v.expanded[i]; ok {
		return open
	}
	return v.allOpen
}

// transcriptSize is the viewer's inner size (inside its border).
func (t *Tab) transcriptSize() (w, h int) {
	return max(t.width-2, 10), max(t.height-2, 3)
}

// transcriptTextWidth is the width entry text wraps at: the inner width
// minus the cursor gutter.
func (t *Tab) transcriptTextWidth() int {
	w, _ := t.transcriptSize()
	return max(w-2, 8)
}

// transcriptHeaderLines is the height of the summary header.
const transcriptHeaderLines = 3

// layoutTranscript re-renders the entries into the viewport, keeping the
// scroll position (or the bottom, when following).
func (t *Tab) layoutTranscript() {
	v := t.transcript
	if v == nil {
		return
	}
	w, h := t.transcriptSize()
	v.vp.SetWidth(w)
	v.vp.SetHeight(max(h-transcriptHeaderLines, 1))

	th := t.theme
	text := t.transcriptTextWidth()
	var lines []string
	v.starts = v.starts[:0]
	for i, e := range v.tr.Entries {
		// A blank line between entries, except within a run of tool calls.
		if i > 0 && (e.Kind != transcript.Tool || v.tr.Entries[i-1].Kind != transcript.Tool) {
			lines = append(lines, "")
		}
		v.starts = append(v.starts, len(lines))
		gutter := "  "
		if i == v.cursor {
			gutter = th.TranscriptCursor.Render("▌ ")
		}
		for _, l := range strings.Split(t.renderEntry(i, e, text), "\n") {
			lines = append(lines, gutter+l)
		}
	}
	y := v.vp.YOffset()
	v.vp.SetContentLines(lines)
	if v.follow {
		v.vp.GotoBottom()
	} else {
		v.vp.SetYOffset(y)
	}
}

// scrollToCursor scrolls the least needed to show the cursor entry.
func (t *Tab) scrollToCursor() {
	v := t.transcript
	if v == nil || v.cursor >= len(v.starts) {
		return
	}
	if v.follow {
		v.vp.GotoBottom()
		return
	}
	top := v.starts[v.cursor]
	end := v.vp.TotalLineCount()
	if v.cursor+1 < len(v.starts) {
		end = v.starts[v.cursor+1]
	}
	y, h := v.vp.YOffset(), v.vp.Height()
	switch {
	case top < y:
		v.vp.SetYOffset(top)
	case end > y+h:
		v.vp.SetYOffset(min(top, end-h))
	}
}

func (t *Tab) renderEntry(i int, e transcript.Entry, width int) string {
	th := t.theme
	v := t.transcript
	wrap := func(s string, st lipgloss.Style) string {
		return st.Width(width).Render(s)
	}
	switch e.Kind {
	case transcript.User:
		return th.TranscriptUser.Render("you") + "\n" + wrap(e.Text, th.TranscriptPrompt)
	case transcript.Assistant:
		body, ok := v.md[i]
		if !ok || v.mdSrc[i] != e.Text {
			body = wrap(e.Text, th.Body)
		}
		return th.TranscriptAgent.Render("claude") + "\n" + body
	case transcript.Tool:
		return t.renderTool(i, e, width)
	case transcript.Notice:
		return th.TranscriptNotice.Render(ansi.Truncate("── "+e.Text+" ──", width, "…"))
	}
	return ""
}

func (t *Tab) renderTool(i int, e transcript.Entry, width int) string {
	th := t.theme
	status := th.TranscriptTool.Render("…")
	switch {
	case e.HasResult && e.IsError:
		status = th.TranscriptToolErr.Render("✗")
	case e.HasResult:
		status = th.TranscriptToolOK.Render("✓")
	}
	open := t.transcript.isOpen(i)
	arrow := "▸"
	if open {
		arrow = "▾"
	}
	head := arrow + " ⚒ " + e.ToolName
	if e.Summary != "" {
		head += "  " + e.Summary
	}
	line := th.TranscriptTool.Render(ansi.Truncate(head, max(width-2, 1), "…")) + " " + status
	if !open {
		return line
	}
	detail := func(s string) []string {
		var out []string
		for _, l := range capLines(s, toolDetailLines) {
			out = append(out, th.TranscriptDetail.Render("  │ "+ansi.Truncate(l, max(width-4, 1), "…")))
		}
		return out
	}
	parts := []string{line}
	parts = append(parts, detail(toolInput(e))...)
	if e.HasResult {
		res := e.Result
		if strings.TrimSpace(res) == "" {
			res = "(no output)"
		}
		parts = append(parts, th.TranscriptDetail.Render("  ├─ result"))
		parts = append(parts, detail(res)...)
	}
	return strings.Join(parts, "\n")
}

// toolInput is a readable form of a tool call's input.
func toolInput(e transcript.Entry) string {
	var in map[string]any
	if json.Unmarshal(e.Input, &in) != nil {
		return string(e.Input)
	}
	str := func(k string) string { s, _ := in[k].(string); return s }
	switch e.ToolName {
	case "Bash":
		return "$ " + str("command")
	case "Edit":
		var b strings.Builder
		b.WriteString(str("file_path"))
		for _, l := range strings.Split(str("old_string"), "\n") {
			b.WriteString("\n- " + l)
		}
		for _, l := range strings.Split(str("new_string"), "\n") {
			b.WriteString("\n+ " + l)
		}
		return b.String()
	case "Write":
		return str("file_path") + "\n" + str("content")
	}
	var buf bytes.Buffer
	if json.Indent(&buf, e.Input, "", "  ") != nil {
		return string(e.Input)
	}
	return buf.String()
}

// capLines splits s into lines, keeping at most n and noting the rest.
func capLines(s string, n int) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		more := len(lines) - n
		lines = append(lines[:n], fmt.Sprintf("… %d more lines", more))
	}
	for i, l := range lines {
		lines[i] = strings.ReplaceAll(l, "\t", "    ")
	}
	return lines
}

func (t *Tab) transcriptView() string {
	v := t.transcript
	th := t.theme
	w, h := t.transcriptSize()
	if !v.loaded {
		body := anim.HammerFrame(t.anim.Frame(), t.anim.Settings(), th) + " " + th.Hint.Render("reading transcript…")
		return th.PaneFocused.Render(ui.Box(th.TranscriptTitle.Render(v.name)+"\n\n"+body, w, h))
	}
	content := t.transcriptHeader(w) + "\n" + v.vp.View()
	return th.PaneFocused.Render(ui.Box(content, w, h))
}

func (t *Tab) transcriptHeader(width int) string {
	v := t.transcript
	th := t.theme
	s := v.tr.Summary
	stat := func(label, value string) string {
		return th.DetailLabel.Render(label+" ") + th.TranscriptStat.Render(value)
	}
	tokens := fmt.Sprintf("in %s · out %s · cache %s",
		count(s.Tokens.Input+s.Tokens.CacheWrite), count(s.Tokens.Output), count(s.Tokens.CacheRead))
	if s.Estimated {
		tokens = "~" + count(s.Tokens.Total())
	}
	follow := ""
	if v.follow {
		follow = "  " + th.TranscriptCursor.Render("● following")
	}
	line1 := th.TranscriptTitle.Render(v.name) + "  " +
		stat("messages", fmt.Sprint(s.Messages)) + "  " +
		stat("tools", fmt.Sprint(s.ToolCalls)) + "  " +
		stat("activity", Ago(t.now(), s.LastActivity)) + "  " +
		stat("tokens", tokens) + follow
	files := "—"
	if n := len(s.FilesTouched); n > 0 {
		files = fmt.Sprintf("%d: %s", n, shortFiles(s.FilesTouched, v.session.Worktree))
	}
	line2 := stat("files", files)
	return ansi.Truncate(line1, width, "…") + "\n" +
		ansi.Truncate(line2, width, "…") + "\n" +
		th.PreviewRule.Render(strings.Repeat("─", width))
}

// shortFiles lists files relative to the worktree, at most four, then "+N".
func shortFiles(files []string, root string) string {
	const shown = 4
	var out []string
	for i, f := range files {
		if i == shown {
			out = append(out, fmt.Sprintf("+%d", len(files)-shown))
			break
		}
		if prefix := strings.TrimSuffix(root, "/") + "/"; root != "" && strings.HasPrefix(f, prefix) {
			f = strings.TrimPrefix(f, prefix)
		}
		out = append(out, f)
	}
	return strings.Join(out, ", ")
}

// count renders a token count compactly: 950, 12.3k, 4.1M.
func count(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

func (t *Tab) transcriptHelp() []key.Binding {
	k := t.keys.Transcript
	return []key.Binding{k.Toggle, k.Down, k.Up, k.ToggleAll, k.Follow, k.Close}
}
