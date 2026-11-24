package mfa

import (
	"context"
	"fmt"
	"slices"

	"github.com/vislake/speed/pkg/authn"
)

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

func kindValid(kind authn.FactorKind) bool { return len(methodsOf(kind)) > 0 }

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

// Require decides what a login whose first factor is proven must do next: MFAFactor with the usable factors, MFAEnroll when a factor is required and none is active, or MFANone. The requirement account decides the outcome and the proof account's factors are offered. It returns ErrInvalidAccountID for an empty account and ErrUnavailable when a dependency cannot be read.
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
		// A tenant read that fails counts as required, so the login cannot complete without registering one.
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

// Assurance composes the assurance level from the factors proven: multi-factor for a first factor with user verification or a disjoint second factor, and for an IdP declaration marked IDPAssured when no local factor exists. A first factor that names no method is single.
func (m *product) Assurance(first authn.Proof, second *authn.Pass) authn.MFAResult {
	if len(first.Methods) == 0 {
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

func (m *product) unavailable(err error) error {
	return fmt.Errorf("%w: %w", authn.ErrUnavailable, err)
}
