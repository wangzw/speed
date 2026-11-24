// Package password is what the authentication capability does with a password
// once it has one: the PHC form a password hash is stored in, the derivation
// and the check against it, the bounds on what a derivation may cost, the cache
// of checks that held, and the in-process back-off of failed attempts.
//
// Two kinds of backend use it. The static table, which is a credential a
// request presents every time, uses all of it. The account backend, whose
// passwords are checked at an interactive login, uses the form, the derivation,
// the bounds and the decoy, and keeps its back-off in the database.
//
// An entry is read before anything is derived from it. Reading is strict and
// bounded, so that what an entry says cannot decide what one check costs: a
// table or a database row that holds an entry asking for gigabytes of memory is
// refused, and the refusal costs nothing.
package password

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/alexedwards/argon2id"
)

// ErrMalformedHash reports a password hash this package cannot read, or one it
// would not write because no reader would accept the result. It is declared
// here because the reading is, and the static backend exports it under its own
// name: the format is the stored contract between whoever writes an entry and
// the code that reads it.
var ErrMalformedHash = errors.New("authn/static: password hash is not in a readable format")

// Algorithm names a password hashing algorithm as the first segment of a PHC
// string does.
type Algorithm string

// The algorithms an entry may use.
const (
	Argon2id     Algorithm = "argon2id"
	PBKDF2SHA256 Algorithm = "pbkdf2-sha256"
)

// The bounds an entry is held to before anything is derived from it. They are
// constants of this package and not input items: a deployment that could widen
// them could let an entry decide what a check costs.
const (
	// MaxMemoryKiB, MaxIterations and MaxParallelism bound an Argon2id entry. The
	// memory bound is 256 MiB, which is at most half of the default budget a
	// derivation draws on, so one entry cannot take the whole of it.
	MaxMemoryKiB   = 262144
	MaxIterations  = 10
	MaxParallelism = 16
	// MaxPBKDF2Iterations bounds a PBKDF2 entry, a PHC one and a legacy one.
	MaxPBKDF2Iterations = 10_000_000

	minSalt, maxSalt = 8, 64
	minKey, maxKey   = 16, 64

	// legacyKeyLen is the length of the key a legacy entry holds. The legacy
	// form never wrote another.
	legacyKeyLen  = 32
	legacyMinSalt = 1

	pbkdf2SaltLen = 16
	pbkdf2KeyLen  = 32
	argonSaltLen  = 16
	argonKeyLen   = 32
)

// argonPHC is the one shape an Argon2id string may have. The library that reads
// the parameters accepts more than the standard form, such as a parameter list
// with something after it, so the shape is held here first: nothing reaches the
// library that is not already canonical, with no whitespace, no padding, no
// unknown or repeated parameter and no number with a sign or leading zero.
var argonPHC = regexp.MustCompile(
	`^\$argon2id\$v=19\$m=[1-9][0-9]{0,9},t=[1-9][0-9]{0,9},p=[1-9][0-9]{0,9}\$[A-Za-z0-9+/]+\$[A-Za-z0-9+/]+$`)

// pbkdf2Params is the one shape the parameter segment of a PBKDF2 string may
// have: the iteration count and then the output length, each a plain number, in
// that order and each once.
var pbkdf2Params = regexp.MustCompile(`^i=([1-9][0-9]{0,9}),l=([1-9][0-9]{0,9})$`)

// b64 is the encoding of a salt and a key in every form: the standard alphabet
// without padding, strict about the bits after the last full byte. Padding is a
// character several configuration formats quote differently, and a decoder that
// ignored line endings would let an entry carry them.
var b64 = base64.RawStdEncoding.Strict()

// Params are the cost parameters an entry was written with, or one would be
// written with.
type Params struct {
	// Algorithm is the derivation algorithm.
	Algorithm Algorithm
	// MemoryKiB, Iterations and Parallelism are the Argon2id parameters.
	MemoryKiB uint32
	// Iterations is the Argon2id iteration count.
	Iterations uint32
	// Parallelism is the Argon2id lane count.
	Parallelism uint8
	// PBKDF2Iterations is the iteration count of PBKDF2-SHA256.
	PBKDF2Iterations int
}

// DefaultParams is what a new entry is written with when nothing says
// otherwise: Argon2id at 19 MiB, two iterations and one lane, the OWASP minimum
// combination that costs the least memory.
func DefaultParams() Params {
	return Params{Algorithm: Argon2id, MemoryKiB: 19456, Iterations: 2, Parallelism: 1}
}

// BelowFloor reports whether the parameters are weaker than the least OWASP
// recommends: Argon2id at one of five (memory, iterations) combinations with at
// least one lane, or PBKDF2-SHA256 at 600000 iterations. A reader does not
// refuse a weaker entry; it names it.
func (p Params) BelowFloor() bool {
	switch p.Algorithm {
	case Argon2id:
		if p.Parallelism < 1 {
			return true
		}
		for _, f := range [...]struct{ m, t uint32 }{
			{47104, 1}, {19456, 2}, {12288, 3}, {9216, 4}, {7168, 5},
		} {
			if p.MemoryKiB >= f.m && p.Iterations >= f.t {
				return false
			}
		}
		return true
	case PBKDF2SHA256:
		return p.PBKDF2Iterations < 600_000
	}
	return true
}

// Weaker reports whether an entry written with p is weaker than one written
// with current, which is what asks for it to be written again after a check
// that held. An entry of another algorithm than the current one is weaker by
// that alone: a deployment that changed algorithm moves its entries over as
// each is used.
func (p Params) Weaker(current Params) bool {
	if p.Algorithm != current.Algorithm {
		return true
	}
	switch p.Algorithm {
	case Argon2id:
		return p.MemoryKiB < current.MemoryKiB || p.Iterations < current.Iterations ||
			p.Parallelism < current.Parallelism
	case PBKDF2SHA256:
		return p.PBKDF2Iterations < current.PBKDF2Iterations
	}
	return true
}

// Entry is one password hash, read and bounded. The zero Entry is not one and
// verifies nothing.
type Entry struct {
	params  Params
	legacy  bool
	salt    []byte
	key     []byte
	keyLen  uint32
	encoded string
}

// Params is what the entry was written with.
func (e Entry) Params() Params { return e.params }

// Legacy reports whether the entry is in the form without a leading dollar sign,
// which the static table reads and nothing writes.
func (e Entry) Legacy() bool { return e.legacy }

// Digest is the SHA-256 of the entry as it was written, which is what stands for
// the entry in a cache key. An entry that changes, a password changed, changes
// the digest and so retires every cached check against it.
func (e Entry) Digest() [sha256.Size]byte { return sha256.Sum256([]byte(e.encoded)) }

// PHC is the entry in the PHC form: the string itself for an entry that is
// already in it, and for a legacy entry the same salt and key, byte for byte, in
// the PHC shell, so that the one password verifies against both. A legacy entry
// whose salt is shorter than a PHC entry may carry cannot be written that way and
// is refused with ErrMalformedHash.
func (e Entry) PHC() (string, error) {
	switch {
	case e.params.Algorithm == "":
		return "", fmt.Errorf("%w: the entry is the zero entry", ErrMalformedHash)
	case !e.legacy:
		return e.encoded, nil
	case len(e.salt) < minSalt:
		return "", fmt.Errorf("%w: the legacy salt is shorter than %d bytes, which the PHC form requires",
			ErrMalformedHash, minSalt)
	}
	return "$pbkdf2-sha256$i=" + strconv.Itoa(e.params.PBKDF2Iterations) + ",l=" + strconv.Itoa(len(e.key)) +
		"$" + b64.EncodeToString(e.salt) + "$" + b64.EncodeToString(e.key), nil
}

// MemoryKiB is the memory one derivation against the entry takes, which is what
// a derivation draws from the budget. PBKDF2 takes none worth counting.
func (e Entry) MemoryKiB() int64 {
	if e.params.Algorithm == Argon2id {
		return int64(e.params.MemoryKiB)
	}
	return 0
}

// Parse reads an entry. A string that begins with a dollar sign is a PHC string
// and is dispatched on its first segment; any other is read as the legacy form
// pbkdf2-sha256$<iterations>$<salt>$<key>.
//
// Reading is strict: nothing is accepted that is not canonical, and every number
// is held to its bound before the entry is returned, so an entry that is
// returned can be derived from within a known cost. The error never carries the
// string or any part of it.
func Parse(encoded string) (Entry, error) {
	for i := 0; i < len(encoded); i++ {
		if c := encoded[i]; c <= ' ' || c == 0x7f {
			return Entry{}, fmt.Errorf("%w: the entry carries whitespace or a control character",
				ErrMalformedHash)
		}
	}
	switch {
	case strings.HasPrefix(encoded, "$argon2id$"):
		return parseArgon2id(encoded)
	case strings.HasPrefix(encoded, "$pbkdf2-sha256$"):
		return parsePBKDF2(encoded)
	case strings.HasPrefix(encoded, "$"):
		return Entry{}, fmt.Errorf("%w: the algorithm is not argon2id or pbkdf2-sha256", ErrMalformedHash)
	default:
		return parseLegacy(encoded)
	}
}

func parseArgon2id(encoded string) (Entry, error) {
	if !argonPHC.MatchString(encoded) {
		return Entry{}, fmt.Errorf("%w: an argon2id entry is $argon2id$v=19$m=<memory>,t=<iterations>,"+
			"p=<lanes>$<salt>$<hash>, with unpadded base64 and no other parameter", ErrMalformedHash)
	}
	params, salt, key, err := argon2id.DecodeHash(encoded)
	if err != nil {
		return Entry{}, fmt.Errorf("%w: the argon2id entry cannot be decoded", ErrMalformedHash)
	}
	switch {
	case params.Memory > MaxMemoryKiB || params.Iterations > MaxIterations ||
		params.Parallelism > MaxParallelism:
		return Entry{}, fmt.Errorf("%w: an argon2id parameter is above its bound (memory %d KiB, "+
			"iterations %d, lanes %d)", ErrMalformedHash, MaxMemoryKiB, MaxIterations, MaxParallelism)
	case params.Memory < 8*uint32(params.Parallelism):
		return Entry{}, fmt.Errorf("%w: the argon2id memory is below eight KiB for each lane",
			ErrMalformedHash)
	case len(salt) < minSalt || len(salt) > maxSalt:
		return Entry{}, fmt.Errorf("%w: the salt is not %d to %d bytes", ErrMalformedHash, minSalt, maxSalt)
	case len(key) < minKey || len(key) > maxKey:
		return Entry{}, fmt.Errorf("%w: the hash is not %d to %d bytes", ErrMalformedHash, minKey, maxKey)
	}
	return Entry{
		params: Params{
			Algorithm:   Argon2id,
			MemoryKiB:   params.Memory,
			Iterations:  params.Iterations,
			Parallelism: params.Parallelism,
		},
		salt:    salt,
		key:     key,
		keyLen:  params.KeyLength,
		encoded: encoded,
	}, nil
}

func parsePBKDF2(encoded string) (Entry, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 {
		return Entry{}, fmt.Errorf("%w: a pbkdf2-sha256 entry is $pbkdf2-sha256$i=<iterations>,"+
			"l=<length>$<salt>$<hash>", ErrMalformedHash)
	}
	m := pbkdf2Params.FindStringSubmatch(parts[2])
	if m == nil {
		return Entry{}, fmt.Errorf("%w: the pbkdf2-sha256 parameters are not exactly i and then l, "+
			"each a plain number", ErrMalformedHash)
	}
	iterations, errI := strconv.ParseUint(m[1], 10, 32)
	length, errL := strconv.ParseUint(m[2], 10, 32)
	if errI != nil || errL != nil {
		return Entry{}, fmt.Errorf("%w: a pbkdf2-sha256 parameter is out of range", ErrMalformedHash)
	}
	salt, err := b64.DecodeString(parts[3])
	if err != nil {
		return Entry{}, fmt.Errorf("%w: the salt is not unpadded base64", ErrMalformedHash)
	}
	key, err := b64.DecodeString(parts[4])
	if err != nil {
		return Entry{}, fmt.Errorf("%w: the hash is not unpadded base64", ErrMalformedHash)
	}
	switch {
	case iterations > MaxPBKDF2Iterations:
		return Entry{}, fmt.Errorf("%w: the pbkdf2-sha256 iteration count is above %d",
			ErrMalformedHash, MaxPBKDF2Iterations)
	case length < minKey || length > maxKey:
		return Entry{}, fmt.Errorf("%w: the pbkdf2-sha256 output length is not %d to %d bytes",
			ErrMalformedHash, minKey, maxKey)
	case uint64(len(key)) != length:
		return Entry{}, fmt.Errorf("%w: the pbkdf2-sha256 hash is not the length l states", ErrMalformedHash)
	case len(salt) < minSalt || len(salt) > maxSalt:
		return Entry{}, fmt.Errorf("%w: the salt is not %d to %d bytes", ErrMalformedHash, minSalt, maxSalt)
	}
	return pbkdf2Entry(encoded, int(iterations), salt, key, false)
}

func parseLegacy(encoded string) (Entry, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 {
		return Entry{}, fmt.Errorf("%w: expected a PHC string, or pbkdf2-sha256$<iterations>$<salt>$<key>, "+
			"found %d field(s)", ErrMalformedHash, len(parts))
	}
	if parts[0] != string(PBKDF2SHA256) {
		return Entry{}, fmt.Errorf("%w: the legacy scheme is not pbkdf2-sha256", ErrMalformedHash)
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 || parts[1][0] == '+' {
		return Entry{}, fmt.Errorf("%w: the iteration count is not a positive whole number", ErrMalformedHash)
	}
	if iterations > MaxPBKDF2Iterations {
		return Entry{}, fmt.Errorf("%w: the iteration count is above %d", ErrMalformedHash, MaxPBKDF2Iterations)
	}
	// The legacy form is standard base64 without padding, and no stricter was
	// ever demanded of it: the reader that wrote a table accepted any trailing
	// bits, so an entry another tool wrote is read as that tool wrote it.
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) < legacyMinSalt || len(salt) > maxSalt {
		return Entry{}, fmt.Errorf("%w: the salt is not unpadded base64 of 1 to %d bytes",
			ErrMalformedHash, maxSalt)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(key) != legacyKeyLen {
		return Entry{}, fmt.Errorf("%w: the derived key is not unpadded base64 of %d bytes",
			ErrMalformedHash, legacyKeyLen)
	}
	return pbkdf2Entry(encoded, iterations, salt, key, true)
}

// pbkdf2Entry finishes a PBKDF2 entry with the one check that depends on the
// process: whether a derivation can run at all with these parameters. In FIPS
// 140-only mode the standard library refuses a salt shorter than 128 bits, and
// a table entry that could never verify is a startup failure and not a refusal
// only the holder of that account would run into. A single round is enough to
// ask, and the answer does not depend on the password or on the count.
func pbkdf2Entry(encoded string, iterations int, salt, key []byte, legacy bool) (Entry, error) {
	if _, err := pbkdf2.Key(sha256.New, "", salt, 1, len(key)); err != nil {
		return Entry{}, fmt.Errorf("%w: this process cannot derive a key from it: %w", ErrMalformedHash, err)
	}
	return Entry{
		params:  Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: iterations},
		legacy:  legacy,
		salt:    salt,
		key:     key,
		encoded: encoded,
	}, nil
}
