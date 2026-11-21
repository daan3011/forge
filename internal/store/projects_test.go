package store

import (
	"context"
	"testing"
	"time"
)

func TestProjects(t *testing.T) {
	s, c := newTestStore(t)
	ctx := context.Background()
	r := s.Projects()
	if err := r.Touch(ctx, "daan/forge", "/code/forge"); err != nil {
		t.Fatal(err)
	}
	c.Advance(time.Minute)
	if err := r.Touch(ctx, "acme/app", ""); err != nil {
		t.Fatal(err)
	}
	c.Advance(time.Minute)
	// Same repo in another case: one row; an empty path keeps the old one.
	if err := r.Touch(ctx, "Daan/Forge", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.SetPlane(ctx, "daan/forge", "p-1"); err != nil {
		t.Fatal(err)
	}
	if err := r.SetPlane(ctx, "acme/app", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.SetPlane(ctx, "acme/new", "p-2"); err != nil { // never touched
		t.Fatal(err)
	}
	got, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	f, a, n := got[0], got[1], got[2]
	if f.Repo != "daan/forge" || f.Path != "/code/forge" || !f.LastUsed.Equal(t0.Add(2*time.Minute)) || f.PlaneProjectID != "p-1" || !f.PlaneDecided {
		t.Errorf("forge = %+v", f)
	}
	if a.Repo != "acme/app" || a.Path != "" || a.PlaneProjectID != "" || !a.PlaneDecided {
		t.Errorf("app = %+v", a)
	}
	if n.Repo != "acme/new" || !n.LastUsed.IsZero() || n.PlaneProjectID != "p-2" {
		t.Errorf("new = %+v", n)
	}
}
