package admin

import (
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
)

// The problem type a write from an unverified self-registration answers with is
// the authentication capability's code, read from that capability's exported
// constant rather than restated here: a rename on that side has to reach this
// surface, and a copy would let the two drift silently.

// writeRouteNames lists the write routes in a fixed order, so a failing case is
// readable and the whole surface is covered.
var writeRouteNames = []string{
	"the role write", "the role delete", "the grant write",
	"the grant delete", "the binding write", "the binding delete",
}

// writeRoutes is every write route, each bound the way the module mounts it, so
// a case can hold that the registration gate covers the whole write surface and
// not one route. Each handler closes over p, so a case asks for the routes of
// the product it drives.
func writeRoutes(p *product) map[string]struct {
	handler gin.HandlerFunc
	method  string
	target  string
	params  gin.Params
	body    any
} {
	return map[string]struct {
		handler gin.HandlerFunc
		method  string
		target  string
		params  gin.Params
		body    any
	}{
		"the role write": {
			rolePutRoute(p), http.MethodPut, "/api/v1/system/tenants/t1/roles/r1",
			params("tenant", "t1", "role", "r1"),
			roleBody{Description: "Runs the editor."},
		},
		"the role delete": {
			writeRoute(p, roleDeleteOperation.Handle(p.handleRoleDelete)), http.MethodDelete,
			"/api/v1/system/tenants/t1/roles/r1", params("tenant", "t1", "role", "r1"), nil,
		},
		"the grant write": {
			writeRoute(p, grantPutOperation.Handle(p.handleGrantPut)), http.MethodPut,
			"/api/v1/system/tenants/t1/roles/r1/grants/authn.account.read",
			params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead)), nil,
		},
		"the grant delete": {
			writeRoute(p, grantDeleteOperation.Handle(p.handleGrantDelete)), http.MethodDelete,
			"/api/v1/system/tenants/t1/roles/r1/grants/authn.account.read",
			params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead)), nil,
		},
		"the binding write": {
			writeRoute(p, bindingPutOperation.Handle(p.handleBindingPut)), http.MethodPut,
			"/api/v1/system/tenants/t1/bindings/acct-1/editor",
			params("tenant", "t1", "account", "acct-1", "role", "editor"), nil,
		},
		"the binding delete": {
			writeRoute(p, bindingDeleteOperation.Handle(p.handleBindingDelete)), http.MethodDelete,
			"/api/v1/system/tenants/t1/bindings/acct-1/editor",
			params("tenant", "t1", "account", "acct-1", "role", "editor"), nil,
		},
	}
}

// newGateRig builds a rig whose catalog holds the permissions every write route
// names and whose manager answers every write.
func newGateRig(t *testing.T) *rig {
	t.Helper()
	return newRig(t, authz.PermissionAccountRead)
}

// TestAWriteFromAnUnverifiedRegistrationIsRefusedBeforeAnythingRuns holds the
// registration gate on the whole write surface: an account whose
// self-registration has not proved an email address is answered 403
// contact-required, the guard is never asked, no audit row lands and no policy
// table is touched. The same requests as a verified account do reach the
// manager, which is what makes the refusal the gate's and not some other
// check's.
func TestAWriteFromAnUnverifiedRegistrationIsRefusedBeforeAnythingRuns(t *testing.T) {
	for _, name := range writeRouteNames {
		t.Run(name, func(t *testing.T) {
			r := newGateRig(t)
			r.signup.answer(authn.SignupPending, nil)
			route := writeRoutes(r.p)[name]

			w := r.serve(route.handler, route.method, route.target, route.params, route.body)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
			}
			if got := decodeProblem(t, w.Body.String()); got.Type != authn.ContactRequiredProblemType {
				t.Errorf("problem type = %q, want %q", got.Type, authn.ContactRequiredProblemType)
			}
			if asked := r.guard.askedFor(); len(asked) != 0 {
				t.Errorf("the guard was asked for %v, want no decision", asked)
			}
			if calls := r.manager.recorded(); len(calls) != 0 {
				t.Errorf("the manager was called %v, want no call", calls)
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
	r := newGateRig(t)
	r.signup.answer(authn.SignupPending, nil)

	w := r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet,
		"/api/v1/system/tenants/t1/roles", params("tenant", "t1"), nil)
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
// self-registration: both reach the manager.
func TestAVerifiedOrGivenAccountWrites(t *testing.T) {
	for name, state := range map[string]authn.SignupState{
		"verified": authn.SignupVerified,
		"given":    authn.SignupNone,
	} {
		t.Run(name, func(t *testing.T) {
			r := newGateRig(t)
			r.signup.answer(state, nil)

			w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
				"/api/v1/system/tenants/t1/bindings/acct-1/editor",
				params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)
			if w.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204: %s", w.Code, w.Body.String())
			}
			if calls := r.manager.recorded(); len(calls) != 1 || calls[0].op != "PutBinding" {
				t.Errorf("the manager recorded %v, want the binding write", calls)
			}
		})
	}
}

// TestAServiceAccountAndAStaticSubjectAreExempt holds the two callers the gate
// never consults: a service account, whose kind is the client-secret method, and
// a static-table subject, which has no account row at all. Each writes while the
// scripted state is pending, and neither causes a state read — the exemption is
// decided on the identity, not inferred from an answer.
func TestAServiceAccountAndAStaticSubjectAreExempt(t *testing.T) {
	t.Run("a service account", func(t *testing.T) {
		r := newGateRig(t)
		r.signup.answer(authn.SignupPending, nil)

		w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
			"/api/v1/system/tenants/t1/bindings/acct-1/editor",
			params("tenant", "t1", "account", "acct-1", "role", "editor"), nil,
			authntest.WithMethods(authn.MethodClientSecret))
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204: %s", w.Code, w.Body.String())
		}
		if reads := r.signup.readCount(); reads != 0 {
			t.Errorf("a service account's state was read %d time(s), want none", reads)
		}
	})

	t.Run("a static subject", func(t *testing.T) {
		r := newGateRig(t)
		r.signup.answer(authn.SignupPending, nil)

		w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
			"/api/v1/system/tenants/t1/bindings/acct-1/editor",
			params("tenant", "t1", "account", "acct-1", "role", "editor"), nil,
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
	r := newGateRig(t)
	r.signup.answer(authn.SignupNone, errors.New("the database is down"))

	w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
		"/api/v1/system/tenants/t1/bindings/acct-1/editor",
		params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", w.Code, w.Body.String())
	}
	if asked := r.guard.askedFor(); len(asked) != 0 {
		t.Errorf("the guard was asked for %v, want no decision", asked)
	}
	if calls := r.manager.recorded(); len(calls) != 0 {
		t.Errorf("the manager was called %v, want no call", calls)
	}
}

// TestTheRegistrationGatePrecedesThePermissionDecision holds the order: a pending
// caller that would also be refused by the guard — no permission, or an outage —
// is answered contact-required, and the guard is never asked. Without the gate
// the answer would be the guard's 403 or 503, so the case distinguishes the two
// placements.
func TestTheRegistrationGatePrecedesThePermissionDecision(t *testing.T) {
	for name, script := range map[string]func(*rig){
		"no permission": func(r *rig) { r.guard.allow = false },
		"an outage":     func(r *rig) { r.guard.err = errors.New("the policy engine is down") },
	} {
		t.Run(name, func(t *testing.T) {
			r := newGateRig(t)
			r.signup.answer(authn.SignupPending, nil)
			script(r)

			w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
				"/api/v1/system/tenants/t1/bindings/acct-1/editor",
				params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 contact-required: %s", w.Code, w.Body.String())
			}
			if got := decodeProblem(t, w.Body.String()); got.Type != authn.ContactRequiredProblemType {
				t.Errorf("problem type = %q, want %q", got.Type, authn.ContactRequiredProblemType)
			}
			if asked := r.guard.askedFor(); len(asked) != 0 {
				t.Errorf("the guard was asked for %v, want the gate to answer first", asked)
			}
		})
	}
}

// TestAnAccountWithNoSignupRecordWrites holds the sentinel's direction: an
// account whose row is not there reads as no self-registration, so the write
// reaches the manager rather than answering 403 or 503. The manager being called
// is what makes the pass-through the gate's decision and not an earlier refusal's.
func TestAnAccountWithNoSignupRecordWrites(t *testing.T) {
	r := newGateRig(t)
	r.signup.answer(authn.SignupNone, authn.ErrAccountNotFound)

	w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
		"/api/v1/system/tenants/t1/bindings/acct-1/editor",
		params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", w.Code, w.Body.String())
	}
	if asked := r.guard.askedFor(); len(asked) != 1 {
		t.Errorf("the guard was asked for %v, want the one decision", asked)
	}
	if calls := r.manager.recorded(); len(calls) != 1 || calls[0].op != "PutBinding" {
		t.Errorf("the manager recorded %v, want the binding write", calls)
	}
}

// TestTheReadRoutesAreNotGated covers every read route of the surface: each
// answers 200, reads no signup state and asks the guard once. The permissions
// read is included because it is the one read with no tenant in its path, and the
// grants, bindings and binding-of-account reads cover the tenant-scoped ones.
func TestTheReadRoutesAreNotGated(t *testing.T) {
	for _, route := range []struct {
		name   string
		target string
	}{
		{"the permissions", "/api/v1/system/permissions"},
		{"the grants", "/api/v1/system/tenants/t1/roles/r1/grants"},
		{"the bindings", "/api/v1/system/tenants/t1/bindings"},
		{"the binding of an account", "/api/v1/system/tenants/t1/bindings/acct-1"},
	} {
		t.Run(route.name, func(t *testing.T) {
			r := newGateRig(t)
			r.signup.answer(authn.SignupPending, nil)

			w := r.send(r.engine(), http.MethodGet, route.target)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
			}
			if reads := r.signup.readCount(); reads != 0 {
				t.Errorf("the read route read the signup state %d time(s), want none", reads)
			}
			if asked := r.guard.askedFor(); len(asked) != 1 {
				t.Errorf("the read asked the guard for %v, want the route's permission", asked)
			}
		})
	}
}

// TestTheGateRefusesBeforeTheBodyShape holds the order over the role write, the
// one route whose body is prescreened before the entry point binds it: a pending
// caller's malformed body is answered contact-required, not this module's
// bad-request. Without the gate in front of the prescreen the answer would be a
// 400 with the bad-request type, so the case distinguishes the two placements.
func TestTheGateRefusesBeforeTheBodyShape(t *testing.T) {
	r := newGateRig(t)
	r.signup.answer(authn.SignupPending, nil)

	w := r.serveRaw(rolePutRoute(r.p), http.MethodPut,
		"/api/v1/system/tenants/t1/roles/r1", params("tenant", "t1", "role", "r1"), "{")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 contact-required: %s", w.Code, w.Body.String())
	}
	if got := decodeProblem(t, w.Body.String()); got.Type != authn.ContactRequiredProblemType {
		t.Errorf("problem type = %q, want %q", got.Type, authn.ContactRequiredProblemType)
	}
	if calls := r.manager.recorded(); len(calls) != 0 {
		t.Errorf("the manager was called %v, want no call", calls)
	}
}

// TestTheGateReadsTheActorNotTheImpersonationTarget holds the subject of the
// read: under an impersonation the state is the operator's (the actor), not the
// target's. The two identities are distinct in the fixture, so reading the wrong
// one is visible in the account the fake recorded.
func TestTheGateReadsTheActorNotTheImpersonationTarget(t *testing.T) {
	r := newGateRig(t)
	r.signup.answer(authn.SignupPending, nil)

	w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
		"/api/v1/system/tenants/t1/bindings/acct-1/editor",
		params("tenant", "t1", "account", "acct-1", "role", "editor"), nil,
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

// TestAnAccountCreatedOutsideSelfRegistrationIsExemptAfterTheRead holds the
// exemption's two bases apart. A service account and a static subject are exempt
// on the identity before any read (see TestAServiceAccountAndAStaticSubjectAreExempt);
// an account created outside self-registration is exempt on its state, which
// means the read happens and answers SignupNone. This case pins the second: the
// write reaches the manager and the state was read once. The three management
// faces decide the identity exemptions on the actor's kind and method; that is a
// deliberate departure from authn.admin, which reads Accounts.Get to tell a
// service account from a user and gates in the account's own request path.
func TestAnAccountCreatedOutsideSelfRegistrationIsExemptAfterTheRead(t *testing.T) {
	r := newGateRig(t)
	r.signup.answer(authn.SignupNone, nil)

	w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
		"/api/v1/system/tenants/t1/bindings/acct-1/editor",
		params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", w.Code, w.Body.String())
	}
	if reads := r.signup.readCount(); reads != 1 {
		t.Errorf("the state was read %d time(s), want once: an account outside self-registration is exempt on its state, not on its kind", reads)
	}
	if calls := r.manager.recorded(); len(calls) != 1 || calls[0].op != "PutBinding" {
		t.Errorf("the manager recorded %v, want the binding write", calls)
	}
}
