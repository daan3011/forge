package demo

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// writeFiles lays out the fictional checkouts: docs, memory files and a
// few artifacts.
func writeFiles(dir, home string) error {
	files := map[string]string{
		"anvil/CLAUDE.md":                                      claudeMD,
		"anvil/.forge/docs/architecture.md":                    architectureMD,
		"anvil/.forge/docs/runbooks/quench-failures.md":        runbookMD,
		"anvil/.forge/docs/decisions/0001-retry-the-quench.md": adr1,
		"anvil/.forge/docs/decisions/0002-stream-heat.md":      adr2,
		"anvil/reports/coverage.html":                          "<!doctype html><title>Coverage · anvil</title><h1>Coverage 81.4%</h1>\n",
		"anvil/design/bellows-panel.svg":                       `<svg xmlns="http://www.w3.org/2000/svg" width="64" height="32"><rect width="64" height="32" fill="#ff7a1a"/></svg>` + "\n",
		"bellows/CLAUDE.md":                                    "# bellows\n\nAirflow controller for the forge. Keep presets in `presets/`, one file per alloy.\n",
		"bellows/.forge/docs/presets.md":                       "# Airflow presets\n\n| Alloy | Flow | Hold |\n|---|---|---|\n| 1095 | 70% | 4 min |\n| 5160 | 65% | 5 min |\n",
		filepath.Join(rel(dir, home), ".claude/CLAUDE.md"):     "# Personal preferences\n\n- Prefer small, reviewed PRs.\n- Run the tests before saying a task is done.\n",
	}
	for path, body := range files {
		p := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			return err
		}
	}
	img, err := heatMap()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "anvil/design/heat-map.png"), img, 0o600)
}

func rel(dir, path string) string {
	r, err := filepath.Rel(dir, path)
	if err != nil {
		return path
	}
	return r
}

// heatMap is a small ember-coloured gradient PNG.
func heatMap() ([]byte, error) {
	const w, h = 96, 48
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		for y := range h {
			t := float64(x) / w
			img.Set(x, y, color.RGBA{R: uint8(60 + 195*t), G: uint8(20 + 110*t*t), B: uint8(10 + 20*(1-t)), A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// transcript is a short Claude Code JSONL conversation.
func transcript(sessionID string, start time.Time, prompt string, turns [][2]string) string {
	var b strings.Builder
	ts := func(i int) string { return start.Add(time.Duration(i) * 40 * time.Second).UTC().Format(time.RFC3339) }
	fmt.Fprintf(&b, `{"type":"user","timestamp":%q,"message":{"role":"user","content":%q},"sessionId":%q}`+"\n", ts(0), prompt, sessionID)
	for i, t := range turns {
		n := 2*i + 1
		fmt.Fprintf(&b, `{"type":"assistant","timestamp":%q,"message":{"id":"msg_%d","role":"assistant","content":[{"type":"text","text":%q}],"usage":{"input_tokens":1200,"output_tokens":340,"cache_read_input_tokens":18000}},"sessionId":%q}`+"\n", ts(n), n, t[0], sessionID)
		if t[1] != "" {
			fmt.Fprintf(&b, `{"type":"assistant","timestamp":%q,"message":{"id":"msg_%dt","role":"assistant","content":[{"type":"tool_use","id":"tu_%d","name":"Bash","input":{"command":%q,"description":"Run it"}}],"usage":{"input_tokens":10,"output_tokens":20}},"sessionId":%q}`+"\n", ts(n), n, n, t[1], sessionID)
			fmt.Fprintf(&b, `{"type":"user","timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_%d","content":"ok","is_error":false}]},"sessionId":%q}`+"\n", ts(n+1), n, sessionID)
		}
	}
	return b.String()
}

const claudeMD = `# anvil

Forge control service. Go 1.26, ` + "`make test`" + ` before every PR.

- Temperatures are always in °C.
- The quench step must be idempotent.
- Runbooks live in .forge/docs/runbooks.
`

const architectureMD = `# Architecture

anvil drives one forge: the **bellows** service sets airflow, the
**crucible** tracks batches, and **temper** runs heat treatment.

    bellows ──► anvil ──► temper
                  │
                  └──► crucible

## Heat readings

Thermocouples report every 250 ms; anvil keeps the last hour in memory
and streams it to the bellows API (see ADR 0002).
`

const runbookMD = `# Runbook: quench failures

1. Check the ambient temperature on the dashboard.
2. If it's under 8 °C, warm the oil bath to 40 °C before retrying.
3. Retry the batch from the crucible view.
4. Still failing? Page the on-call smith.
`

const adr1 = `# 1. Retry the quench step

Status: accepted

## Context
Cold mornings make the quench step fail with ErrTooCold.

## Decision
Retry twice with exponential backoff before failing the batch.

## Consequences
Batches take up to 30 s longer when the shop is cold.
`

const adr2 = `# 2. Stream heat readings

Status: proposed

## Context
The bellows poll anvil every second and miss spikes.

## Decision
Stream readings over a long-lived connection instead.
`
