package store

import (
	"context"
	"testing"
	"time"
)

func TestPins(t *testing.T) {
	s, c := newTestStore(t)
	ctx := context.Background()
	p := s.Pins()

	noErr(t, p.Pin(ctx, Pin{Kind: "artifact", Ref: "/a.html", Project: "forge", Title: "A"}))
	c.Advance(time.Minute)
	noErr(t, p.Pin(ctx, Pin{Kind: "artifact", Ref: "/b.svg", Project: "forge"}))
	c.Advance(time.Minute)
	noErr(t, p.Pin(ctx, Pin{Kind: "artifact", Ref: "/c.png", Project: "other"}))
	noErr(t, p.Pin(ctx, Pin{Kind: "session", Ref: "1", Project: "forge"}))
	// Re-pin updates title and position but keeps the original pin time.
	noErr(t, p.Pin(ctx, Pin{Kind: "artifact", Ref: "/a.html", Project: "forge", Title: "A2", Position: 5}))

	tests := []struct {
		name          string
		kind, project string
		want          []string
	}{
		{"all", "", "", []string{"/b.svg", "/c.png", "1", "/a.html"}},
		{"artifacts in forge", "artifact", "forge", []string{"/b.svg", "/a.html"}},
		{"project only", "", "other", []string{"/c.png"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.List(ctx, tt.kind, tt.project)
			noErr(t, err)
			if len(got) != len(tt.want) {
				t.Fatalf("got %+v", got)
			}
			for i, x := range got {
				if x.Ref != tt.want[i] {
					t.Fatalf("got %+v, want %v", got, tt.want)
				}
			}
		})
	}

	list, err := p.List(ctx, "artifact", "forge")
	noErr(t, err)
	a := list[1]
	if a.Title != "A2" || a.Position != 5 || !a.CreatedAt.Equal(t0) {
		t.Errorf("re-pinned = %+v", a)
	}

	ok, err := p.IsPinned(ctx, "artifact", "/a.html")
	noErr(t, err)
	if !ok {
		t.Error("IsPinned = false")
	}
	noErr(t, p.Unpin(ctx, "artifact", "/a.html"))
	wantErr(t, p.Unpin(ctx, "artifact", "/a.html"), ErrNotFound)
	ok, err = p.IsPinned(ctx, "artifact", "/a.html")
	noErr(t, err)
	if ok {
		t.Error("IsPinned after unpin = true")
	}
}
