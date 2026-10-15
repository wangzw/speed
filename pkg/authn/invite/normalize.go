package invite

import (
	"fmt"
	"strings"
)

// emailMaxBytes is the longest an address may be, which is the longest the SMTP
// path admits.
const emailMaxBytes = 254

// normalizeEmail is the one form an invited address is stored and looked up in.
// It is applied on the way in, so that the same address typed with different
// case or surrounding blanks names one invitation. It mirrors the account
// module's rule for an email identifier: trimmed, lower-cased, printable ASCII,
// exactly one @ with a part on each side, at most 254 bytes. It does not fold a
// sub-address or a dot. A value it cannot take is ErrInvalidEmail, whose text
// never carries the address.
func normalizeEmail(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) > emailMaxBytes {
		return "", fmt.Errorf("%w: the address is longer than %d bytes", ErrInvalidEmail, emailMaxBytes)
	}
	if !printableASCII(trimmed) {
		return "", fmt.Errorf("%w: the address is not printable ASCII", ErrInvalidEmail)
	}
	local, domain, found := strings.Cut(trimmed, "@")
	if !found || local == "" || domain == "" || strings.Contains(domain, "@") {
		return "", fmt.Errorf("%w: the address does not hold one @ with a part on each side", ErrInvalidEmail)
	}
	return strings.ToLower(trimmed), nil
}

// printableASCII reports whether every byte of s is a visible ASCII character,
// the space excluded.
func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}
