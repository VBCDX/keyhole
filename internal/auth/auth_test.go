package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected hash format %q", h)
	}
	if ok, err := VerifyPassword(h, "correct horse battery"); !ok || err != nil {
		t.Fatalf("right password: ok=%v err=%v", ok, err)
	}
	if ok, err := VerifyPassword(h, "wrong horse battery"); ok || err != nil {
		t.Fatalf("wrong password: ok=%v err=%v", ok, err)
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Fatal("two hashes of one password must differ (random salt)")
	}
}

func TestVerifyRejectsMalformedOrHostileHashes(t *testing.T) {
	for _, h := range []string{
		"",
		"plaintext",
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHQ$aGFzaA",
		"$argon2id$v=18$m=19456,t=2,p=1$c2FsdHNhbHQ$aGFzaA",
		"$argon2id$v=19$m=99999999,t=2,p=1$c2FsdHNhbHQ$aGFzaA", // memory bomb
		"$argon2id$v=19$m=19456,t=999,p=1$c2FsdHNhbHQ$aGFzaA",  // time bomb
		"$argon2id$v=19$m=19456,t=2,p=1$!!!$aGFzaA",
	} {
		if ok, err := VerifyPassword(h, "x"); ok || err == nil {
			t.Errorf("VerifyPassword(%q) = %v, %v; want false, error", h, ok, err)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if ValidatePassword("short") == nil {
		t.Error("short password accepted")
	}
	if ValidatePassword(strings.Repeat("a", MaxPasswordLen+1)) == nil {
		t.Error("over-long password accepted")
	}
	if err := ValidatePassword("twelve chars"); err != nil {
		t.Errorf("12-character password rejected: %v", err)
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewLimiter(3, time.Minute)
	l.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if ok, _ := l.Allowed("k"); !ok {
			t.Fatalf("attempt %d refused before the limit", i)
		}
		l.Fail("k")
	}
	ok, wait := l.Allowed("k")
	if ok || wait != time.Minute {
		t.Fatalf("after 3 failures: ok=%v wait=%v; want refused for 1m", ok, wait)
	}
	if ok, _ := l.Allowed("other"); !ok {
		t.Fatal("limits must be per key")
	}

	now = now.Add(time.Minute)
	if ok, _ := l.Allowed("k"); !ok {
		t.Fatal("still refused after the window ended")
	}
	l.Fail("k")
	l.Reset("k")
	if ok, _ := l.Allowed("k"); !ok {
		t.Fatal("Reset did not clear the key")
	}

	l.Fail("stale")
	now = now.Add(2 * time.Minute)
	l.Sweep()
	if len(l.hits) != 0 {
		t.Fatalf("Sweep left %d expired buckets", len(l.hits))
	}
}

func TestBackoff(t *testing.T) {
	now := time.Unix(1000, 0)
	b := NewBackoff(2, time.Second, 5*time.Second, time.Minute)
	b.now = func() time.Time { return now }

	for i, want := range []time.Duration{0, 0, time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second} {
		if got := b.Delay("k"); got != want {
			t.Fatalf("after %d failures: delay %v, want %v", i, got, want)
		}
		b.Fail("k")
	}
	for i := 0; i < 100; i++ {
		b.Fail("k")
	}
	if got := b.Delay("k"); got != 5*time.Second {
		t.Fatalf("after many failures: delay %v, want the 5s cap", got)
	}
	if got := b.Delay("other"); got != 0 {
		t.Fatalf("delays must be per key, got %v", got)
	}

	// Failures are forgotten a window after the last one, not the first.
	now = now.Add(59 * time.Second)
	if b.Delay("k") == 0 {
		t.Fatal("forgotten before the window ended")
	}
	b.Fail("k")
	now = now.Add(59 * time.Second)
	if b.Delay("k") == 0 {
		t.Fatal("a new failure must extend the window")
	}
	now = now.Add(time.Second)
	if got := b.Delay("k"); got != 0 {
		t.Fatalf("delay %v a window after the last failure, want 0", got)
	}

	b.Fail("k")
	b.Fail("k")
	b.Fail("k")
	b.Reset("k")
	if got := b.Delay("k"); got != 0 {
		t.Fatalf("Reset did not clear the key: delay %v", got)
	}

	b.Fail("stale")
	now = now.Add(2 * time.Minute)
	b.Sweep()
	if len(b.hits) != 0 {
		t.Fatalf("Sweep left %d expired entries", len(b.hits))
	}
}

func TestSealer(t *testing.T) {
	s, err := NewSealer("a-secret-that-is-long-enough-for-the-check")
	if err != nil {
		t.Fatal(err)
	}
	sealed := s.Seal("p", []byte("hello"))
	if got, err := s.Open("p", sealed); err != nil || string(got) != "hello" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	if _, err := s.Open("other-purpose", sealed); err == nil {
		t.Fatal("a value sealed for one purpose opened for another")
	}
	tampered := []byte(sealed)
	tampered[len(tampered)-2] ^= 1
	if _, err := s.Open("p", string(tampered)); err == nil {
		t.Fatal("tampered value opened")
	}
	other, _ := NewSealer("a-different-secret-that-is-long-enough!!")
	if _, err := other.Open("p", sealed); err == nil {
		t.Fatal("value opened with a different key")
	}
	random, _ := NewSealer("")
	if got, err := random.Open("p", random.Seal("p", []byte("x"))); err != nil || string(got) != "x" {
		t.Fatalf("random-key sealer round trip = %q, %v", got, err)
	}
}

func TestWaiting(t *testing.T) {
	w := NewWaiting(2, 3)
	if !w.Enter("a") || !w.Enter("a") {
		t.Fatal("the first two places for a key must be free")
	}
	if w.Enter("a") {
		t.Fatal("a third place for the same key was granted")
	}
	if !w.Enter("b") {
		t.Fatal("another key must get a place while the total allows it")
	}
	if w.Enter("c") {
		t.Fatal("a place was granted past the total")
	}
	w.Leave("a")
	if !w.Enter("c") {
		t.Fatal("a freed place must be reusable by any key")
	}
	w.Leave("a")
	w.Leave("b")
	w.Leave("c")
	if w.total != 0 || len(w.byKey) != 0 {
		t.Fatalf("after every Leave: total %d, keys %v", w.total, w.byKey)
	}
}
