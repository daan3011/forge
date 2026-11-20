package store

import (
	"context"
	"testing"
	"time"
)

func TestSessionCreateGet(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	in := Session{
		Name: "auth-fix", ClaudeSessionID: "uuid-1", Project: "forge", Repo: "daan3011/forge",
		BaseBranch: "main", Branch: "agent/auth-fix", Worktree: "/wt/forge/auth-fix",
		TmuxSession: "forge", TmuxWindow: "auth-fix", Template: "implement-issue",
	}
	got, err := s.Sessions().Create(ctx, in)
	noErr(t, err)
	if got.ID == 0 || got.Status != StatusStarting || !got.CreatedAt.Equal(t0) || !got.LastActivityAt.Equal(t0) {
		t.Fatalf("Create returned %+v", got)
	}

	lookups := []struct {
		name string
		get  func() (Session, error)
	}{
		{"id", func() (Session, error) { return s.Sessions().Get(ctx, got.ID) }},
		{"name", func() (Session, error) { return s.Sessions().GetByName(ctx, "auth-fix") }},
		{"claude id", func() (Session, error) { return s.Sessions().GetByClaudeID(ctx, "uuid-1") }},
	}
	for _, l := range lookups {
		t.Run(l.name, func(t *testing.T) {
			x, err := l.get()
			noErr(t, err)
			if x != got {
				t.Errorf("got %+v\nwant %+v", x, got)
			}
		})
	}

	_, err = s.Sessions().Get(ctx, 999)
	wantErr(t, err, ErrNotFound)
}

func TestSessionCreateConflicts(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	mustSession(t, s, "a")
	tests := []struct {
		name string
		in   Session
	}{
		{"dup name", Session{Name: "a", ClaudeSessionID: "other", Project: "p"}},
		{"dup claude id", Session{Name: "b", ClaudeSessionID: "uuid-a", Project: "p"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.Sessions().Create(ctx, tt.in)
			wantErr(t, err, ErrConflict)
		})
	}
}

func TestSessionSetStatus(t *testing.T) {
	s, c := newTestStore(t)
	ctx := context.Background()
	x := mustSession(t, s, "a")
	r := s.Sessions()

	t1 := t0.Add(time.Minute)
	noErr(t, r.SetStatus(ctx, x.ID, StatusWaiting, t1))
	noErr(t, r.MarkWaitingNotified(ctx, x.ID, t1.Add(5*time.Minute)))

	// Same status again: activity moves, changed-at and debounce stay.
	t2 := t1.Add(time.Minute)
	noErr(t, r.SetStatus(ctx, x.ID, StatusWaiting, t2))
	got, err := r.Get(ctx, x.ID)
	noErr(t, err)
	if !got.StatusChangedAt.Equal(t1) || !got.LastActivityAt.Equal(t2) || got.WaitingNotifiedAt.IsZero() {
		t.Errorf("after repeat status: %+v", got)
	}

	// New status: changed-at moves and the debounce resets.
	c.Advance(time.Hour)
	t3 := t2.Add(time.Minute)
	noErr(t, r.SetStatus(ctx, x.ID, StatusWorking, t3))
	got, err = r.Get(ctx, x.ID)
	noErr(t, err)
	if got.Status != StatusWorking || !got.StatusChangedAt.Equal(t3) || !got.WaitingNotifiedAt.IsZero() {
		t.Errorf("after status change: %+v", got)
	}
	if !got.UpdatedAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("UpdatedAt = %v", got.UpdatedAt)
	}

	wantErr(t, r.SetStatus(ctx, 999, StatusIdle, t3), ErrNotFound)
	if err := r.SetStatus(ctx, x.ID, Status("bogus"), t3); err == nil {
		t.Error("invalid status accepted")
	}
}

func TestSessionListOrderAndFilter(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	r := s.Sessions()
	a, b, c := mustSession(t, s, "a"), mustSession(t, s, "b"), mustSession(t, s, "c")
	other, err := r.Create(ctx, Session{Name: "o", ClaudeSessionID: "uo", Project: "other"})
	noErr(t, err)

	noErr(t, r.SetStatus(ctx, a.ID, StatusIdle, t0.Add(3*time.Minute)))
	noErr(t, r.SetStatus(ctx, b.ID, StatusWaiting, t0.Add(1*time.Minute)))
	noErr(t, r.SetStatus(ctx, c.ID, StatusWorking, t0.Add(2*time.Minute)))
	noErr(t, r.Archive(ctx, c.ID, "did things", t0.Add(4*time.Minute)))

	yes, no := true, false
	tests := []struct {
		name string
		f    SessionFilter
		want []string
	}{
		{"all", SessionFilter{}, []string{"b", "a", "c", "o"}},
		{"project", SessionFilter{Project: "forge"}, []string{"b", "a", "c"}},
		{"active", SessionFilter{Project: "forge", Archived: &no}, []string{"b", "a"}},
		{"archived", SessionFilter{Archived: &yes}, []string{"c"}},
	}
	_ = other
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.List(ctx, tt.f)
			noErr(t, err)
			var names []string
			for _, x := range got {
				names = append(names, x.Name)
			}
			if len(names) != len(tt.want) {
				t.Fatalf("got %v, want %v", names, tt.want)
			}
			for i := range names {
				if names[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", names, tt.want)
				}
			}
		})
	}
}

func TestSessionArchiveUpdateTranscriptDelete(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	r := s.Sessions()
	x := mustSession(t, s, "a")

	x.Notes = "remember the flaky test"
	x.Branch = "agent/a"
	noErr(t, r.Update(ctx, x))
	noErr(t, r.SetTranscript(ctx, x.ID, "/t/a.jsonl", 4096))
	noErr(t, r.Archive(ctx, x.ID, "summary", t0))

	got, err := r.Get(ctx, x.ID)
	noErr(t, err)
	if !got.Archived() || got.Summary != "summary" || got.Notes != "remember the flaky test" ||
		got.Branch != "agent/a" || got.TranscriptPath != "/t/a.jsonl" || got.TranscriptOffset != 4096 {
		t.Errorf("got %+v", got)
	}

	noErr(t, r.Unarchive(ctx, x.ID))
	got, err = r.Get(ctx, x.ID)
	noErr(t, err)
	if got.Archived() || got.Notes == "" {
		t.Errorf("after unarchive: %+v", got)
	}

	wantErr(t, r.Update(ctx, Session{ID: 999, Name: "z", ClaudeSessionID: "z", Project: "p", Status: StatusIdle}), ErrNotFound)

	// Delete cascades to events and removes links.
	_, err = s.Events().Append(ctx, Event{ClaudeSessionID: x.ClaudeSessionID, Event: "Stop"})
	noErr(t, err)
	noErr(t, s.Links().Add(ctx, SessionRef(x.ID), PRRef("o/r", 1), LinkMeta{}))
	noErr(t, r.Delete(ctx, x.ID))
	wantErr(t, r.Delete(ctx, x.ID), ErrNotFound)
	_, err = s.Events().Latest(ctx, x.ClaudeSessionID)
	wantErr(t, err, ErrNotFound)
	links, err := s.Links().For(ctx, PRRef("o/r", 1))
	noErr(t, err)
	if len(links) != 0 {
		t.Errorf("links left after delete: %+v", links)
	}
}

func TestSessionSetStatusIf(t *testing.T) {
	s, c := newTestStore(t)
	ctx := context.Background()
	x := mustSession(t, s, "a")
	noErr(t, s.Sessions().SetStatus(ctx, x.ID, StatusWaiting, t0))

	tests := []struct {
		name     string
		from, to Status
		want     bool
		after    Status
	}{
		{"stale from is ignored", StatusIdle, StatusWorking, false, StatusWaiting},
		{"matching from applies", StatusWaiting, StatusWorking, true, StatusWorking},
		{"already moved on", StatusWaiting, StatusWorking, false, StatusWorking},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c.Advance(time.Second)
			ok, err := s.Sessions().SetStatusIf(ctx, x.ID, tt.from, tt.to, c.Now())
			noErr(t, err)
			if ok != tt.want {
				t.Errorf("changed = %v, want %v", ok, tt.want)
			}
			got, err := s.Sessions().Get(ctx, x.ID)
			noErr(t, err)
			if got.Status != tt.after {
				t.Errorf("status = %s, want %s", got.Status, tt.after)
			}
		})
	}
}

func TestSessionSetClaudeID(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	a := mustSession(t, s, "a")
	mustSession(t, s, "b")
	noErr(t, s.Sessions().SetClaudeID(ctx, a.ID, "uuid-new"))
	got, err := s.Sessions().GetByClaudeID(ctx, "uuid-new")
	noErr(t, err)
	if got.ID != a.ID {
		t.Fatalf("got session %d, want %d", got.ID, a.ID)
	}
	wantErr(t, s.Sessions().SetClaudeID(ctx, a.ID, "uuid-b"), ErrConflict)
	wantErr(t, s.Sessions().SetClaudeID(ctx, 999, "x"), ErrNotFound)
}
