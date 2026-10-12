package sms

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestGenerateCodeHasExactlyNDigits holds the length invariant on every draw: a
// code of n digits is exactly n characters, whatever the random value. Drawing
// many codes pins the padding, which a length assertion on a single draw would
// only catch when that draw happened to be short.
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

// TestGenerateCodeSpansTheWholeDecimalRange holds the code space: the draw is
// uniform over [0, 10^n), so a large sample includes codes in the top tenth of
// the range. A draw over a narrower base — [0, 9^n) or one digit over [0, 9) —
// can never reach 9·10^(n-1), so this catches a shrunk alphabet.
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

// TestTTLMinutesRoundsUp holds the message parameter: a code's lifetime in whole
// minutes is rounded up, so a lifetime that is not a whole number of minutes
// names the minute it is valid through and never zero.
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

func pow10(n int) int64 {
	v := int64(1)
	for range n {
		v *= 10
	}
	return v
}
