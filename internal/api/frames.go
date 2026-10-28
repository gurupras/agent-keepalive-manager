package api

import (
	"fmt"
	"regexp"
	"strings"
)

// DefaultFrameAncestors lets only the UI's own origin frame it.
const DefaultFrameAncestors = "'self'"

// Source expressions allowed in frame-ancestors (CSP Level 3): a scheme
// ("https:"), or a host with optional scheme, wildcard subdomain, port
// (or "*") and path.
var (
	schemeSource = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:$`)
	hostSource   = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9+.-]*://)?` +
		`(?:\*|(?:\*\.)?[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*|\[[0-9A-Fa-f:.]+\])` +
		`(?::(?:[0-9]{1,5}|\*))?` +
		`(?:/[A-Za-z0-9._~%!$&()*+=:@/-]*)?$`)
)

// ValidFrameAncestors checks a --frame-ancestors value: space-separated
// CSP source expressions ('self', 'none', scheme:, [scheme://]host[:port]).
// Anything that could end the directive or start another (';', ',',
// quotes other than the two keywords) is refused, since the value is
// written into the Content-Security-Policy header.
func ValidFrameAncestors(v string) error {
	srcs := strings.Fields(v)
	if len(srcs) == 0 {
		return fmt.Errorf("no sources")
	}
	for _, s := range srcs {
		switch {
		case s == "'none'":
			if len(srcs) > 1 {
				return fmt.Errorf("'none' cannot be combined with other sources")
			}
		case s == "'self'":
		case strings.ContainsAny(s, ";,'\"\\"):
			return fmt.Errorf("%q is not a source expression", s)
		case schemeSource.MatchString(s), hostSource.MatchString(s):
		default:
			return fmt.Errorf("%q is not a source expression", s)
		}
	}
	return nil
}
