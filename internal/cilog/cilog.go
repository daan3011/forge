// Package cilog cleans GitHub Actions job logs and pulls out the lines that
// explain a failure (P2.7). It does no I/O; the error summary it builds is
// saved in kv_cache for the "fix this" flow (P2.8).
package cilog

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Context is how many lines are kept around each error line.
const Context = 5

// TailLines is how many trailing lines are shown when no line looks like
// an error.
const TailLines = 40

// Kind is the kv_cache kind of error summaries.
const Kind = "ci_errors"

// Summary caps: lines kept and total bytes.
const (
	maxSummaryLines = 200
	maxSummaryBytes = 16 << 10
)

// tsPrefix is the timestamp GitHub puts before every log line.
var tsPrefix = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?Z ?`)

// Clean splits a raw log into readable lines: no BOM, timestamps, ANSI or
// carriage returns; tabs become spaces; ##[group] markers become "▸ name"
// and ##[endgroup] lines go.
func Clean(raw []byte) []string {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	text := strings.TrimRight(string(raw), "\r\n")
	if text == "" {
		return nil
	}
	parts := strings.Split(text, "\n")
	out := make([]string, 0, len(parts))
	for _, l := range parts {
		l = strings.TrimSuffix(l, "\r")
		l = strings.TrimPrefix(l, "\ufeff")
		l = tsPrefix.ReplaceAllString(l, "")
		l = ansi.Strip(l)
		if i := strings.LastIndexByte(l, '\r'); i >= 0 {
			l = l[i+1:] // progress bars redraw with \r: keep the last frame
		}
		l = strings.ReplaceAll(l, "\t", "    ")
		switch {
		case strings.HasPrefix(l, "##[endgroup]"):
			continue
		case strings.HasPrefix(l, "##[group]"):
			l = "▸ " + strings.TrimPrefix(l, "##[group]")
		}
		out = append(out, l)
	}
	return out
}

// errorWord matches "error" and "panic" as whole words, so "errors" in
// prose and -Werror don't count.
var errorWord = regexp.MustCompile(`(?i)\b(error|panic)\b`)

// IsErrorLine reports whether l looks like part of a failure.
func IsErrorLine(l string) bool {
	if strings.Contains(l, "##[error]") || strings.Contains(l, "FAIL") {
		return true
	}
	low := strings.ToLower(l)
	if strings.Contains(low, "failed") || strings.Contains(low, "exit code") {
		return true
	}
	return errorWord.MatchString(l)
}

// Hunk is a run of lines [Start, End) worth showing.
type Hunk struct{ Start, End int }

// Extract returns the error lines with ctx lines around each; overlapping
// or touching windows merge.
func Extract(lines []string, ctx int) []Hunk {
	var out []Hunk
	for i, l := range lines {
		if !IsErrorLine(l) {
			continue
		}
		h := Hunk{Start: max(i-ctx, 0), End: min(i+ctx+1, len(lines))}
		if n := len(out); n > 0 && h.Start <= out[n-1].End {
			out[n-1].End = max(out[n-1].End, h.End)
			continue
		}
		out = append(out, h)
	}
	return out
}

// Line is one line of the collapsed view. Orig is its index in the full
// log, or -1 for a "lines hidden" separator.
type Line struct {
	Text string
	Orig int
}

// View is the collapsed log.
type View struct {
	Lines []Line
	// Tail is set when nothing looked like an error and the view is the
	// log's last lines.
	Tail bool
}

// Collapse builds the collapsed view: each hunk, with a separator naming
// the hidden lines between (and around) them. With no hunks it is the last
// TailLines lines.
func Collapse(lines []string, hunks []Hunk) View {
	var v View
	if len(hunks) == 0 {
		v.Tail = true
		start := max(len(lines)-TailLines, 0)
		if start > 0 {
			v.Lines = append(v.Lines, sep(start))
		}
		for i := start; i < len(lines); i++ {
			v.Lines = append(v.Lines, Line{Text: lines[i], Orig: i})
		}
		return v
	}
	prev := 0
	for _, h := range hunks {
		if h.Start > prev {
			v.Lines = append(v.Lines, sep(h.Start-prev))
		}
		for i := h.Start; i < h.End; i++ {
			v.Lines = append(v.Lines, Line{Text: lines[i], Orig: i})
		}
		prev = h.End
	}
	if prev < len(lines) {
		v.Lines = append(v.Lines, sep(len(lines)-prev))
	}
	return v
}

func sep(n int) Line {
	word := "lines"
	if n == 1 {
		word = "line"
	}
	return Line{Text: fmt.Sprintf("⋯ %d %s hidden ⋯", n, word), Orig: -1}
}

// Summary is a failed job's extracted error context, saved for P2.8.
type Summary struct {
	Repo        string    `json:"repo"`
	RunID       int64     `json:"run_id"`
	RunNumber   int       `json:"run_number"`
	Attempt     int       `json:"attempt"`
	Workflow    string    `json:"workflow"`
	Branch      string    `json:"branch"`
	HeadSHA     string    `json:"head_sha"`
	JobID       int64     `json:"job_id"`
	Job         string    `json:"job"`
	URL         string    `json:"url"`
	Lines       []string  `json:"lines"`
	Tail        bool      `json:"tail"` // no error lines found; Lines is the log's tail
	ExtractedAt time.Time `json:"extracted_at"`
}

// SummaryKey is the kv_cache key of a job's summary. Job ids change on a
// rerun, so every attempt has its own.
func SummaryKey(repo string, runID, jobID int64) string {
	return fmt.Sprintf("%s:%s#%d:%d", Kind, strings.ToLower(repo), runID, jobID)
}

// SummaryLines is the collapsed view as text, capped to fit in a prompt.
func SummaryLines(v View) []string {
	var out []string
	size := 0
	for _, l := range v.Lines {
		if len(out) == maxSummaryLines || size+len(l.Text)+1 > maxSummaryBytes {
			out = append(out, "⋯ truncated ⋯")
			break
		}
		out = append(out, l.Text)
		size += len(l.Text) + 1
	}
	return out
}
