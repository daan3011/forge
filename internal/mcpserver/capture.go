package mcpserver

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/daan3011/forge/internal/capture"
	"github.com/daan3011/forge/internal/projects"
)

// applyCapture folds in.Capture (quick-capture text, P3.5 syntax) into in
// and returns the forge project to file under (def without a #project)
// and notes for the agent. The words become the title when in.Title is
// empty, !priority sets the priority unless in.Priority does, and +labels
// are added to in.Labels. @person tokens are reported and ignored: a
// proposal has no assignee.
func (s *Server) applyCapture(ctx context.Context, in *CreateIssueIn, def string) (project, notes string, err error) {
	project = def
	text := strings.TrimSpace(in.Capture)
	if text == "" {
		return project, "", nil
	}
	if strings.ContainsAny(text, "\r\n") {
		text, _, _ = strings.Cut(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	}
	d := capture.Parse(text)
	if strings.TrimSpace(in.Title) == "" {
		in.Title = d.Title
	}
	if ts := d.Of(capture.Priority); len(ts) > 0 && in.Priority == "" {
		v := ts[len(ts)-1].Value
		p, ok := capture.ParsePriority(v)
		if !ok {
			return "", "", fmt.Errorf("unknown priority !%s: use urgent, high, medium, low or none (a prefix like !h works)", v)
		}
		in.Priority = string(p)
	}
	for _, t := range d.Of(capture.Label) {
		if !slices.ContainsFunc(in.Labels, func(l string) bool { return strings.EqualFold(l, t.Value) }) {
			in.Labels = append(in.Labels, t.Value)
		}
	}
	if ts := d.Of(capture.Project); len(ts) > 0 {
		project, err = s.captureProject(ctx, ts[len(ts)-1].Value)
		if err != nil {
			return "", "", err
		}
	}
	if ps := d.Of(capture.Person); len(ps) > 0 {
		var names []string
		for _, p := range ps {
			names = append(names, "@"+p.Value)
		}
		notes = " Ignored " + strings.Join(names, ", ") + ": proposals have no assignee; the user assigns it when accepting."
	}
	return project, notes, nil
}

// captureProject resolves a #project token to a forge project: by forge
// project name, else by the Plane project (identifier or name) a forge
// project is linked to.
func (s *Server) captureProject(ctx context.Context, v string) (string, error) {
	fold := func(a string) string { return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(a), " ", "-")) }
	want := fold(v)
	var names []string
	for _, p := range s.d.Config.Projects {
		if fold(p.Name) == want {
			return p.Name, nil
		}
		names = append(names, p.Name)
	}
	if s.d.Plane != nil {
		if ps, err := s.d.Plane.Projects(ctx); err == nil {
			for _, pp := range ps {
				if fold(pp.Identifier) != want && fold(pp.Name) != want {
					continue
				}
				for _, p := range s.d.Config.Projects {
					if id, _ := projects.PlaneLinkOf(p, ps); id == pp.ID {
						return p.Name, nil
					}
				}
			}
		}
	}
	return "", fmt.Errorf("unknown project #%s; forge projects: %s", v, strings.Join(names, ", "))
}
