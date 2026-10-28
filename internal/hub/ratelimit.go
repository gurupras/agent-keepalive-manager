package hub

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"sync"
	"time"
)

// rateLimiter allows each sender limit requests per window, as a token
// bucket refilled continuously. Senders are keyed by a keyed hash of their
// address, so the address itself is never kept, even in memory.
type rateLimiter struct {
	limit  float64
	window time.Duration
	secret []byte

	mu      sync.Mutex
	buckets map[[32]byte]*bucket
	lastGC  time.Time
}

type bucket struct {
	tokens float64
	at     time.Time
}

// maxBuckets bounds the limiter's memory. When it is reached, buckets that
// have refilled are swept and, if none has, new senders are refused.
const maxBuckets = 100_000

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic(err)
	}
	return &rateLimiter{
		limit:   float64(limit),
		window:  window,
		secret:  secret,
		buckets: map[[32]byte]*bucket{},
	}
}

func (l *rateLimiter) key(sender string) [32]byte {
	m := hmac.New(sha256.New, l.secret)
	m.Write([]byte(sender))
	var k [32]byte
	copy(k[:], m.Sum(nil))
	return k
}

// allow takes one token from sender's bucket. It reports false when the
// bucket is empty.
func (l *rateLimiter) allow(sender string, now time.Time) bool {
	k := l.key(sender)
	l.mu.Lock()
	defer l.mu.Unlock()
	if gc := now.Sub(l.lastGC); gc > time.Minute || (len(l.buckets) >= maxBuckets && gc > time.Second) {
		l.sweep(now)
	}
	b, ok := l.buckets[k]
	if !ok {
		if len(l.buckets) >= maxBuckets {
			return false
		}
		b = &bucket{tokens: l.limit, at: now}
		l.buckets[k] = b
	}
	l.refill(b, now)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *rateLimiter) refill(b *bucket, now time.Time) {
	if elapsed := now.Sub(b.at); elapsed > 0 {
		b.tokens += l.limit * float64(elapsed) / float64(l.window)
		if b.tokens > l.limit {
			b.tokens = l.limit
		}
	}
	b.at = now
}

// sweep drops buckets that have refilled completely: they behave exactly
// like a sender never seen.
func (l *rateLimiter) sweep(now time.Time) {
	l.lastGC = now
	for k, b := range l.buckets {
		l.refill(b, now)
		if b.tokens >= l.limit {
			delete(l.buckets, k)
		}
	}
}
