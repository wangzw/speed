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

// The password hash format this backend stores and reads.
//
// It is written out rather than borrowed so that a table stays readable without
// this package: scheme, iteration count, salt and derived key, separated by '$'
// and base64 encoded without padding.
const (
	hashScheme    = "pbkdf2-sha256"
	hashSeparator = "$"
	saltLength    = 16
	keyLength     = 32
)

// hashIterations is what Hash produces today. The count travels inside every
// hash it writes, so raising it does not invalidate the tables already written:
// an existing entry keeps verifying at the count it was made with, and is
// brought up to date by being written again.
const hashIterations = 600_000

// ErrMalformedHash reports a password hash this package cannot read. It is
// judged here rather than in the interface package: the format is this
// backend's own, and no other module has an opinion about it.
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

// Hash derives a storable hash of a password, for writing into the subject
// table. A host exposes it through whatever command it uses to add an account;
// this package deliberately offers no such command of its own, since where
// accounts come from is the host's question.
//
// Two calls on the same password produce different hashes: the salt is drawn
// fresh each time, which is what stops one table entry from confirming another.
func Hash(password string) (string, error) {
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("%s: drawing a salt: %w", ModuleName, err)
	}
	return encode(hashIterations, salt, password)
}

// encode derives and renders a hash at a given cost. Hash goes through it, and
// so does anything that has to produce one cheaply.
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
// refusal only the holder of that account would ever run into.
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
