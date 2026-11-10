package envelope

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func newKEK(t *testing.T) (*KEK, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "kek")
	if err := GenerateKEK(p); err != nil {
		t.Fatal(err)
	}
	k, err := LoadKEK(p)
	if err != nil {
		t.Fatal(err)
	}
	return k, p
}

func TestSealOpenRoundTrip(t *testing.T) {
	k, _ := newKEK(t)
	aad := AAD("org_a", "k_1", 1)
	value := []byte("sk-canary-0123456789")
	e, err := k.Seal(aad, value)
	if err != nil {
		t.Fatal(err)
	}
	got, err := k.Open(e, aad)
	if err != nil || !bytes.Equal(got, value) {
		t.Fatalf("Open = %q, %v; want the sealed value", got, err)
	}
	if e.Alg != Alg || e.DEK.KMSKeyRef != k.Ref() || !strings.HasPrefix(k.Ref(), "file:") {
		t.Fatalf("envelope header = %+v", e)
	}
	raw, _ := json.Marshal(e)
	if bytes.Contains(raw, value) {
		t.Fatal("the stored envelope contains the plaintext")
	}
	again, _ := k.Seal(aad, value)
	if again.Nonce == e.Nonce || again.DEK.Wrapped == e.DEK.Wrapped || again.Ciphertext == e.Ciphertext {
		t.Fatal("two seals of the same value share a nonce, DEK or ciphertext")
	}
}

func TestOpenRefusesEveryMismatch(t *testing.T) {
	k, _ := newKEK(t)
	other, _ := newKEK(t)
	aad := AAD("org_a", "k_1", 1)
	e, err := k.Seal(aad, []byte("v"))
	if err != nil {
		t.Fatal(err)
	}
	flip := func(s string) string {
		b, _ := b64.DecodeString(s)
		b[len(b)-1] ^= 1
		return b64.EncodeToString(b)
	}
	cases := map[string]struct {
		k   *KEK
		e   func(Envelope) Envelope
		aad []byte
	}{
		"other KEK":         {other, func(e Envelope) Envelope { e.DEK.KMSKeyRef = other.Ref(); return e }, aad},
		"other key id":      {k, nil, AAD("org_a", "k_2", 1)},
		"other org":         {k, nil, AAD("org_b", "k_1", 1)},
		"other version":     {k, nil, AAD("org_a", "k_1", 2)},
		"stored aad edited": {k, func(e Envelope) Envelope { e.AAD = b64.EncodeToString(AAD("org_a", "k_2", 1)); return e }, AAD("org_a", "k_2", 1)},
		"ciphertext":        {k, func(e Envelope) Envelope { e.Ciphertext = flip(e.Ciphertext); return e }, aad},
		"wrapped dek":       {k, func(e Envelope) Envelope { e.DEK.Wrapped = flip(e.DEK.Wrapped); return e }, aad},
		"nonce":             {k, func(e Envelope) Envelope { e.Nonce = flip(e.Nonce); return e }, aad},
		"alg":               {k, func(e Envelope) Envelope { e.Alg = "none"; return e }, aad},
		"kek ref":           {k, func(e Envelope) Envelope { e.DEK.KMSKeyRef = "file:0000"; return e }, aad},
		"short wrapped":     {k, func(e Envelope) Envelope { e.DEK.Wrapped = "AA"; return e }, aad},
		"bad base64":        {k, func(e Envelope) Envelope { e.Ciphertext = "!"; return e }, aad},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			in := e
			if c.e != nil {
				in = c.e(e)
			}
			if got, err := c.k.Open(in, c.aad); !errors.Is(err, ErrOpen) || got != nil {
				t.Fatalf("Open = %q, %v; want nil, ErrOpen", got, err)
			}
		})
	}
	// The unmodified envelope still opens, so each case above failed for its own reason.
	if _, err := k.Open(e, aad); err != nil {
		t.Fatal(err)
	}
}

func TestSealBounds(t *testing.T) {
	k, _ := newKEK(t)
	for _, n := range []int{0, MaxValue + 1} {
		if _, err := k.Seal(AAD("o", "k", 1), make([]byte, n)); err == nil {
			t.Errorf("Seal(%d bytes) succeeded", n)
		}
	}
	if _, err := k.Seal(AAD("o", "k", 1), make([]byte, MaxValue)); err != nil {
		t.Errorf("Seal(MaxValue) = %v", err)
	}
}

func TestGenerateKEK(t *testing.T) {
	_, p := newKEK(t)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("KEK file mode = %04o, want 0600", fi.Mode().Perm())
	}
	if err := GenerateKEK(p); err == nil {
		t.Fatal("GenerateKEK overwrote an existing file")
	}
	raw, _ := os.ReadFile(p)
	key, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(string(raw), "\n"))
	if err != nil || len(key) != kekLen {
		t.Fatalf("KEK file content: %d bytes, %v", len(key), err)
	}
	k, _ := LoadKEK(p)
	if strings.Contains(k.Ref(), strings.TrimSuffix(string(raw), "\n")) {
		t.Fatal("the KEK ref reveals the key")
	}
}

func TestLoadKEKRefuses(t *testing.T) {
	dir := t.TempDir()
	_, good := newKEK(t)
	write := func(name string, mode os.FileMode, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	raw, _ := os.ReadFile(good)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"group readable": write("g", 0o640, string(raw)),
		"other readable": write("o", 0o604, string(raw)),
		"symlink":        link,
		"directory":      dir,
		"missing":        filepath.Join(dir, "nope"),
		"not base64":     write("b", 0o600, "not base64!\n"),
		"short key":      write("s", 0o600, base64.StdEncoding.EncodeToString(make([]byte, 16))+"\n"),
		"two newlines":   write("n", 0o600, string(raw)+"\n"),
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			if k, err := LoadKEK(p); err == nil || k != nil {
				t.Fatalf("LoadKEK(%s) = %v, %v; want a refusal", name, k, err)
			}
		})
	}
	// A FIFO passes the mode and owner checks, and reading it would block for
	// ever; the regular-file check refuses it before any read.
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := LoadKEK(fifo); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("a FIFO: %v; want it refused as not a regular file", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LoadKEK blocked reading a FIFO")
	}
	// The owner rule, as any user: Keyhole runs as someone other than the
	// file's owner.
	geteuid = func() int { return os.Geteuid() + 1 }
	k, err := LoadKEK(write("foreign", 0o600, string(raw)))
	geteuid = os.Geteuid
	if err == nil || k != nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("a KEK file owned by another user: %v, %v", k, err)
	}
	if k, err := LoadKEK(write("big", 0o600, string(raw)+strings.Repeat("A", maxKEKFile))); err == nil || k != nil ||
		!strings.Contains(err.Error(), "over") {
		t.Fatalf("an oversized KEK file: %v, %v", k, err)
	}
	if _, err := LoadKEK(dir); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("a directory: %v; want it refused as not a regular file", err)
	}
	if _, err := LoadKEK(write("ok", 0o400, string(raw))); err != nil {
		t.Fatalf("a 0400 KEK file was refused: %v", err)
	}
}

func TestGenerateKEKRemovesAPartialFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "kek")
	writeKEK = func(w io.Writer, s string) (int, error) {
		n, _ := io.WriteString(w, s[:5])
		return n, errors.New("disk full")
	}
	err := GenerateKEK(p)
	writeKEK = io.WriteString
	if err == nil {
		t.Fatal("a failed write reported success")
	}
	if _, serr := os.Lstat(p); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("a partial KEK file was left behind: %v", serr)
	}
	if err := GenerateKEK(p); err != nil {
		t.Fatalf("the rerun after a failed write: %v", err)
	}
}

func TestAADIsUnambiguous(t *testing.T) {
	pairs := [][2][]byte{
		{AAD("a\x00b", "c", 1), AAD("a", "b\x00c", 1)},
		// Collides under any fixed separator, even four NUL bytes: only the lengths tell them apart.
		{AAD("a\x00\x00\x00\x00b", "c", 1), AAD("a", "b\x00\x00\x00\x00c", 1)},
		{AAD("ab", "c", 1), AAD("a", "bc", 1)},
		{AAD("o", "k1", 1), AAD("o", "k", 11)},
	}
	for i, p := range pairs {
		if bytes.Equal(p[0], p[1]) {
			t.Errorf("pair %d: two different (org, key, version) give the same AAD", i)
		}
	}
}
