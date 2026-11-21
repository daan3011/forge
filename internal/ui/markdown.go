package ui

import (
	"strings"

	"charm.land/glamour/v2"
	glamouransi "charm.land/glamour/v2/ansi"
)

// RenderMarkdown renders each text with Glamour at width. It is slow, so
// call it from a tea.Cmd. Texts Glamour fails on (or blank ones) come back
// empty; the caller then wraps the plain text instead.
func RenderMarkdown(style glamouransi.StyleConfig, width int, texts []string) []string {
	out := make([]string, len(texts))
	r, err := glamour.NewTermRenderer(glamour.WithStyles(style), glamour.WithWordWrap(width))
	if err != nil {
		return out
	}
	for i, s := range texts {
		if strings.TrimSpace(s) == "" {
			continue
		}
		md, err := r.Render(s)
		if err != nil {
			continue
		}
		out[i] = strings.Trim(md, "\n")
	}
	return out
}
