package projects

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
)

var testPlaneProjects = []plane.Project{
	{ID: "id-forge", Identifier: "FORGE", Name: "Forge"},
	{ID: "id-web", Identifier: "WEB", Name: "Web App"},
	{ID: "id-anvil", Identifier: "ANV", Name: "Anvil"},
	{ID: "id-old", Identifier: "OLD", Name: "Legacy", ArchivedAt: plane.Date{Time: time.Now()}},
}

const allChoices = "name one of: Anvil (ANV), Forge (FORGE), Web App (WEB)"

func TestPlaneLinkOf(t *testing.T) {
	tests := []struct {
		name    string
		p       config.Project
		ps      []plane.Project
		wantID  string
		wantSrc PlaneSource
	}{
		{"pinned in config", config.Project{Repo: "a/x", PlaneProjectID: "pin"}, testPlaneProjects, "pin", PlaneLinked},
		{"saved link beats a match", config.Project{Repo: "a/forge", PlaneProjectID: "id-web", PlaneDecided: true}, testPlaneProjects, "id-web", PlaneLinked},
		{"saved none beats a match", config.Project{Repo: "a/forge", PlaneDecided: true}, testPlaneProjects, "", PlaneNone},
		{"automatic match", config.Project{Repo: "a/forge"}, testPlaneProjects, "id-forge", PlaneMatched},
		{"match needs the list", config.Project{Repo: "a/forge"}, nil, "", PlaneUnresolved},
		{"no match", config.Project{Repo: "a/lonely"}, testPlaneProjects, "", PlaneUnresolved},
		{"archived never matches", config.Project{Repo: "a/legacy"}, testPlaneProjects, "", PlaneUnresolved},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, src := PlaneLinkOf(tt.p, tt.ps)
			if id != tt.wantID || src != tt.wantSrc {
				t.Fatalf("got %q %v, want %q %v", id, src, tt.wantID, tt.wantSrc)
			}
		})
	}
}

func TestResolvePlane(t *testing.T) {
	projs := []config.Project{
		{Name: "forge", Repo: "daan/forge"},                                               // undecided, matches FORGE
		{Name: "pinned", Repo: "daan/pinned", PlaneProjectID: "id-pin"},                   // config pin
		{Name: "saved", Repo: "daan/saved", PlaneProjectID: "id-web", PlaneDecided: true}, // picked before
		{Name: "anv", Repo: "daan/anv", PlaneDecided: true},                               // picked "none"
		{Name: "lonely", Repo: "daan/lonely"},                                             // undecided, no match
	}
	tests := []struct {
		name      string
		arg, here string
		projs     []config.Project
		listErr   error
		wantID    string
		wantSrc   PlaneSource
		wantLocal string // forge project name; "" = none
		wantLists int    // Plane project list fetches
		wantErr   []string
	}{
		// 1 + 2: a forge project with a link never fetches the list.
		{name: "local name, pinned link", arg: "pinned", wantID: "id-pin", wantSrc: PlaneLinked, wantLocal: "pinned"},
		{name: "local name, saved link", arg: "SAVED", wantID: "id-web", wantSrc: PlaneLinked, wantLocal: "saved"},
		{name: "here, saved link", here: "saved", wantID: "id-web", wantSrc: PlaneLinked, wantLocal: "saved"},
		// 1 + 3: undecided projects match automatically.
		{name: "local name, automatic match", arg: "forge", wantID: "id-forge", wantSrc: PlaneMatched, wantLocal: "forge", wantLists: 1},
		{name: "identifier equal to a local name", arg: "FORGE", wantID: "id-forge", wantSrc: PlaneMatched, wantLocal: "forge", wantLists: 1},
		{name: "here, automatic match", here: "forge", wantID: "id-forge", wantSrc: PlaneMatched, wantLocal: "forge", wantLists: 1},
		// 4: a typed name that isn't (or doesn't resolve as) a forge project.
		{name: "Plane identifier", arg: "web", wantID: "id-web", wantSrc: PlaneNamed, wantLists: 1},
		{name: "Plane name", arg: "web app", wantID: "id-web", wantSrc: PlaneNamed, wantLists: 1},
		{name: "Plane UUID", arg: "id-anvil", wantID: "id-anvil", wantSrc: PlaneNamed, wantLists: 1},
		{name: "archived Plane project by name", arg: "OLD", wantID: "id-old", wantSrc: PlaneNamed, wantLists: 1},
		{name: "local none, typed identifier still works", arg: "anv", wantID: "id-anvil", wantSrc: PlaneNamed, wantLists: 1},
		// No arg and no repo here: the only linked project.
		{
			name: "only linked project", projs: []config.Project{{Name: "forge", Repo: "daan/forge"}, {Name: "saved", Repo: "daan/saved", PlaneProjectID: "id-web"}},
			wantID: "id-web", wantSrc: PlaneLinked, wantLocal: "saved",
		},
		// Errors.
		{name: "several linked", wantErr: []string{"several projects use Plane; pick one of: pinned, saved"}},
		{
			name: "none linked", projs: []config.Project{{Name: "lonely", Repo: "daan/lonely"}}, wantLists: 1,
			wantErr: []string{"no forge project is linked to Plane; " + allChoices},
		},
		{name: "here, no match", here: "lonely", wantLists: 1, wantErr: []string{"no Plane project matches lonely (daan/lonely); " + allChoices}},
		{name: "here, chose none", here: "anv", wantLists: 1, wantErr: []string{"anv has no Plane project (chosen before", allChoices}},
		{name: "unknown", arg: "nope", wantLists: 1, wantErr: []string{`no forge or Plane project "nope"; ` + allChoices}},
		{name: "list fails", arg: "forge", listErr: errors.New("boom"), wantLists: 1, wantErr: []string{"listing Plane projects: boom"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps := tt.projs
			if ps == nil {
				ps = projs
			}
			lists := 0
			list := func(context.Context) ([]plane.Project, error) {
				lists++
				return testPlaneProjects, tt.listErr
			}
			res, err := ResolvePlane(context.Background(), tt.arg, tt.here, ps, list)
			if lists != tt.wantLists {
				t.Errorf("%d list fetches, want %d", lists, tt.wantLists)
			}
			if len(tt.wantErr) > 0 {
				if err == nil {
					t.Fatalf("no error; got %+v", res)
				}
				for _, w := range tt.wantErr {
					if !strings.Contains(err.Error(), w) {
						t.Fatalf("err = %q, want it to contain %q", err, w)
					}
				}
				if strings.Contains(err.Error(), "Legacy") {
					t.Fatalf("archived project listed: %q", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			local := ""
			if res.Project != nil {
				local = res.Project.Name
			}
			if res.ID != tt.wantID || res.Source != tt.wantSrc || local != tt.wantLocal {
				t.Fatalf("got %q %v %q, want %q %v %q", res.ID, res.Source, local, tt.wantID, tt.wantSrc, tt.wantLocal)
			}
			if tt.wantLists > 0 && res.Plane.ID != res.ID && res.ID != "id-pin" {
				t.Errorf("Plane = %+v, want the fetched project", res.Plane)
			}
		})
	}
}
