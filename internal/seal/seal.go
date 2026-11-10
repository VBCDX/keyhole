// Package seal encrypts a resolved value to the sidecar that asked for it.
// docs/resolve.md is the specification the
// sidecar side (vbcdx-core, P4) implements.
//
// The sidecar sends a fresh X25519 public key with each request. Keyhole
// makes its own fresh X25519 key, derives an AES-256-GCM key with
// HKDF-SHA256 over the shared secret, and encrypts the value with additional
// data that names the request (its jti), the tool, the slot, the tool
// version and a digest of the tool definition the reply carries: a value opens only for the destination it was granted for.
// Only the holder of the request's private key can open it; the control
// plane that relays the reply sees ciphertext.
package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strconv"
)

// Alg names the scheme in a sealed reply.
const Alg = "X25519-HKDF-SHA256-A256GCM"

// Info is the HKDF info string, and the first part of the additional data.
// v2 binds the tool definition. v1 is gone: a sidecar that
// builds v1 additional data can't open a v2 reply (one error, no value).
const Info = "vbcdx-keyhole-seal-v2"

// ToolDefTag is the first part of the tool-definition digest's input.
const ToolDefTag = "vbcdx-keyhole-tooldef-v1"

// ErrKey is returned for a recipient key that isn't a usable X25519 key.
var ErrKey = errors.New("seal: not a usable X25519 public key")

// ErrOpen is returned for every failure to open.
var ErrOpen = errors.New("seal: cannot open")

// Sealed is the reply form. Byte fields are base64url without padding.
type Sealed struct {
	Alg   string `json:"alg"`
	EPK   string `json:"epk"`
	Nonce string `json:"nonce"`
	CT    string `json:"ct"`
}

var b64 = base64.RawURLEncoding

// ToolDef is the tool definition a reply carries, every field the sidecar
// uses for its egress call. Arrays keep the reply's order.
type ToolDef struct {
	Version      int
	BaseURL      string
	TimeoutMs    int
	SlotLocation string
	SlotField    string
	SlotScheme   string
	PrivateAllow []string
	Actions      []Action
}

// Action is one allowed method and path.
type Action struct{ Method, Path string }

// field appends f as a 4-byte big-endian length and its bytes.
func field(b []byte, f string) []byte {
	b = binary.BigEndian.AppendUint32(b, uint32(len(f)))
	return append(b, f...)
}

// count appends n as a 4-byte big-endian number.
func count(b []byte, n int) []byte { return binary.BigEndian.AppendUint32(b, uint32(n)) }

// ToolDigest is SHA-256 over ToolDefTag, then, in this order: base_url,
// timeout_ms (decimal), the slot's location, field and scheme, the number
// of private_allow entries and each entry, the number of actions and each
// action's method and path. Every string is a 4-byte big-endian length and
// its bytes, and every number of entries is 4-byte big-endian, so no two
// definitions give the same input (docs/resolve.md, "The seal").
func ToolDigest(d ToolDef) [32]byte {
	b := []byte(ToolDefTag)
	b = field(b, d.BaseURL)
	b = field(b, strconv.Itoa(d.TimeoutMs))
	b = field(b, d.SlotLocation)
	b = field(b, d.SlotField)
	b = field(b, d.SlotScheme)
	b = count(b, len(d.PrivateAllow))
	for _, p := range d.PrivateAllow {
		b = field(b, p)
	}
	b = count(b, len(d.Actions))
	for _, a := range d.Actions {
		b = field(b, a.Method)
		b = field(b, a.Path)
	}
	return sha256.Sum256(b)
}

// AAD binds a sealed value to one request, tool, slot and tool definition:
// Info, then jti, tool, slot, the tool version (decimal) and the
// definition's 32-byte digest, each as a 4-byte big-endian length and its
// bytes, so no two inputs give the same bytes whatever they contain.
func AAD(jti, tool, slot string, def ToolDef) []byte {
	b := []byte(Info)
	for _, f := range []string{jti, tool, slot, strconv.Itoa(def.Version)} {
		b = field(b, f)
	}
	d := ToolDigest(def)
	return field(b, string(d[:]))
}

// To seals plaintext to the X25519 public key recipient (32 bytes,
// base64url) with aad.
func To(recipient string, aad, plaintext []byte) (Sealed, error) {
	rpub, err := b64.DecodeString(recipient)
	if err != nil {
		return Sealed{}, ErrKey
	}
	rk, err := ecdh.X25519().NewPublicKey(rpub)
	if err != nil {
		return Sealed{}, ErrKey
	}
	esk, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return Sealed{}, err
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return Sealed{}, err
	}
	return sealWith(esk, nonce, rk, aad, plaintext)
}

// sealWith is To with the ephemeral key and the nonce given: the test
// vector in docs/resolve.md fixes both.
func sealWith(esk *ecdh.PrivateKey, nonce []byte, rk *ecdh.PublicKey, aad, plaintext []byte) (Sealed, error) {
	shared, err := esk.ECDH(rk)
	if err != nil {
		return Sealed{}, ErrKey // a low-order point gives an all-zero secret
	}
	aead, err := derive(shared, rk.Bytes(), esk.PublicKey().Bytes())
	clear(shared)
	if err != nil {
		return Sealed{}, err
	}
	if len(nonce) != aead.NonceSize() {
		return Sealed{}, errors.New("seal: a nonce must be 12 bytes")
	}
	return Sealed{
		Alg:   Alg,
		EPK:   b64.EncodeToString(esk.PublicKey().Bytes()),
		Nonce: b64.EncodeToString(nonce),
		CT:    b64.EncodeToString(aead.Seal(nil, nonce, plaintext, aad)),
	}, nil
}

// Open is the recipient's side, used by tests and as the reference for P4.
func Open(sk *ecdh.PrivateKey, s Sealed, aad []byte) ([]byte, error) {
	if s.Alg != Alg {
		return nil, ErrOpen
	}
	epk, err1 := b64.DecodeString(s.EPK)
	nonce, err2 := b64.DecodeString(s.Nonce)
	ct, err3 := b64.DecodeString(s.CT)
	if err1 != nil || err2 != nil || err3 != nil {
		return nil, ErrOpen
	}
	ek, err := ecdh.X25519().NewPublicKey(epk)
	if err != nil {
		return nil, ErrOpen
	}
	shared, err := sk.ECDH(ek)
	if err != nil {
		return nil, ErrOpen
	}
	aead, err := derive(shared, sk.PublicKey().Bytes(), epk)
	clear(shared)
	if err != nil || len(nonce) != aead.NonceSize() {
		return nil, ErrOpen
	}
	pt, err := aead.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, ErrOpen
	}
	return pt, nil
}

// derive is HKDF-SHA256(ikm = shared, salt = recipient ‖ ephemeral,
// info = Info) → a 32-byte AES-256-GCM key.
func derive(shared, recipient, ephemeral []byte) (cipher.AEAD, error) {
	salt := append(append([]byte{}, recipient...), ephemeral...)
	key, err := hkdf.Key(sha256.New, shared, salt, Info, 32)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
