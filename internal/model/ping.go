package model

import (
	"encoding/json"
	"regexp"
	"strconv"
	"time"
)

// PingText is the body of every keepalive prompt. It is fixed: the prompt is
// sent into agents that can run tools, so nothing (API, settings, config)
// may change what it says. It is short because every ping writes it to the
// cache, and it is phrased so a reply is one API call of a few tokens.
const PingText = "Reply only: pong"

var pingRe = regexp.MustCompile(`\[keepalive(?: id=([A-Za-z0-9_-]+))?\]`)

// FormatPing builds the keepalive prompt: "[keepalive id=<id>] <PingText>".
func FormatPing(id string) string {
	return "[keepalive id=" + id + "] " + PingText
}

// ParsePing reports whether s carries a keepalive marker and returns its id
// ("" for a marker without one).
func ParsePing(s string) (id string, ok bool) {
	m := pingRe.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// MarshalJSON writes d as seconds, an integer when whole.
func (d Duration) MarshalJSON() ([]byte, error) {
	s := time.Duration(d).Seconds()
	if s == float64(int64(s)) {
		return []byte(strconv.FormatInt(int64(s), 10)), nil
	}
	return []byte(strconv.FormatFloat(s, 'f', 3, 64)), nil
}

// UnmarshalJSON reads seconds.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s float64
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*d = Duration(s * float64(time.Second))
	return nil
}
