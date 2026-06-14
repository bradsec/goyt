// Package auth provides password hashing and signed session tokens for the
// optional web UI login gate. It depends only on the standard library.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

const (
	pbkdf2Iterations = 600000 // OWASP guidance for PBKDF2-HMAC-SHA256
	pbkdf2KeyLen     = 32
	pbkdf2SaltLen    = 16
	pbkdf2Prefix     = "pbkdf2-sha256"
)

// Hash derives a PBKDF2-HMAC-SHA256 hash of password with a random salt and
// returns a self-describing encoded string:
//
//	pbkdf2-sha256$<iterations>$<base64 salt>$<base64 hash>
func Hash(password string) (string, error) {
	salt := make([]byte, pbkdf2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to read salt: %w", err)
	}
	dk, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, pbkdf2KeyLen)
	if err != nil {
		return "", fmt.Errorf("failed to derive key: %w", err)
	}
	return fmt.Sprintf("%s$%d$%s$%s",
		pbkdf2Prefix,
		pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(dk),
	), nil
}

// Verify reports whether password matches the encoded hash. It returns false
// for any malformed input and never panics. Comparison is constant time.
func Verify(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != pbkdf2Prefix {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}
