package password

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"strconv"

	"github.com/alexedwards/argon2id"
	"golang.org/x/crypto/argon2"
)

// Verify reports whether the password derives the entry, comparing in constant
// time so a wrong password cannot be walked towards the right one. An error is a
// derivation the process cannot run, never a wrong password.
func (e Entry) Verify(password string) (bool, error) {
	switch e.params.Algorithm {
	case Argon2id:
		// Parse bounded every number, so the derivation below cannot panic.
		key := argon2.IDKey([]byte(password), e.salt, e.params.Iterations, e.params.MemoryKiB,
			e.params.Parallelism, e.keyLen)
		return subtle.ConstantTimeCompare(key, e.key) == 1, nil
	case PBKDF2SHA256:
		key, err := pbkdf2.Key(sha256.New, password, e.salt, e.params.PBKDF2Iterations, len(e.key))
		if err != nil {
			return false, fmt.Errorf("authn/password: deriving a pbkdf2-sha256 key: %w", err)
		}
		return subtle.ConstantTimeCompare(key, e.key) == 1, nil
	}
	return false, fmt.Errorf("%w: the entry is the zero entry", ErrMalformedHash)
}

// Check says whether p can be written: the algorithm is one of the two and every
// number is within the bounds Parse holds an entry to.
func (p Params) Check() error {
	switch p.Algorithm {
	case Argon2id:
		if p.MemoryKiB < 8*uint32(p.Parallelism) || p.Parallelism < 1 || p.Iterations < 1 ||
			p.MemoryKiB > MaxMemoryKiB || p.Iterations > MaxIterations || p.Parallelism > MaxParallelism {
			return fmt.Errorf("%w: the argon2id parameters are outside what an entry may state",
				ErrMalformedHash)
		}
	case PBKDF2SHA256:
		if p.PBKDF2Iterations < 1 || p.PBKDF2Iterations > MaxPBKDF2Iterations {
			return fmt.Errorf("%w: the pbkdf2-sha256 iteration count %d is outside 1 to %d, which is "+
				"what an entry may state", ErrMalformedHash, p.PBKDF2Iterations, MaxPBKDF2Iterations)
		}
	default:
		return fmt.Errorf("%w: the algorithm %q is not one this package writes", ErrMalformedHash, p.Algorithm)
	}
	return nil
}

// Hash derives a storable PHC string of the password with these parameters and a
// salt drawn fresh, so two calls on one password differ. It does not normalise;
// whether to is the caller's rule.
func (p Params) Hash(password string) (string, error) {
	if err := p.Check(); err != nil {
		return "", err
	}
	switch p.Algorithm {
	case Argon2id:
		return argon2id.CreateHash(password, &argon2id.Params{
			Memory:      p.MemoryKiB,
			Iterations:  p.Iterations,
			Parallelism: p.Parallelism,
			SaltLength:  argonSaltLen,
			KeyLength:   argonKeyLen,
		})
	default:
		salt := make([]byte, pbkdf2SaltLen)
		if _, err := rand.Read(salt); err != nil {
			return "", fmt.Errorf("authn/password: drawing a salt: %w", err)
		}
		return encodePBKDF2(p.PBKDF2Iterations, salt, password)
	}
}

func encodePBKDF2(iterations int, salt []byte, password string) (string, error) {
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, pbkdf2KeyLen)
	if err != nil {
		return "", fmt.Errorf("authn/password: deriving a pbkdf2-sha256 key: %w", err)
	}
	return "$pbkdf2-sha256$i=" + strconv.Itoa(iterations) + ",l=" + strconv.Itoa(pbkdf2KeyLen) +
		"$" + b64.EncodeToString(salt) + "$" + b64.EncodeToString(key), nil
}
