package plane

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/ui"
)

// CreateFormOpts configure RunCreateForm.
type CreateFormOpts struct {
	Theme  *ui.Theme
	Keys   *ui.KeyMap
	Height int // terminal rows, for the description page
	// Run shows the form and returns when it is done; huh.ErrUserAborted
	// means it was abandoned.
	Run func(*huh.Form) error
	Now func() time.Time
}

// RunCreateForm is the create form outside the TUI, for `forge capture`'s
// shift+enter: it loads project's details, runs the form prefilled from d
// and creates the item. ok is false when the form was abandoned.
func RunCreateForm(ctx context.Context, api plane.API, project string, d IssueDraft, o CreateFormOpts) (w plane.WorkItem, ok bool, err error) {
	m, err := fetchMeta(ctx, api, project)
	if err != nil {
		return plane.WorkItem{}, false, fmt.Errorf("plane: project details: %s", reason(err))
	}
	states := m.states
	if len(states) == 0 {
		return plane.WorkItem{}, false, errors.New("plane: the project has no states")
	}
	states = slices.Clone(states)
	slices.SortStableFunc(states, compareStates)
	me, _ := api.Me(ctx) // only marks "(me)" in the assignee list
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	fc := formCtx{theme: o.Theme, me: me, now: now(), height: max(o.Height-2, 6)}
	v := new(d.values(defaultState(states).ID))
	form := issueForm(fc, "New issue", &m, states, v, nil).
		WithTheme(o.Theme.Huh()).WithKeyMap(o.Keys.HuhKeyMap()).WithShowHelp(true)
	if err := o.Run(form); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return plane.WorkItem{}, false, nil
		}
		return plane.WorkItem{}, false, err
	}
	in, err := createInput(*v)
	if err != nil {
		return plane.WorkItem{}, false, err
	}
	w, err = api.CreateWorkItem(ctx, project, in)
	if err != nil {
		return plane.WorkItem{}, false, fmt.Errorf("plane: couldn't create the issue: %s", reason(err))
	}
	if w.Identifier == "" && m.project.Identifier != "" && w.SequenceID > 0 {
		w.Identifier = fmt.Sprintf("%s-%d", m.project.Identifier, w.SequenceID)
	}
	return w, true, nil
}
