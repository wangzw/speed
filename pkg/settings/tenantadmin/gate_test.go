package tenantadmin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// gateWriteTarget is a write the settings module accepts once it is reached.
const gateWriteTarget = base + "/tenant.admission/require-mfa"

// declareGateItem declares the item a write through gateWriteTarget acts on, so
// the shape checks past the decision let an allowed write land.
func declareGateItem(r *rig) {
	r.store.declared = admissionDeclared()
}

// gateWrites is every write route, with what a case sends to it.
var gateWrites = []struct {
	name   string
	method string
	target string
	body   any
}{
	{"the set", http.MethodPut, gateWriteTarget, map[string]any{"value": true}},
	{"the delete", http.MethodDelete, gateWriteTarget, nil},
}

// TestAWriteFromAnUnverifiedRegistrationIsRefusedBeforeAnythingRuns holds the
// registration gate on both write routes: an account whose self-registration has
// not proved an email address is answered 403 contact-required, the guard is
// never asked, the host's seam is not asked, no audit row lands and no setting is
// touched.
func TestAWriteFromAnUnverifiedRegistrationIsRefusedBeforeAnythingRuns(t *testing.T) {
	for _, route := range gateWrites {
		t.Run(route.name, func(t *testing.T) {
			r := newRig(t)
			declareGateItem(r)
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
			if got := r.writeSeam.askedTenants(); len(got) != 0 {
				t.Errorf("the write seam was asked about %v, want no call", got)
			}
			if calls := r.store.recorded(); len(calls) != 0 {
				t.Errorf("the settings module was called %v, want no call", calls)
			}
			if rows := r.audit.entries(); len(rows) != 0 {
				t.Errorf("the audit recorded %d row(s), want none", len(rows))
			}
		})
	}
}

// TestAReadIsNotGated holds that the registration gate covers writes alone: an
// account in the same pending state reads the management face, and its state is
// not even read.
func TestAReadIsNotGated(t *testing.T) {
	r := newRig(t)
	r.signup.answer(authn.SignupPending, nil)

	w := r.do(http.MethodGet, base, nil)
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

// TestAVerifiedOrGivenAccountWrites holds the exemption of an account that
// self-registered and proved an email address, and of one created outside
// self-registration: both reach the settings module.
func TestAVerifiedOrGivenAccountWrites(t *testing.T) {
	for name, state := range map[string]authn.SignupState{
		"verified": authn.SignupVerified,
		"given":    authn.SignupNone,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			declareGateItem(r)
			r.signup.answer(state, nil)

			w := r.do(http.MethodPut, gateWriteTarget, map[string]any{"value": true})
			if w.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204: %s", w.Code, w.Body.String())
			}
			if calls := r.store.recorded(); !hasWriteOp(calls, "Set") {
				t.Errorf("the settings module recorded %v, want the write", calls)
			}
		})
	}
}

// TestAServiceAccountAndAStaticSubjectAreExempt holds the two callers the gate
// never consults: a service account, whose kind is the client-secret method, and
// a static-table subject, which has no account row at all.
func TestAServiceAccountAndAStaticSubjectAreExempt(t *testing.T) {
	t.Run("a service account", func(t *testing.T) {
		r := newRig(t)
		declareGateItem(r)
		r.signup.answer(authn.SignupPending, nil)

		w := r.do(http.MethodPut, gateWriteTarget, map[string]any{"value": true},
			authntest.WithMethods(authn.MethodClientSecret))
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204: %s", w.Code, w.Body.String())
		}
		if reads := r.signup.readCount(); reads != 0 {
			t.Errorf("a service account's state was read %d time(s), want none", reads)
		}
	})

	t.Run("a static subject", func(t *testing.T) {
		r := newRig(t)
		declareGateItem(r)
		r.signup.answer(authn.SignupPending, nil)

		w := r.do(http.MethodPut, gateWriteTarget, map[string]any{"value": true},
			authntest.WithAccount(authn.StaticPrefix+"ops"), authntest.WithBackend("static"))
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204: %s", w.Code, w.Body.String())
		}
		if reads := r.signup.readCount(); reads != 0 {
			t.Errorf("a static subject's state was read %d time(s), want none", reads)
		}
	})
}

// TestAnUnreadableSignupStateIs503 holds the failure direction: a state that
// cannot be read is a 503 and the write is not carried out, never read as
// verified.
func TestAnUnreadableSignupStateIs503(t *testing.T) {
	r := newRig(t)
	declareGateItem(r)
	r.signup.answer(authn.SignupNone, errors.New("the database is down"))

	w := r.do(http.MethodPut, gateWriteTarget, map[string]any{"value": true})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", w.Code, w.Body.String())
	}
	if asked := r.guard.askedFor(); len(asked) != 0 {
		t.Errorf("the guard was asked for %v, want no decision", asked)
	}
	if calls := r.store.recorded(); len(calls) != 0 {
		t.Errorf("the settings module was called %v, want no call", calls)
	}
}

// TestTheRegistrationGatePrecedesThePermissionDecision holds the order: a pending
// caller that would also be refused by the guard — no permission, or an outage —
// is answered contact-required, and the guard is never asked.
func TestTheRegistrationGatePrecedesThePermissionDecision(t *testing.T) {
	for name, script := range map[string]func(*rig){
		"no permission": func(r *rig) { r.guard.allow = false },
		"an outage":     func(r *rig) { r.guard.err = errors.New("the policy engine is down") },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			declareGateItem(r)
			r.signup.answer(authn.SignupPending, nil)
			script(r)

			w := r.do(http.MethodPut, gateWriteTarget, map[string]any{"value": true})
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

// TestTheGateIsDerivedFromTheMethod walks the registered route set and holds the
// write decision to the method: every non-read route reads the acting account's
// signup state, and every read route does not.
func TestTheGateIsDerivedFromTheMethod(t *testing.T) {
	r := newRig(t)
	routes := r.p.routes()
	for i := range routes {
		name := routes[i].method + " " + routes[i].path
		method := routes[i].method
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.signup.answer(authn.SignupPending, nil)
			route := r.p.routes()[i]

			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(method, route.path, nil)
			c.Request = c.Request.WithContext(authntest.Context(t, c.Request.Context()))
			c.Set(realmKey, "api")
			route.handler(c)

			want := 0
			if method != http.MethodGet {
				want = 1
			}
			if got := r.signup.readCount(); got != want {
				t.Errorf("the %s route read the signup state %d time(s), want %d", method, got, want)
			}
		})
	}
}

// TestTheGateRefusesBeforeTheQueryShape holds the order: a pending caller's
// layer or tenant query parameter is answered contact-required, not the
// capability's bad-request. Without the gate in front of the permission decision
// and the shape refusal the answer would be a 400.
func TestTheGateRefusesBeforeTheQueryShape(t *testing.T) {
	r := newRig(t)
	declareGateItem(r)
	r.signup.answer(authn.SignupPending, nil)

	w := r.do(http.MethodPut, base+"/tenant.admission/require-mfa?layer=global", map[string]any{"value": true})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 contact-required: %s", w.Code, w.Body.String())
	}
	if got := problemOf(t, w).Type; got != authn.ContactRequiredProblemType {
		t.Errorf("problem type = %q, want %q", got, authn.ContactRequiredProblemType)
	}
	if calls := r.store.recorded(); len(calls) != 0 {
		t.Errorf("the settings module was called %v, want no call", calls)
	}
}

// TestTheGateReadsTheActorNotTheImpersonationTarget holds the subject of the
// read: under an impersonation the state is the operator's (the actor), not the
// target's.
func TestTheGateReadsTheActorNotTheImpersonationTarget(t *testing.T) {
	r := newRig(t)
	declareGateItem(r)
	r.signup.answer(authn.SignupPending, nil)

	w := r.do(http.MethodPut, gateWriteTarget, map[string]any{"value": true},
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

// TestAnAccountWithNoSignupRecordWrites holds the sentinel's direction: an
// account whose row is not there reads as no self-registration, so the write
// reaches the settings module rather than answering 403 or 503.
func TestAnAccountWithNoSignupRecordWrites(t *testing.T) {
	r := newRig(t)
	declareGateItem(r)
	r.signup.answer(authn.SignupNone, authn.ErrAccountNotFound)

	w := r.do(http.MethodPut, gateWriteTarget, map[string]any{"value": true})
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", w.Code, w.Body.String())
	}
	if asked := r.guard.askedFor(); len(asked) != 1 {
		t.Errorf("the guard was asked for %v, want the one decision", asked)
	}
	if calls := r.store.recorded(); !hasWriteOp(calls, "Set") {
		t.Errorf("the settings module recorded %v, want the write", calls)
	}
}
