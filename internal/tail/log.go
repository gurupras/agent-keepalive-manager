package tail

import "github.com/gurupras/agent-keepalive-manager/internal/model"

// DefaultMaxActivities is the history an ActivityLog keeps by default.
const DefaultMaxActivities = 20000

// ActivityLog is the bounded, sequenced activity history of one session,
// shared by the Claude and Codex sources. Add assigns Seq; Publish marks
// what has been added so far as visible to Since, so consumers only see
// what an earlier Scan read (model.Source) even while a background read is
// still adding. Not safe for concurrent use; the owner locks.
type ActivityLog struct {
	max       int
	acts      []model.Activity // oldest first, consecutive Seq
	seq       int64            // Seq of the last activity added
	published int64            // Seq of the last activity Since may return
}

// NewActivityLog returns a log that keeps the newest max activities.
func NewActivityLog(max int) *ActivityLog {
	if max <= 0 {
		max = DefaultMaxActivities
	}
	return &ActivityLog{max: max}
}

// Add appends a with the next Seq and returns that Seq. Seq keeps counting
// across everything, including a re-read after the file was replaced.
func (l *ActivityLog) Add(a model.Activity) int64 {
	l.seq++
	a.Seq = l.seq
	if a.Usage != nil {
		u := *a.Usage
		a.Usage = &u
	}
	l.acts = append(l.acts, a)
	// Trim only once a quarter of headroom has filled, so appends stay
	// amortised O(1).
	if n := len(l.acts); n > l.max+l.max/4 {
		copy(l.acts, l.acts[n-l.max:])
		clear(l.acts[l.max:n])
		l.acts = l.acts[:l.max]
	}
	return l.seq
}

// Get returns the activity with seq for an in-place update (a response
// recorded over several lines), or nil if it was trimmed. The pointer is
// valid until the next Add.
func (l *ActivityLog) Get(seq int64) *model.Activity {
	if len(l.acts) == 0 {
		return nil
	}
	i := seq - l.acts[0].Seq
	if i < 0 || i >= int64(len(l.acts)) {
		return nil
	}
	return &l.acts[i]
}

// Last returns the newest activity, or nil.
func (l *ActivityLog) Last() *model.Activity {
	if len(l.acts) == 0 {
		return nil
	}
	return &l.acts[len(l.acts)-1]
}

// Seq is the Seq of the newest activity (0 when none).
func (l *ActivityLog) Seq() int64 { return l.seq }

// Len is the number of activities held.
func (l *ActivityLog) Len() int { return len(l.acts) }

// Publish makes everything added so far visible to Since.
func (l *ActivityLog) Publish() { l.published = l.seq }

// Since returns copies of the published activities with Seq > after,
// oldest first.
func (l *ActivityLog) Since(after int64) []model.Activity {
	if len(l.acts) == 0 || after >= l.published {
		return nil
	}
	i := max(after+1-l.acts[0].Seq, 0)
	j := l.published - l.acts[0].Seq + 1
	if j > int64(len(l.acts)) {
		j = int64(len(l.acts))
	}
	if i >= j {
		return nil
	}
	out := make([]model.Activity, 0, j-i)
	for _, a := range l.acts[i:j] {
		if a.Usage != nil {
			u := *a.Usage
			a.Usage = &u
		}
		out = append(out, a)
	}
	return out
}
