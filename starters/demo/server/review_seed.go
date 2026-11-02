//go:build review

package main

// The review build's impersonation fixture.
//
// The impersonation journey needs an operator who is not a member of the target
// tenant, holds two two-step methods with at least one seasoned non-SMS method,
// and can raise a stale session with the other method. Impersonation is a
// platform capability decided in the platform domain, so the operator's
// platform-administrator role is the permission it needs: the target tenant in
// the path is the data the record is confined to, not a domain the initiator
// must hold a grant in. A browser walkthrough can produce that state by hand,
// but the review build is also a demonstration: it seeds the two factors here,
// in the review build only, so the journey is reachable from a fresh database.
//
// The factors are written through the product's own seams, not by raw inserts
// chosen here: the TOTP one through the multi-factor register and the TOTP
// module's encrypted seed register, the SMS one through the register alone (that
// factor keeps no data of its own; it binds the account's proven phone). The
// activation instant is the fixture's choice — a factor enrolled now is not
// seasoned — so the TOTP factor's activated_at is backdated through the
// register's own activation statement, which accepts an instant. See
// pkg/authn/mfa/review_seed.go and pkg/authn/mfa/totp/review_seed.go.
//
// The fixture also proves the operator's email address and phone number through
// the account module's AddIdentifier, which is the same write a completed code
// flow performs. The review assembly can deliver both codes (mail.log and
// sms.log), so the states are states the product reaches; the seed writes
// them directly so the journey starts from a ready account rather than from a
// verification detour.

import (
	"context"
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/mfa"
	mfatotp "github.com/vislake/speed/pkg/authn/mfa/totp"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
)

// reviewOperatorPhone is the phone the SMS factor binds in the review fixture.
// The review SMS sender writes its codes to the process log, and the number's
// region is admitted by the review configuration's authn.sms.allowed-prefixes.
const reviewOperatorPhone = "+15551234567"

// reviewOperatorTOTPSeed is the base32 seed the review fixture writes for the
// operator's TOTP factor. It is fixed on purpose: a person walking the journey
// imports it into an authenticator app (issuer Speed Demo, account the operator
// id, SHA-1, six digits, thirty-second period) and computes the sign-in code
// without reading the database. It is a review fixture and not a credential; a
// deployment's factors are enrolled in the browser.
const reviewOperatorTOTPSeed = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"

// reviewFactorAge is how far into the past the seeded TOTP factor is activated.
// It is well past authn.mfa.sensitive-min-age, which the review configuration
// lowers to its one-hour floor, so the impersonation gate reads the factor as
// seasoned without an hour of waiting.
const reviewFactorAge = 2 * time.Hour

// reviewSeedsFactors reports whether this seed writes second factors: only a
// deployment that opted in with seed.impersonation gets them, because they
// change how its administrator signs in.
func reviewSeedsFactors(in seedInput) bool { return in.Impersonation }

// reviewSeedExtras seeds the impersonation fixture after the accounts, tenants
// and memberships are in place. It is a no-op in a default build, and a no-op
// here when the deployment did not opt in.
func reviewSeedExtras(ctx context.Context, reg *core.Registry, in seedInput) error {
	if !in.Impersonation {
		return nil
	}
	var operator *seedAccount
	for i := range in.Accounts {
		if in.Accounts[i].PlatformAdmin {
			operator = &in.Accounts[i]
			break
		}
	}
	if operator == nil {
		return nil
	}
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		return fmt.Errorf("review fixture: taking up the database: %w", err)
	}
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return fmt.Errorf("review fixture: taking up the account reads: %w", err)
	}
	admin, err := core.Resolve[authn.AccountAdmin](reg)
	if err != nil {
		return fmt.Errorf("review fixture: taking up the account writes: %w", err)
	}
	kind, name, err := seedLoginName(operator.Identifier)
	if err != nil {
		return err
	}
	id, err := accounts.Lookup(ctx, kind, name)
	if err != nil {
		return fmt.Errorf("review fixture: looking up the operator %q: %w", operator.Identifier, err)
	}
	if err := seedReviewIdentifiers(ctx, accounts, admin, id, kind, name); err != nil {
		return err
	}
	return seedReviewFactors(ctx, database, id)
}

// seedReviewIdentifiers proves the operator's email address when the account
// was seeded with one, and its phone number, so the one-time impersonation
// gates and the SMS factor find what they read.
func seedReviewIdentifiers(ctx context.Context, accounts authn.Accounts, admin authn.AccountAdmin,
	id authn.AccountID, kind authn.IdentifierKind, name string,
) error {
	held, err := accounts.Identifiers(ctx, id)
	if err != nil {
		return fmt.Errorf("review fixture: reading the operator's identifiers: %w", err)
	}
	if kind == authn.IdentifierEmail && !holdsProved(held, authn.IdentifierEmail, name) {
		via := authn.Verification{Via: authn.VerifiedViaEmailCode}
		if err := admin.AddIdentifier(ctx, id, authn.IdentifierEmail, name, via); err != nil {
			return fmt.Errorf("review fixture: proving the operator's email: %w", err)
		}
	}
	if !holdsProved(held, authn.IdentifierPhone, reviewOperatorPhone) {
		via := authn.Verification{Via: authn.VerifiedViaSMSCode}
		if err := admin.AddIdentifier(ctx, id, authn.IdentifierPhone, reviewOperatorPhone, via); err != nil {
			return fmt.Errorf("review fixture: proving the operator's phone: %w", err)
		}
	}
	return nil
}

// holdsProved reports whether the account already holds the identifier as a
// proof of ownership, which is what makes the fixture idempotent: a second seed
// run leaves a proof (and its instant) as it is.
func holdsProved(held []authn.Identifier, kind authn.IdentifierKind, value string) bool {
	for _, identifier := range held {
		if identifier.Kind != kind || identifier.Value != value {
			continue
		}
		if (authn.Verification{Via: identifier.VerifiedVia}).Proves() {
			return true
		}
	}
	return false
}

// seedReviewFactors writes the operator's TOTP and SMS factors unless it already
// holds an active one of each kind. The TOTP factor is backdated so it is
// seasoned; the SMS factor is the kind a stale session can step up with, and it
// binds the phone seeded above.
func seedReviewFactors(ctx context.Context, database db.Database, id authn.AccountID) error {
	factors, err := mfa.ReviewFactors(ctx, database, id)
	if err != nil {
		return err
	}
	var haveTOTP, haveSMS bool
	for _, factor := range factors {
		if factor.Status != authn.FactorActive {
			continue
		}
		switch factor.Kind {
		case authn.FactorTOTP:
			haveTOTP = true
		case authn.FactorSMS:
			haveSMS = true
		}
	}
	now := time.Now().UTC()
	if !haveTOTP {
		if _, err := mfatotp.ReviewSeedFactor(ctx, database, id, now.Add(-reviewFactorAge),
			reviewOperatorTOTPSeed); err != nil {
			return fmt.Errorf("review fixture: seeding the TOTP factor: %w", err)
		}
		fmt.Print("seeded: review fixture: the operator's TOTP factor (activated two hours ago)\n")
	}
	if !haveSMS {
		if _, err := mfa.ReviewSeedActiveFactor(ctx, database, id, authn.FactorSMS, now); err != nil {
			return fmt.Errorf("review fixture: seeding the SMS factor: %w", err)
		}
		fmt.Print("seeded: review fixture: the operator's SMS factor\n")
	}
	return nil
}
