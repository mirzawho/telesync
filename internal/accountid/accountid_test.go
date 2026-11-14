package accountid

import (
	"strings"
	"testing"
)

// The shape is a contract, not an accident: the login flow and the account
// store both mint ids and must agree on every property below. A change here is
// a migration, not a refactor.

func TestNewHasTheContractShape(t *testing.T) {
	id, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if !strings.HasPrefix(id, Prefix) {
		t.Fatalf("New() = %q, want prefix %q", id, Prefix)
	}
	suffix := strings.TrimPrefix(id, Prefix)
	if len(suffix) != Length {
		t.Fatalf("New() suffix = %d chars, want %d (id %q)", len(suffix), Length, id)
	}
	if len(id) != len(Prefix)+Length {
		t.Fatalf("New() = %q (%d chars), want %d", id, len(id), len(Prefix)+Length)
	}
	for _, r := range suffix {
		if !strings.ContainsRune("0123456789abcdefghjkmnpqrstvwxyz", r) {
			t.Fatalf("New() = %q, suffix holds %q which is not Crockford base32", id, r)
		}
	}
}

// Crockford excludes i, l, o and u precisely so a read-aloud id cannot be
// misheard. If the alphabet silently gains them the property is gone.
func TestNewExcludesEasilyConfusedLetters(t *testing.T) {
	for i := 0; i < 200; i++ {
		id, err := New()
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		for _, bad := range "ilou" {
			if strings.ContainsRune(id, bad) {
				t.Fatalf("New() = %q, which contains the confusable letter %q", id, bad)
			}
		}
	}
}

// An id is the capability that addresses an account. If it repeats, two
// accounts collide and one becomes addressable as the other.
func TestNewDoesNotRepeat(t *testing.T) {
	seen := make(map[string]bool, 512)
	for i := 0; i < 512; i++ {
		id, err := New()
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if seen[id] {
			t.Fatalf("New() returned %q twice in 512 draws", id)
		}
		seen[id] = true
	}
}

// Nothing about a user-supplied value or a counter may reach the id. Two draws
// from crypto/rand disagree in almost every position; a counter, a timestamp or
// a value derived from a phone number would not. Two 26-character random
// strings differ in ~96% of their characters, so requiring half is safe against
// flaking and still catches sequential generation.
func TestNewIsNotSequential(t *testing.T) {
	a, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	b, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	sa, sb := strings.TrimPrefix(a, Prefix), strings.TrimPrefix(b, Prefix)
	differing := 0
	for i := range sa {
		if sa[i] != sb[i] {
			differing++
		}
	}
	if differing < Length/2 {
		t.Fatalf("New() %q and %q differ in only %d of %d positions — "+
			"that is not what crypto/rand produces", a, b, differing, Length)
	}
}
