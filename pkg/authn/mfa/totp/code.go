package totp

import (
	"crypto/subtle"
	"fmt"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/vislake/speed/pkg/authn/mfa/internal/totpseed"
)

const (
	periodSeconds = totpseed.PeriodSeconds
	secretBytes   = 20
	digitsOfSix   = totpseed.DigitsOfSix
	algorithmSHA1 = totpseed.AlgorithmSHA1
)

const window = 1

const codeField = "code"

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

// match returns the counter of the code at the current step or one either side. Every candidate is generated and compared in constant time, so timing does not show which step matched.
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
	// Keep the step at or above zero: a clock before the epoch would wrap the counter to a huge value.
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
