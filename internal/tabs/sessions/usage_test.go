package sessions

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/usage"
)

type fakeUsage struct {
	mu      sync.Mutex
	scans   int
	lookups []string
	totals  map[string]usage.Totals
}

func (f *fakeUsage) Scan(context.Context) (usage.ScanResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scans++
	return usage.ScanResult{}, nil
}

func (f *fakeUsage) Report(context.Context, string) (usage.Report, error) { return usage.Report{}, nil }

func (f *fakeUsage) Session(_ context.Context, id string) (usage.Totals, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups = append(f.lookups, id)
	return f.totals[id], nil
}

func TestDetailShowsUsage(t *testing.T) {
	fu := &fakeUsage{totals: map[string]usage.Totals{
		"uuid-alpha": {Tokens: store.Tokens{Input: 12_000, Output: 40_000, CacheRead: 1_100_000, CacheWrite: 50_000}, Cost: 3.21},
		"uuid-beta":  {Tokens: store.Tokens{Input: 500}, Unpriced: 500},
	}}
	svc := newFakeSvc(row("alpha", store.StatusWorking), row("beta", store.StatusIdle))
	tab := newTab(svc)
	tab.usage = fu
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	tab.now = func() time.Time { return now }
	pump(tab, tab.Refresh())

	if d := ansi.Strip(tab.detail()); !strings.Contains(d, "usage: 1.2M tokens (in 12k · out 40k · cache 1.1M/50k) · $3.21") {
		t.Errorf("detail:\n%s", d)
	}
	fu.mu.Lock()
	scans := fu.scans
	fu.mu.Unlock()
	if scans != 1 {
		t.Errorf("scans = %d, want 1", scans)
	}

	// Another session: looked up once, unpriced so no cost.
	_, cmd := tab.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	pump(tab, cmd)
	if d := ansi.Strip(tab.detail()); !strings.Contains(d, "usage: 500 tokens (in 500 · out 0 · cache 0/0)") || strings.Contains(d, "$") {
		t.Errorf("detail:\n%s", d)
	}

	// A refresh within a minute doesn't scan again.
	pump(tab, tab.Refresh())
	fu.mu.Lock()
	scans = fu.scans
	fu.mu.Unlock()
	if scans != 1 {
		t.Errorf("scans after quick refresh = %d, want 1", scans)
	}
	now = now.Add(2 * time.Minute)
	pump(tab, tab.Refresh())
	fu.mu.Lock()
	scans = fu.scans
	fu.mu.Unlock()
	if scans != 2 {
		t.Errorf("scans after a minute = %d, want 2", scans)
	}
}

func TestDetailWithoutUsage(t *testing.T) {
	tab := newTab(newFakeSvc(row("alpha", store.StatusWorking)))
	pump(tab, tab.Refresh())
	if d := ansi.Strip(tab.detail()); strings.Contains(d, "usage:") {
		t.Errorf("detail shows usage without a source:\n%s", d)
	}
}
