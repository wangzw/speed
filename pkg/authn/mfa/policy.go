package mfa

import (
	"context"
	"fmt"
	"slices"

	"github.com/vislake/speed/pkg/authn"
)

// methodsOf is the authentication methods a pass of the kind may carry, which is
// also the set a factor of the kind is compared with the first factor by. It is
// the one place the correspondence is written: a TOTP passes as otp, an SMS code
// as sms and a passkey as webauthn. A kind outside the closed set has none, and a
// factor of it can neither be offered nor pass.
func methodsOf(kind authn.FactorKind) []authn.Method {
	switch kind {
	case authn.FactorTOTP:
		return []authn.Method{authn.MethodTOTP}
	case authn.FactorSMS:
		return []authn.Method{authn.MethodSMS}
	case authn.FactorWebAuthn:
		return []authn.Method{authn.MethodWebAuthn}
	}
	return nil
}

// kindValid reports whether kind is one of the closed set a row may carry.
func kindValid(kind authn.FactorKind) bool { return len(methodsOf(kind)) > 0 }

// disjoint reports whether no method is in both lists. An empty list on either
// side is not disjoint from anything: an absent method set is not evidence that
// the two factors differ, so the comparison fails closed.
func disjoint(a, b []authn.Method) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	for _, m := range a {
		if slices.Contains(b, m) {
			return false
		}
	}
	return true
}

// Require decides, in the order of the policy table, what a login whose first
// factor is proven has to do next. The requirement account decides the outcome;
// the proof account is the one whose factors may be offered. They are the same
// account for an ordinary login and differ when an impersonating identity proves
// its own factors for the impersonated account: the impersonated account's
// factors decide how strong the proof must be, and the operator's own are the
// ones that can be proven. The first row that holds is the answer:
//
//  1. A first factor that carried user verification is two factors already, so
//     nothing more is asked.
//  2. An account with an active factor has to use one. The list offers the
//     proof account's active factors that have an implementation in this
//     assembly and whose methods do not intersect the first factor's; it may be
//     empty, and then the login cannot be completed. An enterprise IdP's
//     declaration does not override the account's own factor.
//  3. An account that requires a factor -- by its own flag, the deployment's
//     switch, or a tenant it is an active member of -- and an account that has
//     none: the login can only register one.
//  4. An enterprise IdP's declaration of multi-factor, when none of the above
//     applies: nothing more is asked, and Assurance marks the result as the
//     IdP's.
//  5. Otherwise a single factor completes the login.
//
// Policy only ever tightens: no row cancels another.
func (m *product) Require(ctx context.Context, proof, requirement authn.AccountID, first authn.Proof) (authn.MFAOutcome, []authn.FactorRecord, error) {
	if proof == "" || requirement == "" {
		return authn.MFANone, nil, authn.ErrInvalidAccountID
	}
	if first.UserVerified {
		return authn.MFANone, nil, nil
	}
	active, err := m.register.list(ctx, requirement, int(authn.FactorActive))
	if err != nil {
		return authn.MFANone, nil, m.unavailable(err)
	}
	if len(active) > 0 {
		offered := active
		if proof != requirement {
			offered, err = m.register.list(ctx, proof, int(authn.FactorActive))
			if err != nil {
				return authn.MFANone, nil, m.unavailable(err)
			}
		}
		return authn.MFAFactor, m.usable(offered, first), nil
	}
	account, err := m.accounts.Get(ctx, requirement)
	if err != nil {
		return authn.MFANone, nil, err
	}
	required := account.MFARequired
	if !required {
		required, err = m.settings.globalRequired()
		if err != nil {
			return authn.MFANone, nil, m.unavailable(err)
		}
	}
	if !required && m.tenantMFA != nil {
		// Any tenant the account is an active member of that requires a factor
		// makes the account require one. A read that fails is not an exemption:
		// the account is treated as required, so the login cannot complete
		// without registering one.
		tenantRequired, terr := m.tenantMFA.RequiresMFA(ctx, requirement)
		if terr != nil {
			tenantRequired = true
		}
		required = tenantRequired
	}
	if required {
		return authn.MFAEnroll, nil, nil
	}
	return authn.MFANone, nil, nil
}

// usable keeps the factors the login can be completed with: one whose kind has
// an implementation here and whose methods do not intersect the first factor's.
// A factor whose implementation is absent is unavailable, which is not the same
// as the account having none.
func (m *product) usable(active []authn.FactorRecord, first authn.Proof) []authn.FactorRecord {
	out := make([]authn.FactorRecord, 0, len(active))
	for _, rec := range active {
		if _, ok := m.factors[rec.Kind]; !ok {
			continue
		}
		if !disjoint(first.Methods, methodsOf(rec.Kind)) {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// Assurance composes the assurance of a login. It is the one place a level is
// computed: the gateway's backends, the admission layer and the impersonation
// check read what the login session recorded and judge nothing again. It reads
// no store.
//
// The level follows from the kinds of factor that were proven:
//
//   - a first factor that carried user verification is multi-factor;
//   - a first factor and a second whose methods do not intersect it is
//     multi-factor;
//   - an enterprise IdP's declaration, when the first factor is all there is, is
//     multi-factor and marked IDPAssured, which says it was declared and not
//     verified here. Only the provider's own tenant counts it, and an action that
//     asks for Local does not;
//   - anything else, and a first factor that names no method, is a single factor.
//
// The conditions that make an IdP's declaration count, that the account has no
// active factor and nothing requires one, are the ones Require checks before it
// answers MFANone for such a login, and the login engine calls this only after
// that answer or after a second factor passed. A second factor is therefore
// never combined with a declaration: an account with a factor of its own does not
// have its declaration believed.
//
// A factor registered during this login is not a pass here: the engine does not
// call this for an enrolling login, and so the session records a single factor.
func (m *product) Assurance(first authn.Proof, second *authn.Pass) authn.MFAResult {
	if len(first.Methods) == 0 {
		// A proof that names no method proves nothing, so nothing is built on it.
		return authn.MFAResult{Assurance: authn.AssuranceSingle}
	}
	if first.UserVerified {
		return authn.MFAResult{Assurance: authn.AssuranceMulti}
	}
	if second != nil {
		if disjoint(first.Methods, second.Methods) {
			return authn.MFAResult{Assurance: authn.AssuranceMulti}
		}
		return authn.MFAResult{Assurance: authn.AssuranceSingle}
	}
	if first.IDPAsserted {
		return authn.MFAResult{Assurance: authn.AssuranceMulti, IDPAssured: true}
	}
	return authn.MFAResult{Assurance: authn.AssuranceSingle}
}

// unavailable wraps a failure of a dependency this module reads as ErrUnavailable,
// which the callers answer 503 and never as a refusal.
func (m *product) unavailable(err error) error {
	return fmt.Errorf("%w: %w", authn.ErrUnavailable, err)
}
