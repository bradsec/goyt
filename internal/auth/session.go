package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Token format: base64(expiryUnix) + "." + base64(HMAC-SHA256(secret, payload))
// where payload is the base64(expiryUnix) segment. The token is stateless; its
// validity depends only on the secret and the embedded expiry.
// The format is NOT versioned. To invalidate all existing tokens after a format
// change, rotate the session secret.

// Issue creates a signed session token valid for ttl from now.
func Issue(secret []byte, ttl time.Duration) (string, error) {
	if len(secret) == 0 {
		return "", fmt.Errorf("empty session secret")
	}
	expiry := time.Now().Add(ttl).Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(expiry, 10)))
	sig := sign(secret, payload)
	return payload + "." + sig, nil
}

// Validate reports whether token carries a valid, unexpired signature for
// secret. It returns false for any malformed input and never panics.
func Validate(secret []byte, token string) bool {
	if len(secret) == 0 {
		return false
	}
	payload, sig, found := strings.Cut(token, ".")
	if !found || payload == "" || sig == "" {
		return false
	}
	expected := sign(secret, payload)
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return false
	}
	expiry, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Unix() < expiry
}

func sign(secret []byte, payload string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
