package clipboard

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"testing"
)

func TestCommand(t *testing.T) {
	tests := []struct {
		name    string
		have    []string
		wayland bool
		want    []string
	}{
		{"none", nil, false, nil},
		{"xclip", []string{"xclip", "xsel"}, false, []string{"xclip", "-selection", "clipboard"}},
		{"xsel", []string{"xsel"}, false, []string{"xsel", "--clipboard", "--input"}},
		{"wayland", []string{"wl-copy", "xclip"}, true, []string{"wl-copy"}},
		{"wl-copy without wayland", []string{"wl-copy"}, false, nil},
	}
	if runtime.GOOS == "darwin" {
		tests = append(tests, struct {
			name    string
			have    []string
			wayland bool
			want    []string
		}{"pbcopy", []string{"pbcopy", "xclip"}, false, []string{"pbcopy"}})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := Exec{
				LookPath: func(name string) (string, error) {
					if slices.Contains(tt.have, name) {
						return "/bin/" + name, nil
					}
					return "", errors.New("not found")
				},
				Getenv: func(k string) string {
					if k == "WAYLAND_DISPLAY" && tt.wayland {
						return "wayland-0"
					}
					return ""
				},
			}
			if got := e.Command(); !slices.Equal(got, tt.want) {
				t.Errorf("Command = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCopyWithoutTool(t *testing.T) {
	e := Exec{LookPath: func(string) (string, error) { return "", errors.New("no") }, Getenv: func(string) string { return "" }}
	if err := e.Copy(context.Background(), "x"); !errors.Is(err, ErrNoTool) {
		t.Errorf("err = %v, want ErrNoTool", err)
	}
}
