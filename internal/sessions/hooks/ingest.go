package hooks

import (
	"context"
	"errors"
	"time"

	"github.com/daan3011/forge/internal/store"
)

// Store is the persistence Ingest needs. StoreFrom adapts *store.Store.
type Store interface {
	GetByClaudeID(ctx context.Context, claudeID string) (store.Session, error)
	GetByName(ctx context.Context, name string) (store.Session, error)
	SetClaudeID(ctx context.Context, id int64, claudeID string) error
	SetStatus(ctx context.Context, id int64, st store.Status, at time.Time) error
	SetTranscript(ctx context.Context, id int64, path string, offset int64) error
	AppendEvent(ctx context.Context, e store.Event) (int64, error)
}

type storeAdapter struct {
	*store.Sessions
	events *store.Events
}

func (a storeAdapter) AppendEvent(ctx context.Context, e store.Event) (int64, error) {
	return a.events.Append(ctx, e)
}

// StoreFrom returns the Store backed by the forge database.
func StoreFrom(st *store.Store) Store {
	return storeAdapter{Sessions: st.Sessions(), events: st.Events()}
}

// Result is what Ingest did, for the ping to the TUI.
type Result struct {
	// Session is the forge session name; "" when the event belongs to no
	// forge session.
	Session string
	Status  store.Status // the session's status after the event
	Changed bool         // the event set a status
}

// Ingest records hook event p and applies the status machine (Next) to its
// session.
//
// The session is found by Claude session id. envSession is $FORGE_SESSION
// from the hook's environment (set in every forge agent window): when a
// SessionStart after /clear or /resume brings a new Claude session id, the
// session is found by name instead and adopts the new id. Other events with
// an unknown id (e.g. a nested `claude -p` run by the agent, which inherits
// the environment) are recorded without a session and change nothing.
func Ingest(ctx context.Context, st Store, p Payload, envSession string, now time.Time) (Result, error) {
	x, known, err := resolve(ctx, st, p, envSession)
	if err != nil {
		return Result{}, err
	}
	ev := store.Event{
		ClaudeSessionID: p.SessionID, Event: p.Event, ToolName: p.ToolName,
		Payload: p.StoredPayload(), CreatedAt: now,
	}
	if known {
		ev.SessionID = x.ID
	}
	if _, err := st.AppendEvent(ctx, ev); err != nil {
		return Result{}, err
	}
	if !known {
		return Result{}, nil
	}
	res := Result{Session: x.Name, Status: x.Status}
	if p.TranscriptPath != "" && p.TranscriptPath != x.TranscriptPath {
		if err := st.SetTranscript(ctx, x.ID, p.TranscriptPath, 0); err != nil {
			return res, err
		}
	}
	if next, ok := Next(p, x.Status); ok {
		if err := st.SetStatus(ctx, x.ID, next, now); err != nil {
			return res, err
		}
		res.Status, res.Changed = next, true
	}
	return res, nil
}

func resolve(ctx context.Context, st Store, p Payload, envSession string) (store.Session, bool, error) {
	if p.SessionID == "" {
		return store.Session{}, false, nil
	}
	x, err := st.GetByClaudeID(ctx, p.SessionID)
	switch {
	case err == nil:
		return x, true, nil
	case !errors.Is(err, store.ErrNotFound):
		return store.Session{}, false, err
	}
	if envSession == "" || p.Event != SessionStart || (p.Source != "clear" && p.Source != "resume") {
		return store.Session{}, false, nil
	}
	x, err = st.GetByName(ctx, envSession)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.Session{}, false, nil
	case err != nil:
		return store.Session{}, false, err
	}
	switch err := st.SetClaudeID(ctx, x.ID, p.SessionID); {
	case errors.Is(err, store.ErrConflict):
		return store.Session{}, false, nil // resumed another session's conversation
	case err != nil:
		return store.Session{}, false, err
	}
	x.ClaudeSessionID = p.SessionID
	return x, true, nil
}
