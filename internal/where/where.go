// Package where parses and evaluates `--where` expressions that select
// contexts by live attributes: Kubernetes version, reachability and node
// count.
package where

import (
	"fmt"
	"strconv"
	"strings"
)

// Reach is how a filter treats reachability.
type Reach int

const (
	// ReachAny: an unreachable context is a failure like any other.
	ReachAny Reach = iota
	// ReachOnly: unreachable contexts are skipped, not failures.
	ReachOnly
	// UnreachableOnly: only unreachable contexts are selected.
	UnreachableOnly
)

// Attributes are what a probe learned about a context.
type Attributes struct {
	// Version is the server's gitVersion, e.g. v1.29.8-eks-a737599.
	Version string
	Nodes   int
}

// Filter is a parsed set of --where terms; all must match.
type Filter struct {
	reach    Reach
	versions []versionTerm
	nodes    []nodesTerm
}

type versionTerm struct {
	op   string
	want []int
}

type nodesTerm struct {
	op   string
	want int
}

// Ops in match order: two-character operators first.
var ops = []string{"<=", ">=", "!=", "=", "<", ">"}

// Parse parses --where expressions such as `version<1.30`, `nodes>=50`,
// `reachable` and `!reachable`.
func Parse(exprs []string) (Filter, error) {
	var f Filter
	sawReach, sawUnreach := false, false
	for _, raw := range exprs {
		e := strings.ReplaceAll(strings.TrimSpace(raw), " ", "")
		switch e {
		case "reachable":
			sawReach = true
			continue
		case "!reachable":
			sawUnreach = true
			continue
		}
		attr, op, val, ok := split(e)
		if !ok {
			if e == "version" || e == "nodes" {
				return Filter{}, fmt.Errorf("--where %q needs an operator (= != < <= > >=) and a value", raw)
			}
			return Filter{}, fmt.Errorf("--where %q: unknown attribute %q (want version, nodes, reachable or !reachable)", raw, e)
		}
		switch attr {
		case "version":
			v, ok := parseVersion(val)
			if !ok {
				return Filter{}, fmt.Errorf("--where %q: invalid version %q (want major.minor[.patch])", raw, val)
			}
			f.versions = append(f.versions, versionTerm{op: op, want: v})
		case "nodes":
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 {
				return Filter{}, fmt.Errorf("--where %q: invalid node count %q", raw, val)
			}
			f.nodes = append(f.nodes, nodesTerm{op: op, want: n})
		default:
			return Filter{}, fmt.Errorf("--where %q: unknown attribute %q (want version, nodes, reachable or !reachable)", raw, attr)
		}
	}
	switch {
	case sawReach && sawUnreach:
		return Filter{}, fmt.Errorf("--where cannot select both reachable and !reachable")
	case sawUnreach && (len(f.versions) > 0 || len(f.nodes) > 0):
		// An unreachable context has no version or node count to compare.
		return Filter{}, fmt.Errorf("--where !reachable cannot be combined with version or nodes terms")
	case sawReach:
		f.reach = ReachOnly
	case sawUnreach:
		f.reach = UnreachableOnly
	}
	return f, nil
}

func split(e string) (attr, op, val string, ok bool) {
	for i := range e {
		for _, o := range ops {
			if strings.HasPrefix(e[i:], o) {
				return e[:i], o, e[i+len(o):], i > 0 && len(e) > i+len(o)
			}
		}
	}
	return "", "", "", false
}

// Empty reports whether no terms were given.
func (f Filter) Empty() bool {
	return f.reach == ReachAny && len(f.versions) == 0 && len(f.nodes) == 0
}

// NeedsNodes reports whether matching requires a node count.
func (f Filter) NeedsNodes() bool { return len(f.nodes) > 0 }

// Reachability reports how unreachable contexts are treated.
func (f Filter) Reachability() Reach { return f.reach }

// Match reports whether a reachable context's attributes satisfy every
// version and nodes term. An unparsable server version matches no version
// term.
func (f Filter) Match(a Attributes) bool {
	if len(f.versions) > 0 {
		have, ok := parseVersion(serverVersion(a.Version))
		if !ok {
			return false
		}
		for _, t := range f.versions {
			if !compare(cmpVersion(have, t.want), t.op) {
				return false
			}
		}
	}
	for _, t := range f.nodes {
		c := 0
		switch {
		case a.Nodes < t.want:
			c = -1
		case a.Nodes > t.want:
			c = 1
		}
		if !compare(c, t.op) {
			return false
		}
	}
	return true
}

// serverVersion strips the leading v and any vendor suffix:
// v1.29.8-eks-a737599 and v1.30.2+k3s1 become 1.29.8 and 1.30.2.
func serverVersion(git string) string {
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

func compare(c int, op string) bool {
	switch op {
	case "=":
		return c == 0
	case "!=":
		return c != 0
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	case ">=":
		return c >= 0
	}
	return false
}

// CompareServerVersions orders two server gitVersions numerically,
// ignoring vendor suffixes: -1, 0 or 1. An unparsable version sorts below
// any parsable one.
func CompareServerVersions(a, b string) int {
	va, oka := parseVersion(serverVersion(a))
	vb, okb := parseVersion(serverVersion(b))
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
