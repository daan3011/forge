package capture

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
)

// MaxTitle is Plane's limit on a work item's name.
const MaxTitle = 255

// ErrNoProject is Resolve's error when no #project is given and there is
// no default.
var ErrNoProject = errors.New("which project? add #project")

// ValidateTitle checks a work item title: required, at most MaxTitle
// characters.
func ValidateTitle(s string) error {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return errors.New("a title is required")
	case runeLen(s) > MaxTitle:
		return fmt.Errorf("at most %d characters", MaxTitle)
	}
	return nil
}

// UnknownError is a token that names nothing, with the closest match.
type UnknownError struct {
	Token      Token
	Suggestion string // a token value; "" when nothing is close
}

func (e *UnknownError) Error() string {
	msg := fmt.Sprintf("no %s %q", e.Token.Kind.Noun(), e.Token.Value)
	if e.Suggestion != "" {
		msg += " — did you mean " + e.Suggestion + "?"
	}
	return msg
}

// Fix is the edit that applies the suggestion: replace [Start, End) with
// Text. ok is false without a suggestion.
func (e *UnknownError) Fix() (start, end int, text string, ok bool) {
	if e.Suggestion == "" {
		return 0, 0, "", false
	}
	return e.Token.Start, e.Token.End, e.Token.Kind.Sigil() + e.Suggestion, true
}

// NotLoadedError says Resolve needs more of the catalog first; load Need
// (see Load) and resolve again.
type NotLoadedError struct{ Need Need }

func (e *NotLoadedError) Error() string { return "still loading " + e.Need.String() + "…" }

// Result is a resolved capture.
type Result struct {
	ProjectID string
	Project   plane.Project // zero when the project list isn't loaded
	Create    plane.WorkItemCreate
}

// Resolve turns a draft into a work item for Plane project defaultProject
// unless a #project names another. Unknown tokens are *UnknownError;
// tokens that need data not loaded yet are *NotLoadedError.
func (c Catalog) Resolve(d Draft, defaultProject string) (Result, error) {
	if err := ValidateTitle(d.Title); err != nil {
		return Result{}, err
	}
	var r Result
	r.Create.Name = strings.TrimSpace(d.Title)

	pid, err := c.resolveProject(d, defaultProject)
	if err != nil {
		return Result{}, err
	}
	r.ProjectID = pid
	r.Project = c.project(pid)

	if t, ok := d.last(Priority); ok {
		p, ok := parsePriority(t.Value)
		if !ok {
			return Result{}, &UnknownError{Token: t, Suggestion: closest(t.Value, c.Candidates(Priority, pid))}
		}
		r.Create.Priority = p
	}

	people, labels := d.Of(Person), d.Of(Label)
	var need Need
	if !c.HasDetails(pid) && (len(labels) > 0 || slices.ContainsFunc(people, func(t Token) bool { return !isMe(t.Value) })) {
		need.Details = []string{pid}
	}
	if !c.HasMe() && slices.ContainsFunc(people, func(t Token) bool { return isMe(t.Value) }) {
		need.Me = true
	}
	if !need.Empty() {
		return Result{}, &NotLoadedError{Need: need}
	}

	det := c.Details[pid]
	for _, t := range people {
		id, ok := c.person(det, t.Value)
		if !ok {
			return Result{}, &UnknownError{Token: t, Suggestion: closest(t.Value, c.Candidates(Person, pid))}
		}
		if !slices.Contains(r.Create.AssigneeIDs, id) {
			r.Create.AssigneeIDs = append(r.Create.AssigneeIDs, id)
		}
	}
	for _, t := range labels {
		i := slices.IndexFunc(det.Labels, func(l plane.Label) bool { return norm(l.Name) == norm(t.Value) })
		if i < 0 {
			return Result{}, &UnknownError{Token: t, Suggestion: closest(t.Value, c.Candidates(Label, pid))}
		}
		if id := det.Labels[i].ID; !slices.Contains(r.Create.LabelIDs, id) {
			r.Create.LabelIDs = append(r.Create.LabelIDs, id)
		}
	}
	return r, nil
}

// resolveProject is the last #project's Plane project id, else the
// default.
func (c Catalog) resolveProject(d Draft, defaultProject string) (string, error) {
	t, ok := d.last(Project)
	if !ok {
		if defaultProject == "" {
			return "", ErrNoProject
		}
		return defaultProject, nil
	}
	if !c.HasProjects() {
		return "", &NotLoadedError{Need: Need{Projects: true}}
	}
	if id, ok := c.findProject(t.Value); ok {
		return id, nil
	}
	return "", &UnknownError{Token: t, Suggestion: closest(t.Value, c.Candidates(Project, defaultProject))}
}

// findProject matches a #project value against Plane identifiers, names
// and ids, and the forge project names linked to them.
func (c Catalog) findProject(v string) (string, bool) {
	n := norm(v)
	for _, p := range c.Projects {
		if p.ID == v || norm(p.Identifier) == n || norm(p.Name) == n {
			return p.ID, true
		}
	}
	for _, p := range c.Projects {
		if slices.ContainsFunc(c.Aliases[p.ID], func(a string) bool { return norm(a) == n }) {
			return p.ID, true
		}
	}
	return "", false
}

func isMe(v string) bool { return strings.EqualFold(v, "me") }

// person matches an @value: me, or a member's display name, full name,
// first name or email local part.
func (c Catalog) person(det Details, v string) (string, bool) {
	if isMe(v) {
		return c.Me.ID, c.Me.ID != ""
	}
	n := norm(v)
	for _, m := range det.Members {
		if m.Member == nil {
			continue
		}
		if slices.Contains(personNames(*m.Member), n) {
			return m.MemberID, true
		}
	}
	return "", false
}

// personNames are the folded names a user answers to.
func personNames(u plane.User) []string {
	names := []string{norm(u.DisplayName)}
	if u.FirstName != "" {
		names = append(names, norm(u.FirstName), norm(u.FirstName+" "+u.LastName))
	}
	if local, _, ok := strings.Cut(u.Email, "@"); ok && local != "" {
		names = append(names, norm(local))
	}
	return names
}

// priorities are Plane's priorities, most urgent first.
var priorities = []plane.Priority{
	plane.PriorityUrgent, plane.PriorityHigh, plane.PriorityMedium, plane.PriorityLow, plane.PriorityNone,
}

// parsePriority accepts a priority or a prefix of one (!h, !med).
func parsePriority(v string) (plane.Priority, bool) {
	n := norm(v)
	for _, p := range priorities {
		if strings.HasPrefix(string(p), n) {
			return p, true
		}
	}
	return "", false
}

// maxRounds bounds ResolveLoading: projects, then details and you.
const maxRounds = 3

// ResolveLoading resolves d against cat, loading from src whatever the
// tokens need that cat lacks. It returns the catalog as grown.
func ResolveLoading(ctx context.Context, src Source, forge []config.Project, cat Catalog, d Draft, defaultProject string) (Result, Catalog, error) {
	for range maxRounds {
		r, err := cat.Resolve(d, defaultProject)
		var nl *NotLoadedError
		if !errors.As(err, &nl) {
			return r, cat, err
		}
		more, err := Load(ctx, src, forge, nl.Need)
		cat = cat.Merge(more)
		if err != nil {
			return Result{}, cat, err
		}
	}
	r, err := cat.Resolve(d, defaultProject)
	return r, cat, err
}

// ResolveLenient is Resolve for prefilling the full form: tokens that
// name nothing (or need data not loaded yet) are left out, and the title
// isn't checked; the form takes it from there. Only a project that can't
// be found is an error.
func (c Catalog) ResolveLenient(d Draft, defaultProject string) (Result, error) {
	var r Result
	r.Create.Name = strings.TrimSpace(d.Title)
	pid, err := c.resolveProject(d, defaultProject)
	if err != nil {
		var ue *UnknownError
		if defaultProject == "" || !errors.As(err, &ue) && !errors.As(err, new(*NotLoadedError)) {
			return Result{}, err
		}
		pid = defaultProject
	}
	r.ProjectID = pid
	r.Project = c.project(pid)
	if t, ok := d.last(Priority); ok {
		r.Create.Priority, _ = parsePriority(t.Value)
	}
	det := c.Details[pid]
	for _, t := range d.Of(Person) {
		if id, ok := c.person(det, t.Value); ok && !slices.Contains(r.Create.AssigneeIDs, id) {
			r.Create.AssigneeIDs = append(r.Create.AssigneeIDs, id)
		}
	}
	for _, t := range d.Of(Label) {
		i := slices.IndexFunc(det.Labels, func(l plane.Label) bool { return norm(l.Name) == norm(t.Value) })
		if i >= 0 && !slices.Contains(r.Create.LabelIDs, det.Labels[i].ID) {
			r.Create.LabelIDs = append(r.Create.LabelIDs, det.Labels[i].ID)
		}
	}
	return r, nil
}
