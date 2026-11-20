package sessions

import (
	"context"

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
