package mfa

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// requireCase is one row of the policy table: the account's rows and flags, the
// proof the first factor gave, and what Require has to answer.
type requireCase struct {
	name string
	// rows are written for the account before the call.
	rows []rowSpec
	// mfaRequired is the account's own flag, and global the deployment's.
	mfaRequired bool
	global      bool
	// factors are the kinds taken in; the default is TOTP, SMS and WebAuthn.
	factors []authn.FactorKind
	first   authn.Proof
	want    authn.MFAOutcome
	// offered are the kinds of the factors the list has to hold, in order.
	offered []authn.FactorKind
	// noAccountRead says the answer was found without reading the account.
	noAccountRead bool
}

type rowSpec struct {
	kind   authn.FactorKind
	status authn.FactorStatus
}

var (
	byPassword = authn.Proof{Methods: []authn.Method{authn.MethodPassword}}
	bySMS      = authn.Proof{Methods: []authn.Method{authn.MethodSMS}}
	byIDP      = authn.Proof{Methods: []authn.Method{authn.MethodOIDC}, IDPAsserted: true, Provider: "p"}
	byPasskey  = authn.Proof{Methods: []authn.Method{authn.MethodWebAuthn}, UserVerified: true}
)

func requireCases() []requireCase {
	active := func(kind authn.FactorKind) rowSpec { return rowSpec{kind, authn.FactorActive} }
	return []requireCase{
		{
			name: "a first factor with user verification asks for nothing more, whatever the account holds",
			rows: []rowSpec{active(authn.FactorTOTP)}, mfaRequired: true, global: true, first: byPasskey,
			want: authn.MFANone, noAccountRead: true,
		},
		{
			name: "an account with an active factor has to use one",
			rows: []rowSpec{active(authn.FactorTOTP)}, first: byPassword,
			want: authn.MFAFactor, offered: []authn.FactorKind{authn.FactorTOTP}, noAccountRead: true,
		},
		{
			name: "an IdP's declaration does not cover an account's own factor",
			rows: []rowSpec{active(authn.FactorTOTP)}, first: byIDP,
			want: authn.MFAFactor, offered: []authn.FactorKind{authn.FactorTOTP},
		},
		{
			name:  "a factor of the method the first factor used is not offered, and the others are",
			rows:  []rowSpec{active(authn.FactorSMS), active(authn.FactorTOTP)},
			first: bySMS, want: authn.MFAFactor, offered: []authn.FactorKind{authn.FactorTOTP},
		},
		{
			name: "an account with factors of the one kind the first factor used cannot finish the login",
			rows: []rowSpec{active(authn.FactorSMS)}, first: bySMS, want: authn.MFAFactor,
		},
		{
			name:  "a passkey that is the first factor without user verification does not serve as the second",
			rows:  []rowSpec{active(authn.FactorWebAuthn)},
			first: authn.Proof{Methods: []authn.Method{authn.MethodWebAuthn}}, want: authn.MFAFactor,
		},
		{
			name: "a factor whose kind has no implementation is unavailable, not absent",
			rows: []rowSpec{active(authn.FactorWebAuthn)}, factors: []authn.FactorKind{authn.FactorTOTP},
			first: byPassword, want: authn.MFAFactor,
		},
		{
			name:    "the unavailable factor is left out and the available one offered",
			rows:    []rowSpec{active(authn.FactorWebAuthn), active(authn.FactorTOTP)},
			factors: []authn.FactorKind{authn.FactorTOTP}, first: byPassword,
			want: authn.MFAFactor, offered: []authn.FactorKind{authn.FactorTOTP},
		},
		{
			name: "a first factor that names no method leaves nothing the second can differ from",
			rows: []rowSpec{active(authn.FactorTOTP)}, first: authn.Proof{}, want: authn.MFAFactor,
		},
		{
			name: "a pending factor is not one the account has",
			rows: []rowSpec{{authn.FactorTOTP, authn.FactorPending}}, first: byPassword, want: authn.MFANone,
		},
		{
			name: "a disabled factor is not one the account has",
			rows: []rowSpec{{authn.FactorTOTP, authn.FactorDisabled}}, first: byPassword, want: authn.MFANone,
		},
		{
			name: "an account that requires a factor and has only a pending one registers",
			rows: []rowSpec{{authn.FactorTOTP, authn.FactorPending}}, mfaRequired: true, first: byPassword,
			want: authn.MFAEnroll,
		},
		{
			name:        "an account that requires a factor and has none registers",
			mfaRequired: true, first: byPassword, want: authn.MFAEnroll,
		},
		{
			name:   "a deployment that requires a factor and an account with none registers",
			global: true, first: byPassword, want: authn.MFAEnroll,
		},
		{
			name:        "the account's flag and the deployment's both require, and either is enough",
			mfaRequired: true, global: true, first: byPassword, want: authn.MFAEnroll,
		},
		{
			name:        "an IdP's declaration does not cover an account that requires a factor",
			mfaRequired: true, first: byIDP, want: authn.MFAEnroll,
		},
		{
			name:   "an IdP's declaration does not cover a deployment that requires a factor",
			global: true, first: byIDP, want: authn.MFAEnroll,
		},
		{
			name:  "an IdP's declaration, with no factor and nothing requiring one, asks for nothing more",
			first: byIDP, want: authn.MFANone,
		},
		{
			name:  "a single factor with no policy completes the login",
			first: byPassword, want: authn.MFANone,
		},
	}
}

func TestRequireFollowsThePolicyTable(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		for i, c := range requireCases() {
			t.Run(c.name, func(t *testing.T) {
				kinds := c.factors
				if kinds == nil {
					kinds = allKinds()
				}
				impls := make([]authn.Factor, len(kinds))
				for j, k := range kinds {
					impls[j] = newFactor(k)
				}
				r := newRig(t, h, impls...)
				account := authn.AccountID(fmt.Sprintf("require-%d", i))
				for _, row := range c.rows {
					r.insertRow(account, row.kind, row.status, r.clock.read(), nil)
				}
				// A factor of another account is no evidence about this one.
				r.activeFactor("someone-else", authn.FactorTOTP, time.Hour)
				r.accounts.required[account] = c.mfaRequired
				r.store.set(globalRequiredPath, c.global)

				got, offered, err := r.p.Require(context.Background(), account, c.first)
				if err != nil {
					t.Fatalf("Require: %v", err)
				}
				if got != c.want {
					t.Errorf("Require = %v, want %v", got, c.want)
				}
				kindsOffered := make([]authn.FactorKind, len(offered))
				for j, rec := range offered {
					kindsOffered[j] = rec.Kind
					if rec.Status != authn.FactorActive || rec.Account != account {
						t.Errorf("the list offers %+v, which is not an active factor of the account", rec)
					}
				}
				if !slices.Equal(kindsOffered, c.offered) {
					t.Errorf("the list offers %v, want %v", kindsOffered, c.offered)
				}
				if c.noAccountRead && r.accounts.gets != 0 {
					t.Errorf("the account was read %d times for an answer that does not depend on it", r.accounts.gets)
				}
			})
		}
	})
}

// A deployment's requirement and an account's are each enough alone, and an IdP
// declaration is no substitute for either: three conditions have to hold for the
// declaration to raise anything, and each one failing is the cases above. This one
// holds that the three are the whole set, by running every combination of the two
// flags and the account's factor against an IdP-asserted proof.
func TestAnIDPDeclarationNeedsAllThreeConditionsEachOfWhichIsNecessary(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h, newFactor(authn.FactorTOTP))
		n := 0
		for _, hasFactor := range []bool{false, true} {
			for _, accountFlag := range []bool{false, true} {
				for _, global := range []bool{false, true} {
					n++
					account := authn.AccountID(fmt.Sprintf("idp-%d", n))
					if hasFactor {
						r.activeFactor(account, authn.FactorTOTP, time.Hour)
					}
					r.accounts.required[account] = accountFlag
					r.store.set(globalRequiredPath, global)
					got, _, err := r.p.Require(context.Background(), account, byIDP)
					if err != nil {
						t.Fatalf("Require: %v", err)
					}
					raised := got == authn.MFANone
					want := !hasFactor && !accountFlag && !global
					if raised != want {
						t.Errorf("factor=%v account flag=%v global=%v: the declaration is believed = %v, want %v (outcome %v)",
							hasFactor, accountFlag, global, raised, want, got)
					}
				}
			}
		}
	})
}

func TestRequireRefusesWhenTheMandatorySwitchCannotBeRead(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h, newFactor(authn.FactorTOTP))
		r.store.fail(globalRequiredPath, errors.New("settings: not loaded"))

		for name, first := range map[string]authn.Proof{"a single factor": byPassword, "an IdP's declaration": byIDP} {
			outcome, _, err := r.p.Require(context.Background(), "no-factors", first)
			if !errors.Is(err, authn.ErrUnavailable) {
				t.Errorf("%s: Require = %v, %v, want an error that is ErrUnavailable, so that it is answered 503 and the login does not complete", name, outcome, err)
			}
			if outcome != authn.MFANone {
				t.Errorf("%s: the outcome beside the error is %v, want the zero outcome", name, outcome)
			}
		}

		// The switch is not consulted when the answer does not depend on it: an
		// account with a factor has to use it whatever the switch says.
		r.activeFactor("with-factor", authn.FactorTOTP, time.Hour)
		outcome, _, err := r.p.Require(context.Background(), "with-factor", byPassword)
		if err != nil || outcome != authn.MFAFactor {
			t.Errorf("an account with a factor: Require = %v, %v, want MFAFactor and no error", outcome, err)
		}
		// An account that requires one is asked to register without the switch too.
		r.accounts.required["flagged"] = true
		outcome, _, err = r.p.Require(context.Background(), "flagged", byPassword)
		if err != nil || outcome != authn.MFAEnroll {
			t.Errorf("an account that requires a factor: Require = %v, %v, want MFAEnroll and no error", outcome, err)
		}
	})
}

func TestRequirePassesTheAccountReadFailureOn(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h, newFactor(authn.FactorTOTP))
		r.accounts.getErr = authn.ErrAccountNotFound
		if _, _, err := r.p.Require(context.Background(), "gone", byPassword); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("an account that is not there: Require gave %v, want ErrAccountNotFound", err)
		}
		if _, _, err := r.p.Require(context.Background(), "", byPassword); !errors.Is(err, authn.ErrInvalidAccountID) {
			t.Errorf("no account: Require gave %v, want ErrInvalidAccountID", err)
		}
	})
}

func TestAssuranceComposesTheLevelFromTheFactorsProven(t *testing.T) {
	r := &rig{p: build(nil, nil, nil, nil, nil)}
	otp := authn.Pass{Methods: []authn.Method{authn.MethodTOTP}}
	cases := []struct {
		name       string
		first      authn.Proof
		second     *authn.Pass
		want       authn.Assurance
		idpAssured bool
	}{
		{"a passkey with user verification is two factors", byPasskey, nil, authn.AssuranceMulti, false},
		{"a passkey with user verification, an IdP declaring besides, is verified here", authn.Proof{
			Methods: []authn.Method{authn.MethodWebAuthn}, UserVerified: true, IDPAsserted: true,
		}, nil, authn.AssuranceMulti, false},
		{"a passkey without user verification is one factor", authn.Proof{Methods: []authn.Method{authn.MethodWebAuthn}}, nil, authn.AssuranceSingle, false},
		{"a password and a TOTP are two kinds", byPassword, &otp, authn.AssuranceMulti, false},
		{"an SMS code and a TOTP are two kinds", bySMS, &otp, authn.AssuranceMulti, false},
		{"an SMS login and an SMS factor are one", bySMS, &authn.Pass{Methods: []authn.Method{authn.MethodSMS}}, authn.AssuranceSingle, false},
		{
			"a passkey first factor and a passkey second are one kind",
			authn.Proof{Methods: []authn.Method{authn.MethodWebAuthn}},
			&authn.Pass{Methods: []authn.Method{authn.MethodWebAuthn}}, authn.AssuranceSingle, false,
		},
		{
			"a second factor that intersects any method of the first does not count",
			authn.Proof{Methods: []authn.Method{authn.MethodPassword, authn.MethodTOTP}},
			&otp, authn.AssuranceSingle, false,
		},
		{"a second factor that names no method proves nothing", byPassword, &authn.Pass{}, authn.AssuranceSingle, false},
		{"a second factor with the first's methods unnamed proves nothing", authn.Proof{}, &otp, authn.AssuranceSingle, false},
		{"an IdP's declaration with nothing else is multi-factor and the IdP's", byIDP, nil, authn.AssuranceMulti, true},
		{
			"an IdP's declaration is not combined with a second factor, which an account with a factor of its own has",
			byIDP, &otp, authn.AssuranceMulti, false,
		},
		{"an IdP that did not declare is one factor", authn.Proof{Methods: []authn.Method{authn.MethodOIDC}, Provider: "p"}, nil, authn.AssuranceSingle, false},
		{"one factor alone is one factor", byPassword, nil, authn.AssuranceSingle, false},
		{"user verification on a proof that names no method proves nothing", authn.Proof{UserVerified: true}, nil, authn.AssuranceSingle, false},
		{"an IdP's declaration on a proof that names no method proves nothing", authn.Proof{IDPAsserted: true}, nil, authn.AssuranceSingle, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.p.Assurance(c.first, c.second)
			if got.Assurance != c.want || got.IDPAssured != c.idpAssured {
				t.Errorf("Assurance = %+v, want {%v %v}", got, c.want, c.idpAssured)
			}
		})
	}
}

// A factor registered during a login is not counted in that login: the engine does
// not ask for the level of a login that enrolled, the session records a single
// factor, and nothing in the registration produces a pass the level could be built
// on.
func TestAFactorRegisteredDuringALoginAddsNothingToItsStrength(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		totp := newFactor(authn.FactorTOTP)
		r := newRig(t, h, totp)
		r.accounts.required["new"] = true

		outcome, _, err := r.p.Require(context.Background(), "new", byPassword)
		if err != nil || outcome != authn.MFAEnroll {
			t.Fatalf("Require = %v, %v, want MFAEnroll", outcome, err)
		}
		got, err := r.p.Enroll(context.Background(), flow("new"), authn.FactorTOTP, authn.StepInput{})
		if err != nil {
			t.Fatalf("Enroll: %v", err)
		}
		if step.KindOf(got) != step.KindEnrolled {
			t.Fatalf("Enroll answered %v, want the factor enrolled", step.KindOf(got))
		}
		if pass := step.PassOf(got); len(pass.Methods) != 0 || pass.UserVerified {
			t.Errorf("the registration carries the pass %+v, which a level could be built on", pass)
		}
		if res := r.p.Assurance(byPassword, nil); res.Assurance != authn.AssuranceSingle {
			t.Errorf("the level of the login that registered is %v, want a single factor", res.Assurance)
		}
		// The factor is the account's from the next login on, and not before the
		// registration finished: its activation is the instant of the last step, which
		// is what step-up compares with the time the session authenticated.
		rec := step.RecordOf(got)
		if rec.Status != authn.FactorActive || !rec.ActivatedAt.Equal(r.clock.read()) {
			t.Errorf("the registered factor is %+v, want it active as of the last step", rec)
		}
		outcome, offered, err := r.p.Require(context.Background(), "new", byPassword)
		if err != nil || outcome != authn.MFAFactor || len(offered) != 1 {
			t.Errorf("the next login: Require = %v, %v, %v, want MFAFactor offering the new factor", outcome, offered, err)
		}
	})
}
