package auth

import (
	"testing"
	"time"
)

func TestHashVerify(t *testing.T) {
	encoded, err := Hash("correct horse")
	if err != nil {
		t.Fatalf("Hash returned error: %v", err)
	}
	if !Verify("correct horse", encoded) {
		t.Error("Verify should accept the correct password")
	}
	if Verify("wrong password", encoded) {
		t.Error("Verify should reject the wrong password")
	}
}

func TestVerifyMalformed(t *testing.T) {
	cases := []string{
		"",
		"nothashed",
		"pbkdf2-sha256$notanint$salt$hash",
		"pbkdf2-sha256$600000$!!!$hash",
		"bcrypt$600000$c2FsdA==$aGFzaA==",
		"pbkdf2-sha256$600000$c2FsdA==$",
	}
	for _, c := range cases {
		if Verify("anything", c) {
			t.Errorf("Verify should reject malformed encoded string %q", c)
		}
	}
}

func TestHashUniqueSalt(t *testing.T) {
	a, err := Hash("same")
	if err != nil {
		t.Fatalf("Hash returned error: %v", err)
	}
	b, err := Hash("same")
	if err != nil {
		t.Fatalf("Hash returned error: %v", err)
	}
	if a == b {
		t.Error("two hashes of the same password should differ (random salt)")
	}
}

func TestSessionIssueValidate(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	tok, err := Issue(secret, time.Hour)
	if err != nil {
		t.Fatalf("Issue error: %v", err)
	}
	if !Validate(secret, tok) {
		t.Error("freshly issued token should validate")
	}
}

func TestSessionTampered(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	tok, err := Issue(secret, time.Hour)
	if err != nil {
		t.Fatalf("Issue error: %v", err)
	}
	if Validate(secret, tok+"x") {
		t.Error("tampered token should not validate")
	}
	if Validate([]byte("different-secret-different-secret"), tok) {
		t.Error("token should not validate under a different secret")
	}
}

func TestSessionExpired(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	tok, err := Issue(secret, -time.Minute) // already expired
	if err != nil {
		t.Fatalf("Issue error: %v", err)
	}
	if Validate(secret, tok) {
		t.Error("expired token should not validate")
	}
}

func TestSessionMalformed(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	for _, c := range []string{"", "nodot", "a.b.c", "notbase64.sig"} {
		if Validate(secret, c) {
			t.Errorf("malformed token %q should not validate", c)
		}
	}
}
