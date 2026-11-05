package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/tenant"
)

// The two host-supplied seams the write routes take up. The compiler holds the
// stubs to the interfaces, and each keeps the calls it answered, so a case can
// hold what the handler passed on and that an action it refused never reached
// the seam.

// writeGuardCall is one question the write guard answered.
type writeGuardCall struct {
	id tenant.ID
	op tenant.WriteOp
}

// stubWriteGuard answers from what a case scripted and records every question.
type stubWriteGuard struct {
	mu     sync.Mutex
	denied bool
	err    error
	calls  []writeGuardCall
	// during runs inside the answer, before the call is recorded, so a case can
	// hold what had already happened when the guard was asked.
	during func()
}

func (g *stubWriteGuard) Denied(_ context.Context, id tenant.ID, op tenant.WriteOp) (bool, error) {
	if g.during != nil {
		g.during()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, writeGuardCall{id: id, op: op})
	return g.denied, g.err
}

func (g *stubWriteGuard) recorded() []writeGuardCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]writeGuardCall(nil), g.calls...)
}

func (g *stubWriteGuard) refuse(err error) { g.mu.Lock(); g.err = err; g.mu.Unlock() }

// provisionCall is one registration the provisioner was asked for.
type provisionCall struct {
	tenant  tenant.Tenant
	creator string
}

// provisionedAt is the creation instant the stand-in provisioner records on the
// row it stores. The real provisioner writes the roster's own UTC instant; a
// fixed value lets a case hold that the answer is read back from the roster
// rather than built from the request, which would carry the zero instant.
var provisionedAt = time.Date(2026, 7, 8, 9, 10, 11, 0, time.UTC)

// stubProvisioner records every registration and answers from what a case
// scripted. roster, when set, receives the row a successful registration
// wrote, standing in for the roster the real provisioner fills through
// Catalog.Create.
type stubProvisioner struct {
	mu     sync.Mutex
	err    error
	calls  []provisionCall
	roster *stubCatalog
	// during runs inside the call, before it is recorded.
	during func()
}

func (p *stubProvisioner) Provision(_ context.Context, t tenant.Tenant, creator string) error {
	if p.during != nil {
		p.during()
	}
	p.mu.Lock()
	p.calls = append(p.calls, provisionCall{tenant: t, creator: creator})
	err := p.err
	p.mu.Unlock()
	if err != nil {
		return err
	}
	if p.roster != nil {
		written := t
		written.CreatedAt = provisionedAt
		p.roster.store(written)
	}
	return nil
}

func (p *stubProvisioner) recorded() []provisionCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provisionCall(nil), p.calls...)
}

func (p *stubProvisioner) fail(err error) { p.mu.Lock(); p.err = err; p.mu.Unlock() }

// TestTheCreateGoesThroughTheProvisionerNotTheRoster holds the registration
// route's seam: opening a tenant is the provisioner's action, because a tenant
// and its first administrator have to come into being together. The roster's
// Create is not called from here, and the mandatory audit row has already landed
// when the provisioner runs.
func TestTheCreateGoesThroughTheProvisionerNotTheRoster(t *testing.T) {
	r := newRig(t)
	auditRowsAtProvision := -1
	r.provisioner.during = func() { auditRowsAtProvision = len(r.audit.entries()) }

	w := r.do(http.MethodPost, pathTenants, createBody{ID: "t_acme", Alias: "acme"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", w.Code, w.Body.String())
	}
	calls := r.provisioner.recorded()
	if len(calls) != 1 {
		t.Fatalf("the provisioner was asked %d time(s), want once: %+v", len(calls), calls)
	}
	want := tenant.Tenant{ID: "t_acme", Alias: "acme", Status: tenant.StatusActive}
	if calls[0].tenant != want {
		t.Errorf("the provisioner got %+v, want %+v", calls[0].tenant, want)
	}
	if calls[0].creator != string(authntest.DefaultAccount) {
		t.Errorf("creator = %q, want the acting account %q", calls[0].creator, authntest.DefaultAccount)
	}
	for _, call := range r.catalog.recorded() {
		if call.op == "Create" {
			t.Errorf("the management surface called Catalog.Create directly: %+v", call)
		}
	}
	if auditRowsAtProvision != 1 {
		t.Errorf("the provisioner ran with %d audit row(s), want the mandatory row already landed", auditRowsAtProvision)
	}
}

// TestTheProvisionerIsGivenTheActingAccount holds the subject the creator is
// taken from: under an impersonation the operator whose hands are on the request
// becomes the first administrator, not the account the request is taken to be
// from. The two are distinct in the fixture, so reading the wrong one shows.
func TestTheProvisionerIsGivenTheActingAccount(t *testing.T) {
	r := newRig(t)
	w := r.do(http.MethodPost, pathTenants, createBody{ID: "t_acme", Alias: "acme"},
		authntest.WithAccount("target-account"),
		authntest.WithActor("operator-account"),
		authntest.WithSession("session-1"),
		authntest.WithScope("acme"),
	)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", w.Code, w.Body.String())
	}
	calls := r.provisioner.recorded()
	if len(calls) != 1 || calls[0].creator != "operator-account" {
		t.Fatalf("the provisioner was asked %+v, want the actor operator-account and not the target", calls)
	}
}

// TestTheProvisionerRefusalsMapLikeTheRoster keeps the closed mapping over the
// new seam: an identifier or alias the roster refuses is a 409 with the
// capability's conflict type, and any other failure is a 503 with no type. The
// failed row is written beside the mandatory one, as a roster refusal closes a
// write.
func TestTheProvisionerRefusalsMapLikeTheRoster(t *testing.T) {
	for name, tc := range map[string]struct {
		err    error
		status int
		typ    string
	}{
		"an identifier the table holds": {tenant.ErrTenantExists, http.StatusConflict, typePrefix + conflictCode},
		"an alias another holds":        {tenant.ErrAliasConflict, http.StatusConflict, typePrefix + conflictCode},
		"an unknown failure":            {errors.New("policy store down"), http.StatusServiceUnavailable, ""},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.provisioner.fail(tc.err)
			w := r.do(http.MethodPost, pathTenants, createBody{ID: "t_acme", Alias: "acme"})
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.status, w.Body.String())
			}
			if tc.typ != "" {
				if got := problemOf(t, w).Type; got != tc.typ {
					t.Errorf("problem type = %q, want %q", got, tc.typ)
				}
			}
			rows := r.audit.entries()
			if len(rows) != 2 || rows[0].Outcome != outcomeSuccess || rows[1].Outcome != outcomeFailed {
				t.Errorf("audit rows = %+v, want the mandatory row and a linked failed row", rows)
			}
		})
	}
}

// TestTheStatusRouteAsksTheWriteGuardBeforeTheAuditAndTheRoster holds the guard
// on the status route: a tenant the host declares protected refuses the write
// with 403 and the capability's protected type, and the action writes no audit
// row and reaches no roster. The guard is asked after the shape checks — the
// case reaches it, so the shape was accepted — and before the mandatory audit,
// which is empty when the guard answers.
func TestTheStatusRouteAsksTheWriteGuardBeforeTheAuditAndTheRoster(t *testing.T) {
	r := newRig(t)
	r.writeGuard.denied = true
	auditRowsAtGuard := -1
	r.writeGuard.during = func() { auditRowsAtGuard = len(r.audit.entries()) }

	w := r.do(http.MethodPost, pathTenants+"/t_acme/status", statusBody{Status: "disabled"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", w.Code, w.Body.String())
	}
	if got := problemOf(t, w).Type; got != typePrefix+protectedCode {
		t.Errorf("problem type = %q, want %q", got, typePrefix+protectedCode)
	}
	asked := r.writeGuard.recorded()
	if len(asked) != 1 || asked[0].id != "t_acme" || asked[0].op != tenant.WriteDisable {
		t.Fatalf("the write guard was asked %+v, want one WriteDisable of t_acme", asked)
	}
	if auditRowsAtGuard != 0 {
		t.Errorf("the guard was asked with %d audit row(s) already written, want none", auditRowsAtGuard)
	}
	if len(r.audit.entries()) != 0 {
		t.Errorf("a write the guard refused wrote an audit row: %+v", r.audit.entries())
	}
	for _, call := range r.catalog.recorded() {
		if call.op == "SetStatus" {
			t.Error("a write the guard refused reached SetStatus")
		}
	}
}

// TestAWriteGuardThatCannotAnswerIsA503 holds the failure direction: an answer
// that cannot be read is a 503, never a pass, and the action is not carried out.
func TestAWriteGuardThatCannotAnswerIsA503(t *testing.T) {
	r := newRig(t)
	r.writeGuard.refuse(errors.New("the policy store is down"))

	w := r.do(http.MethodPost, pathTenants+"/t_acme/status", statusBody{Status: "disabled"})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", w.Code, w.Body.String())
	}
	if len(r.audit.entries()) != 0 {
		t.Errorf("a write whose guard failed wrote an audit row: %+v", r.audit.entries())
	}
	for _, call := range r.catalog.recorded() {
		if call.op == "SetStatus" {
			t.Error("a write whose guard failed reached SetStatus")
		}
	}
	if !strings.Contains(r.logs.String(), "policy store is down") {
		t.Error("the cause of the 503 did not reach the log")
	}
}

// TestTheWriteGuardIsAskedForADisableAndNotForAnEnable holds the guard's range:
// a disable is one of the operator writes a host protects, so the status route
// asks it there; an enable is not, so the route asks nothing and the write
// lands. A protected tenant's enable is therefore not blocked by the guard,
// while its disable is.
func TestTheWriteGuardIsAskedForADisableAndNotForAnEnable(t *testing.T) {
	t.Run("a disable asks the guard", func(t *testing.T) {
		r := newRig(t)
		w := r.do(http.MethodPost, pathTenants+"/t_acme/status", statusBody{Status: "disabled"})
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body %s)", w.Code, w.Body.String())
		}
		asked := r.writeGuard.recorded()
		if len(asked) != 1 || asked[0].id != "t_acme" || asked[0].op != tenant.WriteDisable {
			t.Fatalf("the write guard was asked %+v, want one WriteDisable of t_acme", asked)
		}
	})

	t.Run("an enable asks nothing", func(t *testing.T) {
		r := newRig(t)
		w := r.do(http.MethodPost, pathTenants+"/t_acme/status", statusBody{Status: "active"})
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body %s)", w.Code, w.Body.String())
		}
		if asked := r.writeGuard.recorded(); len(asked) != 0 {
			t.Fatalf("an enable asked the write guard %+v, want nothing", asked)
		}
	})
}

// TestTheWriteGuardIsAskedAfterTheShapeChecks holds the guard behind the shape:
// a status outside the closed set and an identifier outside the domain are 400s
// and the guard is not asked, so a malformed request never reaches the host's
// rule.
func TestTheWriteGuardIsAskedAfterTheShapeChecks(t *testing.T) {
	for name, tc := range map[string]struct {
		target string
		body   statusBody
	}{
		"a status outside the closed set": {pathTenants + "/t_acme/status", statusBody{Status: "pending"}},
		"the deleted state":               {pathTenants + "/t_acme/status", statusBody{Status: "deleted"}},
		"a blank identifier":              {pathTenants + "/t%20acme/status", statusBody{Status: "disabled"}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.writeGuard.denied = true
			w := r.do(http.MethodPost, tc.target, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
			}
			if asked := r.writeGuard.recorded(); len(asked) != 0 {
				t.Errorf("a shape-refused request asked the write guard: %+v", asked)
			}
		})
	}
}

// TestTheWriteGuardIsNotAskedOnAnyOtherRoute holds the guard's scope: only the
// status route writes a transition a host protects. A registration goes through
// the provisioner and a profile write names the profile columns, and neither
// asks the guard.
func TestTheWriteGuardIsNotAskedOnAnyOtherRoute(t *testing.T) {
	cases := map[string]struct {
		method string
		target string
		body   any
	}{
		"list":    {http.MethodGet, pathTenants, nil},
		"get":     {http.MethodGet, pathTenants + "/t_acme", nil},
		"create":  {http.MethodPost, pathTenants, createBody{ID: "t_acme", Alias: "acme"}},
		"alias":   {http.MethodPatch, pathTenants + "/t_acme", aliasBody{Alias: "acme-labs"}},
		"profile": {http.MethodPatch, pathTenants + "/t_acme/profile", fullProfile()},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.catalog.found = true
			r.writeGuard.denied = true
			r.do(tc.method, tc.target, tc.body)
			if asked := r.writeGuard.recorded(); len(asked) != 0 {
				t.Errorf("the write guard was asked on the %s route: %+v", name, asked)
			}
		})
	}
}
