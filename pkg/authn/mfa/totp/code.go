package totp

import (
	"crypto/subtle"
	"fmt"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// The parameters a seed is generated with. They are the convention every
// authenticator app follows: a 20-byte random secret, HMAC-SHA1, six digits and
// a thirty-second step. They are not settings; the step is fixed so that a wider
// window cannot be turned on, which would only widen an attacker's.
const (
	periodSeconds = 30
	secretBytes   = 20
	digitsOfSix   = 6
	algorithmSHA1 = "SHA1"
)

// window is how many steps either side of the current one a code is accepted
// for: one, so a clock that drifts by one step still works and a code two steps
// out does not. It is fixed and not a setting.
const window = 1

// codeField is the input key a code arrives under, in a challenge answer and in
// the confirmation step of an enrollment. The name is this factor's own.
const codeField = "code"

// validateOpts turns a seed's stored algorithm and digit count into the options
// the library's code generator takes. The step is the fixed one: the design
// fixes it at thirty seconds, so the column that records it is not read here.
func validateOpts(s seed) (totp.ValidateOpts, error) {
	algorithm, err := algorithmByName(s.Algorithm)
	if err != nil {
		return totp.ValidateOpts{}, err
	}
	digits := s.Digits
	if digits <= 0 {
		digits = digitsOfSix
	}
	return totp.ValidateOpts{Period: periodSeconds, Digits: otp.Digits(digits), Algorithm: algorithm}, nil
}

// algorithmByName maps the algorithm column to the library's value. It is the
// one statement of the correspondence.
func algorithmByName(name string) (otp.Algorithm, error) {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "SHA1", "":
		return otp.AlgorithmSHA1, nil
	case "SHA256":
		return otp.AlgorithmSHA256, nil
	case "SHA512":
		return otp.AlgorithmSHA512, nil
	case "MD5":
		return otp.AlgorithmMD5, nil
	default:
		return 0, fmt.Errorf("the TOTP algorithm %q is not one this factor generates codes for", name)
	}
}

// match reports whether code is the code the seed produces at the current step
// or one step either side, and the counter it matched. The library's own
// Validate is not used: it answers only whether some counter matched and reports
// none of them, and the counter is what the replay check needs.
//
// The comparison is constant time, and every candidate is generated before the
// first match is honoured, so the time taken before a match does not depend on
// which step matched. A code of the wrong length is generated against all the
// same and falls out at the constant-time comparison, which leaks nothing a
// code's length does not already.
func match(s seed, now time.Time, code string) (int64, bool) {
	code = strings.TrimSpace(code)
	if code == "" {
		return 0, false
	}
	opts, err := validateOpts(s)
	if err != nil {
		return 0, false
	}
	current := now.Unix() / periodSeconds
	// Guard the step below zero, so a clock set before the epoch cannot wrap a
	// counter around to a huge value that would then be taken for a fresh one.
	if current < 0 {
		return 0, false
	}
	matched := int64(0)
	found := false
	for step := -window; step <= window; step++ {
		counter := current + int64(step)
		if counter < 0 {
			continue
		}
		want, err := totp.GenerateCodeCustom(s.Secret, time.Unix(counter*periodSeconds, 0).UTC(), opts)
		if err != nil {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 && !found {
			matched = counter
			found = true
		}
	}
	return matched, found
}
