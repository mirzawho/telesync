// Package accountid mints the opaque identifier of a user-owned Telegram/Cat
// account.
//
// It exists because two callers need the same value and a security-relevant
// format must have exactly one implementation: the login flow that creates an
// account at the moment the remote identity is confirmed, and the account store
// that records it. If the two drew independently they could drift in alphabet
// or length, and an id minted on one side would not be recognised on the other.
//
// The shape is pinned by the v0.0.5 contract: `acc_` plus 26 lower-cased
// Crockford base32 characters drawn from crypto/rand. Nothing about a username,
// a phone number, a server key or any other user-supplied value takes part, so
// no client can choose, predict or collide with an account id.
package accountid

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
)

// Prefix marks an account id.
const Prefix = "acc_"

// Length is how many base32 characters follow the prefix.
const Length = 26

// crockford is the base32 alphabet the id contract names, lower-cased. It is
// Crockford's, so it excludes the letters that are easy to confuse (i, l, o, u)
// — an operator reading an id aloud is less likely to be misheard.
var crockford = base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)

// New draws a fresh account id.
//
// Twenty bytes of crypto/rand encode to 32 base32 characters; the first 26 are
// kept, which is still 130 bits of entropy — far beyond anything guessable, so
// an id is unguessable even by a caller who may try to address accounts by
// enumeration (see the v0.0.5 cross-user test matrix).
func New() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("accountid: draw an account id: %w", err)
	}
	return Prefix + crockford.EncodeToString(raw)[:Length], nil
}
