package auth

import (
	"sync"
	"time"
)

// Limiter counts failed sign-ins per key in a fixed window. Once a key
// reaches its limit, further attempts are refused until the window that
// began with the first failure ends. State is in process memory; a
// multi-node deployment moves it to the shared cache later.
type Limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	hits   map[string]*bucket
}

type bucket struct {
	count int
	reset time.Time
}

// NewLimiter allows max failures per key within window.
func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, now: time.Now, hits: map[string]*bucket{}}
}

// Allowed reports whether key may attempt a sign-in, and if not, how long
// until it may.
func (l *Limiter) Allowed(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.hits[key]
	now := l.now()
	if b == nil || !now.Before(b.reset) {
		return true, 0
	}
	if b.count >= l.max {
		return false, b.reset.Sub(now)
	}
	return true, 0
}

// Fail records a failed attempt for key.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.hits[key]
	if b == nil || !now.Before(b.reset) {
		b = &bucket{reset: now.Add(l.window)}
		l.hits[key] = b
	}
	b.count++
}

// Reset clears key after a successful sign-in.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}

// Sweep drops expired buckets so memory stays bounded.
func (l *Limiter) Sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for k, b := range l.hits {
		if !now.Before(b.reset) {
			delete(l.hits, k)
		}
	}
}

// Backoff slows attempts on a key after repeated failures instead of
// refusing them. Past the free failures, each attempt waits a delay that
// doubles per further failure, up to a cap. Nobody can lock an account
// owner out of their correct password this way; the most a guesser costs
// them is one wait of at most max. Failures are forgotten window after the
// last one. State is in process memory, like Limiter.
type Backoff struct {
	mu        sync.Mutex
	free      int
	base, max time.Duration
	window    time.Duration
	now       func() time.Time
	hits      map[string]*bucket
}

// NewBackoff lets free failures per key through undelayed, then delays
// attempts by base, doubling per further failure up to max.
func NewBackoff(free int, base, max, window time.Duration) *Backoff {
	return &Backoff{free: free, base: base, max: max, window: window, now: time.Now, hits: map[string]*bucket{}}
}

// Delay is how long an attempt on key must wait before it is checked.
func (b *Backoff) Delay(key string) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.hits[key]
	if h == nil || !b.now().Before(h.reset) || h.count < b.free {
		return 0
	}
	n := h.count - b.free
	if n >= 30 {
		return b.max
	}
	return min(b.max, b.base<<n)
}

// Fail records a failed attempt for key.
func (b *Backoff) Fail(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	h := b.hits[key]
	if h == nil || !now.Before(h.reset) {
		h = &bucket{}
		b.hits[key] = h
	}
	h.count++
	h.reset = now.Add(b.window)
}

// Reset clears key after a successful sign-in.
func (b *Backoff) Reset(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.hits, key)
}

// Sweep drops expired entries so memory stays bounded.
func (b *Backoff) Sweep() {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	for k, h := range b.hits {
		if !now.Before(h.reset) {
			delete(b.hits, k)
		}
	}
}

// Waiting bounds how many slowed-down attempts may wait at once, per key
// and in all, so the slow-down cannot be used to hold connections open.
type Waiting struct {
	mu          sync.Mutex
	perKey, max int
	total       int
	byKey       map[string]int
}

// NewWaiting allows perKey waiting attempts per key and max in all.
func NewWaiting(perKey, max int) *Waiting {
	return &Waiting{perKey: perKey, max: max, byKey: map[string]int{}}
}

// Enter claims a place for key. It reports false, without claiming, when
// key or the whole server already has as many waiting as allowed. Every
// successful Enter must be followed by Leave.
func (w *Waiting) Enter(key string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.total >= w.max || w.byKey[key] >= w.perKey {
		return false
	}
	w.byKey[key]++
	w.total++
	return true
}

// Leave gives back a place claimed by Enter.
func (w *Waiting) Leave(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.byKey[key]--; w.byKey[key] <= 0 {
		delete(w.byKey, key)
	}
	w.total--
}
