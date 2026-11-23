package proposals

import (
	"context"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/store"
)

// Queue is the Home tab's view of the proposals waiting for the user
// (tabs.ProposalQueue).
type Queue struct {
	Store *store.Store
	Plane plane.API
	// Projects returns the resolved project list at call time.
	Projects func() []config.Project
}

// Pending lists the project's proposals still waiting ("" = all projects).
func (q Queue) Pending(ctx context.Context, project string) ([]store.Proposal, error) {
	return q.Store.Proposals().List(ctx, store.ProposalFilter{Status: store.ProposalProposed, Project: project})
}

// Accept creates the proposal in Plane (see Accept) and returns it as
// accepted, with its Plane key.
func (q Queue) Accept(ctx context.Context, id int64) (store.Proposal, error) {
	var ps []config.Project
	if q.Projects != nil {
		ps = q.Projects()
	}
	if _, err := Accept(ctx, Deps{Store: q.Store, Plane: q.Plane, Projects: ps}, id); err != nil {
		return store.Proposal{}, err
	}
	return q.Store.Proposals().Get(ctx, id)
}

// Edit changes a waiting proposal.
func (q Queue) Edit(ctx context.Context, id int64, e store.ProposalEdit) (store.Proposal, error) {
	return q.Store.Proposals().Update(ctx, id, e)
}

// Drop discards a waiting proposal.
func (q Queue) Drop(ctx context.Context, id int64) error {
	return q.Store.Proposals().Drop(ctx, id)
}
