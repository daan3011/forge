package doctor

import (
	"fmt"
	"io"
)

// Styles colour the report; the zero value prints plain text.
type Styles struct {
	OK, Warn, Fail, Skip, Hint func(string) string
}

func (s Styles) apply(f func(string) string, v string) string {
	if f == nil {
		return v
	}
	return f(v)
}

// Symbol is the status mark.
func (s Status) Symbol() string {
	return [...]string{"✓", "–", "!", "✗"}[s]
}

// Render writes one line per check, an indented fix line under each
// problem, and a summary.
func Render(w io.Writer, cs []Check, st Styles) error {
	width := 0
	for _, c := range cs {
		width = max(width, len(c.Name))
	}
	var n [4]int
	for _, c := range cs {
		n[c.Status]++
		style := [...]func(string) string{st.OK, st.Skip, st.Warn, st.Fail}[c.Status]
		line := fmt.Sprintf("%s %-*s  %s", st.apply(style, c.Status.Symbol()), width, c.Name, c.Detail)
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
		if c.Fix != "" {
			if _, err := fmt.Fprintf(w, "  %*s  %s\n", width, "", st.apply(st.Hint, "fix: "+c.Fix)); err != nil {
				return err
			}
		}
	}
	summary := fmt.Sprintf("\n%d ok, %d warning(s), %d failed", n[OK], n[Warn], n[Fail])
	if n[Skip] > 0 {
		summary += fmt.Sprintf(", %d skipped", n[Skip])
	}
	switch {
	case n[Fail] > 0:
		summary = st.apply(st.Fail, summary)
	case n[Warn] > 0:
		summary = st.apply(st.Warn, summary)
	default:
		summary = st.apply(st.OK, summary+": the forge is hot")
	}
	_, err := fmt.Fprintln(w, summary)
	return err
}
