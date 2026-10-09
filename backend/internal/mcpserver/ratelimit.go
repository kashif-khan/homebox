package mcpserver

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// limiter is a fixed-window request counter keyed by credential. It protects the
// database from a runaway agent loop; it is not a security boundary, so the
// simplicity of a fixed window is fine and the memory is bounded by sweeping
// idle entries.
type limiter struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	buckets map[uuid.UUID]*bucket
	swept   time.Time
}

type bucket struct {
	start time.Time
	count int
}

func newLimiter(perMinute int) *limiter {
	if perMinute <= 0 {
		return nil
	}
	return &limiter{limit: perMinute, window: time.Minute, now: time.Now, buckets: map[uuid.UUID]*bucket{}}
}

// allow reports whether the credential may make another request, and when the
// current window ends.
func (l *limiter) allow(id uuid.UUID) (ok bool, retryAfter time.Duration) {
	if l == nil {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweep(now)

	b := l.buckets[id]
	if b == nil || now.Sub(b.start) >= l.window {
		b = &bucket{start: now}
		l.buckets[id] = b
	}
	if b.count >= l.limit {
		return false, b.start.Add(l.window).Sub(now)
	}
	b.count++
	return true, 0
}

func (l *limiter) sweep(now time.Time) {
	if now.Sub(l.swept) < l.window {
		return
	}
	l.swept = now
	for id, b := range l.buckets {
		if now.Sub(b.start) >= l.window {
			delete(l.buckets, id)
		}
	}
}
