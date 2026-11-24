package static

import (
	"fmt"

	"github.com/vislake/speed/pkg/authn/internal/password"
)

// ErrMalformedHash reports a password hash this package cannot read, and an entry it refuses to
// write because no startup would read the result. It is the password package's sentinel; the
// format belongs to this backend's table.
var ErrMalformedHash = password.ErrMalformedHash

// Hash derives a storable Argon2id entry of a password for the subject table, as a PHC string at
// 19 MiB, two iterations and one lane. Each call draws a fresh salt, so two calls on one password
// differ. The password is hashed as bytes, as it is checked. An error means no entry was derived.
func Hash(plaintext string) (string, error) {
	encoded, err := password.DefaultParams().Hash(plaintext)
	if err != nil {
		return "", fmt.Errorf("%s: deriving a password hash: %w", ModuleName, err)
	}
	return encoded, nil
}

// HashPBKDF2 derives a storable PBKDF2-SHA256 entry as a PHC string at the given iteration count,
// with a fresh salt. Every count from one to ten million is written; one outside that range is
// refused with ErrMalformedHash. An error means no entry was derived.
func HashPBKDF2(plaintext string, iterations int) (string, error) {
	p := password.Params{Algorithm: password.PBKDF2SHA256, PBKDF2Iterations: iterations}
	encoded, err := p.Hash(plaintext)
	if err != nil {
		return "", fmt.Errorf("%s: deriving a password hash: %w", ModuleName, err)
	}
	return encoded, nil
}

// ParseHash checks that an entry is one a table can hold: the PHC form or the legacy form without
// a leading dollar sign, every number within its bound and every field the length it has to be.
// It derives nothing, judges no strength, and its error wraps ErrMalformedHash without the entry.
func ParseHash(encoded string) error {
	_, err := password.Parse(encoded)
	return err
}
