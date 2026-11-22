package store

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestCacheStaleness(t *testing.T) {
	s, c := newTestStore(t)
	ctx := context.Background()
	cache := s.Cache()
	noErr(t, cache.Put(ctx, "prs:mine", "github", "forge", []byte(`[1,2]`), time.Minute))
	noErr(t, cache.Put(ctx, "palette:recent", "palette", "", []byte(`x`), 0))

	tests := []struct {
		name    string
		advance time.Duration
		key     string
		stale   bool
	}{
		{"fresh", 0, "prs:mine", false},
		{"just before ttl", 59 * time.Second, "prs:mine", false},
		{"at ttl", time.Second, "prs:mine", true},
		{"ttl 0 never stale", 24 * time.Hour, "palette:recent", false},
	}
	for _, tt := range tests {
		c.Advance(tt.advance)
		e, err := cache.Get(ctx, tt.key)
		noErr(t, err)
		if e.Stale != tt.stale {
			t.Errorf("%s: Stale = %v, want %v", tt.name, e.Stale, tt.stale)
		}
	}

	e, err := cache.Get(ctx, "prs:mine")
	noErr(t, err)
	if !bytes.Equal(e.Value, []byte(`[1,2]`)) || e.Kind != "github" || e.Project != "forge" ||
		e.TTL != time.Minute || !e.FetchedAt.Equal(t0) {
		t.Errorf("entry = %+v", e)
	}

	// Put refreshes fetched_at.
	noErr(t, cache.Put(ctx, "prs:mine", "github", "forge", []byte(`[3]`), time.Minute))
	e, err = cache.Get(ctx, "prs:mine")
	noErr(t, err)
	if e.Stale || string(e.Value) != "[3]" {
		t.Errorf("after refresh = %+v", e)
	}

	noErr(t, cache.Delete(ctx, "prs:mine"))
	noErr(t, cache.Delete(ctx, "prs:mine"))
	_, err = cache.Get(ctx, "prs:mine")
	wantErr(t, err, ErrNotFound)

	noErr(t, cache.Put(ctx, "empty", "github", "", nil, 0))
	e, err = cache.Get(ctx, "empty")
	noErr(t, err)
	if len(e.Value) != 0 {
		t.Errorf("empty value = %q", e.Value)
	}
}

func TestCacheClearPruneStats(t *testing.T) {
	seed := func(t *testing.T) (*Store, *clock) {
		s, c := newTestStore(t)
		ctx := context.Background()
		put := func(key, kind, project, v string) {
			noErr(t, s.Cache().Put(ctx, key, kind, project, []byte(v), 0))
		}
		put("g1", "github", "forge", "aaaa")
		put("g2", "github", "forge", "bb")
		c.Advance(time.Hour)
		put("g3", "github", "other", "c")
		put("p1", "plane", "forge", "dddddd")
		return s, c
	}

	t.Run("stats", func(t *testing.T) {
		s, _ := seed(t)
		got, err := s.Cache().Stats(context.Background())
		noErr(t, err)
		want := []CacheStat{
			{"github", "forge", 2, 6, t0},
			{"github", "other", 1, 1, t0.Add(time.Hour)},
			{"plane", "forge", 1, 6, t0.Add(time.Hour)},
		}
		if len(got) != len(want) {
			t.Fatalf("got %+v", got)
		}
		for i := range want {
			if got[i].Kind != want[i].Kind || got[i].Project != want[i].Project ||
				got[i].Entries != want[i].Entries || got[i].Bytes != want[i].Bytes ||
				!got[i].Oldest.Equal(want[i].Oldest) {
				t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
			}
		}
	})

	clears := []struct {
		name, kind, project string
		removed             int64
		left                int
	}{
		{"kind all projects", "github", "", 3, 1},
		{"kind and project", "github", "forge", 2, 2},
		{"project all kinds", "", "forge", 3, 1},
		{"everything", "", "", 4, 0},
	}
	for _, tt := range clears {
		t.Run("clear "+tt.name, func(t *testing.T) {
			s, _ := seed(t)
			ctx := context.Background()
			n, err := s.Cache().Clear(ctx, tt.kind, tt.project)
			noErr(t, err)
			if n != tt.removed {
				t.Errorf("removed %d, want %d", n, tt.removed)
			}
			stats, err := s.Cache().Stats(ctx)
			noErr(t, err)
			left := 0
			for _, st := range stats {
				left += st.Entries
			}
			if left != tt.left {
				t.Errorf("left %d, want %d", left, tt.left)
			}
		})
	}

	t.Run("prune", func(t *testing.T) {
		s, _ := seed(t)
		n, err := s.Cache().PruneOlderThan(context.Background(), t0.Add(time.Minute))
		noErr(t, err)
		if n != 2 {
			t.Errorf("pruned %d, want 2", n)
		}
	})
}

func TestCacheRowsAndDeleteKeys(t *testing.T) {
	s, c := newTestStore(t)
	ctx := context.Background()
	cache := s.Cache()
	noErr(t, cache.Put(ctx, "tab:prs:a", "tab", "forge", []byte("123"), 0))
	c.Advance(time.Second)
	noErr(t, cache.Put(ctx, "tab:home:a", "tab", "forge", []byte("1"), 0))
	noErr(t, cache.Put(ctx, "plane:x", "plane", "", []byte("12"), 0))
	noErr(t, cache.Put(ctx, "pr-detail:o/r#1", "tab", "", nil, 0))

	keys := func(rows []CacheRow) string {
		var out []string
		for _, r := range rows {
			out = append(out, r.Key)
		}
		return strings.Join(out, ",")
	}
	tests := []struct {
		name string
		f    CacheFilter
		want string
	}{
		{"all", CacheFilter{}, "tab:prs:a,plane:x,pr-detail:o/r#1,tab:home:a"},
		{"kind", CacheFilter{Kinds: []string{"plane"}}, "plane:x"},
		{"kinds", CacheFilter{Kinds: []string{"plane", "tab"}}, "tab:prs:a,plane:x,pr-detail:o/r#1,tab:home:a"},
		{"prefixes", CacheFilter{Kinds: []string{"tab"}, KeyPrefixes: []string{"tab:prs:", "pr-detail:"}}, "tab:prs:a,pr-detail:o/r#1"},
		{"prefix is literal", CacheFilter{KeyPrefixes: []string{"tab%"}}, ""},
	}
	for _, tt := range tests {
		rows, err := cache.Rows(ctx, tt.f)
		noErr(t, err)
		if got := keys(rows); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
	rows, err := cache.Rows(ctx, CacheFilter{Kinds: []string{"tab"}, KeyPrefixes: []string{"tab:prs:"}})
	noErr(t, err)
	if r := rows[0]; r.Bytes != 3 || r.Project != "forge" || r.Kind != "tab" || !r.FetchedAt.Equal(t0) {
		t.Errorf("row = %+v", r)
	}

	n, err := cache.DeleteKeys(ctx, []string{"plane:x", "tab:prs:a", "missing"})
	noErr(t, err)
	if n != 2 {
		t.Errorf("deleted %d, want 2", n)
	}
	if n, err := cache.DeleteKeys(ctx, nil); n != 0 || err != nil {
		t.Errorf("empty delete = %d, %v", n, err)
	}
}
