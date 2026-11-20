package tmux

import (
	"errors"
	"testing"
)

func TestSanitizeName(t *testing.T) {
	long := ""
	for range 70 {
		long += "a"
	}
	tests := []struct{ in, want string }{
		{"agent", "agent"},
		{"fix-login_bug2", "fix-login_bug2"},
		{"my agent", "my-agent"},
		{"a.b:c", "a-b-c"},
		{"=@%$!^{}", ""},
		{"  spaced  out  ", "spaced-out"},
		{"_forge", "forge"},
		{"--x--", "x"},
		{"émoji 🔥 name", "moji-name"},
		{"semi;colon", "semi-colon"},
		{"", ""},
		{long, long[:64]},
		{long[:63] + ".b", long[:63]},
	}
	for _, tt := range tests {
		if got := SanitizeName(tt.in); got != tt.want {
			t.Errorf("SanitizeName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCheckName(t *testing.T) {
	for _, ok := range []string{"a", "agent-1", "a_b"} {
		if err := CheckName(ok); err != nil {
			t.Errorf("CheckName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "a b", "a:b", "a.b", "_forge", "-a"} {
		if err := CheckName(bad); !errors.Is(err, ErrInvalidName) {
			t.Errorf("CheckName(%q) = %v, want ErrInvalidName", bad, err)
		}
	}
}

func TestTarget(t *testing.T) {
	tg := Target{Session: "proj", Window: "agent"}
	if got := tg.String(); got != "=proj:=agent" {
		t.Errorf("String = %q", got)
	}
	if err := tg.Validate(); err != nil {
		t.Errorf("Validate = %v", err)
	}
	if err := (Target{Session: "proj", Window: "a.b"}).Validate(); !errors.Is(err, ErrInvalidName) {
		t.Errorf("Validate(a.b) = %v", err)
	}
}

func TestLastLines(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"a\nb\nc\n\n  \n", 0, "a\nb\nc"},
		{"a\nb\nc\n", 2, "b\nc"},
		{"a\nb\n", 5, "a\nb"},
		{"", 3, ""},
		{"\n\n", 3, ""},
	}
	for _, tt := range tests {
		if got := LastLines(tt.in, tt.n); got != tt.want {
			t.Errorf("LastLines(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
		}
	}
}
