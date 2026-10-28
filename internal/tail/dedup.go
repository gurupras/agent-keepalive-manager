package tail

// Seen is a bounded map from ids (message ids, response ids) to a value,
// used to count each API call once even when a transcript records it on
// several lines. It forgets the oldest ids once it holds more than its
// capacity: duplicates are written close together, so an id that has aged
// out will not come back. Not safe for concurrent use.
type Seen[V any] struct {
	max   int
	m     map[string]V
	order []string // insertion order, a ring of len max
	next  int
}

// DefaultSeen is the capacity NewSeen uses for a non-positive max.
const DefaultSeen = 50000

// NewSeen returns a Seen that remembers at most max ids.
func NewSeen[V any](max int) *Seen[V] {
	if max <= 0 {
		max = DefaultSeen
	}
	return &Seen[V]{max: max, m: map[string]V{}}
}

// Get returns the value stored for id.
func (s *Seen[V]) Get(id string) (V, bool) {
	v, ok := s.m[id]
	return v, ok
}

// Put stores v for id, evicting the oldest id when full. Updating an id
// that is present does not refresh its age.
func (s *Seen[V]) Put(id string, v V) {
	if _, ok := s.m[id]; ok {
		s.m[id] = v
		return
	}
	if len(s.order) < s.max {
		s.order = append(s.order, id)
	} else {
		delete(s.m, s.order[s.next])
		s.order[s.next] = id
		s.next = (s.next + 1) % s.max
	}
	s.m[id] = v
}

// Add records id and reports whether it was new.
func (s *Seen[V]) Add(id string) bool {
	if _, ok := s.m[id]; ok {
		return false
	}
	var zero V
	s.Put(id, zero)
	return true
}

// Len is the number of ids held.
func (s *Seen[V]) Len() int { return len(s.m) }

// Clear forgets every id.
func (s *Seen[V]) Clear() {
	clear(s.m)
	s.order, s.next = s.order[:0], 0
}
