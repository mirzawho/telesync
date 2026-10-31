package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/auth"
)

func TestHashPasswordCommandPrintsAVerifiableHash(t *testing.T) {
	var out bytes.Buffer
	if err := runHashPassword(context.Background(), strings.NewReader("a-strong-password\n"), &out); err != nil {
		t.Fatalf("runHashPassword: %v", err)
	}
	hash := strings.TrimSpace(out.String())
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("output = %q, want a PHC Argon2id hash", hash)
	}
	if strings.Contains(hash, "a-strong-password") {
		t.Fatal("the output contains the password")
	}
	if ok, err := auth.VerifyPassword(hash, "a-strong-password"); err != nil || !ok {
		t.Fatalf("the hash does not verify: %v %v", ok, err)
	}
}

func TestHashPasswordCommandRefusesAnEmptyPassword(t *testing.T) {
	var out bytes.Buffer
	if err := runHashPassword(context.Background(), strings.NewReader("\n"), &out); err == nil {
		t.Fatal("an empty password was accepted")
	}
	if out.Len() != 0 {
		t.Fatalf("a refusal wrote output: %q", out.String())
	}
}
