package admission

import (
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/settings"
)

// TestStrengthRequirementsBindFromTheNextSignIn checks that both strength requirements bind only sessions authenticated at or after the moment their value was written.
func TestStrengthRequirementsBindFromTheNextSignIn(t *testing.T) {
	written := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	before := written.Add(-time.Minute)
	after := written.Add(time.Minute)

	t.Run("a session older than the tenant row is admitted", func(t *testing.T) {
		r := newRig(t)
		r.withSettings().setAt(settings.ForTenant(acme), requireMFAKey, true, written)
		if rp := r.serve(r.as(acme, authntest.WithAuthTime(before))); !rp.reached() {
			t.Fatalf("answered %d %q, want the older session admitted", rp.status, rp.body)
		}
		r.onlyCounted(admitted)
	})

	t.Run("a session newer than the tenant row is refused", func(t *testing.T) {
		r := newRig(t)
		r.withSettings().setAt(settings.ForTenant(acme), requireMFAKey, true, written)
		stepUp(t, "a single-factor session established after the write",
			r.serve(r.as(acme, authntest.WithAuthTime(after))), acrMultiFactor)
		r.onlyCounted(stepUpMFA)
	})

	t.Run("a session authenticated at the moment of the write is refused", func(t *testing.T) {
		r := newRig(t)
		r.withSettings().setAt(settings.ForTenant(acme), requireMFAKey, true, written)
		stepUp(t, "a session established at the moment the policy was written",
			r.serve(r.as(acme, authntest.WithAuthTime(written))), acrMultiFactor)
	})

	t.Run("a sub-second boundary does not exempt the same whole second", func(t *testing.T) {
		r := newRig(t)
		boundary := written.Add(300 * time.Millisecond)
		r.withSettings().setAt(settings.ForTenant(acme), requireMFAKey, true, boundary)
		stepUp(t, "a session in the write's second",
			r.serve(r.as(acme, authntest.WithAuthTime(written))), acrMultiFactor)
		r.onlyCounted(stepUpMFA)
	})

	t.Run("a multi-factor session newer than the row is admitted", func(t *testing.T) {
		r := newRig(t)
		r.withSettings().setAt(settings.ForTenant(acme), requireMFAKey, true, written)
		rp := r.serve(r.as(acme, authntest.WithAuthTime(after),
			authntest.WithAssurance(authn.AssuranceMulti)))
		if !rp.reached() {
			t.Fatalf("answered %d %q, want the multi-factor session admitted", rp.status, rp.body)
		}
		r.onlyCounted(admitted)
	})

	t.Run("the platform row behaves the same", func(t *testing.T) {
		r := newRig(t)
		r.withSettings().setAt(settings.Global(), requireMFAKey, true, written)
		if rp := r.serve(r.as(acme, authntest.WithAuthTime(before))); !rp.reached() {
			t.Fatalf("an older session answered %d %q, want it admitted", rp.status, rp.body)
		}
		stepUp(t, "a newer session under the platform row",
			r.serve(r.as(acme, authntest.WithAuthTime(after))), acrMultiFactor)
	})

	t.Run("the session is exempt only before every enabled layer's write", func(t *testing.T) {
		r := newRig(t)
		tenantWritten := written.Add(10 * time.Minute)
		store := r.withSettings()
		store.setAt(settings.Global(), requireMFAKey, true, written)
		store.setAt(settings.ForTenant(acme), requireMFAKey, true, tenantWritten)

		stepUp(t, "a session after the platform write but before the tenant write",
			r.serve(r.as(acme, authntest.WithAuthTime(written.Add(5*time.Minute)))), acrMultiFactor)
		if rp := r.serve(r.as(acme, authntest.WithAuthTime(written.Add(-time.Minute)))); !rp.reached() {
			t.Fatalf("a session before both writes answered %d %q, want it admitted", rp.status, rp.body)
		}
	})

	t.Run("a tenant row of false does not move the platform boundary", func(t *testing.T) {
		r := newRig(t)
		store := r.withSettings()
		store.setAt(settings.Global(), requireMFAKey, true, written)
		store.setAt(settings.ForTenant(acme), requireMFAKey, false, written.Add(time.Hour))
		if rp := r.serve(r.as(acme, authntest.WithAuthTime(before))); !rp.reached() {
			t.Fatalf("answered %d %q, want the session before the platform write admitted", rp.status, rp.body)
		}
	})

	t.Run("an enabled layer with no revision keeps refusing", func(t *testing.T) {
		r := newRig(t)
		r.withSettings().set(settings.ForTenant(acme), requireMFAKey, true)
		stepUp(t, "an older session against a row with no revision",
			r.serve(r.as(acme, authntest.WithAuthTime(before))), acrMultiFactor)
	})

	t.Run("require-sso admits a session older than the tenant row", func(t *testing.T) {
		r := newRig(t)
		r.withProviders()
		r.withSettings().setAt(settings.ForTenant(acme), requireSSOKey, true, written)
		if rp := r.serve(r.as(acme, authntest.WithAuthTime(before))); !rp.reached() {
			t.Fatalf("answered %d %q, want the older session admitted", rp.status, rp.body)
		}
		r.onlyCounted(admitted)
	})

	t.Run("require-sso refuses a session newer than the tenant row", func(t *testing.T) {
		r := newRig(t)
		r.withProviders()
		r.withSettings().setAt(settings.ForTenant(acme), requireSSOKey, true, written)
		stepUp(t, "a password session established after the write",
			r.serve(r.as(acme, authntest.WithAuthTime(after))), acrSSO)
		r.onlyCounted(stepUpSSO)
	})

	t.Run("require-sso reads the platform row the same way", func(t *testing.T) {
		r := newRig(t)
		r.withProviders()
		r.withSettings().setAt(settings.Global(), requireSSOKey, true, written)
		if rp := r.serve(r.as(acme, authntest.WithAuthTime(before))); !rp.reached() {
			t.Fatalf("an older session answered %d %q, want it admitted", rp.status, rp.body)
		}
		stepUp(t, "a newer session under the platform row",
			r.serve(r.as(acme, authntest.WithAuthTime(after))), acrSSO)
	})

	t.Run("require-sso with a revision unavailable refuses", func(t *testing.T) {
		r := newRig(t)
		r.withProviders()
		r.withSettings().set(settings.ForTenant(acme), requireSSOKey, true)
		stepUp(t, "an older session against a row with no revision",
			r.serve(r.as(acme, authntest.WithAuthTime(before))), acrSSO)
	})

	t.Run("an sso-exempt member newer than the row still falls to the MFA judge", func(t *testing.T) {
		r := newRig(t)
		r.withProviders()
		r.withSettings().setAt(settings.ForTenant(acme), requireSSOKey, true, written)
		r.members.setRow(authn.Membership{Account: alice, Tenant: acme, Active: true, SSOExempt: true})

		stepUp(t, "an exempt single-factor session after the write",
			r.serve(r.as(acme, authntest.WithAuthTime(after))), acrMultiFactor)

		multi := r.as(acme, authntest.WithAuthTime(after), authntest.WithAssurance(authn.AssuranceMulti))
		if rp := r.serve(multi); !rp.reached() {
			t.Fatalf("an exempt multi-factor session answered %d %q, want it admitted", rp.status, rp.body)
		}
	})
}
