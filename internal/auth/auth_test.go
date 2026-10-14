package auth

import (
	"bytes"
	"testing"
	"time"
)

func TestPasswordHashing(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "correct horse") {
		t.Fatal("right password rejected")
	}
	if CheckPassword(h, "wrong horse") {
		t.Fatal("wrong password accepted")
	}
	h2, _ := HashPassword("correct horse")
	if h == h2 {
		t.Fatal("hashes must be salted (differ for the same password)")
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got, msg := NormalizeEmail("  Ana@Example.COM "); got != "ana@example.com" || msg != "" {
		t.Fatalf("got %q %q", got, msg)
	}
	for _, bad := range []string{"", "ana", "ana@", "ana@localhost", "Ana <ana@example.com>", "a b@example.com"} {
		if _, msg := NormalizeEmail(bad); msg == "" {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if ValidatePassword("short") == "" {
		t.Error("short password accepted")
	}
	if ValidatePassword(string(bytes.Repeat([]byte("a"), 73))) == "" {
		t.Error("73-byte password accepted")
	}
	if msg := ValidatePassword("long enough"); msg != "" {
		t.Error(msg)
	}
}

func TestSessionTokens(t *testing.T) {
	tok, hash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(hash, HashToken(tok)) {
		t.Fatal("hash must be derivable from the token")
	}
	tok2, _, _ := NewSessionToken()
	if tok == tok2 || len(tok) < 40 {
		t.Fatalf("tokens must be long and random: %q %q", tok, tok2)
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(3, 50*time.Millisecond)
	for i := 0; i < 3; i++ {
		if l.Allow("k") != nil {
			t.Fatal("should allow before limit")
		}
		l.Fail("k")
	}
	if l.Allow("k") == nil {
		t.Fatal("should lock out after 3 failures")
	}
	if l.Allow("other") != nil {
		t.Fatal("other keys must be unaffected")
	}
	time.Sleep(60 * time.Millisecond)
	if l.Allow("k") != nil {
		t.Fatal("should allow again after the window")
	}
	l.Fail("k")
	l.Reset("k")
	if len(l.failures) != 0 {
		t.Fatal("reset should clear the key")
	}
}
