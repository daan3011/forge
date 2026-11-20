package templates

import (
	"context"
	"fmt"
	"strings"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/store"
)

// ContextProvider resolves a reference (a Plane issue key, "owner/repo#12")
// to template variables, e.g. {"issue_title": …, "issue_body": …}. Keys
// are full variable names. P2.1 registers one for github_pr, P3.1 for
// plane_issue.
type ContextProvider interface {
	Resolve(ctx context.Context, ref string) (map[string]string, error)
}

// ProviderFunc adapts a function to ContextProvider.
type ProviderFunc func(ctx context.Context, ref string) (map[string]string, error)

// Resolve implements ContextProvider.
func (f ProviderFunc) Resolve(ctx context.Context, ref string) (map[string]string, error) {
	return f(ctx, ref)
}

// Registry holds context providers by link kind. A nil *Registry has none.
type Registry struct {
	m map[store.LinkKind]ContextProvider
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{m: map[store.LinkKind]ContextProvider{}} }

// Register sets the provider for kind.
func (r *Registry) Register(kind store.LinkKind, p ContextProvider) { r.m[kind] = p }

// Lookup returns the provider for kind.
func (r *Registry) Lookup(kind store.LinkKind) (ContextProvider, bool) {
	if r == nil {
		return nil, false
	}
	p, ok := r.m[kind]
	return p, ok
}

// refVars names the variable holding each kind's reference.
var refVars = map[store.LinkKind]string{
	store.KindPlaneIssue: "issue",
	store.KindGitHubPR:   "pr",
}

// RefKindFor returns the link kind whose reference fills variable name:
// issue / issue_* → plane_issue, pr / pr_* → github_pr.
func RefKindFor(name string) (store.LinkKind, bool) {
	for kind, v := range refVars {
		if name == v || strings.HasPrefix(name, v+"_") {
			return kind, true
		}
	}
	return "", false
}

// RefKinds returns the kinds of reference the template's variables need,
// issue before PR.
func (t Template) RefKinds() []store.LinkKind {
	var issue, pr bool
	for _, v := range t.Vars() {
		switch k, _ := RefKindFor(v); k {
		case store.KindPlaneIssue:
			issue = true
		case store.KindGitHubPR:
			pr = true
		}
	}
	var out []store.LinkKind
	if issue {
		out = append(out, store.KindPlaneIssue)
	}
	if pr {
		out = append(out, store.KindGitHubPR)
	}
	return out
}

// Input is what Resolve knows about the session being started.
type Input struct {
	Project config.Project
	Branch  string
	// Refs are the issue / PR the session is for; blank values are ignored.
	Refs map[store.LinkKind]string
	// Vars are values given explicitly (form fields, --var, P2.8's
	// failing_log); they win over everything else.
	Vars map[string]string
}

// Builtins returns the variables forge knows without asking anyone.
func Builtins(in Input) map[string]string {
	vals := map[string]string{}
	if in.Project.Repo != "" {
		vals["repo"] = in.Project.Repo
	}
	if in.Branch != "" {
		vals["branch"] = in.Branch
	}
	if in.Project.Path != "" || in.Project.DocsDir != "" {
		vals["docs"] = in.Project.DocsPath()
	}
	for kind, ref := range in.Refs {
		if v, ok := refVars[kind]; ok && strings.TrimSpace(ref) != "" {
			vals[v] = strings.TrimSpace(ref)
		}
	}
	return vals
}

// Resolve fills the template's variables: built-ins, then context
// providers for each ref, then front-matter defaults, with in.Vars winning
// over all of them. Variables still without a value are returned in
// missing, in order of use; provider failures are returned in errs (their
// variables are then missing too). Only variables the template uses are
// returned.
func Resolve(ctx context.Context, reg *Registry, t Template, in Input) (vals map[string]string, missing []string, errs []error) {
	all := Builtins(in)
	for _, kind := range t.RefKinds() {
		ref := strings.TrimSpace(in.Refs[kind])
		if ref == "" {
			continue
		}
		p, ok := reg.Lookup(kind)
		if !ok {
			continue
		}
		got, err := p.Resolve(ctx, ref)
		if err != nil {
			errs = append(errs, fmt.Errorf("couldn't fetch %s: %w", ref, err))
			continue
		}
		for k, v := range got {
			if _, set := all[k]; !set {
				all[k] = v
			}
		}
	}
	for _, v := range t.Variables {
		if _, set := all[v.Name]; !set && v.Default != "" {
			all[v.Name] = v.Default
		}
	}
	for k, v := range in.Vars {
		all[k] = v
	}
	vals = map[string]string{}
	for _, name := range t.Vars() {
		if v, ok := all[name]; ok {
			vals[name] = v
		} else {
			missing = append(missing, name)
		}
	}
	return vals, missing, errs
}
