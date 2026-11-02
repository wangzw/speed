package mfa

import "github.com/vislake/speed/pkg/authn"

// kindLimit is how many factors of a kind one account may hold, and whether a
// new registration may stand beside the active row while its own proof is
// being collected.
//
// TOTP is the one kind with a fixed cap of one: an authenticator app entry is
// the account's single shared secret, and a second one would leave the reader
// with two codes and no way to tell which one the page wants. Registering
// another replaces the active row: the new row is written pending beside it,
// the old row keeps verifying until the new code is proved, and the activation
// retires the old row. Every other kind uses the deployment's max-per-kind.
func (m *product) kindLimit(kind authn.FactorKind) (limit int, replaces bool) {
	if kind == authn.FactorTOTP {
		return 1, true
	}
	return m.settings.maxPerKind(), false
}

// factorTakesName reports whether a kind's factors carry a reader-chosen name.
// Only a kind that may be held more than once needs one: several passkeys are
// told apart by their names, while the single authenticator entry is the kind
// itself.
func factorTakesName(kind authn.FactorKind) bool {
	return kind != authn.FactorTOTP
}
