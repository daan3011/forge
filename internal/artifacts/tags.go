package artifacts

import (
	"slices"
	"strings"
)

// ParseTags turns comma separated input into tags: trimmed, without a
// leading '#', lower case, de-duplicated and sorted. Empty pieces are
// dropped.
func ParseTags(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.ToLower(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(p), "#")))
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// JoinTags is the editable form of tags.
func JoinTags(tags []string) string { return strings.Join(tags, ", ") }

// TagSuggestions completes the tag being typed (after the last comma) in
// value from all. Each suggestion is the whole value with that tag filled
// in, since the input matches suggestions against its full text; tags
// already in value are skipped.
func TagSuggestions(value string, all []string) []string {
	head, cur := "", value
	if i := strings.LastIndex(value, ","); i >= 0 {
		head, cur = value[:i+1]+" ", value[i+1:]
	}
	cur = strings.ToLower(strings.TrimLeft(strings.TrimSpace(cur), "#"))
	used := ParseTags(strings.TrimSuffix(head, " "))
	var out []string
	for _, tag := range all {
		if strings.HasPrefix(tag, cur) && !slices.Contains(used, tag) {
			out = append(out, head+tag)
		}
	}
	return out
}
