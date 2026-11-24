package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/settings"
)

const gateWriteTarget = "/api/v1/system/settings/app/mode?layer=global"

func declareGateItem(r *rig) {
	r.store.declared = []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}
}

var gateWrites = []struct {
	name   string
	method string
	target string
	body   any
}{
	{"the set", http.MethodPut, gateWriteTarget, map[string]any{"value": "on"}},
	{"the delete", http.MethodDelete, gateWriteTarget, nil},
}

// TestAWriteFromAnUnverifiedRegistrationIsRefusedBeforeAnythingRuns checks that both write routes
// refuse an unverified registration 403 contact-required before anything runs.
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
			if calls := r.store.recorded(); len(calls) != 0 {
				t.Errorf("the settings module was called %v, want no call", calls)
			}
			if rows := r.audit.entries(); len(rows) != 0 {
				t.Errorf("the audit recorded %d row(s), want none", len(rows))
			}
		})
	}
}

// TestAReadIsNotGated checks that the gate covers writes alone.
func TestAReadIsNotGated(t *testing.T) {
	r := newRig(t)
	r.signup.answer(authn.SignupPending, nil)

	w := r.do(http.MethodGet, "/api/v1/system/settings?layer=global", nil)
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

// TestAVerifiedOrGivenAccountWrites checks that a verified account and one created outside self-registration both write.
func TestAVerifiedOrGivenAccountWrites(t *testing.T) {
	for name, state := range map[string]authn.SignupState{
		"verified": authn.SignupVerified,
		"given":    authn.SignupNone,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			declareGateItem(r)
			r.signup.answer(state, nil)

			w := r.do(http.MethodPut, gateWriteTarget, map[string]any{"value": "on"})
			if w.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204: %s", w.Code, w.Body.String())
			}
			if calls := r.store.recorded(); len(calls) != 1 || calls[0].op != "Set" {
				t.Errorf("the settings module recorded %v, want the write", calls)
			}
		})
	}
}

// TestAServiceAccountAndAStaticSubjectAreExempt checks that the two exempt
// identities write while the scripted state is pending and cause no state read.
func TestAServiceAccountAndAStaticSubjectAreExempt(t *testing.T) {
	t.Run("a service account", func(t *testing.T) {
		r := newRig(t)
		declareGateItem(r)
		r.signup.answer(authn.SignupPending, nil)

		w := r.do(http.MethodPut, gateWriteTarget, map[string]any{"value": "on"},
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

		w := r.do(http.MethodPut, gateWriteTarget, map[string]any{"value": "on"},
			authntest.WithAccount(authn.StaticPrefix+"ops"), authntest.WithBackend("static"))
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204: %s", w.Code, w.Body.String())
		}
		if reads := r.signup.readCount(); reads != 0 {
			t.Errorf("a static subject's state was read %d time(s), want none", reads)
		}
	})
}

// TestAnUnreadableSignupStateIs503 checks that an unreadable state is a 503 and the write does not run.
func TestAnUnreadableSignupStateIs503(t *testing.T) {
	r := newRig(t)
	declareGateItem(r)
	r.signup.answer(authn.SignupNone, errors.New("the database is down"))

	w := r.do(http.MethodPut, gateWriteTarget, map[string]any{"value": "on"})
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

// TestTheRegistrationGatePrecedesThePermissionDecision checks that a pending
// caller gets contact-required and the guard is never asked.
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

			w := r.do(http.MethodPut, gateWriteTarget, map[string]any{"value": "on"})
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

// TestTheGateIsDerivedFromTheMethod checks that every non-read route reads the actor's signup state and every read route does not.
func TestTheGateIsDerivedFromTheMethod(t *testing.T) {
	routes := newRig(t).p.routes()
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
			c.Set(realmKey, "public")
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

// TestAnAccountWithNoSignupRecordWrites checks that an account with no row reads
// as no self-registration and the write reaches the store.
func TestAnAccountWithNoSignupRecordWrites(t *testing.T) {
	r := newRig(t)
	declareGateItem(r)
	r.signup.answer(authn.SignupNone, authn.ErrAccountNotFound)

	w := r.do(http.MethodPut, gateWriteTarget, map[string]any{"value": "on"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", w.Code, w.Body.String())
	}
	if asked := r.guard.askedFor(); len(asked) != 1 {
		t.Errorf("the guard was asked for %v, want the one decision", asked)
	}
	if calls := r.store.recorded(); len(calls) != 1 || calls[0].op != "Set" {
		t.Errorf("the settings module recorded %v, want the write", calls)
	}
}

// TestTheNamespaceReadIsNotGated checks that the namespace read answers 200 and reads no signup state.
func TestTheNamespaceReadIsNotGated(t *testing.T) {
	r := newRig(t)
	declareGateItem(r)
	r.signup.answer(authn.SignupPending, nil)

	w := r.do(http.MethodGet, "/api/v1/system/settings/app?layer=global", nil)
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

// TestTheGateRefusesBeforeTheLayerShape checks that a pending caller gets contact-required before the layer shape is judged.
func TestTheGateRefusesBeforeTheLayerShape(t *testing.T) {
	r := newRig(t)
	declareGateItem(r)
	r.signup.answer(authn.SignupPending, nil)

	w := r.do(http.MethodPut, "/api/v1/system/settings/app/mode?layer=bogus", map[string]any{"value": "on"})
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

// TestTheGateReadsTheActorNotTheImpersonationTarget checks that the state read is the actor's, not the impersonation target's.
func TestTheGateReadsTheActorNotTheImpersonationTarget(t *testing.T) {
	r := newRig(t)
	declareGateItem(r)
	r.signup.answer(authn.SignupPending, nil)

	w := r.do(http.MethodPut, gateWriteTarget, map[string]any{"value": "on"},
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

// TestAnAccountCreatedOutsideSelfRegistrationIsExemptAfterTheRead checks that such an account is exempt on its state after one read.
func TestAnAccountCreatedOutsideSelfRegistrationIsExemptAfterTheRead(t *testing.T) {
	r := newRig(t)
	declareGateItem(r)
	r.signup.answer(authn.SignupNone, nil)

	w := r.do(http.MethodPut, gateWriteTarget, map[string]any{"value": "on"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", w.Code, w.Body.String())
	}
	if reads := r.signup.readCount(); reads != 1 {
		t.Errorf("the state was read %d time(s), want once: an account outside self-registration is exempt on its state, not on its kind", reads)
	}
	if calls := r.store.recorded(); len(calls) != 1 || calls[0].op != "Set" {
		t.Errorf("the settings module recorded %v, want the write", calls)
	}
}
