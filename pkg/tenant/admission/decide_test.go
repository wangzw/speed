package admission

import (
	"encoding/json"
	nethttp "net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/settings"
)

// The cases here are the judgement table, row by row. Each row has a case that
// takes it and, beside it, one that differs from it in the single fact the row
// turns on and so lands in the next row: a row that was widened, narrowed or
// moved is a row some neighbour's case lands in the wrong place.

// notFound holds a reply to the one shape every 404 of this layer has.
func notFound(t *testing.T, what string, rp reply) {
	t.Helper()
	if rp.status != nethttp.StatusNotFound {
		t.Fatalf("%s answered %d, want 404; body %q", what, rp.status, rp.body)
	}
}

func stepUp(t *testing.T, what string, rp reply, acr string) {
	t.Helper()
	if rp.status != nethttp.StatusUnauthorized {
		t.Fatalf("%s answered %d, want 401; body %q", what, rp.status, rp.body)
	}
	want := `error="insufficient_user_authentication", error_description="` + stepUpDescription +
		`", acr_values="` + acr + `"`
	got := rp.header.Get("WWW-Authenticate")
	if !strings.HasSuffix(got, want) {
		t.Fatalf("%s challenged with %q, want it to end with %q", what, got, want)
	}
	// The problem type tells the client what the challenge needs: the SSO
	// challenge is its own code, because a local factor cannot satisfy it.
	wantType := "urn:speed:authn:insufficient_user_authentication"
	if acr == "sso" {
		wantType = "urn:speed:authn:sso_required"
	}
	var problem struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(rp.body), &problem); err != nil || problem.Type != wantType {
		t.Fatalf("%s carries the problem %q (%v), want %s", what, rp.body, err, wantType)
	}
}

// TestRowNoTenantContext holds the first row: a request with no tenant is not
// about one, and is let through, except an impersonation, which exists to be
// confined to a tenant and has none here.
func TestRowNoTenantContext(t *testing.T) {
	t.Run("an anonymous request is admitted", func(t *testing.T) {
		r := newRig(t)
		rp := r.serve(r.anon("", authn.AccessOpen))
		if !rp.reached() {
			t.Fatalf("answered %d %q, want it admitted", rp.status, rp.body)
		}
		r.onlyCounted(admitted)
	})
	t.Run("an identity with no tenant is admitted without reading a membership", func(t *testing.T) {
		r := newRig(t)
		rp := r.serve(r.as("", authntest.WithAccount(bob)))
		if !rp.reached() {
			t.Fatalf("answered %d %q, want it admitted", rp.status, rp.body)
		}
		if n, _ := r.members.reads(); n != 0 {
			t.Fatalf("the membership was read %d times for a request with no tenant", n)
		}
		r.onlyCounted(admitted)
	})
	t.Run("a request with no tenant and no identity on a route that needs one is admitted", func(t *testing.T) {
		// The row comes first: whether a caller is identified is for the rows
		// that have a tenant to compare against.
		r := newRig(t)
		rp := r.serve(r.bare(""))
		if !rp.reached() {
			t.Fatalf("answered %d %q, want it admitted", rp.status, rp.body)
		}
	})
	t.Run("an impersonation is refused 404", func(t *testing.T) {
		r := newRig(t)
		rp := r.serve(r.as("", authntest.WithAccount(alice), authntest.WithActor(bob),
			authntest.WithSession("s1"), authntest.WithScope(acme)))
		notFound(t, "an impersonation with no tenant", rp)
		r.onlyCounted(scopeMismatch)
	})
}

// TestRowNoIdentity holds the second row.
func TestRowNoIdentity(t *testing.T) {
	for name, access := range map[string]authn.Access{
		"an open route":     authn.AccessOpen,
		"an optional route": authn.AccessOptional,
	} {
		t.Run(name+" admits an anonymous request", func(t *testing.T) {
			r := newRig(t)
			rp := r.serve(r.anon(acme, access))
			if !rp.reached() {
				t.Fatalf("answered %d %q, want it admitted", rp.status, rp.body)
			}
			if n, _ := r.members.reads(); n != 0 {
				t.Fatalf("the membership was read %d times for a request with no identity", n)
			}
			r.onlyCounted(admitted)
		})
	}
	t.Run("a level the gateway never recorded is refused 401 and logged at Error", func(t *testing.T) {
		// A context with no gateway behind it reads as AccessUnknown. The
		// level Required, which the gateway records beside an identity, never
		// reaches here without one, and authntest builds no such context.
		r := newRig(t)
		rp := r.serve(r.bare(acme))
		if rp.status != nethttp.StatusUnauthorized {
			t.Fatalf("answered %d, want 401", rp.status)
		}
		if rp.header.Get("WWW-Authenticate") == "" {
			t.Fatal("the 401 carries no challenge, so the caller is told nothing of what to send")
		}
		if strings.Contains(rp.header.Get("WWW-Authenticate"), "insufficient_user_authentication") {
			t.Fatal("a request with no identity was told to step up")
		}
		if errs := r.logs.atLevel(levelError); len(errs) != 1 || errs[0].attrs["outcome"] != "no-identity" {
			t.Fatalf("the Error records are %+v, want one with outcome no-identity", errs)
		}
		r.onlyCounted(noIdentity)
	})
	t.Run("the same request with an identity moves on to the next row", func(t *testing.T) {
		r := newRig(t)
		rp := r.serve(r.as(acme))
		if !rp.reached() {
			t.Fatalf("answered %d %q, want it admitted", rp.status, rp.body)
		}
		if n, _ := r.members.reads(); n != 1 {
			t.Fatalf("the membership was read %d times, want 1", n)
		}
	})
}

// TestRowImpersonationScope holds the third row.
func TestRowImpersonationScope(t *testing.T) {
	t.Run("a scope that is another tenant is refused 404 before anything is read", func(t *testing.T) {
		r := newRig(t)
		r.members.add(alice, globex)
		rp := r.serve(r.as(acme, authntest.WithAccount(alice), authntest.WithActor(bob),
			authntest.WithSession("s1"), authntest.WithScope(globex)))
		notFound(t, "an impersonation confined to another tenant", rp)
		if n, _ := r.members.reads(); n != 0 {
			t.Fatalf("the membership was read %d times for a request refused on its scope", n)
		}
		r.onlyCounted(scopeMismatch)
	})
	t.Run("a scope that is this tenant moves on to the membership row", func(t *testing.T) {
		r := newRig(t)
		rp := r.serve(r.as(acme, authntest.WithAccount(alice), authntest.WithActor(bob),
			authntest.WithSession("s1"), authntest.WithScope(acme)))
		if !rp.reached() {
			t.Fatalf("answered %d %q, want it admitted", rp.status, rp.body)
		}
		r.onlyCounted(admitted)
	})
}

// TestRowMembership holds the fourth row and the error row that goes with it.
func TestRowMembership(t *testing.T) {
	t.Run("a member is admitted", func(t *testing.T) {
		r := newRig(t)
		if rp := r.serve(r.as(acme)); !rp.reached() {
			t.Fatalf("answered %d %q, want it admitted", rp.status, rp.body)
		}
		r.onlyCounted(admitted)
	})
	t.Run("a member of another tenant is refused 404", func(t *testing.T) {
		r := newRig(t)
		notFound(t, "a non-member", r.serve(r.as(globex)))
		r.onlyCounted(notMember)
	})
	t.Run("an account with no membership at all is refused 404", func(t *testing.T) {
		r := newRig(t)
		notFound(t, "a stranger", r.serve(r.as(acme, authntest.WithAccount("static.ops"),
			authntest.WithBackend("static"))))
	})
	t.Run("the account asked about is the effective one, not the actor", func(t *testing.T) {
		// bob acts as alice. alice is the member, so the impersonation goes
		// through, and it ends when she stops being one.
		r := newRig(t)
		impersonating := r.as(acme, authntest.WithAccount(alice), authntest.WithActor(bob),
			authntest.WithSession("s1"), authntest.WithScope(acme))
		if rp := r.serve(impersonating); !rp.reached() {
			t.Fatalf("answered %d %q, want it admitted", rp.status, rp.body)
		}
		r.members.remove(alice, acme)
		r.members.add(bob, acme)
		r.clock.advance(maxCheckTTL)
		notFound(t, "an impersonation of an account that is no longer a member", r.serve(impersonating))
	})
	t.Run("an impersonation of a non-member is refused even when the actor is a member", func(t *testing.T) {
		r := newRig(t)
		r.members.add(bob, acme)
		rp := r.serve(r.as(acme, authntest.WithAccount("carol"), authntest.WithActor(bob),
			authntest.WithSession("s1"), authntest.WithScope(acme)))
		notFound(t, "an impersonation of a non-member", rp)
		r.onlyCounted(notMember)
	})

	t.Run("an error reading the membership is 503 and logged at Error", func(t *testing.T) {
		r := newRig(t)
		r.members.fail(errBoom)
		rp := r.serve(r.as(acme))
		if rp.status != nethttp.StatusServiceUnavailable {
			t.Fatalf("answered %d, want 503", rp.status)
		}
		errs := r.logs.atLevel(levelError)
		if len(errs) != 1 || errs[0].attrs["outcome"] != "unavailable" {
			t.Fatalf("the Error records are %+v, want one with outcome unavailable", errs)
		}
		if !strings.Contains(errs[0].attrs["error"], errBoom.Error()) {
			t.Fatalf("the Error record does not carry the cause: %+v", errs[0])
		}
		r.onlyCounted(unavailable)
	})
	t.Run("an error is not a refusal: the body is the status alone", func(t *testing.T) {
		r := newRig(t)
		r.members.fail(errBoom)
		rp := r.serve(r.as(acme))
		if strings.Contains(rp.body, errBoom.Error()) || strings.Contains(rp.body, "membership") {
			t.Fatalf("the 503 tells the caller what failed: %q", rp.body)
		}
	})
}

// TestRowPermissionModel holds the part of the membership row that asks the
// permission model about a non-member.
func TestRowPermissionModel(t *testing.T) {
	t.Run("with no decider a non-member is refused 404", func(t *testing.T) {
		r := newRig(t)
		notFound(t, "a non-member, no decider", r.serve(r.as(acme, authntest.WithAccount(bob))))
	})
	t.Run("a decider that allows lets a non-member in, and is asked the action about the actor and tenant", func(t *testing.T) {
		r := newRig(t)
		d := r.withDecider(true)
		rp := r.serve(r.as(acme, authntest.WithAccount(bob)))
		if !rp.reached() {
			t.Fatalf("answered %d %q, want it admitted", rp.status, rp.body)
		}
		qs := d.questions()
		if len(qs) != 1 {
			t.Fatalf("the decider was asked %d times, want 1", len(qs))
		}
		q := qs[0]
		if q.Action != authn.ActionTenantAccess || q.Tenant != acme || q.Actor.Account() != bob || q.Target != "" || q.TargetAccount != "" {
			t.Fatalf("the question is %+v, want %s about %s in %s alone", q, authn.ActionTenantAccess, bob, acme)
		}
		r.onlyCounted(admittedByPolicy)
	})
	t.Run("a decider that refuses leaves the 404", func(t *testing.T) {
		r := newRig(t)
		r.withDecider(false)
		notFound(t, "a non-member the decider refused", r.serve(r.as(acme, authntest.WithAccount(bob))))
		r.onlyCounted(notMember)
	})
	t.Run("a member is not asked", func(t *testing.T) {
		r := newRig(t)
		d := r.withDecider(true)
		if rp := r.serve(r.as(acme)); !rp.reached() {
			t.Fatalf("answered %d, want it admitted", rp.status)
		}
		if len(d.questions()) != 0 {
			t.Fatal("the decider was asked about a member")
		}
	})
	t.Run("an impersonation is never asked: it has no way in beside membership", func(t *testing.T) {
		r := newRig(t)
		d := r.withDecider(true)
		rp := r.serve(r.as(acme, authntest.WithAccount(bob), authntest.WithActor("carol"),
			authntest.WithSession("s1"), authntest.WithScope(acme)))
		notFound(t, "an impersonation of a non-member", rp)
		if len(d.questions()) != 0 {
			t.Fatal("the decider was asked about an impersonation, which would let it past its tenant's membership")
		}
	})
	t.Run("an error from the decider is 503, not a refusal and not a permission", func(t *testing.T) {
		r := newRig(t)
		d := r.withDecider(true)
		d.err = errBoom
		rp := r.serve(r.as(acme, authntest.WithAccount(bob)))
		if rp.status != nethttp.StatusServiceUnavailable {
			t.Fatalf("answered %d, want 503", rp.status)
		}
		r.onlyCounted(unavailable)
	})
	t.Run("a panic in the decider is taken for an error", func(t *testing.T) {
		r := newRig(t)
		d := r.withDecider(true)
		d.panics = true
		rp := r.serve(r.as(acme, authntest.WithAccount(bob)))
		if rp.status != nethttp.StatusServiceUnavailable {
			t.Fatalf("answered %d, want 503", rp.status)
		}
	})
	t.Run("a non-member the decider admitted is not held to the tenant's requirements", func(t *testing.T) {
		r := newRig(t)
		r.withDecider(true)
		r.withProviders()
		store := r.withSettings()
		store.set(settings.ForTenant(acme), requireMFAKey, true)
		store.set(settings.ForTenant(acme), requireSSOKey, true)
		rp := r.serve(r.as(acme, authntest.WithAccount(bob)))
		if !rp.reached() {
			t.Fatalf("answered %d %q, want it admitted past both requirements", rp.status, rp.body)
		}
		r.onlyCounted(admittedByPolicy)
		if store.readsOf(requireMFAKey)+store.readsOf(requireSSOKey) != 0 {
			t.Fatal("the requirements were read for an account the permission model admitted")
		}
	})
}

// TestStrengthComesAfterMembership holds that a non-member is only ever told 404
// or let in, never asked to do what only a member would be asked.
func TestStrengthComesAfterMembership(t *testing.T) {
	r := newRig(t)
	r.withProviders()
	store := r.withSettings()
	store.set(settings.ForTenant(acme), requireMFAKey, true)
	store.set(settings.ForTenant(acme), requireSSOKey, true)

	notFound(t, "a non-member in a tenant that requires both", r.serve(r.as(acme, authntest.WithAccount(bob))))
	stepUp(t, "a member of that tenant", r.serve(r.as(acme)), acrMultiFactor)
	if got := store.readsOf(requireMFAKey) + store.readsOf(requireSSOKey); got == 0 {
		t.Fatal("the member's request read no requirement, so the case compared nothing")
	}
}

// TestRowExemption holds the row that spares an impersonation and a service
// account the tenant's strength requirements, and only them.
func TestRowExemption(t *testing.T) {
	withRequirements := func(t *testing.T) *rig {
		r := newRig(t)
		r.withProviders()
		store := r.withSettings()
		store.set(settings.ForTenant(acme), requireMFAKey, true)
		store.set(settings.ForTenant(acme), requireSSOKey, true)
		r.members.add("svc", acme)
		return r
	}
	t.Run("an impersonation is not asked for multi-factor or SSO", func(t *testing.T) {
		r := withRequirements(t)
		rp := r.serve(r.as(acme, authntest.WithAccount(alice), authntest.WithActor(bob),
			authntest.WithSession("s1"), authntest.WithScope(acme)))
		if !rp.reached() {
			t.Fatalf("answered %d %q, want it admitted", rp.status, rp.body)
		}
		r.onlyCounted(admitted)
	})
	t.Run("a service account is not asked for multi-factor or SSO", func(t *testing.T) {
		r := withRequirements(t)
		rp := r.serve(r.as(acme, authntest.WithAccount("svc"), authntest.WithMethods(authn.MethodClientSecret)))
		if !rp.reached() {
			t.Fatalf("answered %d %q, want it admitted", rp.status, rp.body)
		}
		r.onlyCounted(admitted)
	})
	t.Run("a service account still has to be a member", func(t *testing.T) {
		r := withRequirements(t)
		notFound(t, "a service account of another tenant", r.serve(r.as(acme,
			authntest.WithAccount("stranger"), authntest.WithMethods(authn.MethodClientSecret))))
	})
	t.Run("a member signed in with a password is asked", func(t *testing.T) {
		r := withRequirements(t)
		stepUp(t, "a password session", r.serve(r.as(acme)), acrMultiFactor)
	})
	t.Run("a client secret beside another method is still a service account's method set", func(t *testing.T) {
		r := withRequirements(t)
		rp := r.serve(r.as(acme, authntest.WithAccount("svc"),
			authntest.WithMethods(authn.MethodPassword, authn.MethodClientSecret)))
		if !rp.reached() {
			t.Fatalf("answered %d, want it admitted", rp.status)
		}
	})
}

// TestRowMultiFactor holds the multi-factor row.
func TestRowMultiFactor(t *testing.T) {
	multi := authntest.WithAssurance(authn.AssuranceMulti)
	t.Run("a single-factor member of a tenant that requires multi-factor gets 401 acr 2", func(t *testing.T) {
		r := newRig(t)
		r.withSettings().set(settings.ForTenant(acme), requireMFAKey, true)
		stepUp(t, "single factor", r.serve(r.as(acme)), acrMultiFactor)
		r.onlyCounted(stepUpMFA)
	})
	t.Run("a multi-factor member is admitted", func(t *testing.T) {
		r := newRig(t)
		r.withSettings().set(settings.ForTenant(acme), requireMFAKey, true)
		if rp := r.serve(r.as(acme, multi)); !rp.reached() {
			t.Fatalf("answered %d %q, want it admitted", rp.status, rp.body)
		}
		r.onlyCounted(admitted)
	})
	t.Run("the challenge names the scheme the identity authenticated with and no max_age", func(t *testing.T) {
		r := newRig(t)
		r.withSettings().set(settings.ForTenant(acme), requireMFAKey, true)
		for scheme, want := range map[string]string{"Cookie": "Cookie ", "Bearer": "Bearer ", "Basic": "Basic "} {
			rp := r.serve(r.as(acme, authntest.WithChallenge(scheme)))
			got := rp.header.Get("WWW-Authenticate")
			if !strings.HasPrefix(got, want+"error=") {
				t.Fatalf("with the scheme %s the challenge is %q, want it to start with %q", scheme, got, want)
			}
			if strings.Contains(got, "max_age") {
				t.Fatalf("the challenge states a freshness requirement this layer does not have: %q", got)
			}
			// The answer must not be cached for a request of another member.
			if rp.header.Get("Set-Cookie") != "" {
				t.Fatalf("the step-up answer sets a cookie: %v", rp.header)
			}
		}
	})
	t.Run("a requirement of another tenant is not this tenant's", func(t *testing.T) {
		r := newRig(t)
		r.members.add(alice, globex)
		r.withSettings().set(settings.ForTenant(globex), requireMFAKey, true)
		if rp := r.serve(r.as(acme)); !rp.reached() {
			t.Fatalf("answered %d, want it admitted: acme requires nothing", rp.status)
		}
		stepUp(t, "single factor in globex", r.serve(r.as(globex)), acrMultiFactor)
	})
	t.Run("the global row applies to a tenant with none of its own, and the tenant's row wins over it", func(t *testing.T) {
		r := newRig(t)
		r.members.add(alice, globex)
		store := r.withSettings()
		store.set(settings.Global(), requireMFAKey, true)
		store.set(settings.ForTenant(globex), requireMFAKey, false)
		stepUp(t, "acme falling back to the global row", r.serve(r.as(acme)), acrMultiFactor)
		if rp := r.serve(r.as(globex)); !rp.reached() {
			t.Fatalf("answered %d, want it admitted: globex's own row switches it off", rp.status)
		}
	})
	t.Run("with no settings the requirement is off", func(t *testing.T) {
		r := newRig(t)
		if rp := r.serve(r.as(acme)); !rp.reached() {
			t.Fatalf("answered %d, want it admitted", rp.status)
		}
	})
	t.Run("each switch is read for the tenant the request is in and the lifetime from the global layer", func(t *testing.T) {
		r := newRig(t)
		store := r.withSettings()
		r.serve(r.as(acme))
		seen := map[string]settings.Layer{}
		for _, read := range store.reads {
			seen[read.key] = read.layer
		}
		if seen[requireMFAKey] != settings.ForTenant(acme) || seen[requireSSOKey] != settings.ForTenant(acme) {
			t.Fatalf("the switches were read from %+v, want the layer of %s", seen, acme)
		}
		if seen[checkTTLKey] != settings.Global() {
			t.Fatalf("the lifetime was read from %+v, want the global layer", seen[checkTTLKey])
		}
	})
}

// TestRowSSO holds the SSO row and the emergency pass.
func TestRowSSO(t *testing.T) {
	requiring := func(t *testing.T) *rig {
		r := newRig(t)
		r.withProviders()
		r.withSettings().set(settings.ForTenant(acme), requireSSOKey, true)
		return r
	}
	t.Run("a session with no provider gets 401 acr sso", func(t *testing.T) {
		r := requiring(t)
		stepUp(t, "a password session", r.serve(r.as(acme)), acrSSO)
		r.onlyCounted(stepUpSSO)
	})
	t.Run("a tenant the platform exempts is admitted without a provider", func(t *testing.T) {
		// The platform's own tenant hosts the console and has no identity
		// provider of its own: holding it to the row would refuse every request
		// there, so the deployment names it as exempt.
		r := requiring(t)
		r.l.ssoExemptTenants = []string{acme}
		rp := r.serve(r.as(acme))
		if !rp.reached() {
			t.Fatalf("answered %d %q, want it admitted", rp.status, rp.body)
		}
		r.onlyCounted(admitted)
	})
	t.Run("the exemption names tenants, not every tenant", func(t *testing.T) {
		r := requiring(t)
		r.l.ssoExemptTenants = []string{"system"}
		stepUp(t, "a password session in a tenant the list does not name", r.serve(r.as(acme)), acrSSO)
	})
	t.Run("a session through the tenant's own provider is admitted", func(t *testing.T) {
		r := requiring(t)
		rp := r.serve(r.as(acme, authntest.WithProvider(idpOfAcme)))
		if !rp.reached() {
			t.Fatalf("answered %d %q, want it admitted", rp.status, rp.body)
		}
		r.onlyCounted(admitted)
	})
	t.Run("a session through another tenant's provider gets 401 acr sso", func(t *testing.T) {
		r := requiring(t)
		stepUp(t, "a provider of globex", r.serve(r.as(acme, authntest.WithProvider(idpOfGlobex))), acrSSO)
	})
	t.Run("a provider the registry does not know reads as not the tenant's", func(t *testing.T) {
		r := requiring(t)
		stepUp(t, "an unknown provider", r.serve(r.as(acme, authntest.WithProvider(idpUnknown))), acrSSO)
	})
	t.Run("with no registry of providers every request is refused", func(t *testing.T) {
		r := newRig(t)
		r.withSettings().set(settings.ForTenant(acme), requireSSOKey, true)
		stepUp(t, "no registry, a provider on the session", r.serve(r.as(acme, authntest.WithProvider(idpOfAcme))), acrSSO)
	})
	t.Run("an error asking the registry is 503", func(t *testing.T) {
		r := requiring(t)
		r.providers.err = errBoom
		rp := r.serve(r.as(acme, authntest.WithProvider(idpOfAcme)))
		if rp.status != nethttp.StatusServiceUnavailable {
			t.Fatalf("answered %d, want 503", rp.status)
		}
		r.onlyCounted(unavailable)
	})
	t.Run("a session that names no provider does not ask the registry", func(t *testing.T) {
		r := requiring(t)
		r.serve(r.as(acme))
		if n := r.providers.lookups(); n != 0 {
			t.Fatalf("the registry was asked %d times about a session with no provider", n)
		}
	})
	t.Run("the methods are not the criterion: a password session through a provider is admitted", func(t *testing.T) {
		r := requiring(t)
		rp := r.serve(r.as(acme, authntest.WithProvider(idpOfAcme), authntest.WithMethods(authn.MethodPassword, authn.MethodTOTP)))
		if !rp.reached() {
			t.Fatalf("answered %d, want it admitted", rp.status)
		}
	})
	t.Run("a requirement of another tenant is not this tenant's", func(t *testing.T) {
		r := newRig(t)
		r.withProviders()
		r.withSettings().set(settings.ForTenant(globex), requireSSOKey, true)
		if rp := r.serve(r.as(acme)); !rp.reached() {
			t.Fatalf("answered %d, want it admitted", rp.status)
		}
	})

	exempt := func(t *testing.T, row authn.Membership) *rig {
		r := requiring(t)
		row.Account, row.Tenant = alice, acme
		r.members.setRow(row)
		return r
	}
	t.Run("an exempt member needs only multi-factor", func(t *testing.T) {
		r := exempt(t, authn.Membership{Active: true, SSOExempt: true})
		rp := r.serve(r.as(acme, authntest.WithAssurance(authn.AssuranceMulti)))
		if !rp.reached() {
			t.Fatalf("answered %d %q, want it admitted", rp.status, rp.body)
		}
		r.onlyCounted(admitted)
	})
	t.Run("an exempt member short of multi-factor is answered as for multi-factor", func(t *testing.T) {
		r := exempt(t, authn.Membership{Active: true, SSOExempt: true})
		stepUp(t, "exempt, single factor", r.serve(r.as(acme)), acrMultiFactor)
		r.onlyCounted(stepUpMFA)
	})
	t.Run("a member who is not exempt is not spared", func(t *testing.T) {
		r := exempt(t, authn.Membership{Active: true})
		stepUp(t, "not exempt, multi-factor", r.serve(r.as(acme, authntest.WithAssurance(authn.AssuranceMulti))), acrSSO)
	})
	t.Run("a pass on a suspended row is no pass", func(t *testing.T) {
		r := exempt(t, authn.Membership{Active: false, SSOExempt: true})
		stepUp(t, "a suspended exempt row", r.serve(r.as(acme, authntest.WithAssurance(authn.AssuranceMulti))), acrSSO)
	})
	t.Run("an error reading the row is 503", func(t *testing.T) {
		r := exempt(t, authn.Membership{Active: true, SSOExempt: true})
		r.members.failRow(errBoom)
		rp := r.serve(r.as(acme))
		if rp.status != nethttp.StatusServiceUnavailable {
			t.Fatalf("answered %d, want 503", rp.status)
		}
	})
	t.Run("the row is read only for a session that failed the requirement", func(t *testing.T) {
		r := exempt(t, authn.Membership{Active: true, SSOExempt: true})
		r.serve(r.as(acme, authntest.WithProvider(idpOfAcme)))
		if _, n := r.members.reads(); n != 0 {
			t.Fatalf("the membership row was read %d times for a session that met the requirement", n)
		}
	})
}

// TestEffectiveAssurance holds the level the multi-factor requirement compares:
// a multi-factor level an identity provider declared holds only in the tenant
// that provider belongs to.
func TestEffectiveAssurance(t *testing.T) {
	declared := func(provider string) []authntest.Option {
		return []authntest.Option{
			authntest.WithAssurance(authn.AssuranceMulti), authntest.WithIDPAssured(true), authntest.WithProvider(provider),
		}
	}
	requiring := func(t *testing.T) *rig {
		r := newRig(t)
		r.withProviders()
		r.withSettings().set(settings.ForTenant(acme), requireMFAKey, true)
		return r
	}
	t.Run("a declaration of the tenant's own provider counts", func(t *testing.T) {
		r := requiring(t)
		if rp := r.serve(r.as(acme, declared(idpOfAcme)...)); !rp.reached() {
			t.Fatalf("answered %d %q, want it admitted", rp.status, rp.body)
		}
	})
	t.Run("a declaration of another tenant's provider counts as one factor", func(t *testing.T) {
		r := requiring(t)
		stepUp(t, "globex's provider declared multi-factor", r.serve(r.as(acme, declared(idpOfGlobex)...)), acrMultiFactor)
		r.onlyCounted(stepUpMFA)
	})
	t.Run("a provider nobody knows is nobody's", func(t *testing.T) {
		r := requiring(t)
		stepUp(t, "an unknown provider", r.serve(r.as(acme, declared(idpUnknown)...)), acrMultiFactor)
	})
	t.Run("with no registry a declaration counts for nothing", func(t *testing.T) {
		r := newRig(t)
		r.withSettings().set(settings.ForTenant(acme), requireMFAKey, true)
		stepUp(t, "no registry", r.serve(r.as(acme, declared(idpOfAcme)...)), acrMultiFactor)
	})
	t.Run("an error asking the registry is 503", func(t *testing.T) {
		r := requiring(t)
		r.providers.err = errBoom
		if rp := r.serve(r.as(acme, declared(idpOfAcme)...)); rp.status != nethttp.StatusServiceUnavailable {
			t.Fatalf("answered %d, want 503", rp.status)
		}
	})
	t.Run("a level the tenant's own factors reached needs no registry", func(t *testing.T) {
		r := requiring(t)
		rp := r.serve(r.as(acme, authntest.WithAssurance(authn.AssuranceMulti), authntest.WithProvider(idpOfGlobex)))
		if !rp.reached() {
			t.Fatalf("answered %d, want it admitted", rp.status)
		}
		if n := r.providers.lookups(); n != 0 {
			t.Fatalf("the registry was asked %d times about a level that is not a declaration", n)
		}
	})
	t.Run("both requirements ask the registry once", func(t *testing.T) {
		r := newRig(t)
		r.withProviders()
		store := r.withSettings()
		store.set(settings.ForTenant(acme), requireMFAKey, true)
		store.set(settings.ForTenant(acme), requireSSOKey, true)
		if rp := r.serve(r.as(acme, declared(idpOfAcme)...)); !rp.reached() {
			t.Fatalf("answered %d, want it admitted", rp.status)
		}
		if n := r.providers.lookups(); n != 1 {
			t.Fatalf("the registry was asked %d times in one request, want 1", n)
		}
	})
}

// TestSettingsFailures holds the direction of a switch that cannot be read:
// refuse, because the default it would fall back to is the permissive value.
func TestSettingsFailures(t *testing.T) {
	for _, key := range []string{requireMFAKey, requireSSOKey} {
		t.Run(key+" unreadable is 503", func(t *testing.T) {
			r := newRig(t)
			r.withProviders()
			r.withSettings().failKey(key, errBoom)
			rp := r.serve(r.as(acme, authntest.WithAssurance(authn.AssuranceMulti), authntest.WithProvider(idpOfAcme)))
			if rp.status != nethttp.StatusServiceUnavailable {
				t.Fatalf("answered %d, want 503", rp.status)
			}
			r.onlyCounted(unavailable)
		})
	}
	t.Run("an unreadable lifetime does not refuse, and reads as the default", func(t *testing.T) {
		r := newRig(t)
		r.withSettings().failKey(checkTTLKey, errBoom)
		if rp := r.serve(r.as(acme)); !rp.reached() {
			t.Fatalf("answered %d, want it admitted", rp.status)
		}
	})
	t.Run("a tenant the settings cannot name a layer for is refused, not a panic", func(t *testing.T) {
		r := newRig(t)
		r.withSettings()
		odd := "ac me"
		r.members.add(alice, odd)
		rp := r.serve(r.as(odd))
		if rp.status != nethttp.StatusServiceUnavailable {
			t.Fatalf("answered %d, want 503", rp.status)
		}
	})
}

// TestAnAdmittedRequestWritesNothing holds that admission is quiet: only a
// refusal is a line in the log.
func TestAnAdmittedRequestWritesNothing(t *testing.T) {
	r := newRig(t)
	r.serve(r.as(acme))
	if got := r.logs.all(); len(got) != 0 {
		t.Fatalf("admitting a member wrote %+v, want nothing", got)
	}
	r.serve(r.as(acme, authntest.WithAccount(bob)))
	if got := r.logs.atLevel(levelInfo); len(got) != 1 {
		t.Fatalf("refusing a non-member wrote %+v, want one Info line", r.logs.all())
	}
}

// TestNoTenantIdentifierTheSettingsRefuseReachesThem holds the guard in front of
// the settings: an identifier outside their domain is a panic there, and the
// layer must answer it with a refusal instead. The resolution layer writes only
// identifiers the roster carries, so this is the layer not trusting that.
func TestNoTenantIdentifierTheSettingsRefuseReachesThem(t *testing.T) {
	for id, want := range map[string]bool{
		"": false, "ac me": false, "acme\n": false, "a\x00b": false, " ": false,
		"acme": true, "t-1": true, "租户": true,
	} {
		if got := nameableLayer(id); got != want {
			t.Errorf("nameableLayer(%q) = %v, want %v", id, got, want)
		}
	}
	r := newRig(t)
	r.withSettings()
	j := &judging{layer: r.l, ctx: r.base(), id: authntestIdentity(t), tenant: ""}
	if _, err := j.flag(requireMFAKey); err == nil {
		t.Fatal("a switch was read for the empty tenant, which the settings answer with a panic")
	}
}
