package plane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// Date is a Plane date or date-time. It accepts "2026-01-02", RFC 3339 and
// null; the zero value means unset and encodes as null.
type Date struct{ time.Time }

// UnmarshalJSON implements json.Unmarshaler.
func (d *Date) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("null")) || bytes.Equal(b, []byte(`""`)) {
		d.Time = time.Time{}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("plane: date: %w", err)
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999", time.DateOnly} {
		if t, err := time.Parse(layout, s); err == nil {
			d.Time = t
			return nil
		}
	}
	return fmt.Errorf("plane: date: can't parse %q", s)
}

// MarshalJSON implements json.Marshaler.
func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(d.Format(time.RFC3339))
}

// StateGroup is the category every state belongs to.
type StateGroup string

// State groups.
const (
	GroupBacklog   StateGroup = "backlog"
	GroupUnstarted StateGroup = "unstarted"
	GroupStarted   StateGroup = "started"
	GroupCompleted StateGroup = "completed"
	GroupCancelled StateGroup = "cancelled"
	GroupTriage    StateGroup = "triage"
)

// Priority is a work item's priority.
type Priority string

// Priorities, most urgent first.
const (
	PriorityUrgent Priority = "urgent"
	PriorityHigh   Priority = "high"
	PriorityMedium Priority = "medium"
	PriorityLow    Priority = "low"
	PriorityNone   Priority = "none"
)

// User is a Plane user (users/me, expanded assignees and members).
type User struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	FirstName   string `json:"first_name,omitempty"`
	LastName    string `json:"last_name,omitempty"`
	Email       string `json:"email,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
}

// Project is a Plane project.
type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Identifier  string `json:"identifier"`
	Description string `json:"description,omitempty"`
	Network     int    `json:"network"`
	ArchivedAt  Date   `json:"archived_at"`
	CreatedAt   Date   `json:"created_at"`
	UpdatedAt   Date   `json:"updated_at"`
}

// State is a workflow state of a project.
type State struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Color       string     `json:"color"`
	Group       StateGroup `json:"group"`
	Sequence    float64    `json:"sequence"`
	IsDefault   bool       `json:"is_default"`
	IsTriage    bool       `json:"is_triage"`
}

// Label is a project label.
type Label struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Color       string  `json:"color"`
	ParentID    string  `json:"parent_id,omitempty"`
	SortOrder   float64 `json:"sort_order"`
}

// Member is a workspace or project membership. ID is the membership row;
// MemberID is the user.
type Member struct {
	ID       string `json:"id"`
	MemberID string `json:"member_id"`
	Role     string `json:"role"`
	Member   *User  `json:"member,omitempty"`
}

// Cycle is a project cycle (sprint).
type Cycle struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	StartDate   Date   `json:"start_date"`
	EndDate     Date   `json:"end_date"`
	OwnedByID   string `json:"owned_by_id,omitempty"`
	ArchivedAt  Date   `json:"archived_at"`
}

// IsCurrent reports whether now falls inside the cycle. A date-only end
// date counts through the end of that day.
func (c Cycle) IsCurrent(now time.Time) bool {
	if c.StartDate.IsZero() || c.EndDate.IsZero() || !c.ArchivedAt.IsZero() {
		return false
	}
	end := c.EndDate.Time
	if end.Hour() == 0 && end.Minute() == 0 && end.Second() == 0 && end.Nanosecond() == 0 {
		end = end.Add(24*time.Hour - time.Nanosecond)
	}
	return !now.Before(c.StartDate.Time) && !now.After(end)
}

// WorkItem is a Plane work item (formerly "issue"). The expanded fields are
// only set when asked for with WorkItemFilter.Expand.
type WorkItem struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Identifier      string   `json:"identifier"`
	SequenceID      int      `json:"sequence_id"`
	ProjectID       string   `json:"project_id"`
	StateID         string   `json:"state_id"`
	Priority        Priority `json:"priority"`
	AssigneeIDs     []string `json:"assignee_ids"`
	LabelIDs        []string `json:"label_ids"`
	ModuleIDs       []string `json:"module_ids,omitempty"`
	CycleID         string   `json:"cycle_id,omitempty"`
	ParentID        string   `json:"parent_id,omitempty"`
	TypeID          string   `json:"type_id,omitempty"`
	DescriptionHTML string   `json:"description_html,omitempty"`
	IsDraft         bool     `json:"is_draft"`
	StartDate       Date     `json:"start_date"`
	TargetDate      Date     `json:"target_date"`
	CompletedAt     Date     `json:"completed_at"`
	ArchivedAt      Date     `json:"archived_at"`
	CreatedAt       Date     `json:"created_at"`
	UpdatedAt       Date     `json:"updated_at"`
	CreatedByID     string   `json:"created_by_id,omitempty"`

	State     *State  `json:"state,omitempty"`
	Assignees []User  `json:"assignees,omitempty"`
	Labels    []Label `json:"labels,omitempty"`
	Cycle     *Cycle  `json:"cycle,omitempty"`
}

// Key is the human key, e.g. PROJ-12 (Identifier, or ID if unknown).
func (w WorkItem) Key() string {
	if w.Identifier != "" {
		return w.Identifier
	}
	return w.ID
}

// Comment is a comment on a work item.
type Comment struct {
	ID              string `json:"id"`
	WorkItemID      string `json:"work_item_id"`
	ParentID        string `json:"parent_id,omitempty"`
	CommentHTML     string `json:"comment_html"`
	CommentStripped string `json:"comment_stripped"`
	Access          string `json:"access"`
	ActorID         string `json:"actor_id"`
	EditedAt        Date   `json:"edited_at"`
	CreatedAt       Date   `json:"created_at"`
	UpdatedAt       Date   `json:"updated_at"`
}

// WorkItemCreate is the body of CreateWorkItem. Send either the *ID fields
// or their name-based twins (State, Assignees as emails, Labels as names),
// not both.
type WorkItemCreate struct {
	Name            string   `json:"name"`
	DescriptionHTML string   `json:"description_html,omitempty"`
	StateID         string   `json:"state_id,omitempty"`
	State           string   `json:"state,omitempty"`
	Priority        Priority `json:"priority,omitempty"`
	AssigneeIDs     []string `json:"assignee_ids,omitempty"`
	Assignees       []string `json:"assignees,omitempty"`
	LabelIDs        []string `json:"label_ids,omitempty"`
	Labels          []string `json:"labels,omitempty"`
	ParentID        string   `json:"parent_id,omitempty"`
	CycleID         string   `json:"cycle_id,omitempty"`
	StartDate       string   `json:"start_date,omitempty"`  // YYYY-MM-DD
	TargetDate      string   `json:"target_date,omitempty"` // YYYY-MM-DD
}

// WorkItemUpdate is the body of UpdateWorkItem (PATCH): only non-nil
// fields are sent.
type WorkItemUpdate struct {
	Name            *string   `json:"name,omitempty"`
	DescriptionHTML *string   `json:"description_html,omitempty"`
	StateID         *string   `json:"state_id,omitempty"`
	State           *string   `json:"state,omitempty"`
	Priority        *Priority `json:"priority,omitempty"`
	AssigneeIDs     *[]string `json:"assignee_ids,omitempty"`
	LabelIDs        *[]string `json:"label_ids,omitempty"`
	ParentID        *string   `json:"parent_id,omitempty"`
	CycleID         *string   `json:"cycle_id,omitempty"`
	StartDate       *string   `json:"start_date,omitempty"`
	TargetDate      *string   `json:"target_date,omitempty"`
}

// MarshalJSON implements json.Marshaler: a CycleID pointing to "" takes
// the item out of its cycle (null; Plane rejects "" as a UUID).
func (u WorkItemUpdate) MarshalJSON() ([]byte, error) {
	type plain WorkItemUpdate
	b, err := json.Marshal(plain(u))
	if err != nil || u.CycleID == nil || *u.CycleID != "" {
		return b, err
	}
	return bytes.Replace(b, []byte(`"cycle_id":""`), []byte(`"cycle_id":null`), 1), nil
}

// CommentCreate is the body of CreateComment.
type CommentCreate struct {
	CommentHTML string `json:"comment_html"`
	Access      string `json:"access,omitempty"` // INTERNAL (default) or EXTERNAL
	ParentID    string `json:"parent_id,omitempty"`
}
