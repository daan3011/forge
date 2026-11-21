package plane

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/daan3011/forge/internal/config"
)

// fakeLinker records saved Plane links.
type fakeLinker struct {
	mu    sync.Mutex
	saved []string // "repo=id"
	err   error
}

func (f *fakeLinker) SetPlane(_ context.Context, repo, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.saved = append(f.saved, repo+"="+id)
	return nil
}

// detectedConfig has projects as zero-config detection makes them: repos
// and paths, no Plane links yet.
func detectedConfig() *config.Config {
	cfg := testConfig()
	cfg.Projects = []config.Project{
		{Name: "forge", Repo: "daan/forge", Path: "/code/forge"}, // matches Plane "FORGE"
		{Name: "misc", Repo: "daan/misc", Path: "/code/misc"},    // matches nothing
		{Name: "lib", Repo: "acme/lib", Path: "/code/lib"},       // matches "LIB"
	}
	return cfg
}

func TestAutoLinksMatchingRepos(t *testing.T) {
	api := samplePlane()
	lk := &fakeLinker{}
	h := newHarnessWith(api, nil, detectedConfig(), lk, "forge")
	if errs := h.init(); len(errs) > 0 {
		t.Fatal(errs)
	}
	if want := []string{"daan/forge=" + p1, "acme/lib=" + p2}; !slices.Equal(lk.saved, want) {
		t.Errorf("saved = %v, want %v", lk.saved, want)
	}
	if h.dialog != nil {
		t.Error("asked although the active project matched")
	}
	if got := h.keys(); !slices.Equal(got, []string{"FORGE-1", "FORGE-2"}) {
		t.Errorf("rows = %v", got)
	}
	// misc stays undecided, but it isn't active: nobody is asked about it.
	if h.cfg.Projects[1].PlaneDecided {
		t.Error("misc got linked")
	}
	h.drain(h.Refresh())
	if api.projectLists != 1 || len(lk.saved) != 2 {
		t.Errorf("refresh listed Plane projects %d times, saved %v", api.projectLists, lk.saved)
	}
}

func TestPickerForUnmatchedActiveProject(t *testing.T) {
	api := samplePlane()
	lk := &fakeLinker{}
	h := newHarnessWith(api, nil, detectedConfig(), lk, "misc")
	h.init()
	if h.dialog == nil {
		t.Fatalf("no picker:\n%s", h.view())
	}
	mustContain(t, h.view(), "Which Plane project goes with daan/misc?", "Forge (FORGE)", "Lib (LIB)", "None: this repo has no Plane project")
	h.send("down", "enter") // Forge, Lib, None → Lib
	if h.dialog != nil {
		t.Fatal("picker still open")
	}
	if !slices.Contains(lk.saved, "daan/misc="+p2) {
		t.Errorf("saved = %v", lk.saved)
	}
	if got := h.keys(); !slices.Equal(got, []string{"LIB-7"}) {
		t.Errorf("rows = %v", got)
	}
}

func TestPickerNoneAndNotAskedAgain(t *testing.T) {
	api := samplePlane()
	lk := &fakeLinker{}
	h := newHarnessWith(api, nil, detectedConfig(), lk, "misc")
	h.init()
	h.send("down", "down", "enter") // None
	if !slices.Contains(lk.saved, "daan/misc=") {
		t.Errorf("saved = %v", lk.saved)
	}
	mustContain(t, h.view(), "misc has no Plane project · ctrl+k → Plane: link project")
	h.drain(h.Refresh())
	if h.dialog != nil {
		t.Error("asked again after None")
	}
	// The palette command asks again, and a new answer replaces None.
	h.drain(h.linkProject())
	if h.dialog == nil {
		t.Fatal("palette command didn't open the picker")
	}
	h.send("up", "up", "enter") // starts on the current choice (None) → Forge
	if lk.saved[len(lk.saved)-1] != "daan/misc="+p1 {
		t.Errorf("saved = %v", lk.saved)
	}
}

func TestPickerEscAsksOncePerRun(t *testing.T) {
	api := samplePlane()
	h := newHarnessWith(api, nil, detectedConfig(), &fakeLinker{}, "misc")
	h.init()
	h.send("esc")
	if h.dialog != nil {
		t.Fatal("esc didn't close the picker")
	}
	h.drain(h.Refresh())
	if h.dialog != nil {
		t.Error("picker reopened in the same run")
	}
	mustContain(t, h.view(), "misc has no Plane project")
}

func TestLinkSaveFailure(t *testing.T) {
	api := samplePlane()
	lk := &fakeLinker{err: errors.New("disk full")}
	h := newHarnessWith(api, nil, detectedConfig(), lk, "forge")
	errs := h.init()
	if len(errs) != 1 || !strings.Contains(errs[0], "save project link: disk full") {
		t.Errorf("errs = %v", errs)
	}
	if len(h.link.pending) != 0 || h.cfg.Projects[0].PlaneDecided {
		t.Errorf("pending %v decided %v", h.link.pending, h.cfg.Projects[0].PlaneDecided)
	}
}

func TestLinkCommandNeedsAProject(t *testing.T) {
	h := newHarnessWith(samplePlane(), nil, detectedConfig(), nil, "")
	h.init()
	errs, _ := h.drain(h.linkProject())
	if len(errs) != 1 || !strings.Contains(errs[0], "pick a project first") {
		t.Errorf("errs = %v", errs)
	}
	mustContain(t, h.view(), "2 projects") // forge and lib got linked; all shows both
}
