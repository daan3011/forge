package worktree

import (
	"reflect"
	"testing"
)

func TestParseList(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Worktree
	}{
		{"empty", "", nil},
		{
			"main and branch",
			"worktree /code/forge\x00HEAD aaa\x00branch refs/heads/main\x00\x00" +
				"worktree /wt/forge/fix ci\x00HEAD bbb\x00branch refs/heads/agent/fix\x00\x00",
			[]Worktree{
				{Path: "/code/forge", Head: "aaa", Branch: "main"},
				{Path: "/wt/forge/fix ci", Head: "bbb", Branch: "agent/fix"},
			},
		},
		{
			"bare and detached",
			"worktree /code/forge.git\x00bare\x00\x00worktree /wt/x\x00HEAD ccc\x00detached\x00\x00",
			[]Worktree{
				{Path: "/code/forge.git", Bare: true},
				{Path: "/wt/x", Head: "ccc", Detached: true},
			},
		},
		{
			"locked with reason, prunable",
			"worktree /wt/a\x00HEAD d\x00branch refs/heads/agent/a\x00locked on usb\x00\x00" +
				"worktree /wt/b\x00HEAD e\x00branch refs/heads/agent/b\x00locked\x00prunable gitdir file points to non-existent location\x00\x00",
			[]Worktree{
				{Path: "/wt/a", Head: "d", Branch: "agent/a", Locked: true},
				{Path: "/wt/b", Head: "e", Branch: "agent/b", Locked: true, Prunable: true},
			},
		},
		{
			"no trailing record terminator",
			"worktree /a\x00HEAD f\x00branch refs/heads/main",
			[]Worktree{{Path: "/a", Head: "f", Branch: "main"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseList(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
	if _, err := parseList("HEAD aaa\x00"); err == nil {
		t.Error("attribute before worktree line: want error")
	}
}

func TestCheckName(t *testing.T) {
	for _, ok := range []string{"a", "fix-ci", "Fix_CI-2", "x123456789012345678901234567890123456789012345678901234567890123"} {
		if err := CheckName(ok); err != nil {
			t.Errorf("CheckName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-a", "_a", "a/b", "a b", "a.b", "..", "x1234567890123456789012345678901234567890123456789012345678901234"} {
		if err := CheckName(bad); err == nil {
			t.Errorf("CheckName(%q): want error", bad)
		}
	}
}

func TestCheckBase(t *testing.T) {
	for _, ok := range []string{"main", "release/1.2", "dev_x"} {
		if err := checkBase(ok); err != nil {
			t.Errorf("checkBase(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-main", "--upload-pack=x", "a..b", "a b", "a:b", "x.lock", "/a", "a/", "a@{1}", "a~1"} {
		if err := checkBase(bad); err == nil {
			t.Errorf("checkBase(%q): want error", bad)
		}
	}
}
