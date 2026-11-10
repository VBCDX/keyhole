package release

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Known-good vectors from jedisct1/rust-minisign-verify (src/lib.rs tests),
// made by the reference minisign: the same ones the installer's verifier is
// checked against, so this parser is checked against an implementation that
// is not ours.
const (
	refPK        = "RWQf6LRCGA9i53mlYecO4IzT51TGPpvWucNSCh1CBM0QTaLn73Y7GFO3"
	refPrehashed = "untrusted comment: signature from minisign secret key\n" +
		"RUQf6LRCGA9i559r3g7V1qNyJDApGip8MfqcadIgT9CuhV3EMhHoN1mGTkUidF/z7SrlQgXdy8ofjb7bNJJylDOocrCo8KLzZwo=\n" +
		"trusted comment: timestamp:1556193335\tfile:test\n" +
		"y/rUw2y8/hOUYjZU71eHp/Wo1KZ40fGy2VJEDl34XMJM+TX48Ss/17u3IvIfbVR1FkZZSNCisQbuQY+bHwhEBg==\n"
	refLegacy = "untrusted comment: signature from minisign secret key\n" +
		"RWQf6LRCGA9i59SLOFxz6NxvASXDJeRtuZykwQepbDEGt87ig1BNpWaVWuNrm73YiIiJbq71Wi+dP9eKL8OC351vwIasSSbXxwA=\n" +
		"trusted comment: timestamp:1555779966\tfile:test\n" +
		"QtKMXWyYcwdpZAlPF7tE2ENJkRd1ujvKjlj1m9RtHTBnZPa5WKU5uWRs5GoP5M/VqE81QFuMKI5k/SfNQUaOAA==\n"
)

func TestReferenceVectors(t *testing.T) {
	pk, err := ParsePublicKey("untrusted comment: minisign public key E7620F1842B4E81F\n" + refPK + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if pk.IDText() != "E7620F1842B4E81F" || pk.String() != refPK {
		t.Fatalf("parsed %s %s", pk.IDText(), pk.String())
	}
	comment, err := pk.Verify([]byte("test"), []byte(refPrehashed))
	if err != nil || comment != "timestamp:1556193335\tfile:test" {
		t.Fatalf("reference prehashed: %q, %v", comment, err)
	}
	if _, err := pk.Verify([]byte("Test"), []byte(refPrehashed)); err == nil {
		t.Fatal("a changed file verified")
	}
	// A legacy signature (over the raw file) is refused, as the installer refuses it.
	if _, err := pk.Verify([]byte("test"), []byte(refLegacy)); err == nil || !strings.Contains(err.Error(), "prehashed") {
		t.Fatalf("legacy: %v", err)
	}
	// The trusted comment is covered by the global signature.
	tampered := strings.Replace(refPrehashed, "timestamp:1556193335", "timestamp:1556193336", 1)
	if _, err := pk.Verify([]byte("test"), []byte(tampered)); err == nil {
		t.Fatal("a changed trusted comment verified")
	}
}

func TestSemVerIsTheInstallersRule(t *testing.T) {
	for _, ok := range []string{"0.1.0", "1.2.3", "10.20.30", "1.2.3-rc.1", "1.0.0-alpha.0.beta", "2.0.0+build.7", "0.1.0-dev.1", "1.0.0-0a"} {
		if !IsSemVer(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "1", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.3-", "1.2.3-01", "1.2.3+", "1.2.3-a..b", "v1.2.3", "1.2.3-a/b", "../1.2.3", "1.2.3\n", "1.2.3 "} {
		if IsSemVer(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

// dist is a build job's output: the binary and its SHA256SUMS.
func dist(t *testing.T, body string) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), "dist")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, Binary), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	sums := sha256Hex([]byte(body)) + "  ./" + Binary + "\n"
	if err := os.WriteFile(filepath.Join(d, "SHA256SUMS"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}
	return d
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestPackWritesTheInstallersLayout(t *testing.T) {
	d := dist(t, "the keyhole binary")
	out := filepath.Join(t.TempDir(), "npm")
	copies, err := Pack("0.1.0-dev.1", d, out)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(out, "checksums.json"), filepath.Join(out, "@vbcdx/keyhole/checksums.json")}; !slices.Equal(copies, want) {
		t.Fatalf("copies %q", copies)
	}
	a, _ := os.ReadFile(copies[0])
	b, _ := os.ReadFile(copies[1])
	if !bytes.Equal(a, b) {
		t.Fatal("the two checksums.json copies differ")
	}
	hash := sha256Hex([]byte("the keyhole binary"))
	sums := readJSON(t, copies[0])
	if sums["schema"] != float64(1) || sums["package"] != "@vbcdx/keyhole" || sums["version"] != "0.1.0-dev.1" {
		t.Fatalf("checksums.json %v", sums)
	}
	files := sums["files"].(map[string]any)
	if len(files) != len(Platforms) {
		t.Fatalf("files %v", files)
	}
	optional := map[string]any{}
	for _, p := range Platforms {
		name := "@vbcdx/keyhole-" + p.ID
		key := name + "/bin/keyhole"
		if files[key] != hash {
			t.Errorf("%s: %v, want %s", key, files[key], hash)
		}
		fi, err := os.Stat(filepath.Join(out, key))
		if err != nil || fi.Mode().Perm() != 0o755 {
			t.Errorf("%s: %v %v", key, fi, err)
		}
		m := readJSON(t, filepath.Join(out, name, "package.json"))
		if m["name"] != name || m["version"] != "0.1.0-dev.1" ||
			!jsonList(m["os"], p.OS) || !jsonList(m["cpu"], p.CPU) || !jsonList(m["libc"], p.Libc) || !jsonList(m["files"], "bin/") {
			t.Errorf("%s package.json %v", name, m)
		}
		if _, ok := m["scripts"]; ok {
			t.Errorf("%s has scripts", name)
		}
		optional[name] = "0.1.0-dev.1"
	}
	meta := readJSON(t, filepath.Join(out, "@vbcdx/keyhole/package.json"))
	if meta["name"] != "@vbcdx/keyhole" || meta["version"] != "0.1.0-dev.1" || !jsonList(meta["files"], "checksums.json", "checksums.json.minisig") {
		t.Fatalf("meta package.json %v", meta)
	}
	if got, _ := json.Marshal(meta["optionalDependencies"]); string(got) != string(must(json.Marshal(optional))) {
		t.Fatalf("optionalDependencies %s", got)
	}
	// Nothing runs at install time, and nothing is linked onto PATH.
	for _, k := range []string{"scripts", "bin"} {
		if _, ok := meta[k]; ok {
			t.Errorf("the meta package has %q", k)
		}
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

func jsonList(v any, want ...string) bool {
	l, ok := v.([]any)
	if !ok || len(l) != len(want) {
		return false
	}
	for i := range l {
		if l[i] != want[i] {
			return false
		}
	}
	return true
}

func TestPackRefuses(t *testing.T) {
	cases := []struct {
		name    string
		version string
		mutate  func(t *testing.T, dist, out string)
		want    string
	}{
		{"not semver", "1.2", nil, "semantic version"},
		{"a v prefix", "v1.2.3", nil, "semantic version"},
		{"out exists", "1.2.3", func(t *testing.T, _, out string) { mkdir(t, out) }, "already exists"},
		{"an extra file in dist", "1.2.3", func(t *testing.T, d, _ string) { writeFile(t, filepath.Join(d, "extra"), "x") }, "a release is exactly"},
		{"no SHA256SUMS", "1.2.3", func(t *testing.T, d, _ string) { remove(t, filepath.Join(d, "SHA256SUMS")) }, "a release is exactly"},
		{"no binary", "1.2.3", func(t *testing.T, d, _ string) { remove(t, filepath.Join(d, Binary)) }, "a release is exactly"},
		{"the binary changed after its checksum", "1.2.3", func(t *testing.T, d, _ string) { writeFile(t, filepath.Join(d, Binary), "changed") }, "does not match SHA256SUMS"},
		{"SHA256SUMS names another file", "1.2.3", func(t *testing.T, d, _ string) {
			writeFile(t, filepath.Join(d, "SHA256SUMS"), sha256Hex([]byte("b"))+"  ./other\n")
		}, "exactly one file"},
		{"SHA256SUMS lists the binary twice", "1.2.3", func(t *testing.T, d, _ string) {
			l := sha256Hex([]byte("b")) + "  ./keyhole\n"
			writeFile(t, filepath.Join(d, "SHA256SUMS"), l+l)
		}, "exactly one file"},
		{"SHA256SUMS is not a checksum", "1.2.3", func(t *testing.T, d, _ string) { writeFile(t, filepath.Join(d, "SHA256SUMS"), "abc  ./keyhole\n") }, "not `<sha256>"},
		{"SHA256SUMS is empty", "1.2.3", func(t *testing.T, d, _ string) { writeFile(t, filepath.Join(d, "SHA256SUMS"), "\n") }, "lists nothing"},
		{"the binary is a symlink", "1.2.3", func(t *testing.T, d, _ string) {
			target := filepath.Join(filepath.Dir(d), "elsewhere")
			writeFile(t, target, "b")
			remove(t, filepath.Join(d, Binary))
			if err := os.Symlink(target, filepath.Join(d, Binary)); err != nil {
				t.Fatal(err)
			}
		}, "not a regular file"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := dist(t, "b")
			out := filepath.Join(t.TempDir(), "npm")
			if c.mutate != nil {
				c.mutate(t, d, out)
			}
			_, err := Pack(c.version, d, out)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
			// A refusal before writing leaves no tree behind (out exists only when the case made it).
			if c.name != "out exists" {
				if _, err := os.Stat(out); err == nil {
					t.Fatal("a refused pack wrote a tree")
				}
			}
		})
	}
}

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func remove(t *testing.T, p string) {
	t.Helper()
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
}

// packed is a packed tree and a release key with its pinned public file.
func packed(t *testing.T) (copies []string, sk *SecretKey, pub string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "npm")
	copies, err := Pack("0.1.0-dev.1", dist(t, "the keyhole binary"), out)
	if err != nil {
		t.Fatal(err)
	}
	sk, err = GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return copies, sk, sk.Public().File()
}

func keyInput(sk *SecretKey) []byte { return append([]byte("\n"), sk.File()...) }

func TestSignThenVerify(t *testing.T) {
	copies, sk, pub := packed(t)
	comment, err := Sign(copies, keyInput(sk), pub)
	if err != nil {
		t.Fatal(err)
	}
	if comment != "vbcdx-release @vbcdx/keyhole 0.1.0-dev.1" {
		t.Fatalf("comment %q", comment)
	}
	a, _ := os.ReadFile(copies[0] + ".minisig")
	b, _ := os.ReadFile(copies[1] + ".minisig")
	if len(a) == 0 || !bytes.Equal(a, b) {
		t.Fatal("the two signatures differ or are empty")
	}
	if !strings.HasPrefix(string(a), "untrusted comment: "+ReleaseUntrustedComment+"\n") {
		t.Fatalf("signature %q", a)
	}
	// Prehashed: the signature's algorithm bytes are "ED".
	if raw, _ := base64.StdEncoding.DecodeString(strings.Split(string(a), "\n")[1]); string(raw[:2]) != "ED" {
		t.Fatalf("not prehashed: %q", raw[:2])
	}
	root := filepath.Dir(copies[0])
	for _, p := range Platforms {
		if err := Verify(root, pub, "0.1.0-dev.1", p.ID); err != nil {
			t.Fatalf("%s: %v", p.ID, err)
		}
	}
	// The pinned key may also be given as the bare line.
	if err := Verify(root, sk.Public().String(), "0.1.0-dev.1", Platforms[0].ID); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRefuses(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, root string, pub *string, version, platform *string)
		want   string
	}{
		{"another key", func(t *testing.T, _ string, pub *string, _, _ *string) {
			other, _ := GenerateKey()
			*pub = other.Public().String()
		}, "not"},
		{"the same key bytes under another id", func(t *testing.T, _ string, pub *string, _, _ *string) {
			raw, _ := base64.StdEncoding.DecodeString(strings.Split(*pub, "\n")[1])
			raw[2] ^= 0xff
			*pub = base64.StdEncoding.EncodeToString(raw)
		}, "is by key"},
		{"one byte of the binary changed", func(t *testing.T, root string, _ *string, _, _ *string) {
			p := filepath.Join(root, "@vbcdx/keyhole-linux-x64-gnu/bin/keyhole")
			b, _ := os.ReadFile(p)
			b[0] ^= 1
			writeFile(t, p, string(b))
		}, "does not match its signed checksum"},
		{"checksums.json changed", func(t *testing.T, root string, _ *string, _, _ *string) {
			p := filepath.Join(root, "checksums.json")
			b, _ := os.ReadFile(p)
			writeFile(t, p, strings.Replace(string(b), "0.1.0-dev.1", "0.1.0-dev.2", 1))
		}, "does not verify"},
		{"unsigned", func(t *testing.T, root string, _ *string, _, _ *string) {
			remove(t, filepath.Join(root, "checksums.json.minisig"))
		}, "unsigned"},
		{"another version asked for", func(_ *testing.T, _ string, _ *string, v, _ *string) { *v = "0.1.0" }, "not 0.1.0"},
		{"a platform that is not built", func(_ *testing.T, _ string, _ *string, _, p *string) { *p = "linux-arm64-gnu" }, "no checksum"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			copies, sk, pub := packed(t)
			if _, err := Sign(copies, keyInput(sk), pub); err != nil {
				t.Fatal(err)
			}
			root := filepath.Dir(copies[0])
			version, platform := "0.1.0-dev.1", Platforms[0].ID
			c.mutate(t, root, &pub, &version, &platform)
			err := Verify(root, pub, version, platform)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}
}

// A signed checksums.json under another package or with a trusted comment
// that names another version is refused, though the signature itself holds.
func TestVerifyRefusesAReplayedSignature(t *testing.T) {
	copies, sk, pub := packed(t)
	root := filepath.Dir(copies[0])
	data, _ := os.ReadFile(copies[0])
	for _, comment := range []string{"vbcdx-release @vbcdx/keyhole 0.1.0", "vbcdx-release @vbcdx/sidecar 0.1.0-dev.1", "0.1.0-dev.1"} {
		writeFile(t, copies[0]+".minisig", string(sk.Sign(data, comment, "x")))
		if err := Verify(root, pub, "0.1.0-dev.1", Platforms[0].ID); err == nil || !strings.Contains(err.Error(), "trusted comment") {
			t.Errorf("%q: %v", comment, err)
		}
	}
	other := strings.Replace(string(data), `"@vbcdx/keyhole"`, `"@vbcdx/sidecar"`, 1)
	writeFile(t, copies[0], other)
	writeFile(t, copies[0]+".minisig", string(sk.Sign([]byte(other), "vbcdx-release @vbcdx/sidecar 0.1.0-dev.1", "x")))
	if err := Verify(root, pub, "0.1.0-dev.1", Platforms[0].ID); err == nil || !strings.Contains(err.Error(), "not schema 1 for @vbcdx/keyhole") {
		t.Errorf("another package: %v", err)
	}
}

func TestSignRefuses(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, copies []string, sk *SecretKey, input *[]byte, pub *string)
		want   string
	}{
		{"a key that is not the pinned one", func(_ *testing.T, _ []string, _ *SecretKey, input *[]byte, _ *string) {
			other, _ := GenerateKey()
			*input = keyInput(other)
		}, "does not match the pinned public key"},
		{"a password line", func(_ *testing.T, _ []string, sk *SecretKey, input *[]byte, _ *string) {
			*input = append([]byte("hunter2\n"), sk.File()...)
		}, "first line must be empty"},
		{"no empty first line", func(_ *testing.T, _ []string, sk *SecretKey, input *[]byte, _ *string) { *input = sk.File() }, "first line must be empty"},
		{"no key after the empty line", func(_ *testing.T, _ []string, _ *SecretKey, input *[]byte, _ *string) { *input = []byte("\n\n") }, "then the minisign secret key"},
		{"an oversized input", func(_ *testing.T, _ []string, sk *SecretKey, input *[]byte, _ *string) {
			*input = append(keyInput(sk), bytes.Repeat([]byte("x"), KeyInputMax)...)
		}, "over"},
		{"an encrypted key", func(_ *testing.T, _ []string, sk *SecretKey, input *[]byte, _ *string) {
			*input = append([]byte("\n"), editKey(sk, func(raw []byte) { copy(raw[2:4], "Sc") })...)
		}, "encrypted"},
		{"a wrong key checksum", func(_ *testing.T, _ []string, sk *SecretKey, input *[]byte, _ *string) {
			*input = append([]byte("\n"), editKey(sk, func(raw []byte) { raw[130] ^= 1 })...)
		}, "checksum is wrong"},
		{"a corrupt public half", func(_ *testing.T, _ []string, sk *SecretKey, input *[]byte, _ *string) {
			*input = append([]byte("\n"), editKey(sk, func(raw []byte) { raw[100] ^= 1 })...)
		}, "corrupt"},
		{"not a key file", func(_ *testing.T, _ []string, _ *SecretKey, input *[]byte, _ *string) {
			*input = []byte("\nuntrusted comment: x\nnot base64\n")
		}, "not a minisign secret key"},
		{"a pinned key that is not a key", func(_ *testing.T, _ []string, _ *SecretKey, _ *[]byte, pub *string) { *pub = "nonsense" }, "pinned public key"},
		{"the copies differ", func(t *testing.T, copies []string, _ *SecretKey, _ *[]byte, _ *string) { writeFile(t, copies[1], "{}") }, "differs from"},
		{"a binary changed after the pack", func(t *testing.T, copies []string, _ *SecretKey, _ *[]byte, _ *string) {
			writeFile(t, filepath.Join(filepath.Dir(copies[0]), "@vbcdx/keyhole-linux-x64-musl/bin/keyhole"), "changed")
		}, "does not match"},
		{"another package's checksums.json", func(t *testing.T, copies []string, _ *SecretKey, _ *[]byte, _ *string) {
			for _, c := range copies {
				writeFile(t, c, `{"schema":1,"package":"@vbcdx/sidecar","version":"0.1.0","files":{}}`)
			}
		}, "not schema 1 for @vbcdx/keyhole"},
		{"a checksums.json that lists nothing", func(t *testing.T, copies []string, _ *SecretKey, _ *[]byte, _ *string) {
			for _, c := range copies {
				writeFile(t, c, `{"schema":1,"package":"@vbcdx/keyhole","version":"0.1.0","files":{}}`)
			}
		}, "lists no files"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			copies, sk, pub := packed(t)
			input := keyInput(sk)
			c.mutate(t, copies, sk, &input, &pub)
			_, err := Sign(copies, input, pub)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
			// The secret never appears in an error.
			line := strings.Split(string(sk.File()), "\n")[1]
			if strings.Contains(err.Error(), line[:20]) {
				t.Fatal("the error carries the key")
			}
			for _, f := range copies {
				if _, err := os.Stat(f + ".minisig"); err == nil {
					t.Fatalf("a refused sign wrote %s.minisig", f)
				}
			}
		})
	}
}

// editKey returns sk's file with its decoded bytes changed by f.
func editKey(sk *SecretKey, f func(raw []byte)) []byte {
	lines := strings.Split(string(sk.File()), "\n")
	raw, _ := base64.StdEncoding.DecodeString(lines[1])
	f(raw)
	lines[1] = base64.StdEncoding.EncodeToString(raw)
	return []byte(strings.Join(lines, "\n"))
}

// Older `minisign -G -W` writes an all-zero checksum for an unencrypted key;
// that key is still taken (it must still derive the pinned public key).
func TestSignTakesAZeroChecksumKey(t *testing.T) {
	copies, sk, pub := packed(t)
	file := editKey(sk, func(raw []byte) { copy(raw[126:], make([]byte, 32)) })
	if _, err := Sign(copies, append([]byte("\n"), file...), pub); err != nil {
		t.Fatal(err)
	}
	if err := Verify(filepath.Dir(copies[0]), pub, "0.1.0-dev.1", Platforms[1].ID); err != nil {
		t.Fatal(err)
	}
}

func TestSecretKeyFileRoundTrips(t *testing.T) {
	sk, _ := GenerateKey()
	back, err := ParseSecretKey(sk.File())
	if err != nil {
		t.Fatal(err)
	}
	if back.Public() != sk.Public() {
		t.Fatal("the parsed key differs")
	}
	pk, err := ParsePublicKey(sk.Public().File())
	if err != nil || pk != sk.Public() {
		t.Fatalf("public file: %v", err)
	}
}

func TestContractSignUsesAThrowawayKey(t *testing.T) {
	copies, _, _ := packed(t)
	pub, err := ContractSign(copies)
	if err != nil {
		t.Fatal(err)
	}
	sig, _ := os.ReadFile(copies[0] + ".minisig")
	if !strings.HasPrefix(string(sig), "untrusted comment: "+ContractUntrustedComment+"\n") {
		t.Fatalf("signature %q", sig)
	}
	if err := Verify(filepath.Dir(copies[0]), pub, "0.1.0-dev.1", Platforms[0].ID); err != nil {
		t.Fatal(err)
	}
}

// Only Ed25519 keys are taken: a key whose algorithm bytes say otherwise is
// refused, public or secret, even when the rest of it is well formed.
func TestKeysOfAnotherAlgorithmAreRefused(t *testing.T) {
	raw, _ := base64.StdEncoding.DecodeString(refPK)
	copy(raw[:2], "Xx")
	if _, err := ParsePublicKey(base64.StdEncoding.EncodeToString(raw)); err == nil || !strings.Contains(err.Error(), "not an Ed25519") {
		t.Fatalf("public key: %v", err)
	}
	sk, _ := GenerateKey()
	for name, edit := range map[string]func([]byte){
		"signature algorithm": func(raw []byte) { copy(raw[0:2], "Xx") },
		"checksum algorithm":  func(raw []byte) { copy(raw[4:6], "Xx") },
	} {
		if _, err := ParseSecretKey(editKey(sk, edit)); err == nil || !strings.Contains(err.Error(), "not an Ed25519") {
			t.Errorf("secret key %s: %v", name, err)
		}
	}
}
