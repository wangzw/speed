package static

import (
	"fmt"

	"github.com/vislake/speed/pkg/authn/internal/password"
)

// The entry format a table is written in is the one PHC string, with Argon2id
// as the default and PBKDF2-SHA256 as the choice for a deployment that needs a
// FIPS 140 derivation, and the form of an older table, without a leading dollar
// sign, which is still read:
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
//	$pbkdf2-sha256$i=600000,l=32$<salt>$<hash>
//	pbkdf2-sha256$600000$<salt>$<key>             (read, never written)
//
// A salt and a hash are standard base64 without padding. Every number has a
// bound that is checked before anything is derived from the entry, so what an
// entry states cannot decide what one check costs.
//
// The format is a stable contract and not an implementation detail. A table sits
// in a configuration file and outlives the binary that reads it: an entry that
// another tool writes in this format is read like one written here, and an entry
// already written is read as it was.

// ErrMalformedHash reports a password hash this package cannot read, and an
// entry it refuses to write because no startup would read the result. It is the
// sentinel of the password package, which does the reading; the format belongs
// to this backend's table and to nothing else.
var ErrMalformedHash = password.ErrMalformedHash

// Hash derives a storable entry of a password, for writing into the subject
// table. It writes Argon2id at 19 MiB, two iterations and one lane, the OWASP
// combination that costs the least memory. A host exposes it through whatever
// command it uses to add an account; this package deliberately offers no such
// command of its own, since where accounts come from is the host's question.
//
// Two calls on the same password produce different entries: the salt is drawn
// fresh each time, which is what stops one table entry from confirming another.
// The password is hashed as bytes, as it is checked.
func Hash(plaintext string) (string, error) {
	encoded, err := password.DefaultParams().Hash(plaintext)
	if err != nil {
		return "", fmt.Errorf("%s: deriving a password hash: %w", ModuleName, err)
	}
	return encoded, nil
}

// HashPBKDF2 derives a storable PBKDF2-SHA256 entry of a password at the given
// iteration count, in the PHC form, with a salt drawn fresh on every call. It is
// for a deployment that needs the FIPS 140 derivation, for a test that cannot
// afford the default on every entry, and for whoever sets the cost by a policy
// of their own.
//
// Every count from one to ten million is written, since every one of them is one
// ParseHash reads; what an entry costs to verify is its writer's choice, and the
// reader sets no floor under it, though a startup names the entries that are
// below what OWASP recommends. A count outside that range is refused with
// ErrMalformedHash rather than written, since no startup would read the entry.
func HashPBKDF2(plaintext string, iterations int) (string, error) {
	p := password.Params{Algorithm: password.PBKDF2SHA256, PBKDF2Iterations: iterations}
	encoded, err := p.Hash(plaintext)
	if err != nil {
		return "", fmt.Errorf("%s: deriving a password hash: %w", ModuleName, err)
	}
	return encoded, nil
}

// ParseHash checks that an entry is one a table can hold: it is in one of the
// forms above, every number is within its bound and every field is the length it
// has to be. It derives nothing, and it says nothing about the entry's strength.
//
// The error wraps ErrMalformedHash and never carries the entry or any part of it.
func ParseHash(encoded string) error {
	_, err := password.Parse(encoded)
	return err
}
