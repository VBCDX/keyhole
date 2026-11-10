package assertion

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

type vectors struct {
	Now              int64           `json:"now"`
	JWKS             json.RawMessage `json:"jwks"`
	Keyhole          string          `json:"keyhole"`
	Dispatch         string          `json:"dispatch"`
	KeyholeNoSidecar string          `json:"keyhole_no_sidecar"`
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/vbcdx-control-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

var noRefresh Refresh

// TestVerifiesAssertionsIssuedByVbcdxControl checks the Go port against
// assertions the Rust KeySet signed.
func TestVerifiesAssertionsIssuedByVbcdxControl(t *testing.T) {
	vec := loadVectors(t)
	ctx := context.Background()
	v := New("keyhole", vec.JWKS, noRefresh)
	c, err := v.Verify(ctx, vec.Keyhole, vec.Now+1)
	if err != nil {
		t.Fatal(err)
	}
	if c.Sub != "usr_herd790" || c.Kind != "user" || c.Sidecar != "sc_790" || c.Session != "h_790" ||
		c.Org == nil || c.Org.ID != "org_herd" || c.Org.Role == nil || *c.Org.Role != "Owner" ||
		c.LocalPeer == nil || *c.LocalPeer != 1000 || c.JTI != "req_k1" || c.Exp != vec.Now+TTL || c.WS != nil {
		t.Fatalf("claims = %+v", c)
	}
	if _, err := v.Verify(ctx, vec.Keyhole, vec.Now+2); !errors.Is(err, ErrReplayed) {
		t.Fatalf("replay: %v", err)
	}
	if _, err := v.Verify(ctx, vec.Dispatch, vec.Now+1); !errors.Is(err, ErrAudience) {
		t.Fatalf("dispatch's assertion: %v", err)
	}
	c, err = v.Verify(ctx, vec.KeyholeNoSidecar, vec.Now+1)
	if err != nil || c.Sidecar != "" {
		t.Fatalf("no-sidecar assertion = %+v, %v (the verifier passes it; resolve refuses it)", c, err)
	}
	if _, err := New("keyhole", vec.JWKS, noRefresh).Verify(ctx, vec.Keyhole, vec.Now+TTL+Skew); !errors.Is(err, ErrLifetime) {
		t.Fatalf("expired: %v", err)
	}
	if _, err := New("keyhole", vec.JWKS, noRefresh).Verify(ctx, vec.Keyhole, vec.Now+TTL+Skew-1); err != nil {
		t.Fatalf("inside the skew: %v", err)
	}
}

// signer issues assertions the way vbcdx-control does, for refusal cases.
type signer struct {
	kid string
	sk  ed25519.PrivateKey
}

func newSigner(t *testing.T, kid string) signer {
	t.Helper()
	_, sk, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return signer{kid, sk}
}

func (s signer) jwks() []byte {
	b, _ := json.Marshal(map[string]any{"keys": []any{
		map[string]any{"kty": "OKP", "crv": "Ed25519", "alg": "EdDSA", "kid": s.kid, "x": b64.EncodeToString(s.sk.Public().(ed25519.PublicKey))},
	}})
	return b
}

func (s signer) sign(header, payload map[string]any) string {
	h, _ := json.Marshal(header)
	p, _ := json.Marshal(payload)
	in := b64.EncodeToString(h) + "." + b64.EncodeToString(p)
	return in + "." + b64.EncodeToString(ed25519.Sign(s.sk, []byte(in)))
}

const T = 1_800_000_000

func (s signer) header() map[string]any {
	return map[string]any{"alg": "EdDSA", "typ": Typ, "kid": s.kid}
}

func payload(jti string) map[string]any {
	return map[string]any{"iss": Issuer, "aud": "keyhole", "iat": T, "exp": T + TTL, "jti": jti,
		"sub": "usr_1", "kind": "user", "sidecar": "sc_1"}
}

func TestRefusals(t *testing.T) {
	s := newSigner(t, "k1")
	other := newSigner(t, "k1") // same kid, other key
	ctx := context.Background()
	with := func(f func(h, p map[string]any)) string {
		h, p := s.header(), payload("j")
		f(h, p)
		return s.sign(h, p)
	}
	good := s.sign(s.header(), payload("j"))
	parts := strings.Split(good, ".")
	cases := map[string]struct {
		jws  string
		want error
	}{
		"two parts":          {parts[0] + "." + parts[1], ErrMalformed},
		"four parts":         {good + ".x", ErrMalformed},
		"bad base64 header":  {"!." + parts[1] + "." + parts[2], ErrMalformed},
		"bad base64 sig":     {parts[0] + "." + parts[1] + ".!", ErrMalformed},
		"alg none":           {with(func(h, p map[string]any) { h["alg"] = "none" }), ErrAlgorithm},
		"alg case":           {with(func(h, p map[string]any) { delete(h, "alg"); h["ALG"] = "EdDSA" }), ErrAlgorithm},
		"typ":                {with(func(h, p map[string]any) { h["typ"] = "JWT" }), ErrAlgorithm},
		"no kid":             {with(func(h, p map[string]any) { delete(h, "kid") }), ErrMalformed},
		"iss":                {with(func(h, p map[string]any) { p["iss"] = "someone-else" }), ErrMalformed},
		"iss case":           {with(func(h, p map[string]any) { delete(p, "iss"); p["ISS"] = Issuer }), ErrMalformed},
		"aud":                {with(func(h, p map[string]any) { p["aud"] = "dispatch" }), ErrAudience},
		"no aud":             {with(func(h, p map[string]any) { delete(p, "aud") }), ErrAudience},
		"no iat":             {with(func(h, p map[string]any) { delete(p, "iat") }), ErrMalformed},
		"float exp":          {with(func(h, p map[string]any) { p["exp"] = 1.8e9 + 60.5 }), ErrMalformed},
		"too long-lived":     {with(func(h, p map[string]any) { p["exp"] = T + TTL + 1 }), ErrLifetime},
		"from the future":    {with(func(h, p map[string]any) { p["iat"] = T + Skew + 2; p["exp"] = T + Skew + 3 }), ErrLifetime},
		"expired":            {with(func(h, p map[string]any) { p["iat"] = T - 100; p["exp"] = T - Skew }), ErrLifetime},
		"no jti":             {with(func(h, p map[string]any) { delete(p, "jti") }), ErrMalformed},
		"other key":          {other.sign(s.header(), payload("j")), ErrSignature},
		"payload swapped":    {parts[0] + "." + strings.Split(with(func(h, p map[string]any) { p["sub"] = "usr_2" }), ".")[1] + "." + parts[2], ErrSignature},
		"no sub":             {with(func(h, p map[string]any) { delete(p, "sub") }), ErrMalformed},
		"empty kind":         {with(func(h, p map[string]any) { p["kind"] = "" }), ErrMalformed},
		"sidecar not string": {with(func(h, p map[string]any) { p["sidecar"] = 7 }), ErrMalformed},
		"org without id":     {with(func(h, p map[string]any) { p["org"] = map[string]any{"role": "Owner"} }), ErrMalformed},
		"negative peer":      {with(func(h, p map[string]any) { p["local_peer"] = -1 }), ErrMalformed},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			v := New("keyhole", s.jwks(), noRefresh)
			if _, err := v.Verify(ctx, c.jws, T+1); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
	if _, err := New("keyhole", s.jwks(), noRefresh).Verify(ctx, good, T+1); err != nil {
		t.Fatalf("the unmodified assertion: %v", err)
	}
}

func TestUnknownKidRefreshesOnceThenRefuses(t *testing.T) {
	old, rotated := newSigner(t, "old"), newSigner(t, "new")
	ctx := context.Background()
	calls := 0
	serve := old.jwks()
	v := New("keyhole", old.jwks(), func(context.Context) ([]byte, error) { calls++; return serve, nil })

	// A kid the refreshed set doesn't have: one refresh, then refused.
	if _, err := v.Verify(ctx, rotated.sign(rotated.header(), payload("a")), T); !errors.Is(err, ErrUnknownKey) || calls != 1 {
		t.Fatalf("unknown kid: %v after %d refreshes", err, calls)
	}
	// Within MinRefresh another unknown kid doesn't refresh again.
	serve = rotated.jwks()
	if _, err := v.Verify(ctx, rotated.sign(rotated.header(), payload("b")), T+MinRefresh-1); !errors.Is(err, ErrUnknownKey) || calls != 1 {
		t.Fatalf("second unknown kid: %v after %d refreshes", err, calls)
	}
	// After it, the rotated key is fetched and verifies.
	if _, err := v.Verify(ctx, rotated.sign(rotated.header(), payload2("c", T+MinRefresh)), T+MinRefresh); err != nil || calls != 2 {
		t.Fatalf("after the window: %v after %d refreshes", err, calls)
	}
	// The dropped key still verifies until TTL + 2*Skew after the refresh.
	at := int64(T + MinRefresh)
	if _, err := v.Verify(ctx, old.sign(old.header(), payload2("d", at)), at+1); err != nil {
		t.Fatalf("a just-rotated key: %v", err)
	}
	if _, err := v.Verify(ctx, old.sign(old.header(), payload2("e", at+TTL+2*Skew)), at+TTL+2*Skew+1); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("a long-retired key: %v", err)
	}
}

func payload2(jti string, iat int64) map[string]any {
	p := payload(jti)
	p["iat"], p["exp"] = iat, iat+TTL
	return p
}

func TestRefreshFailuresKeepTheOldKeys(t *testing.T) {
	s, stranger := newSigner(t, "s"), newSigner(t, "x")
	ctx := context.Background()
	for name, r := range map[string]Refresh{
		"nil":   nil,
		"error": func(context.Context) ([]byte, error) { return nil, errors.New("down") },
		"empty": func(context.Context) ([]byte, error) { return []byte(`{"keys":[]}`), nil },
		"rsa":   func(context.Context) ([]byte, error) { return []byte(`{"keys":[{"kty":"RSA","kid":"x"}]}`), nil },
		"junk":  func(context.Context) ([]byte, error) { return []byte(`not json`), nil },
	} {
		t.Run(name, func(t *testing.T) {
			v := New("keyhole", s.jwks(), r)
			if _, err := v.Verify(ctx, stranger.sign(stranger.header(), payload("a")), T); !errors.Is(err, ErrKeysUnavailable) {
				t.Fatalf("err = %v, want ErrKeysUnavailable", err)
			}
			if _, err := v.Verify(ctx, s.sign(s.header(), payload("b")), T); err != nil {
				t.Fatalf("the old key stopped verifying: %v", err)
			}
		})
	}
}

func TestSeenJTIsArePruned(t *testing.T) {
	s := newSigner(t, "s")
	ctx := context.Background()
	v := New("keyhole", s.jwks(), noRefresh)
	if _, err := v.Verify(ctx, s.sign(s.header(), payload("a")), T); err != nil {
		t.Fatal(err)
	}
	if v.Remembered() != 1 {
		t.Fatalf("remembered = %d", v.Remembered())
	}
	if _, err := v.Verify(ctx, s.sign(s.header(), payload2("b", T+TTL+Skew)), T+TTL+Skew); err != nil {
		t.Fatal(err)
	}
	if v.Remembered() != 1 {
		t.Fatalf("an expired jti wasn't pruned: %d remembered", v.Remembered())
	}
}
