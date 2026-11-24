package sms

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// TestGenerateCodeHasExactlyNDigits checks that GenerateCode has exactly n digits.
func TestGenerateCodeHasExactlyNDigits(t *testing.T) {
	for _, n := range []int{6, 7, 8} {
		for range 2000 {
			code, err := generateCode(n)
			if err != nil {
				t.Fatalf("generateCode(%d): %v", n, err)
			}
			if len(code) != n {
				t.Fatalf("generateCode(%d) drew %q, which is %d digits, want %d", n, code, len(code), n)
			}
			if strings.Trim(code, "0123456789") != "" {
				t.Fatalf("generateCode(%d) drew %q, which is not all decimal digits", n, code)
			}
		}
	}
}

// TestGenerateCodeSpansTheWholeDecimalRange checks that GenerateCode spans the whole decimal range.
func TestGenerateCodeSpansTheWholeDecimalRange(t *testing.T) {
	const n = 6
	floor := int64(9) * pow10(n-1)
	var max int64
	for range 2000 {
		code, err := generateCode(n)
		if err != nil {
			t.Fatalf("generateCode(%d): %v", n, err)
		}
		v, err := strconv.ParseInt(code, 10, 64)
		if err != nil {
			t.Fatalf("generateCode(%d) drew %q, which is not a number: %v", n, code, err)
		}
		if v > max {
			max = v
		}
	}
	if max < floor {
		t.Errorf("2000 draws reached a maximum of %d, below %d: the code is not drawn over the whole decimal range", max, floor)
	}
}

// TestTTLMinutesRoundsUp checks that TTLMinutes rounds up.
func TestTTLMinutesRoundsUp(t *testing.T) {
	cases := []struct {
		ttl  time.Duration
		want string
	}{
		{90 * time.Second, "2"},
		{5 * time.Minute, "5"},
		{5*time.Minute + 30*time.Second, "6"},
		{30 * time.Second, "1"},
		{time.Second, "1"},
	}
	for _, c := range cases {
		if got := ttlMinutes(c.ttl); got != c.want {
			t.Errorf("ttlMinutes(%v) = %q, want %q", c.ttl, got, c.want)
		}
	}
}

// TestValidPurposeIsTheRootPackageClosedSet checks that ValidPurpose is the root package's closed set.
func TestValidPurposeIsTheRootPackageClosedSet(t *testing.T) {
	for _, p := range []string{authn.SMSPurposeLogin, authn.SMSPurposeMFA, authn.SMSPurposeEnroll, authn.SMSPurposeReset} {
		if !validPurpose(p) {
			t.Errorf("validPurpose(%q) = false, want true: %q is a root package SMSPurpose constant", p, p)
		}
	}
	for _, p := range []string{"", "login ", " login", "LOGIN", "Login", "signin", "verify", "register", "sms", "otp", "mfa-login"} {
		if validPurpose(p) {
			t.Errorf("validPurpose(%q) = true, want false: no root package SMSPurpose constant names %q", p, p)
		}
	}
}

func pow10(n int) int64 {
	v := int64(1)
	for range n {
		v *= 10
	}
	return v
}
