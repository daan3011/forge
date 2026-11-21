package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
)

// captureServer serves the plane fixtures and records created work items.
type captureServer struct {
	mu      sync.Mutex
	created []plane.WorkItemCreate
	paths   []string
	project []string // project of each create
}

func newCaptureServer(t *testing.T) (*httptest.Server, *captureServer) {
	t.Helper()
	cs := &captureServer{}
	routes := map[string]string{
		"/api/v2/users/me/":                                      "me.json",
		"/api/v2/workspaces/smithy/projects/":                    "projects.json",
		"/api/v2/workspaces/smithy/projects/p-forge/labels/":     "labels.json",
		"/api/v2/workspaces/smithy/projects/p-forge/members/":    "members.json",
		"/api/v2/workspaces/smithy/projects/p-anvil/labels/":     "",
		"/api/v2/workspaces/smithy/projects/p-anvil/members/":    "",
		"/api/v2/workspaces/smithy/projects/p-forge/":            "",
		"/api/v2/workspaces/smithy/projects/p-forge/work-items/": "",
		"/api/v2/workspaces/smithy/projects/p-anvil/work-items/": "",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.mu.Lock()
		cs.paths = append(cs.paths, r.Method+" "+r.URL.Path)
		cs.mu.Unlock()
		fixture, ok := routes[r.URL.Path]
		if !ok {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var in plane.WorkItemCreate
			b, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(b, &in); err != nil {
				t.Error(err)
			}
			project := strings.Split(r.URL.Path, "/")[6]
			cs.mu.Lock()
			cs.created = append(cs.created, in)
			cs.project = append(cs.project, project)
			cs.mu.Unlock()
			// Plane's create response has the sequence id, not the key.
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "w-new", "name": in.Name, "sequence_id": 42, "project_id": project})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/p-forge/") {
			_, _ = w.Write([]byte(`{"id": "p-forge", "name": "Forge", "identifier": "FORGE"}`))
			return
		}
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
	return srv, cs
}

const captureProjects = `plane: {workspace_slug: smithy, api_key: k3y, base_url: https://plane.example}
projects:
  - {name: forge, repo: daan/forge, path: /tmp, plane_project_id: p-forge}
  - {name: other, repo: daan/other, path: /tmp}
`

func TestCaptureCommand(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		want     string // stdout
		wantErr  string
		project  string
		create   plane.WorkItemCreate
		notPaths []string // requests that must not happen
	}{
		{
			name: "plain title goes to the only linked project", args: []string{"capture", "fix", "the", "thing"},
			want: "created FORGE-42  https://plane.example/smithy/browse/FORGE-42/", project: "p-forge",
			create:   plane.WorkItemCreate{Name: "fix the thing"},
			notPaths: []string{"GET /api/v2/workspaces/smithy/projects/p-forge/labels/"},
		},
		{
			name: "every token", args: []string{"capture", "fix login #FORGE !high @me @bob +bug +ui"},
			want: "created FORGE-42", project: "p-forge",
			create: plane.WorkItemCreate{
				Name: "fix login", Priority: plane.PriorityHigh,
				AssigneeIDs: []string{"u-ada", "u-bob"}, LabelIDs: []string{"l-bug", "l-ui"},
			},
		},
		{
			name: "another project by identifier", args: []string{"capture", "sharpen #anv"},
			want: "created ANV-42", project: "p-anvil", create: plane.WorkItemCreate{Name: "sharpen"},
		},
		{
			name: "--project", args: []string{"capture", "-p", "anv", "sharpen"},
			want: "created ANV-42", project: "p-anvil", create: plane.WorkItemCreate{Name: "sharpen"},
		},
		{
			name: "unknown label", args: []string{"capture", "fix +bgu"},
			wantErr: `no label "bgu" — did you mean bug?`,
		},
		{
			name: "unknown project", args: []string{"capture", "fix #frge"},
			wantErr: `no project "frge" — did you mean FORGE?`,
		},
		{
			name: "no title", args: []string{"capture", "#forge !high"},
			wantErr: "a title is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, _ := newEnvCLI(t, captureProjects)
			srv, cs := newCaptureServer(t)
			d.newPlane = func(cfg config.Plane, cache plane.Cache) (plane.API, error) {
				cfg.BaseURL = srv.URL
				return plane.New(cfg, "k3y", plane.WithCache(cache, cfg.RefreshInterval.Duration))
			}
			stdout, _, err := run(t, d, tt.args...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				if len(cs.created) != 0 {
					t.Errorf("created %+v despite the error", cs.created)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stdout, tt.want) {
				t.Errorf("stdout = %q, want %q", stdout, tt.want)
			}
			if len(cs.created) != 1 {
				t.Fatalf("%d creates", len(cs.created))
			}
			got := cs.created[0]
			if cs.project[0] != tt.project || got.Name != tt.create.Name || got.Priority != tt.create.Priority ||
				!slices.Equal(got.AssigneeIDs, tt.create.AssigneeIDs) || !slices.Equal(got.LabelIDs, tt.create.LabelIDs) {
				t.Errorf("created %+v in %s, want %+v in %s", got, cs.project[0], tt.create, tt.project)
			}
			for _, p := range tt.notPaths {
				if slices.Contains(cs.paths, p) {
					t.Errorf("unneeded request %s", p)
				}
			}
		})
	}
}
