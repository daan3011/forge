package store

import (
	"context"
	"testing"
)

func TestLinks(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	l := s.Links()
	a, b := mustSession(t, s, "a"), mustSession(t, s, "b")
	issue := IssueRef("PROJ-12")
	pr := PRRef("daan3011/forge", 42)

	noErr(t, l.Add(ctx, SessionRef(a.ID), issue, LinkMeta{Title: "Fix auth", State: "started"}))
	noErr(t, l.Add(ctx, SessionRef(a.ID), pr, LinkMeta{URL: "https://gh/42", State: "open"}))
	noErr(t, l.Add(ctx, SessionRef(b.ID), pr, LinkMeta{URL: "https://gh/42", State: "open"}))
	noErr(t, l.Add(ctx, issue, pr, LinkMeta{})) // P3.7: issue <-> PR without a session
	// Re-adding refreshes meta instead of duplicating.
	noErr(t, l.Add(ctx, SessionRef(a.ID), issue, LinkMeta{Title: "Fix auth flow", State: "started"}))

	t.Run("from session", func(t *testing.T) {
		got, err := l.For(ctx, SessionRef(a.ID))
		noErr(t, err)
		if len(got) != 2 {
			t.Fatalf("got %+v", got)
		}
		byKind := map[LinkKind]Link{}
		for _, x := range got {
			byKind[x.Other.Kind] = x
		}
		if byKind[KindPlaneIssue].Title != "Fix auth flow" || byKind[KindGitHubPR].URL != "https://gh/42" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("reverse direction", func(t *testing.T) {
		got, err := l.For(ctx, pr)
		noErr(t, err)
		if len(got) != 3 {
			t.Fatalf("got %+v", got)
		}
	})

	tests := []struct {
		name string
		ref  Ref
		want []int64
	}{
		{"pr", pr, []int64{a.ID, b.ID}},
		{"issue", issue, []int64{a.ID}},
		{"nothing", PRRef("x/y", 1), nil},
	}
	for _, tt := range tests {
		t.Run("LinkedSessions "+tt.name, func(t *testing.T) {
			got, err := l.LinkedSessions(ctx, tt.ref)
			noErr(t, err)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			seen := map[int64]bool{}
			for _, id := range got {
				seen[id] = true
			}
			for _, id := range tt.want {
				if !seen[id] {
					t.Errorf("missing %d in %v", id, got)
				}
			}
		})
	}

	t.Run("SetState", func(t *testing.T) {
		noErr(t, l.SetState(ctx, pr, "merged"))
		got, err := l.For(ctx, SessionRef(b.ID))
		noErr(t, err)
		if len(got) != 1 || got[0].State != "merged" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("Remove either direction", func(t *testing.T) {
		noErr(t, l.Remove(ctx, pr, SessionRef(b.ID)))
		wantErr(t, l.Remove(ctx, pr, SessionRef(b.ID)), ErrNotFound)
		got, err := l.LinkedSessions(ctx, pr)
		noErr(t, err)
		if len(got) != 1 || got[0] != a.ID {
			t.Errorf("got %v", got)
		}
	})

	t.Run("invalid kind rejected", func(t *testing.T) {
		if err := l.Add(ctx, Ref{Kind: "jira", ID: "1"}, pr, LinkMeta{}); err == nil {
			t.Error("invalid kind accepted")
		}
	})
}
