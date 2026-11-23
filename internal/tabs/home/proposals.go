package home

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/ui"
)

// proposalTimeout bounds an accept (Plane create) or edit.
const proposalTimeout = 30 * time.Second

func success(s string) tea.Cmd { return func() tea.Msg { return ui.SuccessMsg{Text: s} } }

// selectedProposal is the proposal under the cursor, or a hint why not.
func (t *Tab) selectedProposal(verb string) (store.Proposal, tea.Cmd, bool) {
	it, ok := t.selected()
	if !ok {
		return store.Proposal{}, nil, false
	}
	if it.kind != kindProposed {
		return store.Proposal{}, info(verb + " works on issues agents proposed"), false
	}
	if t.props == nil {
		return store.Proposal{}, errCmd(errors.New("proposals are unavailable")), false
	}
	return it.proposal, nil, true
}

// accept creates the selected proposal in Plane.
func (t *Tab) accept() tea.Cmd {
	p, hint, ok := t.selectedProposal(t.keys.Home.Accept.Help().Key + " accepts:")
	if !ok {
		return hint
	}
	q := t.props
	return tea.Batch(info("creating "+p.Handle()+" in Plane…"), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), proposalTimeout)
		defer cancel()
		got, err := q.Accept(ctx, p.ID)
		if err != nil {
			return proposalDoneMsg{err: fmt.Errorf("accept %s: %w", p.Handle(), err)}
		}
		return proposalDoneMsg{text: "created " + got.PlaneKey + " · " + got.Title}
	})
}

// priorityOptions are Plane's priorities for the edit form.
var priorityOptions = []string{"", "urgent", "high", "medium", "low", "none"}

// edit changes the selected proposal's title, description and priority.
func (t *Tab) edit() tea.Cmd {
	p, hint, ok := t.selectedProposal(t.keys.Home.Edit.Help().Key + " edits:")
	if !ok {
		return hint
	}
	title, desc, prio := p.Title, p.Description, p.Priority
	opts := make([]huh.Option[string], len(priorityOptions))
	for i, o := range priorityOptions {
		label := o
		if o == "" {
			label = "(unset)"
		}
		opts[i] = huh.NewOption(label, o)
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Title").Value(&title).Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New("a title is required")
			}
			return nil
		}),
		huh.NewText().Title("Description").Description("markdown").Value(&desc).Lines(8),
		huh.NewSelect[string]().Title("Priority").Options(opts...).Value(&prio),
	).Title("Edit " + p.Handle() + " · proposed by " + p.SessionName))
	q := t.props
	return t.openDialog(form, func() tea.Cmd {
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), proposalTimeout)
			defer cancel()
			title = strings.TrimSpace(title)
			_, err := q.Edit(ctx, p.ID, store.ProposalEdit{Title: &title, Description: &desc, Priority: &prio})
			if err != nil {
				return proposalDoneMsg{err: fmt.Errorf("edit %s: %w", p.Handle(), err)}
			}
			return proposalDoneMsg{text: "updated " + p.Handle()}
		}
	})
}

// drop asks, then discards the selected proposal.
func (t *Tab) drop() tea.Cmd {
	p, hint, ok := t.selectedProposal(t.keys.Home.Drop.Help().Key + " drops:")
	if !ok {
		return hint
	}
	confirm := false
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title("Drop " + p.Handle() + "?").
			Description(p.Title + "\nproposed by " + p.SessionName + "; it won't be created in Plane").
			Affirmative("Drop").Negative("Cancel").Value(&confirm),
	))
	q := t.props
	return t.openDialog(form, func() tea.Cmd {
		if !confirm {
			return nil
		}
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), proposalTimeout)
			defer cancel()
			if err := q.Drop(ctx, p.ID); err != nil {
				return proposalDoneMsg{err: fmt.Errorf("drop %s: %w", p.Handle(), err)}
			}
			return proposalDoneMsg{text: "dropped " + p.Handle()}
		}
	})
}

// openDialog shows form over the tab; done runs once it is completed.
func (t *Tab) openDialog(form *huh.Form, done func() tea.Cmd) tea.Cmd {
	t.dialog = form.WithTheme(t.theme.Huh()).WithKeyMap(t.keys.HuhKeyMap()).WithShowHelp(false)
	t.onDone = done
	init := t.dialog.Init()
	t.sizeDialog()
	return init
}

func (t *Tab) updateDialog(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		return nil // the app owns the window size; SetSize resizes the form
	}
	_, cmd := t.dialog.Update(msg)
	switch t.dialog.State {
	case huh.StateCompleted:
		done := t.onDone
		t.dialog, t.onDone = nil, nil
		if done == nil {
			return nil
		}
		return done()
	case huh.StateAborted:
		t.dialog, t.onDone = nil, nil
		return nil
	}
	return cmd
}

func (t *Tab) dialogSize() (w, h int) { return max(t.width-4, 10), max(t.height-2, 3) }

func (t *Tab) sizeDialog() {
	if t.dialog == nil {
		return
	}
	w, h := t.dialogSize()
	_, _ = t.dialog.Update(tea.WindowSizeMsg{Width: w, Height: h})
}

// CapturingInput implements tabs.Tab: an open form takes every key.
func (t *Tab) CapturingInput() bool { return t.dialog != nil }
