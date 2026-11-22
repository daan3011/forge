package artifacts

import (
	"path"
	"strings"
)

// Match reports whether name matches pattern. Both use forward slashes.
// "**" as a whole segment matches zero or more segments; every other
// segment is matched with path.Match. A malformed pattern matches nothing.
func Match(pattern, name string) bool {
	return matchSegs(splitSegs(pattern), splitSegs(name))
}

func splitSegs(s string) []string {
	s = strings.Trim(s, "/")
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

func matchSegs(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// Collapse repeated "**" and try every split point.
			for len(pat) > 0 && pat[0] == "**" {
				pat = pat[1:]
			}
			if len(pat) == 0 {
				return true
			}
			for i := range name {
				if matchSegs(pat, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], name[0])
		if err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// Root returns the folder part of pattern before its first wildcard
// segment: "/a/b/**/*.html" gives "/a/b", "design/**" gives "design", and
// "*.png" gives "".
func Root(pattern string) string {
	segs := strings.Split(pattern, "/")
	var keep []string
	for i, s := range segs {
		if strings.ContainsAny(s, `*?[\`) || i == len(segs)-1 {
			break
		}
		keep = append(keep, s)
	}
	root := strings.Join(keep, "/")
	if root == "" && strings.HasPrefix(pattern, "/") {
		return "/"
	}
	return root
}
