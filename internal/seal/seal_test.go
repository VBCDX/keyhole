package seal

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"
)

// testDef is a tool definition with every field set.
var testDef = ToolDef{
	Version: 3, BaseURL: "https://llm.example.invalid/v1", TimeoutMs: 60000,
	SlotLocation: "header", SlotField: "Authorization", SlotScheme: "Bearer",
	PrivateAllow: []string{"10.0.0.20/32", "fd00::1/128"},
	Actions:      []Action{{"GET", "/models"}, {"POST", "/chat/completions"}},
}

func recipient(t *testing.T) (*ecdh.PrivateKey, string) {
	t.Helper()
	sk, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return sk, b64.EncodeToString(sk.PublicKey().Bytes())
}

func TestSealOpen(t *testing.T) {
	sk, pub := recipient(t)
	aad := AAD("req_1", "t_litellm", "litellm_key", testDef)
	value := []byte("sk-CANARY-seal-3a7c")
	s, err := To(pub, aad, value)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Open(sk, s, aad)
	if err != nil || !bytes.Equal(got, value) {
		t.Fatalf("Open = %q, %v", got, err)
	}
	raw, _ := json.Marshal(s)
	if bytes.Contains(raw, value) {
		t.Fatal("the sealed form holds the plaintext")
	}
	again, _ := To(pub, aad, value)
	if again.EPK == s.EPK || again.Nonce == s.Nonce || again.CT == s.CT {
		t.Fatal("two seals share an ephemeral key, nonce or ciphertext")
	}
}

func TestOpenRefuses(t *testing.T) {
	sk, pub := recipient(t)
	other, _ := recipient(t)
	aad := AAD("req_1", "t_litellm", "litellm_key", testDef)
	s, err := To(pub, aad, []byte("v"))
	if err != nil {
		t.Fatal(err)
	}
	flip := func(x string) string {
		b, _ := b64.DecodeString(x)
		b[len(b)-1] ^= 1
		return b64.EncodeToString(b)
	}
	cases := map[string]struct {
		sk  *ecdh.PrivateKey
		s   Sealed
		aad []byte
	}{
		"other recipient": {other, s, aad},
		"other jti":       {sk, s, AAD("req_2", "t_litellm", "litellm_key", testDef)},
		"other tool":      {sk, s, AAD("req_1", "t_other", "litellm_key", testDef)},
		"other slot":      {sk, s, AAD("req_1", "t_litellm", "other", testDef)},
		"ct":              {sk, Sealed{s.Alg, s.EPK, s.Nonce, flip(s.CT)}, aad},
		"epk":             {sk, Sealed{s.Alg, flip(s.EPK), s.Nonce, s.CT}, aad},
		"nonce":           {sk, Sealed{s.Alg, s.EPK, flip(s.Nonce), s.CT}, aad},
		"short nonce":     {sk, Sealed{s.Alg, s.EPK, "AAAA", s.CT}, aad},
		"alg":             {sk, Sealed{"none", s.EPK, s.Nonce, s.CT}, aad},
		"bad base64":      {sk, Sealed{s.Alg, "!", s.Nonce, s.CT}, aad},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := Open(c.sk, c.s, c.aad); !errors.Is(err, ErrOpen) || got != nil {
				t.Fatalf("Open = %q, %v; want ErrOpen", got, err)
			}
		})
	}
	if _, err := Open(sk, s, aad); err != nil {
		t.Fatal(err)
	}
}

func TestToRefusesBadKeys(t *testing.T) {
	zero := make([]byte, 32) // a low-order point: the shared secret would be all zero
	for name, k := range map[string]string{
		"not base64": "!",
		"short":      b64.EncodeToString(make([]byte, 31)),
		"long":       b64.EncodeToString(make([]byte, 33)),
		"low order":  b64.EncodeToString(zero),
	} {
		if _, err := To(k, AAD("j", "t", "s", testDef), []byte("v")); !errors.Is(err, ErrKey) {
			t.Errorf("%s: err = %v, want ErrKey", name, err)
		}
	}
}

func TestAADIsUnambiguous(t *testing.T) {
	pairs := [][2][]byte{
		{AAD("j", "a\x00b", "c", testDef), AAD("j", "a", "b\x00c", testDef)},
		{AAD("j", "a\x00\x00\x00\x00b", "c", testDef), AAD("j", "a", "b\x00\x00\x00\x00c", testDef)},
		{AAD("jt", "t", "s", testDef), AAD("j", "tt", "s", testDef)},
		{AAD("j", "t", "s", testDef), AAD("j", "t", "s", withVersion(testDef, 4))},
		{AAD("j", "t", "s", testDef), AAD("j", "t", "s", withBaseURL(testDef, "https://llm.example.invalid/v2"))},
	}
	for i, p := range pairs {
		if bytes.Equal(p[0], p[1]) {
			t.Errorf("pair %d: two different (jti, tool, slot, version, definition) give the same AAD", i)
		}
	}
}

func TestSealWithRefusesABadNonce(t *testing.T) {
	sk, _ := recipient(t)
	esk, _ := ecdh.X25519().GenerateKey(rand.Reader)
	for _, n := range []int{0, 11, 13} {
		if _, err := sealWith(esk, make([]byte, n), sk.PublicKey(), AAD("j", "t", "s", testDef), []byte("v")); err == nil {
			t.Errorf("a %d-byte nonce was accepted", n)
		}
	}
}

// The AAD binds every field of the tool definition the
// sidecar uses for egress, and the version. A value sealed for one
// definition doesn't open under a definition that differs in any of them.
func TestAADBindsEveryToolDefField(t *testing.T) {
	sk, pub := recipient(t)
	base := AAD("req_1", "t_litellm", "litellm_key", testDef)
	s, err := To(pub, base, []byte("v"))
	if err != nil {
		t.Fatal(err)
	}
	with := func(f func(*ToolDef)) ToolDef {
		d := testDef
		d.PrivateAllow = append([]string{}, testDef.PrivateAllow...)
		d.Actions = append([]Action{}, testDef.Actions...)
		f(&d)
		return d
	}
	for name, d := range map[string]ToolDef{
		"version":             with(func(d *ToolDef) { d.Version = 4 }),
		"base_url":            with(func(d *ToolDef) { d.BaseURL = "https://evil.example.invalid/v1" }),
		"timeout_ms":          with(func(d *ToolDef) { d.TimeoutMs = 60001 }),
		"slot location":       with(func(d *ToolDef) { d.SlotLocation = "query" }),
		"slot field":          with(func(d *ToolDef) { d.SlotField = "X-Api-Key" }),
		"slot scheme":         with(func(d *ToolDef) { d.SlotScheme = "" }),
		"private_allow added": with(func(d *ToolDef) { d.PrivateAllow = append(d.PrivateAllow, "10.0.0.0/8") }),
		"private_allow gone":  with(func(d *ToolDef) { d.PrivateAllow = d.PrivateAllow[:1] }),
		"private_allow order": with(func(d *ToolDef) { d.PrivateAllow[0], d.PrivateAllow[1] = d.PrivateAllow[1], d.PrivateAllow[0] }),
		"action method":       with(func(d *ToolDef) { d.Actions[0].Method = "DELETE" }),
		"action path":         with(func(d *ToolDef) { d.Actions[1].Path = "/admin" }),
		"action added":        with(func(d *ToolDef) { d.Actions = append(d.Actions, Action{"GET", "/x"}) }),
		"no actions":          with(func(d *ToolDef) { d.Actions = nil }),
	} {
		aad := AAD("req_1", "t_litellm", "litellm_key", d)
		if bytes.Equal(aad, base) {
			t.Errorf("%s: the AAD didn't change", name)
		}
		if got, err := Open(sk, s, aad); !errors.Is(err, ErrOpen) || got != nil {
			t.Errorf("%s: a value sealed for the original opened (%q, %v)", name, got, err)
		}
	}
	if _, err := Open(sk, s, AAD("req_1", "t_litellm", "litellm_key", with(func(*ToolDef) {}))); err != nil {
		t.Fatalf("the same definition, copied, didn't open: %v", err)
	}
}

// The digest's encoding: entries can't run into each other.
func TestToolDigestIsUnambiguous(t *testing.T) {
	pairs := [][2]ToolDef{
		{{PrivateAllow: []string{"a", "b"}}, {PrivateAllow: []string{"ab"}}},
		{{PrivateAllow: nil}, {PrivateAllow: []string{""}}},
		{{Actions: []Action{{"GE", "T/a"}}}, {Actions: []Action{{"GET", "/a"}}}},
		{{Actions: nil}, {Actions: []Action{{"", ""}}}},
		{{BaseURL: "a", SlotLocation: "b"}, {BaseURL: "ab"}},
		{{TimeoutMs: 1, SlotLocation: "2"}, {TimeoutMs: 12}},
		{{SlotField: "x", PrivateAllow: nil}, {PrivateAllow: []string{"x"}}},
	}
	for i, p := range pairs {
		if ToolDigest(p[0]) == ToolDigest(p[1]) {
			t.Errorf("pair %d: two different definitions give the same digest", i)
		}
	}
	if ToolDigest(testDef) != ToolDigest(testDef) {
		t.Fatal("the digest isn't stable")
	}
}

func withVersion(d ToolDef, v int) ToolDef { d.Version = v; return d }

func withBaseURL(d ToolDef, u string) ToolDef { d.BaseURL = u; return d }
