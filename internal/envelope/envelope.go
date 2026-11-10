// Package envelope seals Local-store key values.
//
// Each value is encrypted with AES-256-GCM under a fresh 256-bit data key
// (DEK). The DEK is itself encrypted with AES-256-GCM under the
// key-encryption key (KEK), and the stored envelope carries only the
// wrapped DEK. Both layers authenticate the same additional data, which
// binds the envelope to its organization, key and version: an envelope
// copied onto another key's row does not open.
//
// The KEK comes from a file for now ([LoadKEK]); a KMS-held KEK is later
// work. Nothing in this package logs, and no error it returns contains a
// value, a DEK or the KEK.
package envelope

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// Alg names the scheme in the envelope.
const Alg = "A256GCM+A256GCMKW"

// MaxValue is the largest value Seal accepts, in bytes.
const MaxValue = 64 << 10

const kekLen = 32

// ErrOpen is what every failure to open an envelope returns, so the
// error says nothing about which check failed.
var ErrOpen = errors.New("envelope: cannot open (wrong KEK, or not this key's envelope)")

// KEK is a loaded key-encryption key.
type KEK struct {
	ref  string
	aead cipher.AEAD
}

// Ref is the KEK's reference as recorded in an envelope's dek.kmsKeyRef:
// "file:" and the first 16 hex digits of SHA-256 over the key. It names
// the KEK without revealing it.
func (k *KEK) Ref() string { return k.ref }

// GenerateKEK writes a new random KEK to path, base64 and a newline. The
// file is created with mode 0600 and must not exist yet.
func GenerateKEK(path string) error {
	key := make([]byte, kekLen)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("envelope: create KEK file: %w", err)
	}
	defer clear(key)
	_, werr := writeKEK(f, base64.StdEncoding.EncodeToString(key)+"\n")
	cerr := f.Close()
	if werr != nil || cerr != nil {
		// A partial file would make the next run refuse (O_EXCL); remove it.
		_ = os.Remove(path)
		return fmt.Errorf("envelope: write KEK file: %w", errors.Join(werr, cerr))
	}
	return nil
}

// geteuid is os.Geteuid; a test swaps it so the owner rule is checked as
// any user, not only as root.
var geteuid = os.Geteuid

// writeKEK is io.WriteString; a test swaps it to fail a write.
var writeKEK = io.WriteString

// maxKEKFile bounds the read: the one-line form is 44 base64 characters
// and a newline.
const maxKEKFile = 64

// LoadKEK reads a KEK file written by GenerateKEK. It opens the file first,
// without following a symlink and without blocking on a FIFO, and then
// checks what it opened: a regular file, owned by the effective user, with
// no group or other permission bits. Checking the path and then reading it
// again would let a file swapped in between skip every check.
func LoadKEK(path string) (*KEK, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("envelope: KEK file (a symlink is refused): %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("envelope: KEK file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("envelope: KEK file %s is not a regular file", path)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return nil, fmt.Errorf("envelope: KEK file %s has mode %04o; it must be 0600 or stricter", path, perm)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != geteuid() {
		return nil, fmt.Errorf("envelope: KEK file %s is not owned by this user (uid %d)", path, geteuid())
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxKEKFile+1))
	defer clear(raw)
	if err != nil {
		return nil, fmt.Errorf("envelope: KEK file: %w", err)
	}
	if len(raw) > maxKEKFile {
		return nil, fmt.Errorf("envelope: KEK file %s is over %d bytes; it is not a KEK file", path, maxKEKFile)
	}
	// The decoder skips \r and \n anywhere, so the one-line form is checked here.
	text := strings.TrimSuffix(string(raw), "\n")
	key, err := base64.StdEncoding.DecodeString(text)
	defer clear(key)
	if err != nil || len(key) != kekLen || strings.ContainsAny(text, "\r\n") {
		return nil, fmt.Errorf("envelope: KEK file %s does not hold a base64 %d-byte key", path, kekLen)
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(key)
	return &KEK{ref: "file:" + hex.EncodeToString(sum[:8]), aead: aead}, nil
}

// Envelope is the stored form, as local_key_values.envelope:
// {alg, nonce, ciphertext, aad, dek {wrapped, kmsKeyRef}}. Byte fields
// are base64url without padding.
type Envelope struct {
	Alg        string `json:"alg"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
	AAD        string `json:"aad"`
	DEK        DEK    `json:"dek"`
}

// DEK is the wrapped data key: the wrap nonce followed by the sealed key.
type DEK struct {
	Wrapped   string `json:"wrapped"`
	KMSKeyRef string `json:"kmsKeyRef"`
}

// AAD is the additional data that binds an envelope to one version of
// one key in one organization: a tag, then each field as a 4-byte
// big-endian length and its bytes, so no two (org, key, version) give the
// same bytes whatever the ids contain.
func AAD(orgID, keyID string, version int) []byte {
	b := []byte("keyhole-local-v1")
	for _, f := range []string{orgID, keyID, strconv.Itoa(version)} {
		b = binary.BigEndian.AppendUint32(b, uint32(len(f)))
		b = append(b, f...)
	}
	return b
}

var b64 = base64.RawURLEncoding

// Seal encrypts plaintext under a fresh DEK and wraps the DEK with k.
func (k *KEK) Seal(aad, plaintext []byte) (Envelope, error) {
	if len(plaintext) == 0 || len(plaintext) > MaxValue {
		return Envelope{}, fmt.Errorf("envelope: a value must be 1 to %d bytes", MaxValue)
	}
	dek := make([]byte, kekLen)
	if _, err := rand.Read(dek); err != nil {
		return Envelope{}, err
	}
	defer clear(dek)
	aead, err := newAEAD(dek)
	if err != nil {
		return Envelope{}, err
	}
	nonce := randNonce(aead)
	wrapNonce := randNonce(k.aead)
	return Envelope{
		Alg:        Alg,
		Nonce:      b64.EncodeToString(nonce),
		Ciphertext: b64.EncodeToString(aead.Seal(nil, nonce, plaintext, aad)),
		AAD:        b64.EncodeToString(aad),
		DEK: DEK{
			Wrapped:   b64.EncodeToString(k.aead.Seal(wrapNonce, wrapNonce, dek, aad)),
			KMSKeyRef: k.ref,
		},
	}, nil
}

// Open decrypts e, which must have been sealed by k with exactly aad.
// Every failure returns ErrOpen.
func (k *KEK) Open(e Envelope, aad []byte) ([]byte, error) {
	// The stored aad field is informational: GCM authenticates the aad
	// passed here, so a mismatch fails below without a separate check.
	if e.Alg != Alg || e.DEK.KMSKeyRef != k.ref {
		return nil, ErrOpen
	}
	wrapped, err1 := b64.DecodeString(e.DEK.Wrapped)
	nonce, err2 := b64.DecodeString(e.Nonce)
	ct, err3 := b64.DecodeString(e.Ciphertext)
	ns := k.aead.NonceSize()
	if err1 != nil || err2 != nil || err3 != nil || len(wrapped) < ns {
		return nil, ErrOpen
	}
	dek, err := k.aead.Open(nil, wrapped[:ns], wrapped[ns:], aad)
	if err != nil {
		return nil, ErrOpen
	}
	defer clear(dek)
	aead, err := newAEAD(dek)
	if err != nil || len(nonce) != aead.NonceSize() {
		return nil, ErrOpen
	}
	pt, err := aead.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, ErrOpen
	}
	return pt, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func randNonce(a cipher.AEAD) []byte {
	n := make([]byte, a.NonceSize())
	if _, err := rand.Read(n); err != nil {
		panic(err) // crypto/rand.Read never fails on supported platforms
	}
	return n
}
