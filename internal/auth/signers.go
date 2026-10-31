package auth

import (
	"reflect"
	"time"
)

// Signers are the operations the API needs from a token signer: mint a token
// for a user, and read the user back out of one.
//
// It is separate from *Tokens so the gateway can hold a signer through an
// interface without being fooled by a typed nil: a (*Tokens)(nil) stored in an
// interface is not == nil, and calling it would dereference nil.
type Signers interface {
	Issue(userID int64) (string, time.Time, error)
	Parse(token string) (int64, error)
}

// Missing reports whether a signer is absent, including a typed nil such as a
// (*Tokens)(nil) that was assigned to an interface.
//
// The gateway calls this before using a signer; TestMissingCatchesATypedNil
// pins the behaviour that made a nil signer panic inside a login request.
func Missing(s Signers) bool {
	if s == nil {
		return true
	}
	v := reflect.ValueOf(s)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return v.IsNil()
	default:
		return false
	}
}

// Tokens implements Signers.
var _ Signers = (*Tokens)(nil)
