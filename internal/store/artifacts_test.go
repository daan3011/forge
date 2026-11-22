package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestArtifactsSyncAndList(t *testing.T) {
	ctx := context.Background()
	s, c := newTestStore(t)
	sess := mustSession(t, s, "s1")
	a := s.Artifacts()
	m1 := t0.Add(-2 * time.Hour)
	m2 := t0.Add(-time.Hour)

	noErr(t, a.Sync(ctx, []string{"/r", "/w"}, []Artifact{
		{Path: "/r/a.html", Project: "forge", Type: "html", Size: 3, ModifiedAt: m1},
		{Path: "/w/b.svg", Project: "forge", SessionID: sess.ID, Type: "svg", Size: 4, ModifiedAt: m2},
		{Path: "/o/c.png", Project: "other", Type: "png", Size: 5, ModifiedAt: m1},
	}))
	got, err := a.List(ctx, "")
	noErr(t, err)
	paths := func(as []Artifact) []string {
		var out []string
		for _, x := range as {
			out = append(out, x.Path)
		}
		return out
	}
	if want := []string{"/w/b.svg", "/o/c.png", "/r/a.html"}; !reflect.DeepEqual(paths(got), want) {
		t.Fatalf("List = %v, want %v", paths(got), want)
	}
	b := got[0]
	if b.SessionID != sess.ID || b.SessionName != "s1" || b.Size != 4 || b.Type != "svg" ||
		!b.CreatedAt.Equal(m2) || !b.ModifiedAt.Equal(m2) {
		t.Errorf("b = %+v", b)
	}

	// A later scan updates a file (created_at kept) and prunes missing
	// files under its roots only.
	c.Advance(time.Hour)
	m3 := t0.Add(30 * time.Minute)
	noErr(t, a.Sync(ctx, []string{"/r", "/w"}, []Artifact{
		{Path: "/r/a.html", Project: "forge", Type: "html", Size: 9, ModifiedAt: m3},
	}))
	got, err = a.List(ctx, "")
	noErr(t, err)
	if want := []string{"/r/a.html", "/o/c.png"}; !reflect.DeepEqual(paths(got), want) {
		t.Fatalf("after resync = %v, want %v", paths(got), want)
	}
	if !got[0].CreatedAt.Equal(m1) || !got[0].ModifiedAt.Equal(m3) || got[0].Size != 9 {
		t.Errorf("a = %+v", got[0])
	}

	got, err = a.List(ctx, "other")
	noErr(t, err)
	if want := []string{"/o/c.png"}; !reflect.DeepEqual(paths(got), want) {
		t.Errorf("List(other) = %v", paths(got))
	}

	// A deleted session leaves the artifact without a name.
	noErr(t, a.Sync(ctx, nil, []Artifact{{Path: "/w/b.svg", Project: "forge", SessionID: sess.ID, Type: "svg", ModifiedAt: m2}}))
	noErr(t, s.Sessions().Delete(ctx, sess.ID))
	got, err = a.List(ctx, "forge")
	noErr(t, err)
	for _, x := range got {
		if x.Path == "/w/b.svg" && (x.SessionName != "" || x.SessionID != sess.ID) {
			t.Errorf("after delete = %+v", x)
		}
	}
}

func TestArtifactsDelete(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	a := s.Artifacts()
	noErr(t, a.Sync(ctx, nil, []Artifact{
		{Path: "/a", Type: "html", ModifiedAt: t0},
		{Path: "/b", Type: "html", ModifiedAt: t0},
	}))
	noErr(t, a.Delete(ctx, []string{"/a", "/nope"}))
	noErr(t, a.Delete(ctx, nil))
	got, err := a.List(ctx, "")
	noErr(t, err)
	if len(got) != 1 || got[0].Path != "/b" {
		t.Errorf("List = %+v", got)
	}
}

func TestUnder(t *testing.T) {
	tests := []struct {
		p     string
		roots []string
		want  bool
	}{
		{"/r/a", []string{"/r"}, true},
		{"/r", []string{"/r"}, true},
		{"/rr/a", []string{"/r"}, false},
		{"/x/a", []string{"/"}, true},
		{"/x/a", nil, false},
	}
	for _, tt := range tests {
		if got := under(tt.p, tt.roots); got != tt.want {
			t.Errorf("under(%q, %v) = %v", tt.p, tt.roots, got)
		}
	}
}
