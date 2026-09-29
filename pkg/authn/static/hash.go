package static

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The password hash format this backend stores and reads:
//
//	pbkdf2-sha256$<iterations>$<salt>$<key>
//
// The iteration count is a positive whole number in decimal, and the salt and
// the key are standard base64 without padding. The key is 32 bytes of PBKDF2
// with HMAC-SHA-256 over the password and the salt at that count; Hash and
// HashAt draw a 16-byte salt.
//
// The format is a stable contract, not an implementation detail. A table sits
// in a configuration file and outlives the binary that reads it, and the format
// is written out rather than borrowed so that a table stays readable, and
// writable, without this package: an entry another tool writes to it is read
// like one this package wrote.
const (
	hashScheme    = "pbkdf2-sha256"
	hashSeparator = "$"
	saltLength    = 16
	keyLength     = 32
)

// hashIterations is the count Hash derives at, the figure OWASP's password
// storage guidance gives for PBKDF2 with HMAC-SHA-256. The count travels inside
// every hash, so raising it does not invalidate the tables already written: an
// existing entry keeps verifying at the count it was made with, and is brought
// up to date by being written again.
const hashIterations = 600_000

// ErrMalformedHash reports a password hash this package cannot read, and the
// iteration count HashAt refuses to write one at because no startup would read
// the result. It is judged here rather than in the interface package: the
// format is this backend's own, and no other module has an opinion about it.
var ErrMalformedHash = errors.New("authn/static: password hash is not in a readable format")

// hashEncoding is padding-free so that a hash carries no '=' — a character
// several configuration formats quote differently.
var hashEncoding = base64.RawStdEncoding

// hashed is one parsed password hash.
type hashed struct {
	iterations int
	salt       []byte
	key        []byte
}

// Hash derives a storable hash of a password at 600,000 iterations, for writing
// into the subject table. A host exposes it through whatever command it uses to
// add an account; this package deliberately offers no such command of its own,
// since where accounts come from is the host's question. HashAt writes the same
// format at a count its caller chooses.
//
// Two calls on the same password produce different hashes: the salt is drawn
// fresh each time, which is what stops one table entry from confirming another.
func Hash(password string) (string, error) {
	return HashAt(password, hashIterations)
}

// HashAt derives a storable hash of a password at the given iteration count, in
// the format Hash writes and ParseHash reads, with a salt drawn fresh on every
// call. It is for a writer that chooses the cost: a test that cannot afford the
// default on every entry, or a host whose own policy sets the count.
//
// Every positive count is written, since every positive count is one ParseHash
// reads; what an entry costs to verify is its writer's choice, and this package
// sets no floor under it. A count below one is refused with ErrMalformedHash
// rather than written, since no startup would read the entry.
func HashAt(password string, iterations int) (string, error) {
	if iterations < 1 {
		return "", fmt.Errorf("%w: an entry at %d iteration(s) is not one this backend reads, "+
			"and the count has to be a positive whole number", ErrMalformedHash, iterations)
	}
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("%s: drawing a salt: %w", ModuleName, err)
	}
	return encode(iterations, salt, password)
}

// encode derives and renders a hash at a given cost over a given salt. HashAt
// goes through it, and so do the cases that need the salt fixed.
func encode(iterations int, salt []byte, password string) (string, error) {
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, keyLength)
	if err != nil {
		return "", fmt.Errorf("%s: deriving a password hash: %w", ModuleName, err)
	}
	return strings.Join([]string{
		hashScheme,
		strconv.Itoa(iterations),
		hashEncoding.EncodeToString(salt),
		hashEncoding.EncodeToString(key),
	}, hashSeparator), nil
}

// ParseHash reads a stored hash. Every table entry goes through it at startup,
// so an entry that could never verify is a startup failure rather than a
// refusal only the holder of that account would ever run into: one whose fields
// cannot be read, one whose derived key is not the length verification
// derives, and one whose parameters this process's derivation refuses.
//
// The error never carries the hash or any part of it.
func ParseHash(encoded string) (hashed, error) {
	parts := strings.Split(encoded, hashSeparator)
	if len(parts) != 4 {
		return hashed{}, fmt.Errorf("%w: expected %s%s<iterations>%s<salt>%s<key>, found %d "+
			"field(s)", ErrMalformedHash, hashScheme, hashSeparator, hashSeparator,
			hashSeparator, len(parts))
	}
	if parts[0] != hashScheme {
		return hashed{}, fmt.Errorf("%w: the scheme is %q, and this backend reads %q alone",
			ErrMalformedHash, parts[0], hashScheme)
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 {
		return hashed{}, fmt.Errorf("%w: the iteration count %q is not a positive whole number",
			ErrMalformedHash, parts[1])
	}
	salt, err := hashEncoding.DecodeString(parts[2])
	if err != nil || len(salt) == 0 {
		return hashed{}, fmt.Errorf("%w: the salt is not unpadded base64 of at least one byte",
			ErrMalformedHash)
	}
	key, err := hashEncoding.DecodeString(parts[3])
	if err != nil || len(key) == 0 {
		return hashed{}, fmt.Errorf("%w: the derived key is not unpadded base64 of at least one "+
			"byte", ErrMalformedHash)
	}
	// Verification derives keyLength bytes and compares in constant time,
	// which never matches keys of two different lengths. A key of any other
	// length is well formed and could never verify, so it is refused here
	// with the rest of what cannot.
	if len(key) != keyLength {
		return hashed{}, fmt.Errorf("%w: the derived key is %d byte(s), and this backend derives "+
			"and compares %d", ErrMalformedHash, len(key), keyLength)
	}
	// Whether a derivation can run at all with these parameters is the
	// derivation's own answer, and it depends on the process: in FIPS 140-only
	// mode the standard library refuses a salt shorter than 128 bits. A
	// single round is enough to ask, and the answer does not depend on the
	// password or on the count.
	if _, err := pbkdf2.Key(sha256.New, "", salt, 1, keyLength); err != nil {
		return hashed{}, fmt.Errorf("%w: this process cannot derive a key from it: %w",
			ErrMalformedHash, err)
	}
	return hashed{iterations: iterations, salt: salt, key: key}, nil
}

// matches reports whether the password derives this hash.
//
// The comparison is constant-time in the length of the derived key, so a wrong
// password cannot be walked towards a right one byte by byte. What it does not
// hide is which subjects exist: the gateway resolves ownership from its own
// table and refuses an unclaimed subject without asking any backend at all, so
// that distinction is observable before this function is ever reached.
func (h hashed) matches(password string) bool {
	key, err := pbkdf2.Key(sha256.New, password, h.salt, h.iterations, keyLength)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(key, h.key) == 1
}
