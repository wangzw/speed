package sms

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// The closed sets this package validates against, taken from the root package's
// single declaration of each: the code purposes and the two message languages.
// The names are this package's own shorthand; the values are the root package's.
const (
	purposeLogin  = authn.SMSPurposeLogin
	purposeMFA    = authn.SMSPurposeMFA
	purposeEnroll = authn.SMSPurposeEnroll

	localeDefault = authn.LocaleZH
	localeEnglish = authn.LocaleEN
)

// decoyMAC is the digest a check compares against when there is no row to
// compare with, so that a missing, spent or expired challenge costs one
// comparison like any other. It is a fixed hexadecimal string of the blind
// index's own width; a real digest equals it with negligible probability.
var decoyMAC = strings.Repeat("0", 64)

// generateCode returns a code of n uniform decimal digits, drawn from
// crypto/rand. The code is a value that never leaves the module except in the
// queued message and the call to the sender.
func generateCode(n int) (string, error) {
	// A uniform integer in [0, 10^n), written with leading zeros to n digits.
	// Drawing a random integer over the whole range rather than one digit at a
	// time keeps the digits uniform without rejection sampling.
	bound := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
	v, err := rand.Int(rand.Reader, bound)
	if err != nil {
		return "", fmt.Errorf("drawing a verification code: %w", err)
	}
	digits := v.String()
	if pad := n - len(digits); pad > 0 {
		digits = strings.Repeat("0", pad) + digits
	}
	return digits, nil
}

// ttlMinutes is the code's lifetime in whole minutes, rounded up, as the message
// parameter carries it. At least one minute.
func ttlMinutes(ttl time.Duration) string {
	minutes := int64((ttl + time.Minute - 1) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	return fmt.Sprintf("%d", minutes)
}

// localeOf reads the message language the caller named through the root
// package's single mapping, the empty string reading as the default.
func localeOf(locale string) string {
	return authn.LocaleOf(locale)
}

// validPurpose reports whether p is one of the closed set of purposes.
func validPurpose(p string) bool {
	switch p {
	case purposeLogin, purposeMFA, purposeEnroll:
		return true
	}
	return false
}

// validLocale reports whether locale is the empty string or one of the two
// languages a message may be written in.
func validLocale(locale string) bool {
	switch locale {
	case "", localeDefault, localeEnglish:
		return true
	}
	return false
}
