package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// ProposalStatus is where an agent-proposed issue stands (P6.5).
type ProposalStatus string

// Proposal statuses.
const (
	ProposalProposed ProposalStatus = "proposed"
	ProposalAccepted ProposalStatus = "accepted"
	ProposalDropped  ProposalStatus = "dropped"
)

// Proposal is an issue an agent filed through forge mcp. It waits on Home
// until the user accepts (creates it in Plane), edits or drops it.
type Proposal struct {
	ID int64
	// SessionID is 0 once the session is deleted; SessionName stays.
	SessionID      int64
	SessionName    string
	Project        string // forge project name
	PlaneProjectID string // resolved when filed; may be empty until accepted
	Title          string
	Description    string // markdown
	Priority       string // plane priority name; "" is none
	Labels         []string
	Status         ProposalStatus
	PlaneIssueID   string
	PlaneKey       string // e.g. FORGE-12, once accepted
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Ref returns the proposal's link Ref.
func (p Proposal) Ref() Ref { return ProposalRef(p.ID) }

// Handle is how agents and the UI name a proposal: "P-<id>".
func (p Proposal) Handle() string { return ProposalHandle(p.ID) }

// ProposalHandle formats a proposal id as "P-<id>".
func ProposalHandle(id int64) string { return "P-" + strconv.FormatInt(id, 10) }

// ParseProposalHandle parses "P-<id>" (case-insensitive "p-" too).
func ParseProposalHandle(s string) (int64, bool) {
	if len(s) < 3 || (s[0] != 'P' && s[0] != 'p') || s[1] != '-' {
		return 0, false
	}
	id, err := strconv.ParseInt(s[2:], 10, 64)
	return id, err == nil && id > 0
}

// ProposalEdit is the editable part of a proposal; nil fields are kept.
type ProposalEdit struct {
	Title       *string
	Description *string
	Priority    *string
	Labels      *[]string
}

// Proposals is the repository for agent-proposed issues.
type Proposals struct{ s *Store }

// Proposals returns the proposals repository.
func (s *Store) Proposals() *Proposals { return &Proposals{s} }

const proposalCols = `id, session_id, session_name, project, plane_project_id, title, description,
	priority, labels, status, plane_issue_id, plane_key, created_at, updated_at`

func scanProposal(sc interface{ Scan(...any) error }) (Proposal, error) {
	var (
		p                                         Proposal
		sid                                       sql.NullInt64
		pp, desc, prio, labels, planeID, planeKey sql.NullString
		created, updated                          int64
	)
	if err := sc.Scan(&p.ID, &sid, &p.SessionName, &p.Project, &pp, &p.Title, &desc,
		&prio, &labels, &p.Status, &planeID, &planeKey, &created, &updated); err != nil {
		return Proposal{}, err
	}
	p.SessionID, p.PlaneProjectID, p.Description, p.Priority = sid.Int64, pp.String, desc.String, prio.String
	p.PlaneIssueID, p.PlaneKey = planeID.String, planeKey.String
	p.CreatedAt, p.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
	if labels.Valid && labels.String != "" {
		if err := json.Unmarshal([]byte(labels.String), &p.Labels); err != nil {
			return Proposal{}, fmt.Errorf("labels: %w", err)
		}
	}
	return p, nil
}

func labelsJSON(ls []string) (sql.NullString, error) {
	if len(ls) == 0 {
		return sql.NullString{}, nil
	}
	b, err := json.Marshal(ls)
	return sql.NullString{String: string(b), Valid: true}, err
}

// Create inserts p (status defaults to proposed) and returns it with its
// id and timestamps.
func (r *Proposals) Create(ctx context.Context, p Proposal) (Proposal, error) {
	if p.Status == "" {
		p.Status = ProposalProposed
	}
	now := r.s.now()
	p.CreatedAt, p.UpdatedAt = now, now
	ls, err := labelsJSON(p.Labels)
	if err != nil {
		return Proposal{}, wrap("create proposal", err)
	}
	sid := sql.NullInt64{Int64: p.SessionID, Valid: p.SessionID != 0}
	res, err := r.s.w.ExecContext(ctx, `INSERT INTO proposed_issues (session_id, session_name, project,
		plane_project_id, title, description, priority, labels, status, plane_issue_id, plane_key,
		created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sid, p.SessionName, p.Project, nullStr(p.PlaneProjectID), p.Title, nullStr(p.Description),
		nullStr(p.Priority), ls, p.Status, nullStr(p.PlaneIssueID), nullStr(p.PlaneKey),
		now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return Proposal{}, wrap("create proposal", err)
	}
	if p.ID, err = res.LastInsertId(); err != nil {
		return Proposal{}, wrap("create proposal", err)
	}
	return p, nil
}

// Get returns the proposal with id.
func (r *Proposals) Get(ctx context.Context, id int64) (Proposal, error) {
	p, err := scanProposal(r.s.r.QueryRowContext(ctx,
		`SELECT `+proposalCols+` FROM proposed_issues WHERE id = ?`, id))
	return p, wrap("get proposal", err)
}

// ProposalFilter narrows List; zero fields match everything.
type ProposalFilter struct {
	Status    ProposalStatus
	Project   string
	SessionID int64
}

// List returns matching proposals, oldest first.
func (r *Proposals) List(ctx context.Context, f ProposalFilter) ([]Proposal, error) {
	q := `SELECT ` + proposalCols + ` FROM proposed_issues WHERE 1=1`
	var args []any
	if f.Status != "" {
		q += ` AND status = ?`
		args = append(args, f.Status)
	}
	if f.Project != "" {
		q += ` AND project = ?`
		args = append(args, f.Project)
	}
	if f.SessionID != 0 {
		q += ` AND session_id = ?`
		args = append(args, f.SessionID)
	}
	rows, err := r.s.r.QueryContext(ctx, q+` ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, wrap("list proposals", err)
	}
	defer rows.Close()
	var out []Proposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, wrap("list proposals", err)
		}
		out = append(out, p)
	}
	return out, wrap("list proposals", rows.Err())
}

// Update applies e to a proposal that is still proposed; otherwise it
// returns ErrConflict (ErrNotFound when there is no such proposal).
func (r *Proposals) Update(ctx context.Context, id int64, e ProposalEdit) (Proposal, error) {
	p, err := r.Get(ctx, id)
	if err != nil {
		return Proposal{}, err
	}
	if p.Status != ProposalProposed {
		return Proposal{}, fmt.Errorf("store: update proposal %s: %s: %w", p.Handle(), p.Status, ErrConflict)
	}
	if e.Title != nil {
		p.Title = *e.Title
	}
	if e.Description != nil {
		p.Description = *e.Description
	}
	if e.Priority != nil {
		p.Priority = *e.Priority
	}
	if e.Labels != nil {
		p.Labels = *e.Labels
	}
	ls, err := labelsJSON(p.Labels)
	if err != nil {
		return Proposal{}, wrap("update proposal", err)
	}
	p.UpdatedAt = r.s.now()
	res, err := r.s.w.ExecContext(ctx, `UPDATE proposed_issues SET title = ?, description = ?,
		priority = ?, labels = ?, updated_at = ? WHERE id = ? AND status = 'proposed'`,
		p.Title, nullStr(p.Description), nullStr(p.Priority), ls, p.UpdatedAt.UnixMilli(), id)
	if err := conflictIfNone("update proposal", res, err); err != nil {
		return Proposal{}, err
	}
	return p, nil
}

// SetPlaneProject records the Plane project a proposal will be created in.
func (r *Proposals) SetPlaneProject(ctx context.Context, id int64, planeProjectID string) error {
	res, err := r.s.w.ExecContext(ctx, `UPDATE proposed_issues SET plane_project_id = ?, updated_at = ?
		WHERE id = ?`, nullStr(planeProjectID), r.s.now().UnixMilli(), id)
	return mustAffect("set proposal plane project", res, err)
}

// Accept marks a proposed issue as created in Plane. It returns ErrConflict
// when the proposal was already accepted or dropped.
func (r *Proposals) Accept(ctx context.Context, id int64, planeIssueID, planeKey string) error {
	res, err := r.s.w.ExecContext(ctx, `UPDATE proposed_issues SET status = 'accepted',
		plane_issue_id = ?, plane_key = ?, updated_at = ? WHERE id = ? AND status = 'proposed'`,
		planeIssueID, planeKey, r.s.now().UnixMilli(), id)
	return conflictIfNone("accept proposal", res, err)
}

// Drop marks a proposed issue as dropped. It returns ErrConflict when it
// was already accepted or dropped.
func (r *Proposals) Drop(ctx context.Context, id int64) error {
	res, err := r.s.w.ExecContext(ctx, `UPDATE proposed_issues SET status = 'dropped', updated_at = ?
		WHERE id = ? AND status = 'proposed'`, r.s.now().UnixMilli(), id)
	return conflictIfNone("drop proposal", res, err)
}

// conflictIfNone is mustAffect for guarded updates: no row changed means
// the row is gone (ErrNotFound) or not in the expected state (ErrConflict).
func conflictIfNone(op string, res sql.Result, err error) error {
	if err != nil {
		return wrap(op, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return wrap(op, err)
	}
	if n == 0 {
		return fmt.Errorf("store: %s: %w", op, ErrConflict)
	}
	return nil
}
