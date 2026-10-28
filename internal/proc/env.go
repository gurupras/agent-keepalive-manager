package proc

import "strings"

// lookupEnv finds name in an environment block whose entries are separated
// by sep ("NAME=value<sep>NAME=value...").
func lookupEnv(block, sep, name string) (string, bool) {
	prefix := name + "="
	for kv := range strings.SplitSeq(block, sep) {
		if strings.HasPrefix(kv, prefix) {
			return kv[len(prefix):], true
		}
	}
	return "", false
}
