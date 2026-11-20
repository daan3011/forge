package prs

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/daan3011/forge/internal/delta"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/tabs"
	"github.com/daan3011/forge/internal/ui"
)

// sampleDiff has three files; the second is long enough to scroll.
var sampleDiff = func() string {
	var b strings.Builder
	b.WriteString("diff --git a/main.go b/main.go\nindex 1..2 100644\n--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,3 @@\n package main\n-var x = 1\n+var x = 2\n")
	b.WriteString("diff --git a/examples/tree/main.go b/examples/tree/main.go\nnew file mode 100644\n--- /dev/null\n+++ b/examples/tree/main.go\n@@ -0,0 +1,40 @@\n")
	for i := range 40 {
		fmt.Fprintf(&b, "+line %d\n", i)
	}
	b.WriteString("diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@ -1 +1 @@\n-old readme\n+new readme\n")
	return b.String()
}()

// byAlice makes #1 someone else's PR, so it can be approved.
func byAlice(gh *fakeGH) {
	res := gh.results["author:@me"]
	res.PRs = slices.Clone(res.PRs)
	res.PRs[0].Author = "alice"
	gh.results["author:@me"] = res
}

func TestDiffViewer(t *testing.T) {
	r := newActionRig(t, byAlice)
	if got, _ := r.keys("v"); len(got.errs) > 0 {
		t.Fatal(got.errs)
	}
	if !slices.Equal(r.gh.diffCalls, []string{"acme/bubbletea#1"}) {
		t.Fatalf("diff calls = %v", r.gh.diffCalls)
	}
	if !r.tb.CapturingInput() {
		t.Error("viewer doesn't capture input")
	}
	v := view(r.tb)
	mustContain(t, v, "#1 feat(examples): tree", "main ← feat-1", "3 files +42 -2", "file 1/3",
		"M main.go", "examples/tree/main.go", "README.md", "+var x = 2", "-var x = 1")
	mustLack(t, v, "index 1..2", "--- a/main.go") // the band repeats them

	// n / p jump between files; the header and the list follow.
	dv := r.tb.diff
	r.keys("n")
	if dv.file != 1 || dv.vp.YOffset() != dv.starts[1] {
		t.Fatalf("after n: file %d, offset %d, starts %v", dv.file, dv.vp.YOffset(), dv.starts)
	}
	mustContain(t, view(r.tb), "file 2/3", "+line 0")
	r.keys("n", "n") // the last file stays the last
	if dv.file != 2 {
		t.Errorf("file = %d after n past the end", dv.file)
	}
	r.keys("p", "p", "p")
	if dv.file != 0 || dv.vp.YOffset() != 0 {
		t.Errorf("after p: file %d, offset %d", dv.file, dv.vp.YOffset())
	}
	// Scrolling into the second file selects it.
	for range dv.starts[1] {
		r.keys("j")
	}
	if dv.file != 1 {
		t.Errorf("scrolled to line %d: file %d", dv.vp.YOffset(), dv.file)
	}
	r.keys("g")
	if dv.file != 0 {
		t.Errorf("after g: file %d", dv.file)
	}

	// Close; reopening is served from memory.
	r.keys("esc")
	if r.tb.diff != nil || r.tb.CapturingInput() {
		t.Fatal("esc didn't close the viewer")
	}
	mustContain(t, view(r.tb), "feat(examples): tree") // the list again
	r.keys("v")
	if r.tb.diff == nil || !r.tb.diff.loaded || len(r.gh.diffCalls) != 1 {
		t.Errorf("reopen: loaded %v, calls %v", r.tb.diff != nil && r.tb.diff.loaded, r.gh.diffCalls)
	}
}

func TestDiffViewerKeysOnlyItsOwn(t *testing.T) {
	r := newActionRig(t, byAlice)
	r.keys("v")
	// l would switch sections, m would merge: in the viewer they don't.
	r.keys("l", "m")
	if r.tb.active != 0 || r.tb.dialog != nil {
		t.Errorf("list keys leaked into the viewer: section %d, dialog %v", r.tb.active, r.tb.dialog != nil)
	}
	if h := r.tb.ShortHelp(); len(h) == 0 || h[0].Help().Desc != "next file" {
		t.Errorf("help = %v", h)
	}
}

func TestDiffWithDelta(t *testing.T) {
	fake := &delta.Fake{}
	r := newActionRig(t, byAlice)
	r.tb.delta = fake
	r.keys("v")
	mustContain(t, view(r.tb), "Δ diff --git a/main.go b/main.go", "Δ +var x = 2")
	if !r.tb.diff.delta {
		t.Error("not marked as rendered by delta")
	}
	w := r.tb.diffTextWidth()
	if calls := fake.Calls(); len(calls) != 3 || calls[0] != w {
		t.Fatalf("delta calls = %v, want 3 at width %d", calls, w)
	}

	// A resize renders again for the new width.
	r.tb.SetSize(160, 30)
	_, cmd := r.tb.Update(struct{}{})
	r.feed(cmd)
	calls := fake.Calls()
	if len(calls) != 6 || calls[5] != r.tb.diffTextWidth() || r.tb.diff.width != r.tb.diffTextWidth() {
		t.Errorf("after resize: calls %v, width %d", calls, r.tb.diff.width)
	}
}

func TestDiffDeltaFailureFallsBack(t *testing.T) {
	r := newActionRig(t, byAlice)
	r.tb.delta = &delta.Fake{Func: func(string, int) (string, error) { return "", errors.New("unknown option") }}
	got, _ := r.keys("v")
	if len(got.info) != 1 || !strings.Contains(got.info[0], "delta failed") {
		t.Errorf("toasts = %+v", got)
	}
	mustContain(t, view(r.tb), "+var x = 2")
	mustLack(t, view(r.tb), "Δ")
	// Once per tab, not per diff.
	r.keys("esc", "j", "v")
	r.tb.diffs = diffCache{}
	if got, _ := r.keys("esc", "v"); len(got.info) != 0 {
		t.Errorf("second failure toasted again: %+v", got)
	}
}

func TestDiffErrors(t *testing.T) {
	r := newActionRig(t, func(gh *fakeGH) {
		gh.diffErr = fmt.Errorf("github: acme/bubbletea#1: %w", github.ErrDiffTooLarge)
	})
	got, _ := r.keys("v")
	if len(got.info) != 1 || !strings.Contains(got.info[0], "too large") || !strings.Contains(got.info[0], "(o)") {
		t.Errorf("too large: toasts = %+v", got)
	}
	if r.tb.diff != nil {
		t.Error("viewer stayed open")
	}
	r.gh.diffErr = errors.New("boom")
	if got, _ := r.keys("v"); !slices.Equal(got.errs, []string{"load the diff of #1 failed: boom"}) {
		t.Errorf("errs = %v", got.errs)
	}
}

func TestDiffStaleResultDropped(t *testing.T) {
	r := newActionRig(t, byAlice)
	first := r.tb.openDiff()
	r.tb.closeDiff()
	r.selectPR(t, "acme/bubbles#2")
	r.tb.openDiff() // not run: stays loading
	r.feed(first)
	if r.tb.diff == nil || r.tb.diff.loaded || r.tb.diff.pr.Number != 2 {
		t.Errorf("stale diff shown: %+v", r.tb.diff)
	}
	// It was still worth caching.
	if _, ok := r.tb.diffs.get(detailKey(r.gh.results["author:@me"].PRs[0])); !ok {
		t.Error("stale result not cached")
	}
}

func TestApproveFromViewer(t *testing.T) {
	r := newActionRig(t, byAlice)
	r.keys("v", "a")
	if r.tb.dialog == nil {
		t.Fatal("no review dialog")
	}
	mustContain(t, view(r.tb), "Approve #1", "optional", "$EDITOR")
	got, _ := r.keys("enter", "enter")
	if want := []string{`review acme/bubbletea#1 APPROVE ""`}; !slices.Equal(r.gh.actions(), want) {
		t.Fatalf("actions = %v", r.gh.actions())
	}
	if !slices.Equal(got.success, []string{"approved #1"}) || len(got.errs) > 0 {
		t.Errorf("toasts = %+v", got)
	}
	if r.tb.diff == nil {
		t.Error("viewer closed after approving")
	}
}

func TestRequestChangesNeedsBody(t *testing.T) {
	r := newActionRig(t, byAlice)
	r.keys("v", "x", "enter")
	if r.tb.dialog == nil || len(r.gh.actions()) != 0 {
		t.Fatalf("empty body accepted: dialog %v, actions %v", r.tb.dialog != nil, r.gh.actions())
	}
	_, cmd := r.tb.Update(tea.PasteMsg{Content: "fix it"})
	r.feed(cmd)
	r.keys("enter")
	got, _ := r.keys("enter")
	if want := []string{`review acme/bubbletea#1 REQUEST_CHANGES "fix it"`}; !slices.Equal(r.gh.actions(), want) {
		t.Fatalf("actions = %v", r.gh.actions())
	}
	if !slices.Equal(got.success, []string{"requested changes on #1"}) {
		t.Errorf("toasts = %+v", got)
	}

	// Esc cancels a review.
	r.keys("C", "esc")
	if r.tb.dialog != nil || len(r.gh.actions()) != 1 {
		t.Errorf("esc: dialog %v, actions %v", r.tb.dialog != nil, r.gh.actions())
	}
}

func TestReviewGuards(t *testing.T) {
	r := newActionRig(t) // #1 is the viewer's own
	r.keys("v")
	for _, k := range []string{"a", "x"} {
		if got, _ := r.keys(k); len(got.info) != 1 || !strings.Contains(got.info[0], "your own PR") {
			t.Errorf("%s on own PR: toasts = %+v", k, got)
		}
	}
	// Commenting on your own PR is fine.
	r.keys("C")
	if r.tb.dialog == nil {
		t.Error("no comment dialog on own PR")
	}
	r.keys("esc", "esc")

	merged := pr("acme/bubbles", 2, "feat: tree bubble", func(p *github.PR) { p.State = github.StateMerged; p.Author = "bob" })
	res := r.gh.results["author:@me"]
	res.PRs = []github.PR{res.PRs[0], merged}
	r.gh.results["author:@me"] = res
	drain(r.tb, r.tb.Refresh())
	r.selectPR(t, "acme/bubbles#2")
	r.keys("v")
	if got, _ := r.keys("a"); !slices.Equal(got.info, []string{"#2 is merged"}) {
		t.Errorf("approve merged: toasts = %+v", got)
	}
	if len(r.gh.actions()) != 0 {
		t.Errorf("actions = %v", r.gh.actions())
	}
}

func TestReviewPaletteCommands(t *testing.T) {
	r := newActionRig(t, byAlice)
	byID := func() map[string]tabs.Command {
		m := map[string]tabs.Command{}
		for _, c := range r.tb.Commands() {
			m[c.ID] = c
		}
		return m
	}
	cmds := byID()
	for _, id := range []string{"pr_diff", "diff_approve", "diff_request_changes", "diff_comment"} {
		if _, ok := cmds[id]; !ok {
			t.Errorf("no palette command %s", id)
		}
	}
	if k := cmds["pr_diff"].Key.Help().Key; k != "v" {
		t.Errorf("pr_diff key = %q", k)
	}
	if _, ok := cmds["diff_next_file"]; ok {
		t.Error("viewer commands offered while it is closed")
	}

	// From the list, approve works on the selected PR.
	r.feed(cmds["diff_approve"].Run())
	if r.tb.dialog == nil {
		t.Fatal("palette approve opened no dialog")
	}
	r.keys("esc")

	r.feed(cmds["pr_diff"].Run())
	if r.tb.diff == nil || !r.tb.diff.loaded {
		t.Fatal("palette didn't open the viewer")
	}
	cmds = byID()
	if k := cmds["diff_approve"].Key.Help().Key; k != "a" {
		t.Errorf("diff_approve key in viewer = %q", k)
	}
	r.feed(cmds["diff_next_file"].Run())
	if r.tb.diff.file != 1 {
		t.Errorf("file = %d", r.tb.diff.file)
	}
	r.feed(cmds["diff_close"].Run())
	if r.tb.diff != nil {
		t.Error("diff_close didn't close")
	}
}

func TestColorizeFile(t *testing.T) {
	th := ui.NewTheme(true, nil)
	files := github.ParseDiff("diff --git a/a b/b\nsimilarity index 90%\nrename from a\nrename to b\nindex 1..2\n--- a/a\n+++ b/b\n@@ -1 +1 @@\n-x\n+\ty\n\\ No newline at end of file\n")
	got := strings.Split(ansi.Strip(colorizeFile(th, files[0], 30)), "\n")
	want := []string{
		" a → b  +1 -1",
		"similarity index 90%",
		"rename from a",
		"rename to b",
		"@@ -1 +1 @@",
		"-x",
		"+    y",
		`\ No newline at end of file`,
	}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if strings.TrimRight(got[i], " ") != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	// Long lines are cut to the width.
	long := github.ParseDiff("diff --git a/x b/x\n@@ -1 +1 @@\n+" + strings.Repeat("y", 100) + "\n")
	for _, l := range strings.Split(colorizeFile(th, long[0], 20), "\n") {
		if w := ansi.StringWidth(l); w > 20 {
			t.Errorf("line %q is %d wide", ansi.Strip(l), w)
		}
	}
}

func TestShortenLeft(t *testing.T) {
	tests := []struct {
		in    string
		width int
		want  string
	}{
		{"main.go", 10, "main.go"},
		{"internal/tabs/prs/diffview.go", 15, "…rs/diffview.go"},
		{"abc", 3, "abc"},
	}
	for _, tt := range tests {
		if got := shortenLeft(tt.in, tt.width); got != tt.want {
			t.Errorf("shortenLeft(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
		}
		if ansi.StringWidth(shortenLeft(tt.in, tt.width)) > tt.width {
			t.Errorf("shortenLeft(%q, %d) too wide", tt.in, tt.width)
		}
	}
}
