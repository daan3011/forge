package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestImports(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"plain", "See @docs/a.md for more", []string{"docs/a.md"}},
		{"line start", "@README.md\n@~/x.md", []string{"README.md", "~/x.md"}},
		{"trailing punctuation", "read @docs/SPEC.md.", []string{"docs/SPEC.md"}},
		{"email ignored", "mail me@example.com", nil},
		{"code span", "use `@not/this.md` but @this.md", []string{"this.md"}},
		{"double backtick span", "``@a `x` @b`` @c", []string{"c"}},
		{"fenced", "```\n@inside.md\n```\n@outside.md", []string{"outside.md"}},
		{"tilde fence", "~~~go\n@inside.md\n~~~\n", nil},
		{"unclosed fence", "```\n@inside.md", nil},
		{"escaped space", `@Design\ Docs/api.md next`, []string{"Design Docs/api.md"}},
		{"quoted", `@"a.md" @'b.md'`, nil},
		{"duplicates", "@a.md @a.md", []string{"a.md"}},
		{"bare at", "@ nothing @", nil},
		{"absolute", "@/etc/x.md", []string{"/etc/x.md"}},
		{"crlf", "@a.md\r\n@b.md", []string{"a.md", "b.md"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Imports(tt.in); !slices.Equal(got, tt.want) {
				t.Errorf("Imports(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSlug(t *testing.T) {
	tests := map[string]string{
		"/Users/me/Documents/Projects/forge": "-Users-me-Documents-Projects-forge",
		"/home/me/my.repo_x":                 "-home-me-my-repo-x",
	}
	for in, want := range tests {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{{"", 0}, {"a", 1}, {"abcd", 1}, {"abcde", 2}, {"ééééé", 2}}
	for _, tt := range tests {
		if got := EstimateTokens(tt.in); got != tt.want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestRuleGlobs(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"none", "# rule", nil},
		{"list", "---\npaths:\n  - \"src/**/*.go\"\n  - docs/*.md\n---\nbody", []string{"src/**/*.go", "docs/*.md"}},
		{"string", "---\npaths: src/**, test/**\n---\n", []string{"src/**", "test/**"}},
		{"other keys", "---\ntitle: x\n---\n", nil},
		{"bad yaml", "---\npaths: [unclosed\n---\n", nil},
		{"crlf", "---\r\npaths: a/**\r\n---\r\n", []string{"a/**"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RuleGlobs(tt.in); !slices.Equal(got, tt.want) {
				t.Errorf("RuleGlobs = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTruncateAutoMemory(t *testing.T) {
	short := "a\nb\n"
	if got, cut := TruncateAutoMemory(short); got != short || cut {
		t.Errorf("short: got %q cut=%v", got, cut)
	}
	var lines strings.Builder
	for i := range 250 {
		fmt.Fprintf(&lines, "line %d\n", i)
	}
	got, cut := TruncateAutoMemory(lines.String())
	if !cut || strings.Count(got, "\n") != AutoMemoryLines {
		t.Errorf("long: cut=%v lines=%d", cut, strings.Count(got, "\n"))
	}
	exact := strings.Repeat("x\n", AutoMemoryLines)
	if _, cut := TruncateAutoMemory(exact); cut {
		t.Error("exactly 200 lines should not be cut")
	}
	big := strings.Repeat("y", AutoMemoryBytes+10)
	if got, cut := TruncateAutoMemory(big); !cut || len(got) != AutoMemoryBytes {
		t.Errorf("bytes: cut=%v len=%d", cut, len(got))
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	home, managed, repo string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{
		home:    filepath.Join(base, "home"),
		managed: filepath.Join(base, "etc", "CLAUDE.md"),
		repo:    filepath.Join(base, "work", "proj"),
	}
	write(t, f.managed, "managed")
	write(t, filepath.Join(f.home, ".claude", "CLAUDE.md"), "user @~/.claude/extra.md")
	write(t, filepath.Join(f.home, ".claude", "extra.md"), "extra")
	write(t, filepath.Join(f.home, ".claude", "rules", "style.md"), "style")
	write(t, filepath.Join(f.home, ".claude", "rules", "go.md"), "---\npaths: \"**/*.go\"\n---\ngo")
	write(t, filepath.Join(base, "work", "CLAUDE.md"), "parent")
	write(t, filepath.Join(f.repo, "CLAUDE.md"), "project @docs/a.md @missing.md @../../outside.md\n```\n@code.md\n```")
	write(t, filepath.Join(f.repo, "docs", "a.md"), "a @b.md")
	write(t, filepath.Join(f.repo, "docs", "b.md"), "b")
	write(t, filepath.Join(base, "outside.md"), "outside")
	write(t, filepath.Join(f.repo, "CLAUDE.local.md"), "local")
	write(t, filepath.Join(f.repo, ".claude", "rules", "testing.md"), "testing")
	write(t, filepath.Join(f.repo, "sub", "CLAUDE.md"), "nested")
	write(t, filepath.Join(f.repo, "node_modules", "x", "CLAUDE.md"), "skipped")
	write(t, filepath.Join(f.repo, ".hidden", "CLAUDE.md"), "skipped")
	write(t, AutoMemoryPath(f.home, f.repo), "- memory\n")
	return f
}

func (f fixture) env() Env {
	return Env{Home: f.home, Managed: f.managed, Cwd: f.repo}
}

type got struct {
	rel      string
	src      Source
	onDemand bool
	exists   bool
}

func summarize(base string, files []File) []got {
	out := make([]got, len(files))
	for i, f := range files {
		rel, _ := filepath.Rel(base, f.Path)
		out[i] = got{rel, f.Source, f.OnDemand, f.Exists}
	}
	return out
}

func TestResolveOrder(t *testing.T) {
	f := newFixture(t)
	files := Resolve(f.env())
	base := filepath.Dir(filepath.Dir(f.repo))
	want := []got{
		{"etc/CLAUDE.md", Managed, false, true},
		{"home/.claude/CLAUDE.md", User, false, true},
		{"home/.claude/extra.md", Import, false, true},
		{"home/.claude/rules/style.md", UserRule, false, true},
		{"work/CLAUDE.md", Ancestor, false, true},
		{"work/proj/CLAUDE.md", Project, false, true},
		{"work/proj/docs/a.md", Import, false, true},
		{"work/proj/docs/b.md", Import, false, true},
		{"work/proj/missing.md", Import, false, false},
		{"outside.md", Import, false, true},
		{"work/proj/CLAUDE.local.md", Local, false, true},
		{"work/proj/.claude/rules/testing.md", ProjectRule, false, true},
		{"home/.claude/projects/" + Slug(f.repo) + "/memory/MEMORY.md", AutoMemory, false, true},
		{"home/.claude/rules/go.md", PathRule, true, true},
		{"work/proj/sub/CLAUDE.md", Nested, true, true},
	}
	if g := summarize(base, files); !slices.Equal(g, want) {
		t.Errorf("Resolve:\n got %v\nwant %v", g, want)
	}

	byPath := map[string]File{}
	for _, x := range files {
		byPath[x.Path] = x
	}
	if x := byPath[filepath.Join(base, "outside.md")]; !x.External {
		t.Error("outside.md should be external")
	}
	if x := byPath[filepath.Join(f.home, ".claude", "extra.md")]; x.External {
		t.Error("imports from the user file are never external")
	}
	if x := byPath[filepath.Join(f.repo, "docs", "b.md")]; x.Depth != 2 || x.Parent != filepath.Join(f.repo, "docs", "a.md") {
		t.Errorf("b.md depth/parent = %d %s", x.Depth, x.Parent)
	}
	if x := byPath[filepath.Join(f.home, ".claude", "rules", "go.md")]; !slices.Equal(x.Globs, []string{"**/*.go"}) {
		t.Errorf("globs = %q", x.Globs)
	}
	want0 := 0
	for _, x := range files {
		if x.Exists && !x.OnDemand {
			want0 += x.Tokens
		}
	}
	if got := Total(files); got != want0 || got == 0 {
		t.Errorf("Total = %d, want %d", got, want0)
	}
}

func TestResolvePlaceholdersAndAgentsFallback(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	home, repo := filepath.Join(base, "home"), filepath.Join(base, "repo")
	write(t, filepath.Join(repo, "AGENTS.md"), "agents")
	files := Resolve(Env{Home: home, Cwd: repo})
	want := []got{
		{"home/.claude/CLAUDE.md", User, false, false},
		{"repo/AGENTS.md", AgentsMD, false, true},
		{"repo/CLAUDE.md", Project, false, false},
		{"repo/CLAUDE.local.md", Local, false, false},
	}
	if g := summarize(base, files); !slices.Equal(g, want) {
		t.Errorf("got %v\nwant %v", g, want)
	}

	// With a CLAUDE.md, AGENTS.md is ignored.
	write(t, filepath.Join(repo, "CLAUDE.md"), "project")
	for _, f := range Resolve(Env{Home: home, Cwd: repo}) {
		if f.Source == AgentsMD {
			t.Error("AGENTS.md loaded despite CLAUDE.md")
		}
	}
}

func TestResolveImportDepthAndCycles(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(base, "repo")
	write(t, filepath.Join(repo, "CLAUDE.md"), "@1.md")
	for i := 1; i <= 6; i++ {
		write(t, filepath.Join(repo, fmt.Sprintf("%d.md", i)), fmt.Sprintf("@%d.md @CLAUDE.md", i+1))
	}
	var depths []int
	for _, f := range Resolve(Env{Home: filepath.Join(base, "home"), Cwd: repo, SkipNested: true}) {
		if f.Source == Import {
			depths = append(depths, f.Depth)
		}
	}
	if !slices.Equal(depths, []int{1, 2, 3, 4}) {
		t.Errorf("import depths = %v, want 1..4", depths)
	}
}

func TestResolveTooBig(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(base, "repo")
	write(t, filepath.Join(repo, "CLAUDE.md"), strings.Repeat("x", MaxFileBytes+1))
	for _, f := range Resolve(Env{Home: filepath.Join(base, "home"), Cwd: repo, SkipNested: true}) {
		if f.Source == Project {
			if !f.TooBig || f.Counts() {
				t.Errorf("too big file: %+v", f)
			}
		}
	}
}

func TestResolveAutoMemoryTruncated(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	home, repo := filepath.Join(base, "home"), filepath.Join(base, "repo")
	write(t, AutoMemoryPath(home, repo), strings.Repeat("0123456\n", 300))
	for _, f := range Resolve(Env{Home: home, Cwd: repo, SkipNested: true}) {
		if f.Source == AutoMemory {
			if !f.Truncated || f.Lines != 300 || f.Chars != 200*8 {
				t.Errorf("auto memory: %+v", f)
			}
			return
		}
	}
	t.Error("no auto memory")
}

func TestInheritance(t *testing.T) {
	f := newFixture(t)
	wt := filepath.Join(filepath.Dir(f.repo), "wt1")
	write(t, filepath.Join(wt, "CLAUDE.md"), "project changed on branch")
	write(t, filepath.Join(wt, ".claude", "rules", "testing.md"), "testing")

	main := Resolve(f.env())
	other := Resolve(Env{Home: f.home, Managed: f.managed, Cwd: wt, MainRoot: f.repo})
	rows := Inheritance([]Tree{{Root: f.repo, Files: main}, {Root: wt, Files: other}})

	cells := map[string][]Cell{}
	for _, r := range rows {
		key := r.Rel
		if key == "" {
			key = filepath.Base(r.Path)
		}
		cells[key] = r.Cells
	}
	tests := map[string][]Cell{
		"CLAUDE.md":                {Same, Differs},
		"CLAUDE.local.md":          {Same, Absent},
		".claude/rules/testing.md": {Same, Same},
		"docs/a.md":                {Same, Absent},
		"MEMORY.md":                {Shared, Shared},
		"style.md":                 {Shared, Shared},
	}
	for k, want := range tests {
		if got := cells[k]; !slices.Equal(got, want) {
			t.Errorf("%s: cells %v, want %v", k, got, want)
		}
	}
	for _, r := range rows {
		if r.Source == PathRule || r.Source == Nested {
			t.Errorf("on-demand file in matrix: %s", r.Path)
		}
	}
}
