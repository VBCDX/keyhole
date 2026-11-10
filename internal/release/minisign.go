package release

// Minisign (https://jedisct1.github.io/minisign/), the subset a release
// needs: Ed25519 keys, prehashed "ED" signatures (Ed25519 over BLAKE2b-512
// of the file) with a trusted comment, and passwordless secret keys.
//
//	public key   base64( "Ed" | key id (8) | Ed25519 public key (32) )
//	secret key   base64( "Ed" | kdf "\0\0" | "B2" | salt (32) | opslimit (8) | memlimit (8)
//	                     | key id (8) | Ed25519 secret key (64) | checksum (32) )
//	signature    untrusted comment line
//	             base64( "ED" | key id (8) | Ed25519 signature (64) )
//	             "trusted comment: <text>"
//	             base64( Ed25519 signature over ( signature (64) | <text> ) )

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"

	"golang.org/x/crypto/blake2b"
)

const (
	keyIDLen     = 8
	publicKeyLen = 2 + keyIDLen + ed25519.PublicKeySize                            // 42
	secretKeyLen = 2 + 2 + 2 + 32 + 8 + 8 + keyIDLen + ed25519.PrivateKeySize + 32 // 158
	signatureLen = 2 + keyIDLen + ed25519.SignatureSize                            // 74
)

// PublicKey is a minisign Ed25519 public key.
type PublicKey struct {
	ID  [keyIDLen]byte
	Key [ed25519.PublicKeySize]byte
}

// IDText is the key id as minisign prints it: little-endian hex.
func (p PublicKey) IDText() string {
	return fmt.Sprintf("%016X", binary.LittleEndian.Uint64(p.ID[:]))
}

// String is the public key's base64 line.
func (p PublicKey) String() string {
	b := append([]byte("Ed"), p.ID[:]...)
	return base64.StdEncoding.EncodeToString(append(b, p.Key[:]...))
}

// File is a minisign .pub file.
func (p PublicKey) File() string {
	return "untrusted comment: minisign public key " + p.IDText() + "\n" + p.String() + "\n"
}

// ParsePublicKey reads the bare base64 line or a .pub file's two lines.
func ParsePublicKey(text string) (PublicKey, error) {
	var line string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "untrusted comment:") {
			line = l
		}
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(line)
	if err != nil || len(raw) != publicKeyLen {
		return PublicKey{}, fmt.Errorf("not a minisign public key")
	}
	if string(raw[:2]) != "Ed" {
		return PublicKey{}, fmt.Errorf("not an Ed25519 minisign public key")
	}
	var p PublicKey
	copy(p.ID[:], raw[2:10])
	copy(p.Key[:], raw[10:])
	return p, nil
}

// Verify checks a prehashed signature over data and returns its trusted
// comment. Legacy "Ed" signatures (over the raw file) are refused, as the
// installer refuses them.
func (p PublicKey) Verify(data, sigFile []byte) (string, error) {
	lines := strings.Split(string(sigFile), "\n")
	if len(lines) < 4 || !strings.HasPrefix(lines[0], "untrusted comment:") {
		return "", fmt.Errorf("the signature file is malformed")
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(lines[1]))
	if err != nil || len(sig) != signatureLen {
		return "", fmt.Errorf("the signature is malformed")
	}
	if string(sig[:2]) != "ED" {
		return "", fmt.Errorf("the signature is not prehashed (ED)")
	}
	if !bytes.Equal(sig[2:10], p.ID[:]) {
		return "", fmt.Errorf("the signature is by key %016X, not %s", binary.LittleEndian.Uint64(sig[2:10]), p.IDText())
	}
	comment, ok := strings.CutPrefix(strings.TrimSuffix(lines[2], "\r"), "trusted comment: ")
	if !ok {
		return "", fmt.Errorf("the trusted comment line is missing")
	}
	global, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(lines[3]))
	if err != nil || len(global) != ed25519.SignatureSize {
		return "", fmt.Errorf("the global signature is malformed")
	}
	h := blake2b.Sum512(data)
	if !ed25519.Verify(p.Key[:], h[:], sig[10:]) {
		return "", fmt.Errorf("the signature does not verify")
	}
	if !ed25519.Verify(p.Key[:], append(append([]byte{}, sig[10:]...), comment...), global) {
		return "", fmt.Errorf("the trusted comment's signature does not verify")
	}
	return comment, nil
}

// SecretKey is a passwordless minisign Ed25519 secret key.
type SecretKey struct {
	id  [keyIDLen]byte
	key ed25519.PrivateKey
}

func (s *SecretKey) wipe() {
	for i := range s.key {
		s.key[i] = 0
	}
}

// Public is the key's public half.
func (s *SecretKey) Public() PublicKey {
	var p PublicKey
	p.ID = s.id
	copy(p.Key[:], s.key.Public().(ed25519.PublicKey))
	return p
}

// GenerateKey makes a new key (for tests and the contract signature).
func GenerateKey() (*SecretKey, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	s := &SecretKey{key: key}
	if _, err := rand.Read(s.id[:]); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *SecretKey) checksum() [32]byte {
	h, _ := blake2b.New256(nil)
	h.Write([]byte("Ed"))
	h.Write(s.id[:])
	h.Write(s.key)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// File is the key as an unencrypted minisign secret key file (what
// `minisign -G -W` writes). It holds the secret: callers write it 0600.
func (s *SecretKey) File() []byte {
	b := make([]byte, 0, secretKeyLen)
	b = append(b, "Ed"...)
	b = append(b, 0, 0) // no KDF: the key is not encrypted
	b = append(b, "B2"...)
	b = append(b, make([]byte, 32+8+8)...) // salt, opslimit, memlimit: unused without a KDF
	b = append(b, s.id[:]...)
	b = append(b, s.key...)
	sum := s.checksum()
	b = append(b, sum[:]...)
	return []byte("untrusted comment: minisign secret key " + s.Public().IDText() + "\n" +
		base64.StdEncoding.EncodeToString(b) + "\n")
}

// ParseSecretKey reads an unencrypted minisign secret key file. Its checksum
// must be right, or all zero (older `minisign -W` writes zeros), and its
// Ed25519 key must be well formed. The key's bytes never appear in an error.
func ParseSecretKey(text []byte) (*SecretKey, error) {
	lines := strings.Split(string(text), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "untrusted comment:") {
		return nil, fmt.Errorf("the secret key is not a minisign secret key file")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(lines[1]))
	defer func() {
		for i := range raw {
			raw[i] = 0
		}
	}()
	if err != nil || len(raw) != secretKeyLen {
		return nil, fmt.Errorf("the secret key is not a minisign secret key file")
	}
	if string(raw[:2]) != "Ed" || string(raw[4:6]) != "B2" {
		return nil, fmt.Errorf("the secret key is not an Ed25519 minisign key")
	}
	if raw[2] != 0 || raw[3] != 0 {
		return nil, fmt.Errorf("the secret key is encrypted; only a passwordless key is taken")
	}
	s := &SecretKey{key: make(ed25519.PrivateKey, ed25519.PrivateKeySize)}
	copy(s.id[:], raw[54:62])
	copy(s.key, raw[62:126])
	// An Ed25519 secret key is seed | public key: the public half must be the
	// one the seed derives, or the key is corrupt.
	derived := ed25519.NewKeyFromSeed(s.key.Seed())
	ok := subtle.ConstantTimeCompare(derived, s.key) == 1
	for i := range derived {
		derived[i] = 0
	}
	if !ok {
		s.wipe()
		return nil, fmt.Errorf("the secret key is corrupt")
	}
	sum := s.checksum()
	if got := raw[126:158]; subtle.ConstantTimeCompare(got, sum[:]) != 1 && !bytes.Equal(got, make([]byte, 32)) {
		s.wipe()
		return nil, fmt.Errorf("the secret key's checksum is wrong")
	}
	return s, nil
}

// Sign returns a prehashed minisign signature file over data.
func (s *SecretKey) Sign(data []byte, trustedComment, untrustedComment string) []byte {
	h := blake2b.Sum512(data)
	sig := ed25519.Sign(s.key, h[:])
	global := ed25519.Sign(s.key, append(append([]byte{}, sig...), trustedComment...))
	line := append([]byte("ED"), s.id[:]...)
	line = append(line, sig...)
	return []byte("untrusted comment: " + untrustedComment + "\n" +
		base64.StdEncoding.EncodeToString(line) + "\n" +
		"trusted comment: " + trustedComment + "\n" +
		base64.StdEncoding.EncodeToString(global) + "\n")
}
