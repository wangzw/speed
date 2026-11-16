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
	"time"

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

// TestTheAcceptanceRoutesKeepTheirDeclaredAccessLevels holds the registration
// split the design depends on: the acceptance read is Public so an anonymous
// visitor reaches the landing page even with a stale credential, the acceptance
// write is Optional so a caller who has one carries an identity, and the
// eligibility read is Guarded because its answer is about an account. A read
// turned Optional would answer 401 to an expired cookie and break the landing
// page; an eligibility read turned Optional would let the handler answer an
// anonymous caller it has nothing to say about.
func TestTheAcceptanceRoutesKeepTheirDeclaredAccessLevels(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		method string
		path   string
		want   mounttest.Kind
	}{
		{http.MethodGet, "/api/v1/auth/invitations/:token", mounttest.Public},
		{http.MethodPost, "/api/v1/auth/invitations/:token", mounttest.Optional},
		{http.MethodGet, "/api/v1/auth/invitations/:token/eligibility", mounttest.Guarded},
	}
	for _, c := range cases {
		reg, ok := h.mounter.Registered(c.method, c.path)
		if !ok {
			t.Errorf("%s %s is not registered", c.method, c.path)
			continue
		}
		if reg.Kind != c.want {
			t.Errorf("%s %s is %s, want %s", c.method, c.path, reg.Kind, c.want)
		}
	}
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

// TestIssueRouteAnswersAnUnreadableBodyAsBadRequest holds the problem type of
// the binding refusal. The route answers several 400s, and the declaration
// keeps one response per status, so the type a request refused before the
// handler runs must be the one on that response rather than the last 400 the
// operation names.
func TestIssueRouteAnswersAnUnreadableBodyAsBadRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		body io.Reader
	}{
		{name: "no body", body: nil},
		{name: "not a JSON object", body: strings.NewReader("not json")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.asAdmin()
			req := httptest.NewRequest(http.MethodPost, pathAdmin, tc.body)
			req.Header.Set("Content-Type", "application/json")
			w := h.mounter.Do(req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
			}
			var problem struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
				t.Fatalf("decoding the problem: %v", err)
			}
			if want := typePrefix + "bad-request"; problem.Type != want {
				t.Errorf("problem type = %q, want %q", problem.Type, want)
			}
			if h.rowCount() != 0 {
				t.Errorf("a row was written for a body that could not be read")
			}
		})
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
	if _, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token}); err != nil {
		t.Fatalf("accepting: %v", err)
	}
	h.asAdmin()
	w := h.request(http.MethodDelete, "/api/v1/tenants/tenant-a/invitations/"+issued.Invitation.ID, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 revoking an accepted invitation", w.Code)
	}
	if !strings.Contains(w.Body.String(), "urn:speed:authn:invitation-terminal") {
		t.Errorf("body %s does not carry invitation-terminal", w.Body.String())
	}
}

func TestAcceptFaceReadIsPublicAndWriteNeedsAnIdentity(t *testing.T) {
	h := newHarness(t)
	issued := h.issue("tenant-a", "alice@example.com")

	// The GET carries no identity: the route is public.
	h.mounter.AsAnonymous()
	w := h.request(http.MethodGet, "/api/v1/auth/invitations/"+issued.Token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("accept view status = %d, want 200: %s", w.Code, w.Body.String())
	}

	// The POST does not: the token alone is no credential.
	w = h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]string{})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous accept status = %d, want 401: %s", w.Code, w.Body.String())
	}
	if h.members.count != 0 {
		t.Errorf("an anonymous acceptance wrote %d membership(s)", h.members.count)
	}

	// A signed-in holder of the invited address redeems it.
	h.acceptAsHTTP("acct-alice", "alice@example.com")
	w = h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]string{})
	if w.Code != http.StatusCreated {
		t.Fatalf("accept status = %d, want 201: %s", w.Code, w.Body.String())
	}
	if h.members.count != 1 {
		t.Errorf("memberships written = %d, want one", h.members.count)
	}
	// A second acceptance of the same token is refused.
	w = h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]string{})
	if w.Code != http.StatusConflict {
		t.Fatalf("second accept status = %d, want 409", w.Code)
	}
}

// TestTheAcceptViewShowsTheMaskedLinkAndThenTheOrganization holds the three
// shapes of the public read: a pending link names the organization and the
// inviter by their display names and the address in the server's masked form,
// never the full address or the issuer's account identifier; an accepted link
// names the organization and shows neither the address nor the inviter; and an
// expired or revoked link answers its state alone.
func TestTheAcceptViewShowsTheMaskedLinkAndThenTheOrganization(t *testing.T) {
	h := newHarness(t)
	h.tenantDescs.byID["tenant-a"] = authn.TenantDescription{Name: "acme", DisplayName: "Acme Org"}
	h.accounts.named(adminID, "Demo Operator")

	// A status request against a token, decoded into the members the public read
	// may publish.
	read := func(token string) (string, map[string]any, string) {
		t.Helper()
		w := h.request(http.MethodGet, "/api/v1/auth/invitations/"+token, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("view status = %d, want 200: %s", w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("the body %q is not JSON: %v", w.Body.String(), err)
		}
		state, _ := body["state"].(string)
		link, _ := body["invitation"].(map[string]any)
		return state, link, w.Body.String()
	}

	h.mounter.AsAnonymous()
	pending := h.issue("tenant-a", "alice@example.com")
	state, link, raw := read(pending.Token)
	if state != viewStatePending {
		t.Fatalf("pending state = %q, want %q", state, viewStatePending)
	}
	if got, _ := link["tenant_display_name"].(string); got != "Acme Org" {
		t.Errorf("tenant_display_name = %q, want the organization's display name", got)
	}
	if got, _ := link["tenant"].(string); got != "acme" {
		t.Errorf("tenant = %q, want the organization's name beside the display name", got)
	}
	if got, _ := link["invited_by_name"].(string); got != "Demo Operator" {
		t.Errorf("invited_by_name = %q, want the inviter's display name", got)
	}
	if got, _ := link["email"].(string); got != "a***@example.com" {
		t.Errorf("email = %q, want the server-masked a***@example.com", got)
	}
	if _, ok := link["expires_at"]; !ok {
		t.Error("a pending link carries no deadline")
	}
	if _, ok := link["invited_by"]; ok {
		t.Error("the public read carries the issuer's account identifier")
	}
	for _, leaked := range []string{"alice@example.com", string(adminID)} {
		if strings.Contains(raw, leaked) {
			t.Errorf("the public read carries %q: %s", leaked, raw)
		}
	}

	// Accepted: the organization's name and display name, no address or inviter.
	h.acceptAsHTTP("acct-alice", "alice@example.com")
	w := h.request(http.MethodPost, "/api/v1/auth/invitations/"+pending.Token, map[string]string{})
	if w.Code != http.StatusCreated {
		t.Fatalf("accepting: %d: %s", w.Code, w.Body.String())
	}
	h.mounter.AsAnonymous()
	state, link, raw = read(pending.Token)
	if state != viewStateAccepted {
		t.Fatalf("accepted state = %q, want %q", state, viewStateAccepted)
	}
	if got, _ := link["tenant"].(string); got != "acme" {
		t.Errorf("accepted tenant = %q, want the organization's name", got)
	}
	if got, _ := link["tenant_display_name"].(string); got != "Acme Org" {
		t.Errorf("accepted tenant_display_name = %q, want the organization's display name", got)
	}
	for _, absent := range []string{"email", "invited_by_name", "expires_at"} {
		if _, ok := link[absent]; ok {
			t.Errorf("an accepted link carries %q", absent)
		}
	}
	if strings.Contains(raw, "alice@example.com") {
		t.Errorf("the accepted read carries the invited address: %s", raw)
	}

	// Revoked and expired: the state alone.
	for _, held := range []struct {
		name  string
		setup func(Issued)
	}{
		{
			name: "revoked",
			setup: func(issued Issued) {
				h.asAdmin()
				if revoked := h.request(http.MethodDelete,
					"/api/v1/tenants/tenant-a/invitations/"+issued.Invitation.ID, nil); revoked.Code != http.StatusNoContent {
					t.Fatalf("revoking: %d: %s", revoked.Code, revoked.Body.String())
				}
				h.mounter.AsAnonymous()
			},
		},
		{
			name: "expired",
			setup: func(Issued) {
				h.clock.advance(DefaultTTL + time.Hour)
			},
		},
	} {
		t.Run(held.name, func(t *testing.T) {
			issued := h.issue("tenant-a", held.name+"@example.com")
			h.mounter.AsAnonymous()
			held.setup(issued)

			state, link, raw := read(issued.Token)
			if state != held.name {
				t.Errorf("state = %q, want %q", state, held.name)
			}
			if link != nil {
				t.Error("a dead token's answer carries an invitation summary")
			}
			for _, member := range []string{`"email"`, `"tenant"`, `"expires_at"`, `"invited_by_name"`} {
				if strings.Contains(raw, member) {
					t.Errorf("a dead token's answer carries %q: %s", member, raw)
				}
			}
		})
	}
}

// TestTheAcceptViewCarriesTheTenantNameWhenTheDisplayNameIsEmpty covers an
// organization that never chose a display name, such as a seeded one: the
// pending link still names it, because the name is what the landing page falls
// back to. Without the name the invitee's page would say they were invited to
// join "".
func TestTheAcceptViewCarriesTheTenantNameWhenTheDisplayNameIsEmpty(t *testing.T) {
	h := newHarness(t)
	h.tenantDescs.byID["tenant-a"] = authn.TenantDescription{Name: "acme"}

	issued := h.issue("tenant-a", "alice@example.com")
	h.mounter.AsAnonymous()
	w := h.request(http.MethodGet, "/api/v1/auth/invitations/"+issued.Token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("view status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var body struct {
		State      string `json:"state"`
		Invitation struct {
			Tenant            string `json:"tenant"`
			TenantDisplayName string `json:"tenant_display_name"`
		} `json:"invitation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("the body %q is not JSON: %v", w.Body.String(), err)
	}
	if body.State != viewStatePending {
		t.Fatalf("state = %q, want %q", body.State, viewStatePending)
	}
	if body.Invitation.Tenant != "acme" {
		t.Errorf("tenant = %q, want the organization's name so the landing can name it", body.Invitation.Tenant)
	}
	if body.Invitation.TenantDisplayName != "" {
		t.Errorf("tenant_display_name = %q, want empty for an organization that chose none", body.Invitation.TenantDisplayName)
	}
}

// TestAnUnprovedCallerOnAcceptanceAnswers403OfItsOwn holds the answer the client
// turns into the proof step: a caller with another proven address is told the
// invited address is unproved, the invitation stays pending, and no membership
// is written.
func TestAnUnprovedCallerOnAcceptanceAnswers403OfItsOwn(t *testing.T) {
	h := newHarness(t)
	h.acceptAsHTTP("acct-alice", "alice@example.com")
	issued := h.issue("tenant-a", "zoe@example.com")

	w := h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]string{})
	if w.Code != http.StatusForbidden {
		t.Fatalf("accept status = %d, want 403: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("the body %q is not JSON: %v", w.Body.String(), err)
	}
	if got, _ := body["type"].(string); got != "urn:speed:authn:invitation-email-unproved" {
		t.Errorf("problem type = %q, want urn:speed:authn:invitation-email-unproved", got)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusPending {
		t.Errorf("status after the 403 = %d, want pending so the proof step can be shown", got)
	}
	if h.members.count != 0 {
		t.Errorf("the refused acceptance wrote %d membership(s)", h.members.count)
	}
}

// TestASuspendedMemberOnAcceptanceAnswers409OfItsOwn holds that a suspended
// membership is not silently reactivated: the acceptance answers the dedicated
// conflict, writes nothing, and revokes the invitation.
func TestASuspendedMemberOnAcceptanceAnswers409OfItsOwn(t *testing.T) {
	h := newHarness(t)
	h.acceptAsHTTP("acct-alice", "alice@example.com")
	issued := h.issue("tenant-a", "alice@example.com")
	// The suspension lands after issuance: an address that is already a member
	// cannot be invited, and this case is about acceptance meeting one.
	h.members.hold(authn.Membership{Account: "acct-alice", Tenant: "tenant-a", Active: false})

	w := h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]string{})
	if w.Code != http.StatusConflict {
		t.Fatalf("accept status = %d, want 409: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("the body %q is not JSON: %v", w.Body.String(), err)
	}
	if got, _ := body["type"].(string); got != "urn:speed:authn:membership-inactive" {
		t.Errorf("problem type = %q, want urn:speed:authn:membership-inactive", got)
	}
	if h.members.count != 0 {
		t.Errorf("the refused acceptance wrote %d membership(s)", h.members.count)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusRevoked {
		t.Errorf("status after the 409 = %d, want revoked", got)
	}
}

// TestAFailingMembershipStoreOnAcceptanceAnswers503 holds the acceptance face's
// mapping of a dependency failure: the invitation is back to pending and the
// caller is told the dependency could not answer rather than that the token is
// a conflict.
func TestAFailingMembershipStoreOnAcceptanceAnswers503(t *testing.T) {
	h := newHarness(t)
	h.acceptAsHTTP("acct-zoe", "zoe@example.com")
	h.members.err = errors.New("the membership store is down")
	issued := h.issue("tenant-a", "zoe@example.com")

	w := h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]string{})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("accept status = %d, want 503: %s", w.Code, w.Body.String())
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusPending {
		t.Errorf("status after the 503 = %d, want back to pending", got)
	}
}

// TestAcceptanceWithoutItsRequiredCapabilitiesFailsClosed holds the assembly
// rule at the HTTP layer: every acceptance writes through the transaction-aware
// membership writer, and an invitation that carries a role needs the policy to
// re-judge it and the binder to write it. With one absent the request answers
// 503 and the invitation stays pending, so an assembly that gains the capability
// leaves the same token redeemable.
func TestAcceptanceWithoutItsRequiredCapabilitiesFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		issue  func(*harness) Issued
		remove func(*product)
	}{
		{
			name:   "the membership writer",
			issue:  func(h *harness) Issued { return h.issue(tenant, "alice@example.com") },
			remove: func(p *product) { p.membershipWriter = nil },
		},
		{
			name:   "the role policy",
			issue:  func(h *harness) Issued { return h.issueRole(tenant, "alice@example.com", "editor") },
			remove: func(p *product) { p.rolePolicy = nil },
		},
		{
			name:   "the role binder",
			issue:  func(h *harness) Issued { return h.issueRole(tenant, "alice@example.com", "editor") },
			remove: func(p *product) { p.roleBinder = nil },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			issued := tc.issue(h)
			h.acceptAsHTTP("acct-alice", "alice@example.com")
			tc.remove(h.p)

			w := h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]string{})
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("accept status = %d, want 503 without %s: %s", w.Code, tc.name, w.Body.String())
			}
			if got := problemTypeOf(t, w); got != typePrefix+"unavailable" {
				t.Errorf("problem type = %q, want %s", got, typePrefix+"unavailable")
			}
			if got := h.row(issued.Invitation.ID).Status; got != statusPending {
				t.Errorf("status after the 503 = %d, want pending", got)
			}
		})
	}
}

// TestTerminalInvitationsOnAcceptanceAnswerInvitationTerminal holds the
// acceptance face's terminal refusal: a token whose invitation is already
// accepted, revoked or expired answers 409 invitation-terminal, the code a
// client reads as "this link no longer grants anything".
func TestTerminalInvitationsOnAcceptanceAnswerInvitationTerminal(t *testing.T) {
	// redeem sends one acceptance through the route as the proven holder of the
	// invited address.
	redeem := func(h *harness, token string) *httptest.ResponseRecorder {
		h.t.Helper()
		h.acceptAsHTTP("acct-alice", "alice@example.com")
		return h.request(http.MethodPost, "/api/v1/auth/invitations/"+token, map[string]string{})
	}
	for _, tc := range []struct {
		name  string
		setup func(*harness, Issued)
	}{
		{
			name: "already accepted",
			setup: func(h *harness, issued Issued) {
				if w := redeem(h, issued.Token); w.Code != http.StatusCreated {
					h.t.Fatalf("the first acceptance = %d, want 201: %s", w.Code, w.Body.String())
				}
			},
		},
		{
			name: "revoked",
			setup: func(h *harness, issued Issued) {
				h.asAdmin()
				w := h.request(http.MethodDelete, pathAdmin+"/"+issued.Invitation.ID, nil)
				if w.Code != http.StatusNoContent {
					h.t.Fatalf("revoking = %d, want 204: %s", w.Code, w.Body.String())
				}
			},
		},
		{
			name:  "expired",
			setup: func(h *harness, _ Issued) { h.clock.advance(DefaultTTL + time.Hour) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			issued := h.issue(tenant, "alice@example.com")
			tc.setup(h, issued)

			w := redeem(h, issued.Token)
			if w.Code != http.StatusConflict {
				t.Fatalf("accept status = %d, want 409: %s", w.Code, w.Body.String())
			}
			if got := problemTypeOf(t, w); got != typePrefix+"invitation-terminal" {
				t.Errorf("problem type = %q, want %s", got, typePrefix+"invitation-terminal")
			}
		})
	}
}

// TestADisabledTenantOnAcceptanceAnswers409OfItsOwn holds the refusal direction
// the design gives a suspended organization: the acceptance answers 409
// invitation-tenant-inactive, not the terminal conflict, and the invitation
// stays pending, so enabling the tenant makes the same token redeemable again.
func TestADisabledTenantOnAcceptanceAnswers409OfItsOwn(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "alice@example.com")
	h.tenantStates.states[tenant] = authn.TenantDisabled
	h.acceptAsHTTP("acct-alice", "alice@example.com")

	w := h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]string{})
	if w.Code != http.StatusConflict {
		t.Fatalf("accept status = %d, want 409: %s", w.Code, w.Body.String())
	}
	if got := problemTypeOf(t, w); got != typePrefix+"invitation-tenant-inactive" {
		t.Errorf("problem type = %q, want %s", got, typePrefix+"invitation-tenant-inactive")
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusPending {
		t.Errorf("status after the 409 = %d, want pending so enabling the tenant restores the token", got)
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
	issued := h.issue("tenant-a", "alice@example.com")

	h.acceptAsHTTP("acct-alice", "alice@example.com")
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
	issued := h.issue("tenant-a", "alice@example.com")

	h.acceptAsHTTP("acct-alice", "alice@example.com")
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
	issued := h.issue("tenant-a", "alice@example.com")

	h.acceptAsHTTP("acct-alice", "alice@example.com")
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
