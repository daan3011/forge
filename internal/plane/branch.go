package plane

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxBranchLen is the longest branch name BranchFor returns.
const MaxBranchLen = 50

// foldASCII spells common non-ASCII Latin letters in ASCII; other
// non-ASCII runes are dropped by the slug.
var foldASCII = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'ā': "a", 'ą': "a",
	'æ': "ae", 'ç': "c", 'ć': "c", 'č': "c", 'ď': "d", 'đ': "d", 'ð': "d",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ē': "e", 'ę': "e", 'ě': "e",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'ī': "i", 'ı': "i",
	'ł': "l", 'ñ': "n", 'ń': "n", 'ň': "n",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'ō': "o", 'œ': "oe",
	'ř': "r", 'ś': "s", 'š': "s", 'ş': "s", 'ß': "ss", 'ť': "t", 'þ': "th",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ū': "u", 'ů': "u",
	'ý': "y", 'ÿ': "y", 'ź': "z", 'ż': "z", 'ž': "z",
}

// slug lower-cases s to ASCII letters and digits joined by single dashes.
func slug(s string) string {
	var b strings.Builder
	dash := false
	put := func(t string) {
		if dash && b.Len() > 0 {
			b.WriteByte('-')
		}
		dash = false
		b.WriteString(t)
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			put(string(r))
		case foldASCII[r] != "":
			put(foldASCII[r])
		case unicode.Is(unicode.Mn, r):
			// a combining mark (decomposed accent): drop it, keep the word
		default:
			dash = true
		}
	}
	return b.String()
}

// BranchFor is the git branch for working on w (P3.7): the lower-cased
// key and a slug of the title, "proj-123-short-title", ASCII only and at
// most MaxBranchLen bytes, cut at a word boundary where it can be.
func BranchFor(w WorkItem) string {
	key := slug(w.Key())
	if key == "" {
		key = "issue"
	}
	if len(key) >= MaxBranchLen {
		return strings.Trim(key[:MaxBranchLen], "-")
	}
	title := slug(w.Name)
	if title == "" {
		return key
	}
	b := key + "-" + title
	if len(b) <= MaxBranchLen {
		return b
	}
	b = b[:MaxBranchLen]
	if i := strings.LastIndexByte(b, '-'); i > len(key) {
		b = b[:i]
	}
	return strings.TrimRight(b, "-")
}

// FindKey looks for a work item key ("PROJ-123") of one of the project
// identifiers in texts, in order (e.g. a PR's branch, then its title),
// ignoring case. It returns the key upper-cased. Only known identifiers
// match, so "utf-8" or "p3-7" don't.
func FindKey(identifiers []string, texts ...string) (string, bool) {
	re := keyRe(identifiers)
	if re == nil {
		return "", false
	}
	for _, t := range texts {
		if m := re.FindStringSubmatch(t); m != nil {
			return strings.ToUpper(m[1]) + "-" + m[2], true
		}
	}
	return "", false
}

// keyRe matches a key of any identifier, not inside a longer word; nil
// when there are none.
func keyRe(identifiers []string) *regexp.Regexp {
	var alts []string
	for _, id := range identifiers {
		if id = strings.TrimSpace(id); id != "" {
			alts = append(alts, regexp.QuoteMeta(id))
		}
	}
	if len(alts) == 0 {
		return nil
	}
	// Longest first, so PROJX wins over PROJ.
	slices.SortFunc(alts, func(a, b string) int { return len(b) - len(a) })
	return regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(` + strings.Join(alts, "|") + `)-(\d+)(?:[^a-z0-9]|$)`)
}
