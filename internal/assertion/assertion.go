// Package assertion verifies the Vbcdx-Assertion that vbcdx-control
// attaches when it forwards a request to Keyhole.
//
// It is a port of vbcdx-control's Rust Verifier (crates/vbcdx-control/
// src/assertion.rs) with the same checks in the same order: a compact
// EdDSA JWS of type vbcdx-assertion+jwt; iss vbcdx-control; aud the
// service; a lifetime of at most 60 s, checked with 5 s of skew; the
// signature against the published keys (an unknown kid refreshes them at
// most once every 30 s, then refuses); and a jti refused if seen again.
// testdata/vbcdx-control-vectors.json holds assertions the Rust KeySet
// issued, so the two can't drift apart unnoticed.
package assertion

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

// The constants vbcdx-control uses (assertion.rs).
const (
	Typ        = "vbcdx-assertion+jwt"
	Issuer     = "vbcdx-control"
	TTL        = 60
	Skew       = 5
	MinRefresh = 30
)

// Why an assertion was refused. Every one is a 401 to the caller; the
// reason is for the service's log.
var (
	ErrMalformed       = errors.New("assertion: malformed")
	ErrAlgorithm       = errors.New("assertion: wrong alg or typ")
	ErrAudience        = errors.New("assertion: not for this service")
	ErrLifetime        = errors.New("assertion: expired, not yet valid, or too long-lived")
	ErrUnknownKey      = errors.New("assertion: unknown signing key")
	ErrKeysUnavailable = errors.New("assertion: the control plane's keys could not be fetched")
	ErrSignature       = errors.New("assertion: bad signature")
	ErrReplayed        = errors.New("assertion: replayed jti")
)

// Claims are what vbcdx-control asserts about the caller.
type Claims struct {
	Sub       string `json:"sub"`
	Kind      string `json:"kind"`
	Org       *Ref   `json:"org,omitempty"`
	WS        *Ref   `json:"ws,omitempty"`
	Sidecar   string `json:"sidecar,omitempty"`
	Session   string `json:"session,omitempty"`
	LocalPeer *int64 `json:"local_peer,omitempty"`
	// JTI and Exp come from the signed payload too.
	JTI string `json:"jti"`
	Exp int64  `json:"exp"`
}

// Ref is an {id, role} claim.
type Ref struct {
	ID   string  `json:"id"`
	Role *string `json:"role"`
}

// Refresh fetches the control plane's JWK Set (GET /control/v1/keys).
type Refresh func(ctx context.Context) ([]byte, error)

// Verifier checks assertions for one audience.
type Verifier struct {
	aud     string
	refresh Refresh

	mu          sync.Mutex
	current     map[string]ed25519.PublicKey
	retired     map[string]retiredKey
	lastRefresh *int64
	seen        map[string]int64
	prunedAt    int64
}

type retiredKey struct {
	pk    ed25519.PublicKey
	until int64
}

var b64 = base64.RawURLEncoding

// ParseJWKS returns the Ed25519 keys of a JWK Set by kid; other entries
// are skipped.
func ParseJWKS(raw []byte) map[string]ed25519.PublicKey {
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Crv string `json:"crv"`
			Kid string `json:"kid"`
			X   string `json:"x"`
		} `json:"keys"`
	}
	out := map[string]ed25519.PublicKey{}
	if json.Unmarshal(raw, &set) != nil {
		return out
	}
	for _, k := range set.Keys {
		if k.Kty != "OKP" || k.Crv != "Ed25519" || k.Kid == "" {
			continue
		}
		pk, err := b64.DecodeString(k.X)
		if err == nil && len(pk) == ed25519.PublicKeySize {
			out[k.Kid] = pk
		}
	}
	return out
}

// New returns a verifier for aud that starts from jwks and calls refresh
// on an unknown kid.
func New(aud string, jwks []byte, refresh Refresh) *Verifier {
	return &Verifier{aud: aud, refresh: refresh, current: ParseJWKS(jwks),
		retired: map[string]retiredKey{}, seen: map[string]int64{}}
}

// Verify checks jws at Unix time now and returns its claims.
func (v *Verifier) Verify(ctx context.Context, jws string, now int64) (Claims, error) {
	parts := strings.Split(jws, ".")
	if len(parts) != 3 {
		return Claims{}, ErrMalformed
	}
	header, err := object(parts[0])
	if err != nil {
		return Claims{}, ErrMalformed
	}
	if alg, typ := str(header, "alg"), str(header, "typ"); alg == nil || *alg != "EdDSA" || typ == nil || *typ != Typ {
		return Claims{}, ErrAlgorithm
	}
	kid := str(header, "kid")
	if kid == nil {
		return Claims{}, ErrMalformed
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil {
		return Claims{}, ErrMalformed
	}
	// The cheap refusals come before any key lookup, so a junk assertion
	// never costs a refresh. They read the bytes the signature covers.
	p, err := object(parts[1])
	if err != nil {
		return Claims{}, ErrMalformed
	}
	if iss := str(p, "iss"); iss == nil || *iss != Issuer {
		return Claims{}, ErrMalformed
	}
	if aud := str(p, "aud"); aud == nil || *aud != v.aud {
		return Claims{}, ErrAudience
	}
	iat, exp := i64(p, "iat"), i64(p, "exp")
	if iat == nil || exp == nil {
		return Claims{}, ErrMalformed
	}
	if *exp-*iat > TTL || *iat > now+Skew || *exp+Skew <= now {
		return Claims{}, ErrLifetime
	}
	jti := str(p, "jti")
	if jti == nil {
		return Claims{}, ErrMalformed
	}
	pk, err := v.keyFor(ctx, *kid, now)
	if err != nil {
		return Claims{}, err
	}
	if !ed25519.Verify(pk, []byte(parts[0]+"."+parts[1]), sig) {
		return Claims{}, ErrSignature
	}
	c, ok := claimsOf(p)
	if !ok {
		return Claims{}, ErrMalformed
	}
	c.JTI, c.Exp = *jti, *exp
	v.mu.Lock()
	defer v.mu.Unlock()
	if now != v.prunedAt {
		for j, exp := range v.seen {
			if exp+Skew <= now {
				delete(v.seen, j)
			}
		}
		v.prunedAt = now
	}
	if _, ok := v.seen[c.JTI]; ok {
		return Claims{}, ErrReplayed
	}
	v.seen[c.JTI] = c.Exp
	return c, nil
}

func (v *Verifier) key(kid string, now int64) (ed25519.PublicKey, bool) {
	if pk, ok := v.current[kid]; ok {
		return pk, true
	}
	if r, ok := v.retired[kid]; ok && now <= r.until {
		return r.pk, true
	}
	return nil, false
}

func (v *Verifier) keyFor(ctx context.Context, kid string, now int64) (ed25519.PublicKey, error) {
	v.mu.Lock()
	pk, ok := v.key(kid, now)
	if ok {
		v.mu.Unlock()
		return pk, nil
	}
	if v.lastRefresh != nil && abs(now-*v.lastRefresh) < MinRefresh {
		v.mu.Unlock()
		return nil, ErrUnknownKey
	}
	t := now
	v.lastRefresh = &t
	v.mu.Unlock()

	if v.refresh == nil {
		return nil, ErrKeysUnavailable
	}
	raw, err := v.refresh(ctx)
	if err != nil {
		return nil, ErrKeysUnavailable
	}
	fresh := ParseJWKS(raw)
	if len(fresh) == 0 {
		return nil, ErrKeysUnavailable // never an empty key set; the old keys stay
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	until := now + TTL + 2*Skew
	for k, old := range v.current {
		if _, still := fresh[k]; !still {
			v.retired[k] = retiredKey{old, until}
		}
	}
	for k, r := range v.retired {
		if now > r.until {
			delete(v.retired, k)
		}
	}
	v.current = fresh
	if pk, ok := v.key(kid, now); ok {
		return pk, nil
	}
	return nil, ErrUnknownKey
}

// Remembered is how many jtis are held (for metrics and tests).
func (v *Verifier) Remembered() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.seen)
}

// object decodes one base64url JSON object. Its keys are matched exactly,
// as serde_json does on the Rust side (encoding/json alone would match
// "ISS" to iss).
func object(s string) (map[string]json.RawMessage, error) {
	b, err := b64.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil || m == nil {
		return nil, ErrMalformed
	}
	return m, nil
}

func str(m map[string]json.RawMessage, k string) *string {
	var s string
	if raw, ok := m[k]; !ok || json.Unmarshal(raw, &s) != nil || string(raw) == "null" {
		return nil
	}
	return &s
}

func i64(m map[string]json.RawMessage, k string) *int64 {
	var n int64
	if raw, ok := m[k]; !ok || string(raw) == "null" || json.Unmarshal(raw, &n) != nil {
		return nil
	}
	return &n
}

// claimsOf reads the claims by exact key: sub and kind are required
// strings, the rest optional, as the Rust Claims struct declares them.
func claimsOf(m map[string]json.RawMessage) (Claims, bool) {
	sub, kind := str(m, "sub"), str(m, "kind")
	if sub == nil || kind == nil || *sub == "" || *kind == "" {
		return Claims{}, false
	}
	c := Claims{Sub: *sub, Kind: *kind}
	for _, f := range []struct {
		key string
		dst **Ref
	}{{"org", &c.Org}, {"ws", &c.WS}} {
		raw, ok := m[f.key]
		if !ok || string(raw) == "null" {
			continue
		}
		r, ok := refOf(raw)
		if !ok {
			return Claims{}, false
		}
		*f.dst = r
	}
	for _, f := range []struct {
		key string
		dst *string
	}{{"sidecar", &c.Sidecar}, {"session", &c.Session}} {
		if raw, ok := m[f.key]; ok && string(raw) != "null" {
			s := str(m, f.key)
			if s == nil {
				return Claims{}, false
			}
			*f.dst = *s
		}
	}
	if raw, ok := m["local_peer"]; ok && string(raw) != "null" {
		n := i64(m, "local_peer")
		if n == nil || *n < 0 || *n > 1<<32-1 {
			return Claims{}, false
		}
		c.LocalPeer = n
	}
	return c, true
}

func refOf(raw json.RawMessage) (*Ref, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, false
	}
	id := str(m, "id")
	if id == nil {
		return nil, false
	}
	r := &Ref{ID: *id}
	if rr, ok := m["role"]; ok && string(rr) != "null" {
		if r.Role = str(m, "role"); r.Role == nil {
			return nil, false
		}
	}
	return r, true
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
