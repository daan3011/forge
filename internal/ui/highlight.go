package ui

import "strings"

// Highlight renders s in base with each case-insensitive occurrence of q
// in hl.
func Highlight(s, q string, base, hl interface{ Render(...string) string }) string {
	if q == "" {
		return base.Render(s)
	}
	low, lq := strings.ToLower(s), strings.ToLower(q)
	if len(low) != len(s) {
		if strings.Contains(low, lq) {
			return hl.Render(s) // lower-casing moved bytes: mark the line
		}
		return base.Render(s)
	}
	var b strings.Builder
	for {
		i := strings.Index(low, lq)
		if i < 0 {
			break
		}
		if i > 0 {
			b.WriteString(base.Render(s[:i]))
		}
		b.WriteString(hl.Render(s[i : i+len(lq)]))
		s, low = s[i+len(lq):], low[i+len(lq):]
	}
	if s != "" {
		b.WriteString(base.Render(s))
	}
	return b.String()
}
