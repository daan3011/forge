package usage

import (
	"path/filepath"
	"sort"
	"time"

	"github.com/daan3011/forge/internal/store"
)

// Day is one day of the bar chart.
type Day struct {
	Day time.Time
	Totals
}

// ProjectRow is one line of the project table.
type ProjectRow struct {
	Project string
	Totals
}

// SessionRow is one line of the top sessions list.
type SessionRow struct {
	ClaudeSessionID string
	// Name is the forge session name, or "" for sessions forge didn't start.
	Name    string
	Project string
	Cwd     string
	// Last is the last day with usage.
	Last time.Time
	Totals
}

// Label is the name to show: the forge session name, else the short
// session id and the working directory's base name.
func (s SessionRow) Label() string {
	if s.Name != "" {
		return s.Name
	}
	id := s.ClaudeSessionID
	if len(id) > 8 {
		id = id[:8]
	}
	if s.Cwd != "" {
		return id + " · " + filepath.Base(s.Cwd)
	}
	return id
}

// Report is everything the usage view shows.
type Report struct {
	Since time.Time
	// Project filters Days and Sessions; "" means every project. Projects
	// always lists them all.
	Project  string
	Days     []Day
	Projects []ProjectRow
	Sessions []SessionRow
	// Total covers Days (so it follows the filter).
	Total Totals
}

// Unpriced reports whether any tokens in the report lack a price.
func (r Report) Unpriced() bool {
	if r.Total.Unpriced > 0 {
		return true
	}
	for _, p := range r.Projects {
		if p.Unpriced > 0 {
			return true
		}
	}
	return false
}

// AggregateInput is what Aggregate works from.
type AggregateInput struct {
	Rows     []store.UsageRow
	Prices   Prices
	Resolver Resolver
	// Cwds maps a session to its working directory, for labels.
	Cwds    map[string]string
	Today   time.Time
	Days    int
	Top     int
	Project string
}

// Aggregate builds the report: the last in.Days days (empty days included,
// oldest first), every project and the in.Top sessions using the most
// tokens, both largest first.
func Aggregate(in AggregateInput) Report {
	days := max(in.Days, 1)
	today := dayOf(in.Today)
	since := today.AddDate(0, 0, -(days - 1))
	r := Report{Since: since, Project: in.Project, Days: make([]Day, days)}
	for i := range r.Days {
		r.Days[i].Day = since.AddDate(0, 0, i)
	}
	projects := map[string]*ProjectRow{}
	sessions := map[string]*SessionRow{}
	for _, u := range in.Rows {
		d := dayOf(u.Day)
		if d.Before(since) || d.After(today) {
			continue
		}
		p := projects[u.Project]
		if p == nil {
			p = &ProjectRow{Project: u.Project}
			projects[u.Project] = p
		}
		p.Add(in.Prices, u.Model, u.Tokens)
		if in.Project != "" && u.Project != in.Project {
			continue
		}
		i := daysBetween(since, d)
		r.Days[i].Add(in.Prices, u.Model, u.Tokens)
		r.Total.Add(in.Prices, u.Model, u.Tokens)
		if u.ClaudeSessionID == "" {
			continue
		}
		s := sessions[u.ClaudeSessionID]
		if s == nil {
			s = &SessionRow{ClaudeSessionID: u.ClaudeSessionID, Project: u.Project, Cwd: in.Cwds[u.ClaudeSessionID]}
			if x, ok := in.Resolver.Session(u.ClaudeSessionID); ok {
				s.Name = x.Name
			}
			sessions[u.ClaudeSessionID] = s
		}
		s.Add(in.Prices, u.Model, u.Tokens)
		if d.After(s.Last) {
			s.Last = d
		}
	}
	for _, p := range projects {
		r.Projects = append(r.Projects, *p)
	}
	sort.Slice(r.Projects, func(i, j int) bool {
		a, b := r.Projects[i], r.Projects[j]
		if a.Total() != b.Total() {
			return a.Total() > b.Total()
		}
		return a.Project < b.Project
	})
	for _, s := range sessions {
		r.Sessions = append(r.Sessions, *s)
	}
	sort.Slice(r.Sessions, func(i, j int) bool {
		a, b := r.Sessions[i], r.Sessions[j]
		if a.Total() != b.Total() {
			return a.Total() > b.Total()
		}
		return a.ClaudeSessionID < b.ClaudeSessionID
	})
	if in.Top > 0 && len(r.Sessions) > in.Top {
		r.Sessions = r.Sessions[:in.Top]
	}
	return r
}

// dayOf is the local midnight of t's local date.
func dayOf(t time.Time) time.Time {
	t = t.In(time.Local)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
}

// daysBetween counts calendar days from a to b (both local midnights),
// robust to DST changes.
func daysBetween(a, b time.Time) int {
	n := 0
	for d := a; d.Before(b); d = d.AddDate(0, 0, 1) {
		n++
	}
	return n
}
