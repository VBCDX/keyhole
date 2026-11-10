package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// Sealer encrypts and authenticates small values (AES-256-GCM) so they can
// ride in a cookie without the browser being able to read or alter them.
type Sealer struct{ aead cipher.AEAD }

// NewSealer derives the key from secret. An empty secret gets a random key,
// which is fine for a single process but means in-flight sign-ins fail
// after a restart or on another instance.
func NewSealer(secret string) (*Sealer, error) {
	var key [32]byte
	if secret == "" {
		if _, err := rand.Read(key[:]); err != nil {
			return nil, err
		}
	} else {
		key = sha256.Sum256([]byte("keyhole-sealer-v1:" + secret))
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal encrypts plaintext; purpose is bound as associated data so a value
// sealed for one use cannot be replayed as another.
func (s *Sealer) Seal(purpose string, plaintext []byte) string {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(s.aead.Seal(nonce, nonce, plaintext, []byte(purpose)))
}

// Open reverses Seal.
func (s *Sealer) Open(purpose, sealed string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return nil, errors.New("auth: malformed sealed value")
	}
	n := s.aead.NonceSize()
	return s.aead.Open(nil, raw[:n], raw[n:], []byte(purpose))
}
