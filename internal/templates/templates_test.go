package templates

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/store"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name, path, data string
		want             Template
		wantErr          string
	}{
		{
			name: "no front matter", path: "/t/plain.md", data: "Do {thing}.\n",
			want: Template{Name: "plain", Body: "Do {thing}.\n", Path: "/t/plain.md"},
		},
		{
			name: "full", path: "/t/x.md",
			data: "---\nname: impl\ndescription: Implement\nvariables:\n  - issue_title\n  - name: scope\n    description: what\n    default: all\n---\nBody {scope}\n",
			want: Template{
				Name: "impl", Description: "Implement", Path: "/t/x.md", Body: "Body {scope}\n",
				Variables: []Var{{Name: "issue_title"}, {Name: "scope", Description: "what", Default: "all"}},
			},
		},
		{
			name: "missing name uses file", path: "/t/fix-ci.md", data: "---\ndescription: d\n---\nB",
			want: Template{Name: "fix-ci", Description: "d", Path: "/t/fix-ci.md", Body: "B"},
		},
		{
			name: "empty front matter", path: "/t/e.md", data: "---\n---\nB\n",
			want: Template{Name: "e", Path: "/t/e.md", Body: "B\n"},
		},
		{
			name: "front matter only", path: "/t/f.md", data: "---\nname: f\n---",
			want: Template{Name: "f", Path: "/t/f.md"},
		},
		{
			name: "crlf", path: "/t/c.md", data: "---\r\nname: c\r\n---\r\nB\r\n",
			want: Template{Name: "c", Path: "/t/c.md", Body: "B\n"},
		},
		{name: "unclosed", path: "/t/u.md", data: "---\nname: u\nB\n", wantErr: "no closing ---"},
		{name: "bad yaml", path: "/t/b.md", data: "---\nname: [\n---\nB", wantErr: "front matter"},
		{name: "bad var name", path: "/t/v.md", data: "---\nvariables: [Issue]\n---\nB", wantErr: `variable "Issue"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.path, []byte(tt.data))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestVarsAndRender(t *testing.T) {
	tests := []struct {
		name, body  string
		vals        map[string]string
		wantVars    []string
		wantOut     string
		wantMissing []string
	}{
		{
			name: "all set", body: "{a} and {b} and {a}", vals: map[string]string{"a": "1", "b": "2"},
			wantVars: []string{"a", "b"}, wantOut: "1 and 2 and 1",
		},
		{
			name: "missing kept", body: "{a} {b} {b}", vals: map[string]string{"a": "1"},
			wantVars: []string{"a", "b"}, wantOut: "1 {b} {b}", wantMissing: []string{"b"},
		},
		{
			name: "escape and non-idents", body: `\{a} {A} {} { a } {1x} func() {}`,
			wantOut: `{a} {A} {} { a } {1x} func() {}`,
		},
		{
			name: "value with braces is not re-expanded", body: "{a}", vals: map[string]string{"a": "{b}"},
			wantVars: []string{"a"}, wantOut: "{b}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Vars(tt.body); !slices.Equal(got, tt.wantVars) {
				t.Errorf("Vars = %v, want %v", got, tt.wantVars)
			}
			out, missing := Render(tt.body, tt.vals)
			if out != tt.wantOut || !slices.Equal(missing, tt.wantMissing) {
				t.Errorf("Render = %q %v, want %q %v", out, missing, tt.wantOut, tt.wantMissing)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("b.md", "---\nname: zeta\n---\nZ")
	write("a.md", "A")
	write("bad.md", "---\nname: [\n---\n")
	write("dup.md", "---\nname: a\n---\n")
	write("notes.txt", "ignored")
	if err := os.Mkdir(filepath.Join(dir, "sub.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	ts, errs := Load(dir)
	var names []string
	for _, x := range ts {
		names = append(names, x.Name)
	}
	if !slices.Equal(names, []string{"a", "zeta"}) {
		t.Errorf("names = %v", names)
	}
	if len(errs) != 2 {
		t.Errorf("errs = %v, want bad.md and dup.md", errs)
	}
	if ts, errs := Load(filepath.Join(dir, "nope")); ts != nil || errs != nil {
		t.Errorf("missing dir: %v %v", ts, errs)
	}
}

func TestEnsureDefaults(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "templates")
	wrote, err := EnsureDefaults(dir)
	if err != nil || !wrote {
		t.Fatalf("first run: %v %v", wrote, err)
	}
	ts, errs := Load(dir)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	var names []string
	for _, x := range ts {
		names = append(names, x.Name)
	}
	want := []string{"explore", "fix-ci", "implement-issue", "refactor", "review-pr", "write-tests"}
	if !slices.Equal(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}

	edited := filepath.Join(dir, "explore.md")
	if err := os.WriteFile(edited, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "refactor.md")); err != nil {
		t.Fatal(err)
	}
	if wrote, err := EnsureDefaults(dir); err != nil || wrote {
		t.Fatalf("second run: %v %v", wrote, err)
	}
	if b, _ := os.ReadFile(edited); string(b) != "mine" {
		t.Errorf("edited template overwritten: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "refactor.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("deleted default came back")
	}
}

func TestDefaultsUseKnownOrDeclaredVars(t *testing.T) {
	ds := Defaults()
	if len(ds) != 6 {
		t.Fatalf("got %d defaults", len(ds))
	}
	for _, d := range ds {
		if d.Description == "" {
			t.Errorf("%s: no description", d.Name)
		}
		for _, v := range Vars(d.Body) {
			if _, known := Known[v]; !known {
				if _, ok := d.Var(v); !ok {
					t.Errorf("%s: {%s} is neither known nor declared", d.Name, v)
				}
			}
		}
	}
	impl, _ := Find(ds, "implement-issue")
	if !slices.Equal(impl.RefKinds(), []store.LinkKind{store.KindPlaneIssue}) {
		t.Errorf("implement-issue refs = %v", impl.RefKinds())
	}
	review, _ := Find(ds, "review-pr")
	if !slices.Equal(review.RefKinds(), []store.LinkKind{store.KindGitHubPR}) {
		t.Errorf("review-pr refs = %v", review.RefKinds())
	}
}

func TestResolve(t *testing.T) {
	tmpl := Template{
		Body:      "{repo} {branch} {docs} {issue} {issue_title} {issue_body} {scope} {other}",
		Variables: []Var{{Name: "scope", Default: "all"}, {Name: "extra"}},
	}
	proj := config.Project{Name: "f", Repo: "o/f", Path: "/src/f"}
	issueOK := ProviderFunc(func(_ context.Context, ref string) (map[string]string, error) {
		return map[string]string{"issue_title": "Title of " + ref, "issue_body": "Body", "issue": "ignored"}, nil
	})
	issueErr := ProviderFunc(func(context.Context, string) (map[string]string, error) {
		return nil, errors.New("offline")
	})
	reg := func(p ContextProvider) *Registry {
		r := NewRegistry()
		if p != nil {
			r.Register(store.KindPlaneIssue, p)
		}
		return r
	}
	refs := map[store.LinkKind]string{store.KindPlaneIssue: " PROJ-1 "}

	tests := []struct {
		name        string
		reg         *Registry
		in          Input
		want        map[string]string
		wantMissing []string
		wantErrs    int
	}{
		{
			name: "provider fills issue",
			reg:  reg(issueOK),
			in:   Input{Project: proj, Branch: "agent/x", Refs: refs},
			want: map[string]string{
				"repo": "o/f", "branch": "agent/x", "docs": "/src/f/.forge/docs", "issue": "PROJ-1",
				"issue_title": "Title of PROJ-1", "issue_body": "Body", "scope": "all",
			},
			wantMissing: []string{"other", "extra"},
		},
		{
			name: "vars win over provider and defaults",
			reg:  reg(issueOK),
			in: Input{
				Project: proj, Branch: "agent/x", Refs: refs,
				Vars: map[string]string{"issue_title": "mine", "scope": "pkg", "other": "o", "extra": "e"},
			},
			want: map[string]string{
				"repo": "o/f", "branch": "agent/x", "docs": "/src/f/.forge/docs", "issue": "PROJ-1",
				"issue_title": "mine", "issue_body": "Body", "scope": "pkg", "other": "o", "extra": "e",
			},
		},
		{
			name: "provider error leaves vars missing",
			reg:  reg(issueErr),
			in:   Input{Project: proj, Branch: "agent/x", Refs: refs},
			want: map[string]string{
				"repo": "o/f", "branch": "agent/x", "docs": "/src/f/.forge/docs", "issue": "PROJ-1", "scope": "all",
			},
			wantMissing: []string{"issue_title", "issue_body", "other", "extra"},
			wantErrs:    1,
		},
		{
			name: "no provider registered",
			reg:  nil,
			in:   Input{Project: proj, Branch: "agent/x", Refs: refs},
			want: map[string]string{
				"repo": "o/f", "branch": "agent/x", "docs": "/src/f/.forge/docs", "issue": "PROJ-1", "scope": "all",
			},
			wantMissing: []string{"issue_title", "issue_body", "other", "extra"},
		},
		{
			name: "no ref",
			reg:  reg(issueOK),
			in:   Input{Project: proj, Branch: "agent/x"},
			want: map[string]string{
				"repo": "o/f", "branch": "agent/x", "docs": "/src/f/.forge/docs", "scope": "all",
			},
			wantMissing: []string{"issue", "issue_title", "issue_body", "other", "extra"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, missing, errs := Resolve(context.Background(), tt.reg, tmpl, tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("vals = %v\nwant   %v", got, tt.want)
			}
			if !slices.Equal(missing, tt.wantMissing) {
				t.Errorf("missing = %v, want %v", missing, tt.wantMissing)
			}
			if len(errs) != tt.wantErrs {
				t.Errorf("errs = %v", errs)
			}
		})
	}
}

func TestRefKindFor(t *testing.T) {
	for name, want := range map[string]store.LinkKind{
		"issue": store.KindPlaneIssue, "issue_body": store.KindPlaneIssue,
		"pr": store.KindGitHubPR, "pr_title": store.KindGitHubPR,
		"issues": "", "prompt": "", "repo": "",
	} {
		got, ok := RefKindFor(name)
		if got != want || ok != (want != "") {
			t.Errorf("RefKindFor(%q) = %q %v", name, got, ok)
		}
	}
}
