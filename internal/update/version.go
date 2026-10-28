package update

import (
	"strconv"
	"strings"
)

// Version is a semantic version: MAJOR.MINOR.PATCH[-PRERELEASE][+BUILD].
type Version struct {
	Major, Minor, Patch int
	Pre                 []string // dot-separated prerelease identifiers
}

// Prerelease reports whether v has a prerelease part.
func (v Version) Prerelease() bool { return len(v.Pre) > 0 }

// ParseVersion parses s, allowing a leading "v". Build metadata is ignored.
func ParseVersion(s string) (Version, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	s, _, _ = strings.Cut(s, "+")
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	var n [3]int
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 || p == "" || (len(p) > 1 && p[0] == '0') {
			return Version{}, false
		}
		n[i] = x
	}
	v := Version{Major: n[0], Minor: n[1], Patch: n[2]}
	if hasPre {
		v.Pre = strings.Split(pre, ".")
		for _, id := range v.Pre {
			if id == "" {
				return Version{}, false
			}
		}
	}
	return v, true
}

// Compare orders a and b by semver precedence: -1, 0 or 1.
func (a Version) Compare(b Version) int {
	for _, c := range [3][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if c[0] != c[1] {
			return cmpInt(c[0], c[1])
		}
	}
	// A release outranks its prereleases.
	switch {
	case len(a.Pre) == 0 && len(b.Pre) == 0:
		return 0
	case len(a.Pre) == 0:
		return 1
	case len(b.Pre) == 0:
		return -1
	}
	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		x, y := a.Pre[i], b.Pre[i]
		nx, ex := strconv.Atoi(x)
		ny, ey := strconv.Atoi(y)
		var c int
		switch {
		case ex == nil && ey == nil:
			c = cmpInt(nx, ny)
		case ex == nil: // numeric identifiers sort first
			c = -1
		case ey == nil:
			c = 1
		default:
			c = strings.Compare(x, y)
		}
		if c != 0 {
			return c
		}
	}
	return cmpInt(len(a.Pre), len(b.Pre))
}

// Newer reports whether latest should be offered to a program running
// current: it must be a higher version, and a stable build is never offered
// a prerelease.
func Newer(latest, current string) bool {
	l, ok1 := ParseVersion(latest)
	c, ok2 := ParseVersion(current)
	if !ok1 || !ok2 {
		return false
	}
	if l.Prerelease() && !c.Prerelease() {
		return false
	}
	return l.Compare(c) > 0
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
