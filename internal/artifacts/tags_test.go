package artifacts

import (
	"reflect"
	"testing"
)

func TestParseTags(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{" , ,", nil},
		{"a, B, a", []string{"a", "b"}},
		{"#Design,ui , #ui", []string{"design", "ui"}},
		{"multi word", []string{"multi word"}},
		{"z,y,x", []string{"x", "y", "z"}},
	}
	for _, tt := range tests {
		if got := ParseTags(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseTags(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestTagSuggestions(t *testing.T) {
	all := []string{"brand", "design", "draft", "ui"}
	tests := []struct {
		value string
		want  []string
	}{
		{"", []string{"brand", "design", "draft", "ui"}},
		{"d", []string{"design", "draft"}},
		{"#D", []string{"design", "draft"}},
		{"ui, d", []string{"ui, design", "ui, draft"}},
		{"design,", []string{"design, brand", "design, draft", "design, ui"}},
		{"design, dr", []string{"design, draft"}},
		{"design, de", nil},
		{"x", nil},
	}
	for _, tt := range tests {
		if got := TagSuggestions(tt.value, all); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("TagSuggestions(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}
