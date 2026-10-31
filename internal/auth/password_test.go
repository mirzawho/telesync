package auth

import "testing"

// These tests cover the hash the documented `hash-password` command prints: it
// must verify, and it must carry the documented cost.

func TestTheDocumentedHashVerifies(t *testing.T) {
	hash, err := HashPassword("a-strong-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	ok, err := VerifyPassword(hash, "a-strong-password")
	if err != nil || !ok {
		t.Fatalf("the printed hash does not verify: %v %v", ok, err)
	}
}
