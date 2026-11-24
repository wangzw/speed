package invite

import (
	"fmt"
	"strings"
)

// emailMaxBytes is the longest address the SMTP path admits.
const emailMaxBytes = 254

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

func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}
