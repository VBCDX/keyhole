package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VBCDX/keyhole/internal/release"
)

// The whole CLI path a tag run takes: keygen (test key), pack, sign with the
// key on stdin, verify. keygen never overwrites and writes the secret 0600.
func TestPackSignVerify(t *testing.T) {
	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "keyhole"), []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"pack", "1.0.0", dist, filepath.Join(dir, "npm")}, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("packed without SHA256SUMS")
	}
	h := sha256.Sum256([]byte("bin"))
	sum := hex.EncodeToString(h[:])
	if err := os.WriteFile(filepath.Join(dist, "SHA256SUMS"), []byte(sum+"  keyhole\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	npm := filepath.Join(dir, "npm")
	if err := run([]string{"pack", "1.0.0", dist, npm}, nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	sk, pub := filepath.Join(dir, "sk"), filepath.Join(dir, "minisign.pub")
	if err := run([]string{"keygen", "--secret-out", sk, "--public-out", pub}, nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(sk); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("secret key file: %v %v", fi, err)
	}
	if err := run([]string{"keygen", "--secret-out", sk, "--public-out", pub}, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("keygen overwrote an existing secret key")
	}

	key, err := os.ReadFile(sk)
	if err != nil {
		t.Fatal(err)
	}
	copies := []string{filepath.Join(npm, "checksums.json"), filepath.Join(npm, "@vbcdx/keyhole/checksums.json")}
	var out bytes.Buffer
	stdin := bytes.NewReader(append([]byte("\n"), key...))
	if err := run(append([]string{"sign", "--public-key", pub}, copies...), stdin, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "signed: vbcdx-release @vbcdx/keyhole 1.0.0\n" {
		t.Fatalf("sign printed %q", out.String())
	}
	if strings.Contains(out.String(), strings.Split(string(key), "\n")[1][:16]) {
		t.Fatal("sign printed the key")
	}
	out.Reset()
	if err := run([]string{"verify", npm, pub, "1.0.0"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "verified ") != len(release.Platforms) {
		t.Fatalf("verify printed %q", out.String())
	}
	if err := run([]string{"verify", npm, pub, "1.0.1"}, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("verified under another version")
	}
}

// Usage mistakes exit 2 (release.ErrUsage); everything else is an error.
func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"bogus"}, {"pack", "1.0.0"}, {"sign"}, {"sign", "--public-key", "x"}, {"contract-sign", "a"}, {"verify", "a"}, {"keygen", "--secret-out", "a"}} {
		if err := run(args, nil, &bytes.Buffer{}); !errors.Is(err, release.ErrUsage) {
			t.Errorf("%q: %v, want a usage error", args, err)
		}
	}
}
