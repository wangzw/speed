package password

import "golang.org/x/text/unicode/norm"

// DefaultPBKDF2Iterations is the PBKDF2-SHA256 iteration count a new entry is
// written with when nothing states another: the OWASP minimum.
const DefaultPBKDF2Iterations = 600_000

// Normalize is the form a password of the account store is hashed and checked
// in: NFKC, so that the same characters typed on two keyboards derive one key.
// An entry of the static table is not put through it and is hashed as the bytes
// it was written with.
func Normalize(plaintext string) string { return norm.NFKC.String(plaintext) }

// DefaultParamsFor is what a new entry of the algorithm is written with when
// nothing states otherwise: the default Argon2id combination, or PBKDF2-SHA256
// at the OWASP minimum. An algorithm that is neither reads as Argon2id.
func DefaultParamsFor(a Algorithm) Params {
	if a == PBKDF2SHA256 {
		return Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: DefaultPBKDF2Iterations}
	}
	return DefaultParams()
}

// Effective is the parameters a new entry is written with when p is what the
// settings state. The Argon2id settings are each judged within their own range
// and the OWASP floor is judged on the combination, so a combination of three
// legal values may still be below it, or may be one an entry cannot state; such
// a combination falls back to the default of its algorithm and the second
// result says so. A combination at or above the floor is returned as it is.
func (p Params) Effective() (Params, bool) {
	if p.Check() != nil || p.BelowFloor() {
		return DefaultParamsFor(p.Algorithm), true
	}
	return p, false
}
