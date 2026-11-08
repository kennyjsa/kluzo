package guard

import (
	"path"
	"strings"
)

// Match reports whether the slash-separated name matches the glob. It is
// path.Match with "**" added: a segment that is exactly "**" stands for any
// number of directories (zero included), so ".kluzo/**" matches ".kluzo/a/b"
// and "**/.env" matches ".env" and "web/.env". A pattern without a slash also
// matches the base name at any depth, as .gitignore does ("Makefile", ".env.*").
func Match(pattern, name string) bool {
	pattern, name = strings.TrimPrefix(pattern, "./"), strings.TrimPrefix(name, "./")
	if !strings.Contains(pattern, "/") {
		for _, seg := range strings.Split(name, "/") {
			if ok, _ := path.Match(pattern, seg); ok {
				return true
			}
		}
		return false
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchSegments(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], name[0]); !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// matchAny reports whether the name matches one of the globs.
func matchAny(globs []string, name string) bool {
	for _, g := range globs {
		if Match(g, name) {
			return true
		}
	}
	return false
}
