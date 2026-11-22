package plane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/store"
)

var testNow = time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)

// reply is one canned response.
type reply struct {
	status  int
	fixture string // file in testdata; "" = empty body
	header  http.Header
}

// seen is one request the server got.
type seen struct {
	method, path string
	query        url.Values
	key          string
	body         string
}

// fakePlane is an httptest server answering requests in order.
type fakePlane struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	replies []reply
	got     []seen
}

func newFake(t *testing.T, replies ...reply) *fakePlane {
	t.Helper()
	f := &fakePlane{t: t, replies: replies}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePlane) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, seen{
		method: r.Method, path: r.URL.Path, query: r.URL.Query(),
		key: r.Header.Get("X-API-Key"), body: string(body),
	})
	if len(f.replies) == 0 {
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusTeapot)
		return
	}
	rp := f.replies[0]
	f.replies = f.replies[1:]
	for k, v := range rp.header {
		w.Header()[k] = v
	}
	if rp.fixture != "" {
		w.Header().Set("Content-Type", "application/json")
		if rp.status >= 400 {
			w.Header().Set("Content-Type", "application/problem+json")
		}
	}
	w.WriteHeader(rp.status)
	if rp.fixture != "" {
		b, err := os.ReadFile("testdata/" + rp.fixture)
		if err != nil {
			f.t.Fatalf("fixture: %v", err)
		}
		_, _ = w.Write(b)
	}
}

func (f *fakePlane) client(t *testing.T, opts ...Option) (*Client, *[]time.Duration) {
	t.Helper()
	var waits []time.Duration
	base := []Option{
		WithClock(func() time.Time { return testNow }),
		WithSleep(func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }),
	}
	c, err := New(config.Plane{BaseURL: f.srv.URL + "/", Workspace: "smithy"}, "k3y", append(base, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return c, &waits
}

func ok(fixture string) reply { return reply{status: http.StatusOK, fixture: fixture} }

const wsBase = "/api/v2/workspaces/smithy/"

func TestClientRequests(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		replies   []reply
		call      func(*Client) (any, error)
		wantReqs  []seen // method, path, query subset and body (JSON) checked
		check     func(t *testing.T, got any)
		wantErrIs func(error) bool
	}{
		{
			name:     "me",
			replies:  []reply{ok("me.json")},
			call:     func(c *Client) (any, error) { return c.Me(ctx) },
			wantReqs: []seen{{method: "GET", path: "/api/v2/users/me/"}},
			check: func(t *testing.T, got any) {
				if u := got.(User); u.ID != "u-ada" || u.DisplayName != "ada" {
					t.Errorf("user = %+v", u)
				}
			},
		},
		{
			name:      "me 404 means no v2",
			replies:   []reply{{status: 404, fixture: "error_404.json"}},
			call:      func(c *Client) (any, error) { return c.Me(ctx) },
			wantReqs:  []seen{{method: "GET", path: "/api/v2/users/me/"}},
			wantErrIs: func(err error) bool { return errors.Is(err, ErrNoV2) && IsNotFound(err) },
		},
		{
			name:     "projects",
			replies:  []reply{ok("projects.json")},
			call:     func(c *Client) (any, error) { return c.Projects(ctx) },
			wantReqs: []seen{{method: "GET", path: wsBase + "projects/", query: url.Values{"paginate": {"cursor"}, "per_page": {"200"}}}},
			check: func(t *testing.T, got any) {
				ps := got.([]Project)
				if len(ps) != 2 || ps[0].Identifier != "FORGE" || ps[1].Network != 2 {
					t.Errorf("projects = %+v", ps)
				}
				if want := time.Date(2026, 11, 1, 12, 30, 0, 123456000, time.UTC); !ps[0].UpdatedAt.Equal(want) {
					t.Errorf("updated_at = %v", ps[0].UpdatedAt)
				}
			},
		},
		{
			name:     "project",
			replies:  []reply{ok("work_item.json")}, // shape doesn't matter here
			call:     func(c *Client) (any, error) { return c.Project(ctx, "p-forge") },
			wantReqs: []seen{{method: "GET", path: wsBase + "projects/p-forge/"}},
		},
		{
			name:    "states",
			replies: []reply{ok("states.json")},
			call:    func(c *Client) (any, error) { return c.States(ctx, "p-forge") },
			// Plane can't cursor-page its default state ordering.
			wantReqs: []seen{{method: "GET", path: wsBase + "projects/p-forge/states/", query: url.Values{
				"order_by": {"created_at"}, "paginate": {"cursor"},
			}}},
			check: func(t *testing.T, got any) {
				ss := got.([]State)
				if len(ss) != 4 || ss[2].Group != GroupStarted || !ss[0].IsDefault {
					t.Errorf("states = %+v", ss)
				}
			},
		},
		{
			name:     "labels",
			replies:  []reply{ok("labels.json")},
			call:     func(c *Client) (any, error) { return c.Labels(ctx, "p-forge") },
			wantReqs: []seen{{method: "GET", path: wsBase + "projects/p-forge/labels/"}},
			check: func(t *testing.T, got any) {
				if ls := got.([]Label); len(ls) != 2 || ls[1].Name != "ui" {
					t.Errorf("labels = %+v", ls)
				}
			},
		},
		{
			name:    "current cycle",
			replies: []reply{ok("cycles.json")},
			call:    func(c *Client) (any, error) { cy, _, err := c.CurrentCycle(ctx, "p-forge"); return cy, err },
			// Nor its default cycle ordering.
			wantReqs: []seen{{method: "GET", path: wsBase + "projects/p-forge/cycles/", query: url.Values{
				"order_by": {"created_at"}, "paginate": {"cursor"},
			}}},
			check: func(t *testing.T, got any) {
				if cy := got.(Cycle); cy.ID != "c-now" {
					t.Errorf("current cycle = %+v", cy)
				}
			},
		},
		{
			name:     "project members",
			replies:  []reply{ok("members.json")},
			call:     func(c *Client) (any, error) { return c.ProjectMembers(ctx, "p-forge") },
			wantReqs: []seen{{method: "GET", path: wsBase + "projects/p-forge/members/", query: url.Values{"expand": {"member"}}}},
			check: func(t *testing.T, got any) {
				ms := got.([]Member)
				if len(ms) != 2 || ms[1].MemberID != "u-bob" || ms[1].Member == nil || ms[1].Member.DisplayName != "bob" {
					t.Errorf("members = %+v", ms)
				}
			},
		},
		{
			name:     "workspace members",
			replies:  []reply{ok("members.json")},
			call:     func(c *Client) (any, error) { return c.WorkspaceMembers(ctx) },
			wantReqs: []seen{{method: "GET", path: wsBase + "members/"}},
		},
		{
			name:    "work items follow cursor pages",
			replies: []reply{ok("work_items_page1.json"), ok("work_items_page2.json")},
			call: func(c *Client) (any, error) {
				return c.WorkItems(ctx, "p-forge", WorkItemFilter{StateGroups: []StateGroup{GroupStarted, GroupUnstarted}, Expand: []string{"state"}})
			},
			wantReqs: []seen{
				{method: "GET", path: wsBase + "projects/p-forge/work-items/", query: url.Values{
					"paginate": {"cursor"}, "per_page": {"200"}, "state_group__in": {"started,unstarted"}, "expand": {"state"},
				}},
				{method: "GET", path: wsBase + "projects/p-forge/work-items/", query: url.Values{
					"cursor": {"b3A9Mg"}, "state_group__in": {"started,unstarted"},
				}},
			},
			check: func(t *testing.T, got any) {
				ws := got.([]WorkItem)
				if len(ws) != 3 || ws[0].Key() != "FORGE-12" || ws[2].ID != "w-10" {
					t.Fatalf("items = %+v", ws)
				}
				w := ws[0]
				if w.State == nil || w.State.Name != "In Progress" || w.Priority != PriorityHigh ||
					!reflect.DeepEqual(w.LabelIDs, []string{"l-bug", "l-ui"}) || w.CycleID != "c-now" {
					t.Errorf("item = %+v", w)
				}
				if want := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC); !w.TargetDate.Equal(want) {
					t.Errorf("target_date = %v", w.TargetDate)
				}
				if ws[1].CycleID != "" || !ws[1].TargetDate.IsZero() {
					t.Errorf("nulls not empty: %+v", ws[1])
				}
			},
		},
		{
			name:    "work items stop at limit",
			replies: []reply{ok("work_items_page1.json")},
			call: func(c *Client) (any, error) {
				return c.WorkItems(ctx, "p-forge", WorkItemFilter{Limit: 1})
			},
			wantReqs: []seen{{method: "GET", path: wsBase + "projects/p-forge/work-items/", query: url.Values{"per_page": {"1"}}}},
			check: func(t *testing.T, got any) {
				if ws := got.([]WorkItem); len(ws) != 1 {
					t.Errorf("got %d items", len(ws))
				}
			},
		},
		{
			name:    "priority order uses offset paging",
			replies: []reply{ok("work_items_offset.json")},
			call: func(c *Client) (any, error) {
				return c.WorkItems(ctx, "p-forge", WorkItemFilter{OrderBy: "-priority"})
			},
			wantReqs: []seen{{method: "GET", path: wsBase + "projects/p-forge/work-items/", query: url.Values{
				"order_by": {"-priority"}, "paginate": nil,
			}}},
			check: func(t *testing.T, got any) {
				if ws := got.([]WorkItem); len(ws) != 1 {
					t.Errorf("got %d items", len(ws))
				}
			},
		},
		{
			// Plane orders labels by sort_order, which it can't cursor-page
			// (400 "This ordering can't be used with cursor pagination").
			name:    "labels use offset paging",
			replies: []reply{ok("labels.json")},
			call:    func(c *Client) (any, error) { return c.Labels(ctx, "p-forge") },
			wantReqs: []seen{{method: "GET", path: wsBase + "projects/p-forge/labels/", query: url.Values{
				"order_by": {"sort_order"}, "paginate": nil,
			}}},
			check: func(t *testing.T, got any) {
				if ls := got.([]Label); len(ls) != 2 || ls[0].Name != "bug" {
					t.Errorf("labels = %+v", ls)
				}
			},
		},
		{
			name:     "work item by id",
			replies:  []reply{ok("work_item.json")},
			call:     func(c *Client) (any, error) { return c.WorkItem(ctx, "p-forge", "w-12") },
			wantReqs: []seen{{method: "GET", path: wsBase + "projects/p-forge/work-items/w-12/"}},
			check: func(t *testing.T, got any) {
				if w := got.(WorkItem); !strings.Contains(w.DescriptionHTML, "flicker") {
					t.Errorf("description = %q", w.DescriptionHTML)
				}
			},
		},
		{
			name:     "work item by identifier",
			replies:  []reply{ok("work_item.json")},
			call:     func(c *Client) (any, error) { return c.WorkItemByIdentifier(ctx, " FORGE-12 ") },
			wantReqs: []seen{{method: "GET", path: wsBase + "work-items/FORGE-12/"}},
		},
		{
			name:     "work item not found",
			replies:  []reply{{status: 404, fixture: "error_404.json"}},
			call:     func(c *Client) (any, error) { return c.WorkItemByIdentifier(ctx, "FORGE-999") },
			wantReqs: []seen{{method: "GET", path: wsBase + "work-items/FORGE-999/"}},
			wantErrIs: func(err error) bool {
				return IsNotFound(err) && strings.Contains(err.Error(), "Work item not found.")
			},
		},
		{
			name:    "create work item",
			replies: []reply{{status: 201, fixture: "work_item.json"}},
			call: func(c *Client) (any, error) {
				return c.CreateWorkItem(ctx, "p-forge", WorkItemCreate{Name: "New", State: "Todo", Priority: PriorityLow, Labels: []string{"bug"}})
			},
			wantReqs: []seen{{
				method: "POST", path: wsBase + "projects/p-forge/work-items/",
				body: `{"name":"New","state":"Todo","priority":"low","labels":["bug"]}`,
			}},
		},
		{
			name:     "create work item validation error",
			replies:  []reply{{status: 400, fixture: "error_400.json"}},
			call:     func(c *Client) (any, error) { return c.CreateWorkItem(ctx, "p-forge", WorkItemCreate{Name: "x"}) },
			wantReqs: []seen{{method: "POST", path: wsBase + "projects/p-forge/work-items/"}},
			wantErrIs: func(err error) bool {
				var e *APIError
				return errors.As(err, &e) && e.Code == "invalid_request" && len(e.Errors) == 1 &&
					strings.Contains(err.Error(), "name: This field is required.")
			},
		},
		{
			name:    "update work item sends only set fields",
			replies: []reply{ok("work_item.json")},
			call: func(c *Client) (any, error) {
				state, prio := "s-done", PriorityNone
				return c.UpdateWorkItem(ctx, "p-forge", "w-12", WorkItemUpdate{StateID: &state, Priority: &prio})
			},
			wantReqs: []seen{{
				method: "PATCH", path: wsBase + "projects/p-forge/work-items/w-12/",
				body: `{"state_id":"s-done","priority":"none"}`,
			}},
		},
		{
			name:     "comments",
			replies:  []reply{ok("comments.json")},
			call:     func(c *Client) (any, error) { return c.Comments(ctx, "p-forge", "w-12") },
			wantReqs: []seen{{method: "GET", path: wsBase + "projects/p-forge/work-items/w-12/comments/"}},
			check: func(t *testing.T, got any) {
				if cs := got.([]Comment); len(cs) != 1 || cs[0].CommentStripped != "Repro on kitty too." {
					t.Errorf("comments = %+v", cs)
				}
			},
		},
		{
			name:    "create comment",
			replies: []reply{{status: 201, fixture: "comment.json"}},
			call: func(c *Client) (any, error) {
				return c.CreateComment(ctx, "p-forge", "w-12", CommentCreate{CommentHTML: "<p>On it</p>"})
			},
			wantReqs: []seen{{
				method: "POST", path: wsBase + "projects/p-forge/work-items/w-12/comments/",
				body: `{"comment_html":"<p>On it</p>"}`,
			}},
			check: func(t *testing.T, got any) {
				if cm := got.(Comment); cm.ID != "cm-2" {
					t.Errorf("comment = %+v", cm)
				}
			},
		},
		{
			name:      "unauthorized",
			replies:   []reply{{status: 401, header: http.Header{"Content-Type": {"application/json"}}}},
			call:      func(c *Client) (any, error) { return c.Projects(ctx) },
			wantReqs:  []seen{{method: "GET", path: wsBase + "projects/"}},
			wantErrIs: func(err error) bool { return IsAuthError(err) && strings.Contains(err.Error(), "API key") },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(t, tt.replies...)
			c, _ := f.client(t)
			got, err := tt.call(c)
			if tt.wantErrIs != nil {
				if err == nil || !tt.wantErrIs(err) {
					t.Fatalf("err = %v", err)
				}
			} else if err != nil {
				t.Fatalf("err = %v", err)
			}
			checkRequests(t, f.got, tt.wantReqs)
			if tt.check != nil && err == nil {
				tt.check(t, got)
			}
		})
	}
}

// checkRequests compares method, path and key exactly, the wanted query
// keys (nil value = must be absent) and the JSON body if wanted.
func checkRequests(t *testing.T, got, want []seen) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d requests, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.method != w.method || g.path != w.path {
			t.Errorf("request %d = %s %s, want %s %s", i, g.method, g.path, w.method, w.path)
		}
		if g.key != "k3y" {
			t.Errorf("request %d X-API-Key = %q", i, g.key)
		}
		for k, v := range w.query {
			if v == nil {
				if g.query.Has(k) {
					t.Errorf("request %d has %s=%q, want none", i, k, g.query.Get(k))
				}
			} else if !reflect.DeepEqual(g.query[k], v) {
				t.Errorf("request %d %s = %q, want %q", i, k, g.query[k], v)
			}
		}
		if w.body != "" && !jsonEqual(g.body, w.body) {
			t.Errorf("request %d body = %s, want %s", i, g.body, w.body)
		}
	}
}

func jsonEqual(a, b string) bool {
	var x, y any
	return json.Unmarshal([]byte(a), &x) == nil && json.Unmarshal([]byte(b), &y) == nil && reflect.DeepEqual(x, y)
}

func TestRateLimitRetry(t *testing.T) {
	tooMany := func(h http.Header) reply { return reply{status: 429, fixture: "error_429.json", header: h} }
	tests := []struct {
		name      string
		replies   []reply
		wantWaits []time.Duration
		wantErr   bool
	}{
		{
			name: "retry-after then success",
			replies: []reply{
				tooMany(http.Header{"Retry-After": {"2"}}),
				tooMany(http.Header{"Retry-After": {"5"}}),
				ok("projects.json"),
			},
			wantWaits: []time.Duration{2 * time.Second, 5 * time.Second},
		},
		{
			name: "x-ratelimit-reset fallback",
			replies: []reply{
				tooMany(http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"1795251610"}}), // testNow+10s
				ok("projects.json"),
			},
			wantWaits: []time.Duration{10 * time.Second},
		},
		{
			name:      "exponential backoff without headers",
			replies:   []reply{tooMany(nil), tooMany(nil), ok("projects.json")},
			wantWaits: []time.Duration{time.Second, 2 * time.Second},
		},
		{
			name:      "gives up after max retries",
			replies:   []reply{tooMany(nil), tooMany(nil), tooMany(nil), tooMany(nil)},
			wantWaits: []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(t, tt.replies...)
			c, waits := f.client(t)
			_, err := c.Projects(context.Background())
			if tt.wantErr {
				var e *APIError
				if !IsRateLimited(err) || !errors.As(err, &e) || e.Code != "rate_limited" {
					t.Fatalf("err = %v, want rate limited", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(*waits, tt.wantWaits) {
				t.Errorf("waits = %v, want %v", *waits, tt.wantWaits)
			}
		})
	}
}

func TestRateLimitWaitCancelled(t *testing.T) {
	f := newFake(t, reply{status: 429, header: http.Header{"Retry-After": {"60"}}})
	ctx, cancel := context.WithCancel(context.Background())
	c, err := New(config.Plane{BaseURL: f.srv.URL, Workspace: "smithy"}, "k3y")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			f.mu.Lock()
			n := len(f.got)
			f.mu.Unlock()
			if n > 0 {
				cancel()
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	if _, err := c.Projects(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// memCache is an in-memory Cache.
type memCache struct{ m map[string]store.CacheEntry }

func (c *memCache) Get(_ context.Context, key string) (store.CacheEntry, error) {
	e, ok := c.m[key]
	if !ok {
		return store.CacheEntry{}, store.ErrNotFound
	}
	return e, nil
}

func (c *memCache) Put(_ context.Context, key, kind, project string, value []byte, ttl time.Duration) error {
	c.m[key] = store.CacheEntry{Key: key, Kind: kind, Project: project, Value: value, FetchedAt: testNow, TTL: ttl}
	return nil
}

func TestCacheWriteThrough(t *testing.T) {
	ctx := context.Background()
	f := newFake(t, ok("work_items_page2.json"), reply{status: 500})
	cache := &memCache{m: map[string]store.CacheEntry{}}
	c, _ := f.client(t, WithCache(cache, 2*time.Minute))
	filter := WorkItemFilter{AssigneeIDs: []string{"u-bob"}}

	if _, hit := c.CachedWorkItems(ctx, "p-forge", filter); hit {
		t.Fatal("cache hit before any fetch")
	}
	if _, err := c.WorkItems(ctx, "p-forge", filter); err != nil {
		t.Fatal(err)
	}
	if len(cache.m) != 1 {
		t.Fatalf("cache has %d entries", len(cache.m))
	}
	for k, e := range cache.m {
		if !strings.HasPrefix(k, "plane:smithy:work_items:p-forge:") || e.Kind != CacheKind || e.Project != "p-forge" || e.TTL != 2*time.Minute {
			t.Errorf("entry %s = %+v", k, e)
		}
	}
	got, hit := c.CachedWorkItems(ctx, "p-forge", filter)
	if !hit || len(got.Items) != 1 || got.Items[0].Key() != "FORGE-10" || !got.FetchedAt.Equal(testNow) {
		t.Fatalf("cached = %+v, %v", got, hit)
	}
	if _, hit := c.CachedWorkItems(ctx, "p-forge", WorkItemFilter{}); hit {
		t.Error("different filter hit the cache")
	}
	// A failed refresh leaves the cached list alone.
	if _, err := c.WorkItems(ctx, "p-forge", filter); err == nil {
		t.Fatal("want error from 500")
	}
	if got, hit := c.CachedWorkItems(ctx, "p-forge", filter); !hit || len(got.Items) != 1 {
		t.Error("cache lost after failed refresh")
	}
}

func TestIssueContext(t *testing.T) {
	f := newFake(t, ok("work_item.json"))
	c, _ := f.client(t)
	got, err := c.IssueContext(context.Background(), "FORGE-12")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"issue_title": "Ember particles flicker on resize",
		"issue_body":  "When the terminal is resized the embers flicker.\n- shrink the window\n- watch the forge\nExpected: smooth & steady.",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestNewValidates(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Plane
		key  string
		want string
	}{
		{"no base", config.Plane{Workspace: "w"}, "k", "base_url"},
		{"no workspace", config.Plane{BaseURL: "https://api.plane.so"}, "k", "workspace_slug"},
		{"no key", config.Plane{BaseURL: "https://api.plane.so", Workspace: "w"}, " ", "API key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.cfg, tt.key); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want mention of %s", err, tt.want)
			}
		})
	}
}
