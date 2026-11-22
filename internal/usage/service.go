package usage

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/store"
)

// Config configures a Service.
type Config struct {
	Ledger   Ledger
	Sessions SessionLister // nil: no forge session names or projects
	// ProjectsDir is Claude Code's transcript folder (~/.claude/projects).
	ProjectsDir string
	Projects    []config.Project
	Usage       config.Usage
	Now         func() time.Time // nil: time.Now
}

// Service scans transcripts into the ledger and builds reports. It is safe
// for concurrent use; scans run one at a time.
type Service struct {
	cfg    Config
	prices Prices

	scanMu sync.Mutex
	mu     sync.Mutex
	cwds   map[string]string // per session, from the last rollup
}

// New returns a Service.
func New(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Usage.Days <= 0 {
		cfg.Usage.Days = config.DefaultUsageDays
	}
	if cfg.Usage.TopSessions <= 0 {
		cfg.Usage.TopSessions = config.DefaultUsageTopSessions
	}
	return &Service{cfg: cfg, prices: Prices(cfg.Usage.Prices), cwds: map[string]string{}}
}

// Days is the report range in days.
func (s *Service) Days() int { return s.cfg.Usage.Days }

func (s *Service) resolver(ctx context.Context) (Resolver, error) {
	var sessions []store.Session
	if s.cfg.Sessions != nil {
		var err error
		if sessions, err = s.cfg.Sessions.List(ctx, store.SessionFilter{}); err != nil {
			return Resolver{}, err
		}
	}
	return NewResolver(sessions, s.cfg.Projects), nil
}

// Scan reads what the transcripts gained since the last scan into the
// ledger, then rebuilds the daily rollups. New files are ingested oldest
// message first, so when a resumed session's file repeats messages the
// original session keeps them.
func (s *Service) Scan(ctx context.Context) (ScanResult, error) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	var res ScanResult
	files, err := findFiles(s.cfg.ProjectsDir)
	if err != nil {
		return res, err
	}
	res.Files = len(files)
	var todo []pending
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		p, ok, err := readFile(ctx, s.cfg.Ledger, f, &res)
		if err != nil {
			return res, err
		}
		if ok {
			todo = append(todo, p)
		}
	}
	sort.SliceStable(todo, func(i, j int) bool { return todo[i].first.Before(todo[j].first) })
	for _, p := range todo {
		if err := s.cfg.Ledger.Ingest(ctx, p.state, p.msgs); err != nil {
			return res, err
		}
		res.Read++
		res.Messages += len(p.msgs)
	}
	return res, s.rollup(ctx)
}

// rollup rebuilds the usage table from the ledger.
func (s *Service) rollup(ctx context.Context) error {
	groups, err := s.cfg.Ledger.LedgerGroups(ctx)
	if err != nil {
		return err
	}
	res, err := s.resolver(ctx)
	if err != nil {
		return err
	}
	rows := make([]store.UsageRow, 0, len(groups))
	cwds := map[string]string{}
	for _, g := range groups {
		rows = append(rows, store.UsageRow{
			Day: g.Day, Project: res.Project(g.ClaudeSessionID, g.Cwd),
			ClaudeSessionID: g.ClaudeSessionID, Model: g.Model, Tokens: g.Tokens,
		})
		if g.Cwd != "" {
			cwds[g.ClaudeSessionID] = g.Cwd
		}
	}
	if err := s.cfg.Ledger.ReplaceRollups(ctx, rows); err != nil {
		return err
	}
	s.mu.Lock()
	s.cwds = cwds
	s.mu.Unlock()
	return nil
}

// Report builds the usage report from the rollups; project filters the
// chart and top sessions ("" means all).
func (s *Service) Report(ctx context.Context, project string) (Report, error) {
	now := s.cfg.Now()
	since := dayOf(now).AddDate(0, 0, -(s.cfg.Usage.Days - 1))
	rows, err := s.cfg.Ledger.Rows(ctx, since, "")
	if err != nil {
		return Report{}, err
	}
	res, err := s.resolver(ctx)
	if err != nil {
		return Report{}, err
	}
	s.mu.Lock()
	cwds := s.cwds
	s.mu.Unlock()
	return Aggregate(AggregateInput{
		Rows: rows, Prices: s.prices, Resolver: res, Cwds: cwds, Today: now,
		Days: s.cfg.Usage.Days, Top: s.cfg.Usage.TopSessions, Project: project,
	}), nil
}

// Session returns all-time totals for one Claude Code session.
func (s *Service) Session(ctx context.Context, claudeSessionID string) (Totals, error) {
	var t Totals
	if claudeSessionID == "" {
		return t, nil
	}
	rows, err := s.cfg.Ledger.Rows(ctx, time.Time{}, claudeSessionID)
	if err != nil {
		return t, err
	}
	for _, r := range rows {
		t.Add(s.prices, r.Model, r.Tokens)
	}
	return t, nil
}
