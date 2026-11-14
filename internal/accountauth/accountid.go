package accountauth

import "github.com/mirza/telesync/internal/accountid"

// AccountIDPrefix marks an account id. The suffix is 26 base32 characters drawn
// from crypto/rand — opaque, stable, never reused, and never derived from a
// username, a phone number or a server key.
const AccountIDPrefix = accountid.Prefix

// NewAccountID returns the id of a newly authenticated account.
//
// The account record itself is the account store's; the id is drawn by
// `internal/accountid`, the single implementation shared with that store. Both
// sides must agree on its shape, and no client ever chooses it. Keeping the
// draw in one package is what stops the two from drifting apart in alphabet or
// length, which would let one side mint an id the other does not recognise.
func NewAccountID() (string, error) { return accountid.New() }
