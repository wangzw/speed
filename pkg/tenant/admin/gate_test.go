package admin

import (
	"errors"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/tenant"
)

var gateWrites = []struct {
	name   string
	method string
	target string
	body   any
}{
	{
		"the registration", http.MethodPost, "/api/v1/system/tenants",
		map[string]any{"name": "acme"},
	},
	{
		"the profile update", http.MethodPatch, "/api/v1/system/tenants/acme/profile",
		map[string]any{
			"display_name": "Acme", "description": "", "contact_email": "", "website": "",
		},
	},
	{
		"the owner transfer request", http.MethodPost, "/api/v1/system/tenants/acme/owner/requests",
		map[string]any{"account": "acc-2"},
	},
	{
		"the deletion", http.MethodDelete, "/api/v1/system/tenants/acme", nil,
	},
	{
		"the status change", http.MethodPost, "/api/v1/system/tenants/acme/status",
		map[string]any{"status": tenant.StatusDisabled.String()},
	},
}

func TestAWriteFromAnUnverifiedRegistrationIsRefusedBeforeAnythingRuns(t *testing.T) {
	for _, route := range gateWrites {
		t.Run(route.name, func(t *testing.T) {
			r := newRig(t)
			r.signup.answer(authn.SignupPending, nil)

			w := r.do(route.method, route.target, route.body)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
			}
			if got := problemOf(t, w).Type; got != authn.ContactRequiredProblemType {
				t.Errorf("problem type = %q, want %q", got, authn.ContactRequiredProblemType)
			}
			if asked := r.guard.askedFor(); len(asked) != 0 {
				t.Errorf("the guard was asked for %v, want no decision", asked)
			}
			if calls := r.catalog.recorded(); len(calls) != 0 {
				t.Errorf("the roster was called %v, want no call", calls)
			}
			if rows := r.audit.entries(); len(rows) != 0 {
				t.Errorf("the audit recorded %d row(s), want none", len(rows))
			}
		})
	}
}

func TestAReadIsNotGated(t *testing.T) {
	r := newRig(t)
	r.signup.answer(authn.SignupPending, nil)

	w := r.do(http.MethodGet, "/api/v1/system/tenants", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if reads := r.signup.readCount(); reads != 0 {
		t.Errorf("the read route read the signup state %d time(s), want none", reads)
	}
	if asked := r.guard.askedFor(); len(asked) != 1 {
		t.Errorf("the read asked the guard for %v, want the route's permission", asked)
	}
}

func TestAVerifiedOrGivenAccountWrites(t *testing.T) {
	for name, state := range map[string]authn.SignupState{
		"verified": authn.SignupVerified,
		"given":    authn.SignupNone,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.signup.answer(state, nil)

			w := r.do(http.MethodPost, "/api/v1/system/tenants",
				map[string]any{"name": "acme"})
			if w.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
			}
			if calls := r.provisioner.recorded(); len(calls) != 1 {
				t.Errorf("the provisioner recorded %v, want the registration", calls)
			}
		})
	}
}

func TestAServiceAccountAndAStaticSubjectAreExempt(t *testing.T) {
	t.Run("a service account", func(t *testing.T) {
		r := newRig(t)
		r.signup.answer(authn.SignupPending, nil)

		w := r.do(http.MethodPost, "/api/v1/system/tenants",
			map[string]any{"name": "acme"},
			authntest.WithMethods(authn.MethodClientSecret))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
		}
		if reads := r.signup.readCount(); reads != 0 {
			t.Errorf("a service account's state was read %d time(s), want none", reads)
		}
	})

	t.Run("a static subject", func(t *testing.T) {
		r := newRig(t)
		r.signup.answer(authn.SignupPending, nil)

		w := r.do(http.MethodPost, "/api/v1/system/tenants",
			map[string]any{"name": "acme"},
			authntest.WithAccount(authn.StaticPrefix+"ops"), authntest.WithBackend("static"))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
		}
		if reads := r.signup.readCount(); reads != 0 {
			t.Errorf("a static subject's state was read %d time(s), want none", reads)
		}
	})
}

func TestAnUnreadableSignupStateIs503(t *testing.T) {
	r := newRig(t)
	r.signup.answer(authn.SignupNone, errors.New("the database is down"))

	w := r.do(http.MethodPost, "/api/v1/system/tenants", map[string]any{"name": "acme"})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", w.Code, w.Body.String())
	}
	if asked := r.guard.askedFor(); len(asked) != 0 {
		t.Errorf("the guard was asked for %v, want no decision", asked)
	}
	if calls := r.catalog.recorded(); len(calls) != 0 {
		t.Errorf("the roster was called %v, want no call", calls)
	}
}

func TestTheRegistrationGatePrecedesThePermissionDecision(t *testing.T) {
	for name, script := range map[string]func(*rig){
		"no permission": func(r *rig) { r.guard.allow = false },
		"an outage":     func(r *rig) { r.guard.err = errors.New("the policy engine is down") },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.signup.answer(authn.SignupPending, nil)
			script(r)

			w := r.do(http.MethodPost, "/api/v1/system/tenants",
				map[string]any{"name": "acme"})
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 contact-required: %s", w.Code, w.Body.String())
			}
			if got := problemOf(t, w).Type; got != authn.ContactRequiredProblemType {
				t.Errorf("problem type = %q, want %q", got, authn.ContactRequiredProblemType)
			}
			if asked := r.guard.askedFor(); len(asked) != 0 {
				t.Errorf("the guard was asked for %v, want the gate to answer first", asked)
			}
		})
	}
}

func TestAnAccountWithNoSignupRecordWrites(t *testing.T) {
	r := newRig(t)
	r.signup.answer(authn.SignupNone, authn.ErrAccountNotFound)

	w := r.do(http.MethodPost, "/api/v1/system/tenants", map[string]any{"name": "acme"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	if asked := r.guard.askedFor(); len(asked) != 1 {
		t.Errorf("the guard was asked for %v, want the one decision", asked)
	}
	if calls := r.provisioner.recorded(); len(calls) != 1 {
		t.Errorf("the provisioner recorded %v, want the registration", calls)
	}
}

func TestTheTenantReadIsNotGated(t *testing.T) {
	r := newRig(t)
	r.signup.answer(authn.SignupPending, nil)
	r.catalog.held = tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}
	r.catalog.found = true

	w := r.do(http.MethodGet, "/api/v1/system/tenants/acme", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if reads := r.signup.readCount(); reads != 0 {
		t.Errorf("the read route read the signup state %d time(s), want none", reads)
	}
	if asked := r.guard.askedFor(); len(asked) != 1 {
		t.Errorf("the read asked the guard for %v, want the route's permission", asked)
	}
}

func TestTheGateRefusesBeforeTheBodyShape(t *testing.T) {
	r := newRig(t)
	r.signup.answer(authn.SignupPending, nil)

	w := r.doRaw(http.MethodPost, "/api/v1/system/tenants", "{")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 contact-required: %s", w.Code, w.Body.String())
	}
	if got := problemOf(t, w).Type; got != authn.ContactRequiredProblemType {
		t.Errorf("problem type = %q, want %q", got, authn.ContactRequiredProblemType)
	}
	if calls := r.catalog.recorded(); len(calls) != 0 {
		t.Errorf("the roster was called %v, want no call", calls)
	}
}

func TestTheGateReadsTheActorNotTheImpersonationTarget(t *testing.T) {
	r := newRig(t)
	r.signup.answer(authn.SignupPending, nil)

	w := r.do(http.MethodPost, "/api/v1/system/tenants", map[string]any{"name": "acme"},
		authntest.WithAccount("target-account"),
		authntest.WithActor("operator-account"),
		authntest.WithSession("session-1"),
		authntest.WithScope("acme"),
	)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 contact-required: %s", w.Code, w.Body.String())
	}
	if got := r.signup.lastAccount(); got != "operator-account" {
		t.Errorf("the state was read for %q, want the actor operator-account and not the target", got)
	}
}

func TestAnAccountCreatedOutsideSelfRegistrationIsExemptAfterTheRead(t *testing.T) {
	r := newRig(t)
	r.signup.answer(authn.SignupNone, nil)

	w := r.do(http.MethodPost, "/api/v1/system/tenants", map[string]any{"name": "acme"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	if reads := r.signup.readCount(); reads != 1 {
		t.Errorf("the state was read %d time(s), want once: an account outside self-registration is exempt on its state, not on its kind", reads)
	}
	if calls := r.provisioner.recorded(); len(calls) != 1 {
		t.Errorf("the provisioner recorded %v, want the registration", calls)
	}
}
