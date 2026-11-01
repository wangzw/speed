package mfa

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/authn"
)

// labelField is the key a registration's free input map carries the factor's
// optional label under. A factor's own inputs live in the same map, so the name
// is reserved here and not read by any kind.
const labelField = "label"

// maxFactorLabelRunes is the most characters a factor label may hold, which is
// the width the label column admits. The register stores what the caller
// submitted, with surrounding whitespace trimmed and nothing generated, so an
// empty label means the factor has no name.
const maxFactorLabelRunes = 64

// normalizeFactorLabel trims a submitted label and checks it against the
// domain: at most maxFactorLabelRunes characters and no control character,
// which includes a newline or a tab. It returns the trimmed value, which is
// what the register stores; an empty result is a factor with no name.
func normalizeFactorLabel(raw string) (string, error) {
	label := strings.TrimSpace(raw)
	if utf8.RuneCountInString(label) > maxFactorLabelRunes {
		return "", fmt.Errorf("%w: a factor label is longer than %d characters",
			authn.ErrInvalidArgument, maxFactorLabelRunes)
	}
	for _, r := range label {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: a factor label carries a control character", authn.ErrInvalidArgument)
		}
	}
	return label, nil
}
