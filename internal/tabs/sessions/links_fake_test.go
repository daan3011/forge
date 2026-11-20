package sessions

import (
	"context"
	"errors"
	"time"

	agent "github.com/daan3011/forge/internal/sessions"
	"github.com/daan3011/forge/internal/store"
)

func (f *fakeSvc) LinkedSessions(_ context.Context, kind store.LinkKind, id string) ([]store.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.Session
	for _, r := range f.rows {
		for _, l := range r.LinkList {
			if l.Other == (store.Ref{Kind: kind, ID: id}) {
				out = append(out, r.Session)
			}
		}
	}
	return out, nil
}

// SyncPRs links each row to the PR in f.pulls for its branch.
func (f *fakeSvc) SyncPRs(context.Context, string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncs++
	changed := false
	for i := range f.rows {
		pr, ok := f.pulls[f.rows[i].Branch]
		if !ok {
			continue
		}
		found := false
		for j, l := range f.rows[i].LinkList {
			if l.Other == pr.Other {
				found = true
				if l.LinkMeta != pr.LinkMeta {
					f.rows[i].LinkList[j].LinkMeta, changed = pr.LinkMeta, true
				}
			}
		}
		if !found {
			f.rows[i].LinkList = append(f.rows[i].LinkList, pr)
			changed = true
		}
	}
	return changed, nil
}

func (f *fakeSvc) syncCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.syncs
}

// archiveAt is when the fake archives sessions.
var archiveAt = time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)

func (f *fakeSvc) archiveLocked(i int) {
	f.rows[i].ArchivedAt = archiveAt
	f.rows[i].Summary = "summary of " + f.rows[i].Name
}

func (f *fakeSvc) Archive(_ context.Context, x store.Session) (store.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.rows {
		if f.rows[i].Name == x.Name {
			if f.rows[i].Archived() {
				return store.Session{}, agent.ErrArchived
			}
			f.archiveLocked(i)
			return f.rows[i].Session, nil
		}
	}
	return store.Session{}, errors.New("no such session")
}

// AutoArchive archives rows whose PRs are all merged or closed, like the
// real service.
func (f *fakeSvc) AutoArchive(context.Context, string) ([]agent.Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []agent.Row
	for i, r := range f.rows {
		if r.Archived() || r.AutoArchiveOff || r.Status == store.StatusWorking || r.Status == store.StatusWaiting {
			continue
		}
		if agent.PRsFinished(r.LinkList) {
			f.archiveLocked(i)
			out = append(out, f.rows[i])
		}
	}
	return out, nil
}

func (f *fakeSvc) Unarchive(_ context.Context, x store.Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.rows {
		if f.rows[i].Name == x.Name {
			f.rows[i].ArchivedAt, f.rows[i].AutoArchiveOff = time.Time{}, true
			if f.rows[i].Status == store.StatusArchived {
				f.rows[i].Status = store.StatusDead
			}
			return nil
		}
	}
	return errors.New("no such session")
}
