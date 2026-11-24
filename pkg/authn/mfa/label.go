package mfa

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/authn"
)

const labelField = "label"

const maxFactorLabelRunes = 64

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
