// Package versions parses and compares Kubernetes server versions, ignoring
// vendor suffixes such as -eks-... and +k3s1.
package versions

import (
	"strconv"
	"strings"
)

// ShortVersion strips the leading v and any vendor suffix:
// v1.29.8-eks-a737599 and v1.30.2+k3s1 become 1.29.8 and 1.30.2.
func ShortVersion(git string) string {
	s := strings.TrimPrefix(git, "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	return s
}

func parseVersion(s string) ([]int, bool) {
	parts := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(parts) < 2 || len(parts) > 3 {
		return nil, false
	}
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// cmpVersion compares have with want at want's precision, so 1.30.4
// equals 1.30.
func cmpVersion(have, want []int) int {
	for i := range want {
		h := 0
		if i < len(have) {
			h = have[i]
		}
		switch {
		case h < want[i]:
			return -1
		case h > want[i]:
			return 1
		}
	}
	return 0
}

// CompareServerVersions orders two server gitVersions numerically,
// ignoring vendor suffixes: -1, 0 or 1. An unparsable version sorts below
// any parsable one.
func CompareServerVersions(a, b string) int {
	va, oka := parseVersion(ShortVersion(a))
	vb, okb := parseVersion(ShortVersion(b))
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	}
	for len(va) < 3 {
		va = append(va, 0)
	}
	for len(vb) < 3 {
		vb = append(vb, 0)
	}
	return cmpVersion(va, vb)
}

// ValidVersion reports whether want is a version MatchVersion accepts: an
// optional v and two or three numeric parts (1.30, v1.30.4).
func ValidVersion(want string) bool {
	_, ok := parseVersion(want)
	return ok
}

// MatchVersion reports whether a server gitVersion equals want at want's
// precision, ignoring vendor suffixes: v1.30.4-eks-x matches 1.30 and
// 1.30.4, not 1.30.5.
func MatchVersion(git, want string) bool {
	have, ok := parseVersion(ShortVersion(git))
	w, okw := parseVersion(want)
	return ok && okw && cmpVersion(have, w) == 0
}
