package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func artifactByPath(t *testing.T, a *Artifacts, path string) Artifact {
	t.Helper()
	got, err := a.List(context.Background(), "")
	noErr(t, err)
	for _, x := range got {
		if x.Path == path {
			return x
		}
	}
	t.Fatalf("%s not listed", path)
	return Artifact{}
}

func TestArtifactMeta(t *testing.T) {
	ctx := context.Background()
	s, c := newTestStore(t)
	a := s.Artifacts()
	noErr(t, a.Sync(ctx, nil, []Artifact{
		{Path: "/w/a.html", Project: "forge", Type: "html", ModifiedAt: t0},
		{Path: "/r/b.svg", Project: "forge", Type: "svg", ModifiedAt: t0},
		{Path: "/r/.forge/artifacts/s1/a.html", Project: "forge", Type: "html", ModifiedAt: t0},
	}))

	tests := []struct {
		name string
		do   func() error
		path string
		want func(Artifact) bool
	}{
		{
			"title", func() error { return a.SetTitle(ctx, "/w/a.html", "Login mock") }, "/w/a.html",
			func(x Artifact) bool { return x.Title == "Login mock" },
		},
		{
			"tags", func() error { return a.SetTags(ctx, "/w/a.html", []string{"design", "ui"}) }, "/w/a.html",
			func(x Artifact) bool { return reflect.DeepEqual(x.Tags, []string{"design", "ui"}) },
		},
		{
			"pin", func() error { return a.SetPinned(ctx, "/w/a.html", "forge", true) }, "/w/a.html",
			func(x Artifact) bool { return x.Pinned },
		},
		{
			"unpinned stays plain", func() error { return nil }, "/r/b.svg",
			func(x Artifact) bool { return !x.Pinned && x.Title == "" && x.Tags == nil && x.Link == nil },
		},
		{
			"move to copy", func() error { return a.MoveMeta(ctx, "/w/a.html", "/r/.forge/artifacts/s1/a.html") },
			"/r/.forge/artifacts/s1/a.html",
			func(x Artifact) bool {
				return x.Pinned && x.Title == "Login mock" && reflect.DeepEqual(x.Tags, []string{"design", "ui"})
			},
		},
		{
			"original left bare", func() error { return nil }, "/w/a.html",
			func(x Artifact) bool { return !x.Pinned && x.Title == "" && x.Tags == nil },
		},
		{
			"clear title", func() error { return a.SetTitle(ctx, "/r/.forge/artifacts/s1/a.html", "") },
			"/r/.forge/artifacts/s1/a.html", func(x Artifact) bool { return x.Title == "" && x.Pinned },
		},
		{
			"unpin", func() error { return a.SetPinned(ctx, "/r/.forge/artifacts/s1/a.html", "forge", false) },
			"/r/.forge/artifacts/s1/a.html", func(x Artifact) bool { return !x.Pinned },
		},
		{
			"unpin again is fine", func() error { return a.SetPinned(ctx, "/r/.forge/artifacts/s1/a.html", "forge", false) },
			"/r/.forge/artifacts/s1/a.html", func(x Artifact) bool { return !x.Pinned },
		},
	}
	for _, tt := range tests {
		noErr(t, tt.do())
		if x := artifactByPath(t, a, tt.path); !tt.want(x) {
			t.Errorf("%s: %+v", tt.name, x)
		}
	}

	// The pin carries the display title for Home.
	noErr(t, a.SetTitle(ctx, "/r/b.svg", "Logo"))
	noErr(t, a.SetPinned(ctx, "/r/b.svg", "forge", true))
	noErr(t, a.SetTitle(ctx, "/r/b.svg", "Logo v2"))
	pins, err := s.Pins().List(ctx, ArtifactKind, "")
	noErr(t, err)
	if len(pins) != 1 || pins[0].Title != "Logo v2" || pins[0].Ref != "/r/b.svg" {
		t.Errorf("pins = %+v", pins)
	}

	// AllTags lists only tags on indexed artifacts.
	noErr(t, a.SetTags(ctx, "/r/b.svg", []string{"brand"}))
	noErr(t, a.SetTags(ctx, "/gone.png", []string{"zombie"}))
	tags, err := a.AllTags(ctx)
	noErr(t, err)
	if want := []string{"brand", "design", "ui"}; !reflect.DeepEqual(tags, want) {
		t.Errorf("AllTags = %v, want %v", tags, want)
	}

	// Links: synced, then detached once; a later detach keeps the reason.
	cp := "/r/.forge/artifacts/s1/a.html"
	noErr(t, a.Link(ctx, ArtifactLink{CopyPath: cp, SourcePath: "/w/a.html", SessionID: 7, SessionName: "s1"}))
	links, err := a.ActiveLinks(ctx)
	noErr(t, err)
	if len(links) != 1 || links[0].SourcePath != "/w/a.html" || links[0].SessionName != "s1" ||
		!links[0].SyncedAt.Equal(t0) || !links[0].Active() {
		t.Fatalf("links = %+v", links)
	}
	c.Advance(time.Minute)
	noErr(t, a.MarkSynced(ctx, cp, c.Now()))
	if x := artifactByPath(t, a, cp); x.Link == nil || !x.Link.SyncedAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("after sync = %+v", x.Link)
	}
	noErr(t, a.Detach(ctx, cp, "worktree removed", c.Now()))
	noErr(t, a.Detach(ctx, cp, "by hand", c.Now().Add(time.Hour)))
	links, err = a.ActiveLinks(ctx)
	noErr(t, err)
	if len(links) != 0 {
		t.Errorf("active after detach = %+v", links)
	}
	if x := artifactByPath(t, a, cp); x.Link == nil || x.Link.Active() || x.Link.DetachReason != "worktree removed" {
		t.Errorf("detached link = %+v", x.Link)
	}
}

// TestArtifactMetaPersists reopens the database: pins, tags, titles and
// links survive a restart.
func TestArtifactMetaPersists(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "forge.db")
	s, err := Open(ctx, path, WithClock(func() time.Time { return t0 }))
	noErr(t, err)
	a := s.Artifacts()
	noErr(t, a.Sync(ctx, nil, []Artifact{{Path: "/r/a.html", Project: "forge", Type: "html", ModifiedAt: t0}}))
	noErr(t, a.SetPinned(ctx, "/r/a.html", "forge", true))
	noErr(t, a.SetTags(ctx, "/r/a.html", []string{"x"}))
	noErr(t, a.SetTitle(ctx, "/r/a.html", "A"))
	noErr(t, a.Link(ctx, ArtifactLink{CopyPath: "/r/a.html", SourcePath: "/w/a.html", SessionName: "s"}))
	noErr(t, s.Close())

	s, err = Open(ctx, path)
	noErr(t, err)
	defer s.Close()
	x := artifactByPath(t, s.Artifacts(), "/r/a.html")
	if !x.Pinned || x.Title != "A" || !reflect.DeepEqual(x.Tags, []string{"x"}) || x.Link == nil || x.Link.SourcePath != "/w/a.html" {
		t.Errorf("after reopen = %+v", x)
	}
}
