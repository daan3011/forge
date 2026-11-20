package shell

import (
	"context"
	"os/exec"
	"testing"
)

func TestQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "''"},
		{"plain", "'plain'"},
		{"a b", "'a b'"},
		{"it's", `'it'\''s'`},
		{"$HOME `x` \"y\" ;", "'$HOME `x` \"y\" ;'"},
	}
	for _, tt := range tests {
		if got := Quote(tt.in); got != tt.want {
			t.Errorf("Quote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestJoinRoundTrips(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	words := []string{"a b", "it's", "$HOME", ";", "", "\\n"}
	out, err := exec.CommandContext(context.Background(), "sh", "-c", `printf '%s\n' `+Join(words...)).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := ""
	for _, w := range words {
		want += w + "\n"
	}
	if string(out) != want {
		t.Errorf("got %q, want %q", out, want)
	}
}
