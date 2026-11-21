package plane

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/daan3011/forge/internal/store"
)

const (
	maxPerPage = 200
	// maxPages guards against a server that keeps saying has_more.
	maxPages = 50
	// CacheKind is the kv_cache kind of cached Plane lists.
	CacheKind = "plane"
)

// Cache is the key/value cache list responses are written through to.
// *store.Cache satisfies it.
type Cache interface {
	Get(ctx context.Context, key string) (store.CacheEntry, error)
	Put(ctx context.Context, key, kind, project string, value []byte, ttl time.Duration) error
}

// Cached is a list read back from the cache.
type Cached[T any] struct {
	Items     []T
	FetchedAt time.Time
	Stale     bool
}

// page is a v2 list envelope, cursor or offset style.
type page[T any] struct {
	Data       []T    `json:"data"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
	Next       *int   `json:"next"`
	TotalCount *int   `json:"total_count"`
}

// cursorless are the orderings v2 can't page with a cursor.
var cursorless = map[string]bool{"priority": true, "state_group": true, "sort_order": true}

func usesOffset(q url.Values) bool {
	return cursorless[strings.TrimPrefix(q.Get("order_by"), "-")]
}

// listAll fetches every page of path (up to limit items, 0 = no limit).
func listAll[T any](ctx context.Context, c *Client, path string, filter url.Values, limit int) ([]T, error) {
	q := url.Values{}
	for k, v := range filter {
		q[k] = append([]string(nil), v...)
	}
	perPage := maxPerPage
	if limit > 0 && limit < perPage {
		perPage = limit
	}
	q.Set("per_page", strconv.Itoa(perPage))
	offset := usesOffset(q)
	if !offset {
		q.Set("paginate", "cursor")
	}

	var out []T
	for range maxPages {
		var p page[T]
		if err := c.do(ctx, "GET", path, q, nil, &p); err != nil {
			return nil, err
		}
		out = append(out, p.Data...)
		if limit > 0 && len(out) >= limit {
			return out[:limit], nil
		}
		if offset {
			if p.Next == nil || len(p.Data) == 0 {
				return out, nil
			}
			q.Set("offset", strconv.Itoa(*p.Next))
		} else {
			if !p.HasMore || p.NextCursor == "" {
				return out, nil
			}
			q.Set("cursor", p.NextCursor)
		}
	}
	return out, nil
}

// cacheKey is plane:<workspace>:<resource>:<project>:<hash of query+limit>.
func (c *Client) cacheKey(resource, project string, q url.Values, limit int) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(q.Encode()))
	_, _ = h.Write([]byte("|" + strconv.Itoa(limit)))
	return fmt.Sprintf("plane:%s:%s:%s:%x", c.workspace, resource, project, h.Sum64())
}

// cachedList fetches a list and writes it through to the cache. Cache
// failures never fail the call.
func cachedList[T any](ctx context.Context, c *Client, resource, project, path string, q url.Values, limit int) ([]T, error) {
	items, err := listAll[T](ctx, c, path, q, limit)
	if err != nil {
		return nil, err
	}
	if c.cache != nil {
		if items == nil {
			items = []T{}
		}
		if raw, err := json.Marshal(items); err == nil {
			_ = c.cache.Put(ctx, c.cacheKey(resource, project, q, limit), CacheKind, project, raw, c.cacheTTL)
		}
	}
	return items, nil
}

// readCached returns the cached list, ok false when there is none (or no
// cache, or it can't be decoded).
func readCached[T any](ctx context.Context, c *Client, resource, project string, q url.Values, limit int) (Cached[T], bool) {
	if c.cache == nil {
		return Cached[T]{}, false
	}
	e, err := c.cache.Get(ctx, c.cacheKey(resource, project, q, limit))
	if err != nil {
		return Cached[T]{}, false
	}
	var items []T
	if json.Unmarshal(e.Value, &items) != nil {
		return Cached[T]{}, false
	}
	return Cached[T]{Items: items, FetchedAt: e.FetchedAt, Stale: e.Stale}, true
}
