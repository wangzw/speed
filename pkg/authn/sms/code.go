package sms

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

const (
	purposeLogin  = authn.SMSPurposeLogin
	purposeMFA    = authn.SMSPurposeMFA
	purposeEnroll = authn.SMSPurposeEnroll
	purposeReset  = authn.SMSPurposeReset
)

var decoyMAC = strings.Repeat("0", 64)

func generateCode(n int) (string, error) {
	// A uniform integer in [0, 10^n) written with leading zeros; drawing over the whole range keeps the digits uniform without rejection sampling.
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

func ttlMinutes(ttl time.Duration) string {
	minutes := int64((ttl + time.Minute - 1) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	return fmt.Sprintf("%d", minutes)
}

// SupportsPurpose reports whether a code may be sent for purpose. The login, mfa and enroll purposes are always carried; reset only when the carrier declares the authn.code.reset template.
func (p *product) SupportsPurpose(purpose string) bool {
	switch purpose {
	case purposeLogin, purposeMFA, purposeEnroll:
		return true
	case purposeReset:
		return p.resetSupported
	default:
		return false
	}
}

func validPurpose(p string) bool {
	switch p {
	case purposeLogin, purposeMFA, purposeEnroll, purposeReset:
		return true
	}
	return false
}
