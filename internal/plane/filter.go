package plane

import (
	"net/url"
	"strings"
	"time"
)

// Archived selects archived work items.
type Archived string

// Archived values; the zero value is the API default (exclude archived).
const (
	ArchivedExclude Archived = ""
	ArchivedInclude Archived = "true"
	ArchivedOnly    Archived = "only"
)

// WorkItemFilter narrows WorkItems. Multi-value fields match any value.
type WorkItemFilter struct {
	StateIDs     []string
	StateGroups  []StateGroup
	Priorities   []Priority
	AssigneeIDs  []string
	LabelIDs     []string
	CycleID      string
	Search       string // matches the name
	UpdatedAfter time.Time
	Archived     Archived
	// OrderBy is created_at, updated_at, sequence_id, priority, state_group
	// or sort_order, "-" for descending (default -created_at).
	OrderBy string
	// Expand adds objects next to their *_id: state, assignees, labels,
	// cycle, parent, type, modules, estimate_point.
	Expand []string
	// Fields selects a sparse set of fields (description_html is only in
	// lists when named here).
	Fields []string
	// Limit caps the number of items (0 = all, up to the page guard).
	Limit int
}

// encode returns the filter's query parameters (not paging).
func (f WorkItemFilter) encode() url.Values {
	q := url.Values{}
	in := func(name string, vals []string) {
		switch len(vals) {
		case 0:
		case 1:
			q.Set(name, vals[0])
		default:
			q.Set(name+"__in", strings.Join(vals, ","))
		}
	}
	in("state_id", f.StateIDs)
	in("state_group", strs(f.StateGroups))
	in("priority", strs(f.Priorities))
	in("assignee_id", f.AssigneeIDs)
	in("label_id", f.LabelIDs)
	if f.CycleID != "" {
		q.Set("cycle_id", f.CycleID)
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		q.Set("search", s)
	}
	if !f.UpdatedAfter.IsZero() {
		q.Set("updated_at__gte", f.UpdatedAfter.UTC().Format(time.RFC3339))
	}
	if f.Archived != ArchivedExclude {
		q.Set("archived", string(f.Archived))
	}
	if f.OrderBy != "" {
		q.Set("order_by", f.OrderBy)
	}
	if len(f.Expand) > 0 {
		q.Set("expand", strings.Join(f.Expand, ","))
	}
	if len(f.Fields) > 0 {
		q.Set("fields", strings.Join(f.Fields, ","))
	}
	return q
}

func strs[S ~string](vals []S) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = string(v)
	}
	return out
}
