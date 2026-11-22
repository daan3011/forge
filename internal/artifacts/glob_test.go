package artifacts

import "testing"

func TestMatch(t *testing.T) {
	tests := []struct {
		pattern, name string
		want          bool
	}{
		{"**/*.html", "a.html", true},
		{"**/*.html", "x/y/a.html", true},
		{"**/*.html", "x/y/a.htm", false},
		{"*.html", "x/a.html", false},
		{"*.html", "a.html", true},
		{"design/**", "design/a.png", true},
		{"design/**", "design/sub/a.md", true},
		{"design/**", "src/design/a.png", false},
		{"design/**", "design", true},
		{".forge/artifacts/**", ".forge/artifacts/s/a.html", true},
		{"a/**/b/*.svg", "a/b/x.svg", true},
		{"a/**/b/*.svg", "a/1/2/b/x.svg", true},
		{"a/**/b/*.svg", "a/1/2/c/x.svg", false},
		{"**/**/*.png", "a/b.png", true},
		{"/home/me/art/*.html", "/home/me/art/x.html", true},
		{"/home/me/art/*.html", "/home/me/art/sub/x.html", false},
		{"[", "[", false},
	}
	for _, tt := range tests {
		if got := Match(tt.pattern, tt.name); got != tt.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
		}
	}
}

func TestRoot(t *testing.T) {
	tests := []struct{ pattern, want string }{
		{"/a/b/**/*.html", "/a/b"},
		{"/a/b/x.html", "/a/b"},
		{"/*/x", "/"},
		{"design/**", "design"},
		{"*.png", ""},
		{"**/*.png", ""},
		{"/home/me/c?de/*.svg", "/home/me"},
	}
	for _, tt := range tests {
		if got := Root(tt.pattern); got != tt.want {
			t.Errorf("Root(%q) = %q, want %q", tt.pattern, got, tt.want)
		}
	}
}
