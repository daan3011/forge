package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/store"
)

// planeServer serves the plane package's fixtures by path and records the
// work item queries.
type planeServer struct {
	mu      sync.Mutex
	queries []url.Values
}

func newPlaneServer(t *testing.T) (*httptest.Server, *planeServer) {
	t.Helper()
	ps := &planeServer{}
	routes := map[string]string{
		"/api/v2/users/me/":                                      "me.json",
		"/api/v2/workspaces/smithy/projects/":                    "projects.json",
		"/api/v2/workspaces/smithy/projects/p-forge/work-items/": "work_items_page2.json",
		"/api/v2/workspaces/smithy/projects/p-anvil/work-items/": "",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture, ok := routes[r.URL.Path]
		if !ok {
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/work-items/") {
			ps.mu.Lock()
			ps.queries = append(ps.queries, r.URL.Query())
			ps.mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		if fixture == "" {
			_, _ = w.Write([]byte(`{"data":[],"has_more":false}`))
			return
		}
		b, err := os.ReadFile("../../internal/plane/testdata/" + fixture)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv, ps
}

const planeProjects = `plane: {workspace_slug: smithy, api_key: k3y}
projects:
  - {name: forge, repo: daan/forge, path: /tmp, plane_project_id: p-forge}
  - {name: other, repo: daan/other, path: /tmp}
`

func TestPlaneLs(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		want      []string // substrings of stdout
		wantErr   string
		wantQuery url.Values // subset of the work item query
	}{
		{
			name:      "default project, table",
			args:      []string{"plane", "ls"},
			want:      []string{"ID        STATE  PRIO  TITLE", "FORGE-10  Done   none  Hammer animation timing"},
			wantQuery: url.Values{"expand": {"state"}, "per_page": {"50"}},
		},
		{
			name:      "forge project name, filters",
			args:      []string{"plane", "ls", "forge", "--state-group", "started,Unstarted", "--mine", "--search", "hammer", "--limit", "0"},
			want:      []string{"FORGE-10"},
			wantQuery: url.Values{"state_group__in": {"started,unstarted"}, "assignee_id": {"u-ada"}, "search": {"hammer"}, "per_page": {"200"}},
		},
		{
			name: "plane identifier",
			args: []string{"plane", "ls", "anv"},
			want: []string{"no work items"},
		},
		{
			name: "json",
			args: []string{"plane", "ls", "FORGE", "--json"},
			want: []string{`"identifier": "FORGE-10"`, `"name": "Done"`},
		},
		{
			name:    "forge project without plane id",
			args:    []string{"plane", "ls", "other"},
			wantErr: "other has no plane_project_id",
		},
		{
			name:    "unknown project",
			args:    []string{"plane", "ls", "nope"},
			wantErr: `no forge or Plane project "nope"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, _ := newEnvCLI(t, planeProjects)
			srv, ps := newPlaneServer(t)
			d.newPlane = func(cfg config.Plane, cache plane.Cache) (plane.API, error) {
				if cfg.Workspace != "smithy" || cache == nil {
					t.Errorf("cfg = %+v, cache = %v", cfg, cache)
				}
				cfg.BaseURL = srv.URL
				return plane.New(cfg, "k3y", plane.WithCache(cache, cfg.RefreshInterval.Duration))
			}
			stdout, _, err := run(t, d, tt.args...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range tt.want {
				if !strings.Contains(stdout, w) {
					t.Errorf("stdout lacks %q:\n%s", w, stdout)
				}
			}
			if tt.wantQuery != nil {
				if len(ps.queries) != 1 {
					t.Fatalf("%d work item requests", len(ps.queries))
				}
				for k, v := range tt.wantQuery {
					if got := ps.queries[0][k]; strings.Join(got, "|") != strings.Join(v, "|") {
						t.Errorf("%s = %q, want %q", k, got, v)
					}
				}
			}
		})
	}
}

func TestPlaneLsCachesList(t *testing.T) {
	d, _ := newEnvCLI(t, planeProjects)
	srv, _ := newPlaneServer(t)
	d.newPlane = func(cfg config.Plane, cache plane.Cache) (plane.API, error) {
		cfg.BaseURL = srv.URL
		return plane.New(cfg, "k3y", plane.WithCache(cache, cfg.RefreshInterval.Duration))
	}
	if _, _, err := run(t, d, "plane", "ls"); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), d.dbPath())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	stats, err := st.Cache().Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range stats {
		if s.Kind == plane.CacheKind && s.Project == "p-forge" && s.Entries == 1 {
			found = true
		}
	}
	if !found {
		b, _ := json.Marshal(stats)
		t.Errorf("no cached plane list: %s", b)
	}
}
