package mfa

import "github.com/vislake/speed/pkg/authn"

func (m *product) kindLimit(kind authn.FactorKind) (limit int, replaces bool) {
	if kind == authn.FactorTOTP {
		return 1, true
	}
	return m.settings.maxPerKind(), false
}

func factorTakesName(kind authn.FactorKind) bool {
	return kind != authn.FactorTOTP
}
