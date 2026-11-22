package context

import (
	stdctx "context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/usage"
)

var usageNow = time.Date(2026, 11, 23, 12, 0, 0, 0, time.Local)

type fakeUsage struct {
	mu       sync.Mutex
	scans    int
	projects []string // Report calls, in order
	priced   bool
}

func (f *fakeUsage) Scan(stdctx.Context) (usage.ScanResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scans++
	return usage.ScanResult{}, nil
}

func (f *fakeUsage) Report(_ stdctx.Context, project string) (usage.Report, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects = append(f.projects, project)
	tot := func(in int64, cost float64) usage.Totals {
		x := usage.Totals{Tokens: store.Tokens{Input: in, Messages: 2}}
		if f.priced {
			x.Cost = cost
		} else {
			x.Unpriced = in
		}
		return x
	}
	r := usage.Report{Since: usageNow.AddDate(0, 0, -13), Project: project}
	for i := range 14 {
		d := usage.Day{Day: usageNow.AddDate(0, 0, i-13)}
		if i%3 == 0 {
			d.Totals = tot(int64(1000*(i+1)), 1)
		}
		r.Days = append(r.Days, d)
	}
	r.Projects = []usage.ProjectRow{
		{Project: "atlas", Totals: tot(2_500_000, 30)},
		{Project: "forge", Totals: tot(900_000, 9.5)},
		{Project: usage.Other, Totals: tot(1_000, 0.01)},
	}
	switch project {
	case "forge":
		r.Total = tot(900_000, 9.5)
		r.Sessions = []usage.SessionRow{{ClaudeSessionID: "s2", Name: "fix-ci", Project: "forge", Totals: tot(900_000, 9.5)}}
	default:
		r.Total = tot(3_401_000, 39.51)
		r.Sessions = []usage.SessionRow{
			{ClaudeSessionID: "1234567890", Cwd: "/x/atlas", Project: "atlas", Totals: tot(2_500_000, 30)},
			{ClaudeSessionID: "s2", Name: "fix-ci", Project: "forge", Totals: tot(900_000, 9.5)},
		}
	}
	return r, nil
}

func (f *fakeUsage) Session(stdctx.Context, string) (usage.Totals, error) { return usage.Totals{}, nil }

func (f *fakeUsage) calls() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.scans, append([]string(nil), f.projects...)
}

func usageTab(t *testing.T) (*Tab, *fakeUsage) {
	t.Helper()
	tab := newFixture(t).tab(t)
	fu := &fakeUsage{}
	tab.usage = fu
	tab.now = func() time.Time { return usageNow }
	return tab, fu
}

func TestUsageView(t *testing.T) {
	tab, fu := usageTab(t)
	pumpAll(tab, tab.Refresh())
	press(tab, "u")
	if tab.view != viewUsage || !tab.us.loaded {
		t.Fatal("usage view not showing")
	}
	if scans, _ := fu.calls(); scans != 1 {
		t.Errorf("scans = %d, want 1", scans)
	}
	v := view(tab)
	for _, want := range []string{
		"Usage", "Project", "Tokens", "Cost", "All projects", "3.4M", "atlas", "2.5M", "forge", "900k", "other",
		"Tokens per day, last 14 days", "peak 13k on Sun Nov 22", "Top sessions", "12345678 · atlas", "fix-ci",
		"Fill in usage.prices", "last 14 days since Nov 10",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "$") {
		t.Errorf("unpriced view shows a cost:\n%s", v)
	}
	if got := tab.StatusDetail(); got != "14d: 3.4M tokens" {
		t.Errorf("status = %q", got)
	}

	// Moving to a project filters the chart and the sessions.
	press(tab, "j")
	press(tab, "j")
	if tab.us.filter != "forge" {
		t.Fatalf("filter = %q", tab.us.filter)
	}
	v = view(tab)
	if !strings.Contains(v, "900k tokens") || strings.Contains(v, "12345678") {
		t.Errorf("filtered view:\n%s", v)
	}
	if _, projects := fu.calls(); strings.Join(projects, ",") != ",atlas,forge" {
		t.Errorf("reports = %q", projects)
	}

	// Refresh rescans and keeps the selected project.
	pumpAll(tab, tab.Refresh())
	if scans, _ := fu.calls(); scans != 2 || tab.us.filter != "forge" || tab.us.cursor != 2 {
		t.Errorf("after refresh: scans %d, filter %q, cursor %d", scans, tab.us.filter, tab.us.cursor)
	}

	press(tab, "u")
	if tab.view != viewMemory {
		t.Error("u should go back to memory files")
	}
}

func TestUsageViewPriced(t *testing.T) {
	tab, fu := usageTab(t)
	fu.priced = true
	pumpAll(tab, tab.Refresh())
	press(tab, "u")
	v := view(tab)
	for _, want := range []string{"$39.51", "$30.00", "$9.50"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "usage.prices") {
		t.Errorf("priced view nags about prices:\n%s", v)
	}
}

func TestUsageUnavailable(t *testing.T) {
	tab := newFixture(t).tab(t)
	pumpAll(tab, tab.Refresh())
	press(tab, "u")
	if v := view(tab); !strings.Contains(v, "Token usage needs forge's database") {
		t.Errorf("view:\n%s", v)
	}
}

func TestUsagePaletteCommand(t *testing.T) {
	tab, _ := usageTab(t)
	for _, c := range tab.Commands() {
		if c.ID == "context_usage" {
			pumpAll(tab, c.Run())
		}
	}
	if tab.view != viewUsage || !tab.us.loaded {
		t.Error("palette command didn't open the usage view")
	}
}

// TestUsageFlow: open the usage view in a running program and see the
// heaviest project and session.
func TestUsageFlow(t *testing.T) {
	tab, _ := usageTab(t)
	h := &host{tab: tab}
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(120, 30))
	waitFor(t, h, "Total ≈")
	tm.Send(tea.KeyPressMsg{Code: 'u', Text: "u"})
	waitFor(t, h, "atlas", "2.5M", "Top sessions", "fix-ci")
	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})
}
