package invite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	speedhttp "github.com/vislake/speed/pkg/http"
)

const pathAdmin = "/api/v1/tenants/tenant-a/invitations"

// request sends one request through the Mounter with the body given. The caller
// sets the identity first with Mounter.As or Mounter.AsAnonymous.
func (h *harness) request(method, path string, body any) *httptest.ResponseRecorder {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("encoding the body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return h.mounter.Do(req)
}

func (h *harness) asAdmin() { h.mounter.As(authntest.WithAccount(adminID)) }

func (h *harness) rowCount() int64 {
	h.t.Helper()
	var n int64
	if err := h.handle.Model(&invitationRow{}).Count(&n).Error; err != nil {
		h.t.Fatalf("counting rows: %v", err)
	}
	return n
}

func TestIssueRouteAnswers201AndAuditsBeforeTheAction(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var answered struct {
		Invitation AuthnInviteInvitation `json:"invitation"`
		Token      string                `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if answered.Token == "" || answered.Invitation.Status != "pending" {
		t.Errorf("answer = %+v, want a pending invitation and a token", answered)
	}
	rows := h.audit.entries()
	if len(rows) == 0 {
		t.Fatal("no audit row was written for a write")
	}
	if rows[0].Action != actionIssue {
		t.Errorf("audit action = %q, want %q", rows[0].Action, actionIssue)
	}
	dec, ok := h.decider.last()
	if !ok || dec.Action != authn.ActionMembershipManage || dec.Tenant != "tenant-a" {
		t.Errorf("decision = %+v, want membership.manage in tenant-a", dec)
	}
}

func TestIssueRouteWithoutAnIdentityIs401(t *testing.T) {
	h := newHarness(t)
	h.mounter.AsAnonymous()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if h.decider.count() != 0 {
		t.Errorf("the Decider was asked %d times for a request with no identity, want none", h.decider.count())
	}
	if h.rowCount() != 0 {
		t.Errorf("a row was written for a request with no identity")
	}
}

func TestIssueRouteRefusedByTheDeciderWritesNothing(t *testing.T) {
	h := newHarness(t)
	h.decider.allow = false
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if h.rowCount() != 0 {
		t.Errorf("a row was written for a refused request")
	}
	if len(h.audit.entries()) != 0 {
		t.Errorf("an audit row was written for a refused request")
	}
}

func TestIssueRouteWithoutADeciderIs403(t *testing.T) {
	h := newHarness(t)
	h.p.decider = nil
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 when no Decider is present", w.Code)
	}
	if h.rowCount() != 0 {
		t.Errorf("a row was written with no Decider present")
	}
}

func TestIssueRouteWithAFailedAuditDoesNotAct(t *testing.T) {
	h := newHarness(t)
	h.audit.fails(errors.New("the audit store is down"))
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when the mandatory audit does not land", w.Code)
	}
	if h.rowCount() != 0 {
		t.Errorf("a row was written although the mandatory audit did not land")
	}
}

func TestIssueRouteRefusesAMalformedAddress(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "not-an-address"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an address that is not well formed", w.Code)
	}
	if h.rowCount() != 0 {
		t.Errorf("a row was written for a refused address")
	}
}

func TestListAndRevokeRoutes(t *testing.T) {
	h := newHarness(t)
	issued := h.issue("tenant-a", "alice@example.com")
	h.asAdmin()

	w := h.request(http.MethodGet, pathAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if len(h.audit.entries()) != 0 {
		t.Errorf("a read wrote %d audit rows, want none", len(h.audit.entries()))
	}

	w = h.request(http.MethodDelete, "/api/v1/tenants/tenant-a/invitations/"+issued.Invitation.ID, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204: %s", w.Code, w.Body.String())
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusRevoked {
		t.Errorf("status after revoke = %d, want revoked", got)
	}
}

func TestRevokingAnAcceptedRouteAnswers409(t *testing.T) {
	h := newHarness(t)
	issued := h.issue("tenant-a", "alice@example.com")
	if _, err := h.p.Accept(context.Background(), AcceptInvite{Token: issued.Token, Username: "alice"}); err != nil {
		t.Fatalf("accepting: %v", err)
	}
	h.asAdmin()
	w := h.request(http.MethodDelete, "/api/v1/tenants/tenant-a/invitations/"+issued.Invitation.ID, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 revoking an accepted invitation", w.Code)
	}
}

func TestAcceptFaceIsPublicAndTokenAddressed(t *testing.T) {
	h := newHarness(t)
	h.accounts.put("alice@example.com", "acct-alice")
	issued := h.issue("tenant-a", "alice@example.com")

	// The GET carries no identity: the route is public.
	h.mounter.AsAnonymous()
	w := h.request(http.MethodGet, "/api/v1/auth/invitations/"+issued.Token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("accept view status = %d, want 200: %s", w.Code, w.Body.String())
	}

	// The POST redeems with no identity too.
	w = h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]string{"display_name": "Alice"})
	if w.Code != http.StatusCreated {
		t.Fatalf("accept status = %d, want 201: %s", w.Code, w.Body.String())
	}
	if h.members.count != 1 {
		t.Errorf("memberships written = %d, want one", h.members.count)
	}
	// A second acceptance of the same token is refused.
	w = h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]string{"display_name": "Alice"})
	if w.Code != http.StatusConflict {
		t.Fatalf("second accept status = %d, want 409", w.Code)
	}
}

// TestAnIncompleteCleanupOnAcceptanceAnswers503 holds the acceptance face's
// mapping of the propagated cleanup failure: the account exists and the
// invitation is back to pending, and the caller is told the dependency could
// not finish rather than that the token is a conflict.
func TestAnIncompleteCleanupOnAcceptanceAnswers503(t *testing.T) {
	h := newHarness(t)
	h.mounter.AsAnonymous()
	h.admin.err = authn.ErrCleanupIncomplete
	issued := h.issue("tenant-a", "zoe@example.com")

	w := h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token,
		map[string]string{"username": "zoe", "display_name": "Zoe"})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("accept status = %d, want 503: %s", w.Code, w.Body.String())
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusPending {
		t.Errorf("status after the 503 = %d, want back to pending", got)
	}
}

// TestAnUnknownObjectIsAnsweredWithABare404 holds the answer the fragment
// declares for both faces: a missing invitation is a 404 with no problem
// document, not the problem shape the other refusals use.
func TestAnUnknownObjectIsAnsweredWithABare404(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()
	w := h.request(http.MethodDelete, "/api/v1/tenants/tenant-a/invitations/no-such-id", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("revoke status = %d, want 404", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("the 404 carries the body %q, want none", w.Body.String())
	}

	h.mounter.AsAnonymous()
	w = h.request(http.MethodGet, "/api/v1/auth/invitations/spd_inv_unknown", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("accept view status = %d, want 404", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("the 404 carries the body %q, want none", w.Body.String())
	}
}

// TestABodyOfAnotherMediaTypeIs415 holds the answer the entry point writes when
// the request body is not the declared media type. The hand-written fragment
// omitted it, although the handler it described has always answered it.
func TestABodyOfAnotherMediaTypeIs415(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()
	req := httptest.NewRequest(http.MethodPost, pathAdmin,
		strings.NewReader(`{"email":"alice@example.com"}`))
	req.Header.Set("Content-Type", "text/plain")
	w := h.mounter.Do(req)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", w.Code)
	}
	if h.rowCount() != 0 {
		t.Errorf("a row was written for a body of another media type")
	}
}

// TestAcceptRefusesAnEmptyBody holds the one request the entry point would
// otherwise read as the input's zero value: the route has always required a
// body, and a body of "{}" is still a body. A declared length of zero is the
// request that carries none.
func TestAcceptRefusesAnEmptyBody(t *testing.T) {
	h := newHarness(t)
	h.accounts.put("alice@example.com", "acct-alice")
	issued := h.issue("tenant-a", "alice@example.com")

	h.mounter.AsAnonymous()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, nil)
	req.Header.Set("Content-Type", "application/json")
	w := h.mounter.Do(req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an empty body", w.Code)
	}
	if h.members.count != 0 {
		t.Errorf("a membership was written for an empty body")
	}
}

// TestAcceptBindsAnUnknownLengthBody holds the request the declared-length check
// must not refuse: a chunked body, or an HTTP/2 one without Content-Length, that
// carries valid JSON. Its length is unknown until the body is read, and the
// binder reads it like any other body.
func TestAcceptBindsAnUnknownLengthBody(t *testing.T) {
	h := newHarness(t)
	h.accounts.put("alice@example.com", "acct-alice")
	issued := h.issue("tenant-a", "alice@example.com")

	h.mounter.AsAnonymous()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token,
		strings.NewReader(`{"display_name":"Alice"}`))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	w := h.mounter.Do(req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 for an unknown-length body: %s", w.Code, w.Body.String())
	}
	if h.members.count != 1 {
		t.Errorf("memberships written = %d, want one", h.members.count)
	}
}

// TestRoutesMountUnderAConfiguredBasePath holds the mount translation against a
// configured base path: the declaration is fixed before any configuration
// exists, so the relative path is derived from the default base, and a
// configured base moves every route without moving the contract.
func TestRoutesMountUnderAConfiguredBasePath(t *testing.T) {
	h := newHarness(t)
	configured := mounttest.New(t, mounttest.WithBase("/identity"))
	h.p.mounter = configured

	var panicked any
	func() {
		defer func() { panicked = recover() }()
		h.p.mountRoutes()
	}()
	if panicked != nil {
		t.Fatalf("mounting under a configured base path panicked: %v", panicked)
	}

	for _, testCase := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/identity/:tenant/invitations"},
		{http.MethodGet, "/identity/:tenant/invitations"},
		{http.MethodDelete, "/identity/:tenant/invitations/:id"},
		{http.MethodGet, "/identity/invitations/:token"},
		{http.MethodPost, "/identity/invitations/:token"},
	} {
		if _, ok := configured.Registered(testCase.method, testCase.path); !ok {
			t.Errorf("%s %s was not mounted", testCase.method, testCase.path)
		}
	}
}

// TestAcceptTakesADeclaredLengthEmptyObjectBody holds the other side of the
// empty-body refusal: a body of "{}" with a declared length is a body and is
// accepted, as it has always been.
func TestAcceptTakesADeclaredLengthEmptyObjectBody(t *testing.T) {
	h := newHarness(t)
	h.accounts.put("alice@example.com", "acct-alice")
	issued := h.issue("tenant-a", "alice@example.com")

	h.mounter.AsAnonymous()
	w := h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]string{})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 for a declared-length {} body: %s", w.Code, w.Body.String())
	}
	if h.members.count != 1 {
		t.Errorf("memberships written = %d, want one", h.members.count)
	}
}

// TestAManagementHandlerAnswersItsOwn401WithTheChallenge holds the fault a
// handler returns where the gateway is not in front of it: the 401 carries the
// capability's challenge, which is the header the declaration puts on every 401.
func TestAManagementHandlerAnswersItsOwn401WithTheChallenge(t *testing.T) {
	h := newHarness(t)
	_, err := h.p.handleIssue(context.Background(), issueInput{
		Tenant: "tenant-a", Email: "alice@example.com",
	})
	var fault speedhttp.Fault
	if !errors.As(err, &fault) {
		t.Fatalf("error = %v, want a fault", err)
	}
	if fault.Status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", fault.Status)
	}
	want := mount.UnauthorizedChallenge(h.mounter.Endpoint())
	if len(fault.Headers) != 1 || fault.Headers[0].Name != "WWW-Authenticate" ||
		fault.Headers[0].Value != want {
		t.Errorf("headers = %+v, want WWW-Authenticate: %q", fault.Headers, want)
	}
}

// TestManagementRoutesAdmitAnImpersonatingIdentity holds that the management
// face is a tenant-domain capability: an impersonating identity reaches the
// handler, which does not answer impersonation-restricted, and the same
// membership.manage decision is asked in the path's tenant through the effective
// account.
func TestManagementRoutesAdmitAnImpersonatingIdentity(t *testing.T) {
	h := newHarness(t)
	h.mounter.As(
		authntest.WithAccount("acct-target"),
		authntest.WithActor(adminID),
		authntest.WithSession("session-1"),
		authntest.WithScope("tenant-a"),
	)
	w := h.request(http.MethodGet, pathAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("an impersonating list answered %d, want 200: %s", w.Code, w.Body.String())
	}
	dec, ok := h.decider.last()
	if !ok {
		t.Fatal("the Decider was not asked")
	}
	if dec.Action != authn.ActionMembershipManage || dec.Tenant != "tenant-a" {
		t.Errorf("decision = %+v, want membership.manage in tenant-a", dec)
	}
	if !dec.Actor.Impersonating() || dec.Actor.Account() != "acct-target" || dec.Actor.Actor() != adminID {
		t.Errorf("Decision.Actor = account %q actor %q impersonating %t; want the impersonated target decided through the operator",
			dec.Actor.Account(), dec.Actor.Actor(), dec.Actor.Impersonating())
	}
}

// TestManagementRoutesAreImpersonable holds the registration split: the
// management routes (tenant-scoped) are registered Impersonable, and the public
// acceptance routes are not. Acceptance is reached by the token alone, so it has
// no identity to impersonate.
func TestManagementRoutesAreImpersonable(t *testing.T) {
	h := newHarness(t)
	seen := 0
	for _, reg := range h.mounter.Registrations() {
		management := strings.Contains(reg.Path, "/tenants/")
		if management {
			seen++
		}
		if reg.Impersonable != management {
			t.Errorf("%s (%s): Impersonable = %t, want %t", reg.Path, reg.Kind, reg.Impersonable, management)
		}
	}
	if seen == 0 {
		t.Fatal("no tenant-scoped management route was mounted")
	}
}
