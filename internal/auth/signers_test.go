package auth_test

import (
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/auth"
)

// A typed nil in an interface is not == nil. A signer that is a nil *Tokens
// must be reported missing, because calling it panics inside the request that
// asked it to issue a token.
func TestMissingCatchesATypedNil(t *testing.T) {
	var nilSigner *auth.Tokens
	var asInterface auth.Signers = nilSigner

	if asInterface == nil {
		t.Fatal("the premise of this test does not hold: a typed nil compares == nil")
	}
	if !auth.Missing(asInterface) {
		t.Fatal("a nil *Tokens was not reported missing")
	}
	if !auth.Missing(nil) {
		t.Fatal("an untyped nil signer was not reported missing")
	}
	if !auth.Missing(nilSigner) {
		// A nil pointer passed directly converts to the same non-nil interface;
		// both forms go through the same check.
		t.Fatal("a nil *Tokens passed directly was not reported missing")
	}
}

func TestMissingAcceptsARealSigner(t *testing.T) {
	tokens, err := auth.NewTokens(auth.TokenOptions{Secret: []byte(strings.Repeat("s", 48))})
	if err != nil {
		t.Fatal(err)
	}
	if auth.Missing(tokens) {
		t.Fatal("a real signer was reported missing")
	}
}
