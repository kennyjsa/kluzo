// Package version reads and compares the dotted numeric versions of the CLIs
// Kluzo runs (git, claude), for their minimum versions.
package version

import (
	"regexp"
	"strconv"
	"strings"
)

var pattern = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// Find returns the first version in the output of a --version, or "".
func Find(out string) string { return pattern.FindString(out) }

// Compare compares dotted numeric versions: negative when a < b, zero when
// equal, positive when a > b. A missing part counts as zero.
func Compare(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x - y
		}
	}
	return 0
}
