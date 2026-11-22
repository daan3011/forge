package sessions

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/daan3011/forge/internal/ui"
	"github.com/daan3011/forge/internal/usage"
)

// Token usage in the detail pane (P5.4): the selected session's all-time
// totals from the usage ledger, which a throttled background scan keeps
// current.

// usageScanEvery is how often the tab's refresh also scans transcripts.
const usageScanEvery = time.Minute

// usageTimeout bounds a scan plus a totals lookup.
const usageTimeout = 2 * time.Minute

// usageTotalsMsg carries one session's totals. scanned marks the result of
// a scan, after which every other cached total is stale.
type usageTotalsMsg struct {
	id      string
	tot     usage.Totals
	err     error
	scanned bool
}

// TargetTab implements tabs.TargetedMsg.
func (usageTotalsMsg) TargetTab() string { return Title }

// selectedClaudeID is the selected session's Claude Code session id.
func (t *Tab) selectedClaudeID() string {
	r, ok := t.current()
	if !ok {
		return ""
	}
	return r.ClaudeSessionID
}

// loadUsage looks up the selected session's totals unless they are cached.
func (t *Tab) loadUsage() tea.Cmd {
	id := t.selectedClaudeID()
	if t.usage == nil || id == "" {
		return nil
	}
	if _, ok := t.usageTot[id]; ok {
		return nil
	}
	src := t.usage
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), usageTimeout)
		defer cancel()
		tot, err := src.Session(ctx, id)
		return usageTotalsMsg{id: id, tot: tot, err: err}
	}
}

// scanUsage scans transcripts for new usage at most every usageScanEvery,
// then reloads the selected session's totals.
func (t *Tab) scanUsage() tea.Cmd {
	if t.usage == nil || t.usageScanning || t.now().Sub(t.usageScanAt) < usageScanEvery {
		return nil
	}
	t.usageScanning, t.usageScanAt = true, t.now()
	src, id := t.usage, t.selectedClaudeID()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), usageTimeout)
		defer cancel()
		if _, err := src.Scan(ctx); err != nil {
			return usageTotalsMsg{scanned: true, err: err}
		}
		msg := usageTotalsMsg{id: id, scanned: true}
		if id != "" {
			msg.tot, msg.err = src.Session(ctx, id)
		}
		return msg
	}
}

func (t *Tab) usageLoaded(msg usageTotalsMsg) tea.Cmd {
	if msg.scanned {
		t.usageScanning = false
		t.usageTot = map[string]usage.Totals{}
	}
	if msg.err != nil {
		t.usageErr = msg.err
		return nil
	}
	t.usageErr = nil
	if msg.id != "" {
		t.usageTot[msg.id] = msg.tot
	}
	if msg.scanned {
		return t.loadUsage() // the selection may have moved meanwhile
	}
	return nil
}

// usageLine is the detail pane's usage value for a Claude Code session.
func (t *Tab) usageLine(id string) string {
	x, ok := t.usageTot[id]
	switch {
	case !ok && t.usageErr != nil:
		return "unavailable: " + t.usageErr.Error()
	case !ok:
		return "…"
	case x.Total() == 0:
		return "—"
	}
	c := ui.CompactCount
	s := fmt.Sprintf("%s tokens (in %s · out %s · cache %s/%s)",
		c(int(x.Total())), c(int(x.Input)), c(int(x.Output)), c(int(x.CacheRead)), c(int(x.CacheWrite)))
	switch {
	case x.Partial():
		s += " · " + ui.USD(x.Cost) + "*"
	case x.Priced():
		s += " · " + ui.USD(x.Cost)
	}
	return s
}
