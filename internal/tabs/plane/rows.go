package plane

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/ui"
)

// cellSep splits the parts of an encoded cell. It can't be typed, so `/`
// never matches across it.
const cellSep = "\x1f"

// columns are the issue table's columns. The last one, the work item id,
// is hidden and finds the item again.
func (t *Tab) columns() []ui.Column {
	return []ui.Column{
		{Title: "Key", Width: 10},
		{Title: "Title", Width: 16, Flex: true},
		{Title: "State", Width: 12, Render: t.renderState},
		{Title: "P", Width: 2, Render: t.renderPriority},
		{Title: "Labels", Width: 14},
		{Title: "Assignee", Width: 12},
		{Title: "Updated", Width: 7, Render: t.renderUpdated},
		{Title: "id", Hidden: true},
	}
}

// itemRow is a work item's cells, in column order.
func (t *Tab) itemRow(w plane.WorkItem) []string {
	name, group := t.stateOf(w)
	return []string{
		w.Key(),
		w.Name,
		string(group) + cellSep + name,
		string(w.Priority),
		strings.Join(t.labelNames(w), ", "),
		assigneeCell(t.assigneeNames(w)),
		unix(w.UpdatedAt.Time),
		w.ID,
	}
}

// stateOf is the item's state name and group: the expanded state, else
// the project's states, else unknown.
func (t *Tab) stateOf(w plane.WorkItem) (string, plane.StateGroup) {
	if w.State != nil {
		return w.State.Name, w.State.Group
	}
	if m := t.meta[w.ProjectID]; m != nil {
		for _, s := range m.states {
			if s.ID == w.StateID {
				return s.Name, s.Group
			}
		}
	}
	return "", ""
}

// labelNames are the item's label names: expanded, else from the project.
func (t *Tab) labelNames(w plane.WorkItem) []string {
	var out []string
	if len(w.Labels) > 0 {
		for _, l := range w.Labels {
			out = append(out, l.Name)
		}
		return out
	}
	m := t.meta[w.ProjectID]
	if m == nil {
		return nil
	}
	for _, id := range w.LabelIDs {
		for _, l := range m.labels {
			if l.ID == id {
				out = append(out, l.Name)
			}
		}
	}
	return out
}

// assigneeNames are the display names of the item's assignees.
func (t *Tab) assigneeNames(w plane.WorkItem) []string {
	var out []string
	if len(w.Assignees) > 0 {
		for _, u := range w.Assignees {
			out = append(out, userName(u))
		}
		return out
	}
	for _, id := range w.AssigneeIDs {
		out = append(out, t.memberName(w.ProjectID, id))
	}
	return out
}

// memberName is a user's display name from the project's members, or
// "someone".
func (t *Tab) memberName(projectID, userID string) string {
	if t.me.ID != "" && userID == t.me.ID {
		return userName(t.me)
	}
	if m := t.meta[projectID]; m != nil {
		for _, mem := range m.members {
			if mem.MemberID == userID && mem.Member != nil {
				return userName(*mem.Member)
			}
		}
	}
	return "someone"
}

func userName(u plane.User) string {
	switch {
	case u.DisplayName != "":
		return u.DisplayName
	case u.FirstName != "" || u.LastName != "":
		return strings.TrimSpace(u.FirstName + " " + u.LastName)
	case u.Email != "":
		return u.Email
	}
	return "someone"
}

// assigneeCell is the first assignee and how many more: "daan +2".
func assigneeCell(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return names[0] + " +" + strconv.Itoa(len(names)-1)
}

func unix(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return strconv.FormatInt(at.Unix(), 10)
}

func fromUnix(s string) time.Time {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n == 0 {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

func (t *Tab) renderState(cell string, width int, row lipgloss.Style) string {
	group, name, _ := strings.Cut(cell, cellSep)
	return pad(t.theme.StateGroupStyle(group).Inherit(row).Render(ansi.Truncate(name, width, "…")), width, row)
}

func (t *Tab) renderPriority(cell string, width int, row lipgloss.Style) string {
	glyph, st := t.theme.PriorityIcon(cell)
	return pad(st.Inherit(row).Render(ansi.Truncate(glyph, width, "")), width, row)
}

func (t *Tab) renderUpdated(cell string, width int, row lipgloss.Style) string {
	text := ""
	if at := fromUnix(cell); !at.IsZero() {
		text = ui.ShortAge(t.now().Sub(at))
	}
	return pad(t.theme.PRTime.Inherit(row).Render(ansi.Truncate(text, width, "")), width, row)
}

// pad fills s out to width cells in the row's style.
func pad(s string, width int, row lipgloss.Style) string {
	return s + row.Render(strings.Repeat(" ", max(width-ansi.StringWidth(s), 0)))
}
