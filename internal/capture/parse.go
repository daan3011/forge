// Package capture is quick capture (P3.5): a one-line title with inline
// tokens (#project, !priority, @person, +label), parsed, completed and
// resolved into a Plane work item. It has no UI; the ctrl+n overlay and
// `forge capture` both use it.
package capture

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind is a token's kind, named by its sigil.
type Kind int

// Token kinds.
const (
	Project  Kind = iota + 1 // #forge
	Priority                 // !high
	Person                   // @me
	Label                    // +bug
)

// Sigil is the character that starts a token of kind k.
func (k Kind) Sigil() string {
	switch k {
	case Project:
		return "#"
	case Priority:
		return "!"
	case Person:
		return "@"
	case Label:
		return "+"
	}
	return ""
}

// Noun names kind k in messages ("no label …").
func (k Kind) Noun() string {
	switch k {
	case Project:
		return "project"
	case Priority:
		return "priority"
	case Person:
		return "person"
	case Label:
		return "label"
	}
	return "token"
}

func kindOf(r byte) Kind {
	switch r {
	case '#':
		return Project
	case '!':
		return Priority
	case '@':
		return Person
	case '+':
		return Label
	}
	return 0
}

// Token is one inline token. Start and End are byte offsets of the whole
// word (sigil included) in the parsed text.
type Token struct {
	Kind       Kind
	Value      string // without the sigil
	Start, End int
}

// Draft is a parsed capture line.
type Draft struct {
	Title  string // the words that aren't tokens, single-spaced
	Tokens []Token
}

// word is a run of non-space characters at [start, end).
type word struct {
	text       string
	start, end int
}

func words(s string) []word {
	var out []word
	start := -1
	for i, r := range s {
		if unicode.IsSpace(r) {
			if start >= 0 {
				out = append(out, word{s[start:i], start, i})
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, word{s[start:], start, len(s)})
	}
	return out
}

// Parse splits s into its title and tokens. A token is a word that starts
// with a sigil and has more after it; a lone sigil, or a sigil inside a
// word ("C#", "a+b"), is title text. A backslash before a sigil ("\#1")
// keeps the word in the title, without the backslash.
func Parse(s string) Draft {
	var (
		d     Draft
		title []string
	)
	for _, w := range words(s) {
		if k := kindOf(w.text[0]); k != 0 && len(w.text) > 1 {
			d.Tokens = append(d.Tokens, Token{Kind: k, Value: w.text[1:], Start: w.start, End: w.end})
			continue
		}
		if len(w.text) > 1 && w.text[0] == '\\' && kindOf(w.text[1]) != 0 {
			title = append(title, w.text[1:])
			continue
		}
		title = append(title, w.text)
	}
	d.Title = strings.Join(title, " ")
	return d
}

// Of returns the tokens of kind k.
func (d Draft) Of(k Kind) []Token {
	var out []Token
	for _, t := range d.Tokens {
		if t.Kind == k {
			out = append(out, t)
		}
	}
	return out
}

// last is the last token of kind k; ok is false when there is none.
func (d Draft) last(k Kind) (Token, bool) {
	ts := d.Of(k)
	if len(ts) == 0 {
		return Token{}, false
	}
	return ts[len(ts)-1], true
}

// norm folds a name for matching: lower case, with spaces and underscores
// as dashes ("Needs Design" and "needs_design" are both "needs-design").
func norm(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '_' {
			return '-'
		}
		return r
	}, s)
}

// tokenText is name as a token value: spaces become dashes, so it stays
// one word.
func tokenText(name string) string {
	return strings.Join(strings.Fields(name), "-")
}

// runeLen is the number of runes in s.
func runeLen(s string) int { return utf8.RuneCountInString(s) }
