// Package auth holds the sign-in building blocks: password hashing, the
// failed-sign-in limiter and the OpenID Connect client.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters: the OWASP Password Storage Cheat Sheet's first
// recommended configuration (m=19 MiB, t=2, p=1).
// https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html#argon2id
const (
	argonMemory  = 19 * 1024
	argonTime    = 2
	argonThreads = 1
	argonSaltLen = 16
	argonKeyLen  = 32
)

// Password length limits. The upper bound caps hashing work per request.
const (
	MinPasswordLen = 12
	MaxPasswordLen = 1024
)

var (
	ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	ErrPasswordTooLong  = fmt.Errorf("password must be at most %d characters", MaxPasswordLen)
	errMalformedHash    = errors.New("auth: malformed password hash")
)

// ValidatePassword enforces the length policy for new passwords.
func ValidatePassword(pw string) error {
	switch {
	case len(pw) < MinPasswordLen:
		return ErrPasswordTooShort
	case len(pw) > MaxPasswordLen:
		return ErrPasswordTooLong
	}
	return nil
}

// HashPassword returns a PHC-format argon2id hash:
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<key>
func HashPassword(pw string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// VerifyPassword reports whether pw matches the PHC-format hash. The
// parameters stored in the hash are used, so older hashes keep verifying
// after the defaults change.
func VerifyPassword(hash, pw string) (bool, error) {
	if len(pw) > MaxPasswordLen {
		return false, nil
	}
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errMalformedHash
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, errMalformedHash
	}
	// Refuse absurd parameters so a tampered hash cannot exhaust memory.
	if memory == 0 || memory > 1<<20 || time == 0 || time > 16 || threads == 0 {
		return false, errMalformedHash
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return false, errMalformedHash
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, errMalformedHash
	}
	got := argon2.IDKey([]byte(pw), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// dummyHash is verified against when an account does not exist, so a
// sign-in for an unknown email costs the same time as a wrong password.
var dummyHash = sync.OnceValue(func() string {
	h, err := HashPassword("keyhole-timing-equaliser")
	if err != nil {
		panic(err)
	}
	return h
})

// BurnPasswordCheck spends the same work as one VerifyPassword call.
func BurnPasswordCheck(pw string) { _, _ = VerifyPassword(dummyHash(), pw) }
