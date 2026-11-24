package invite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

func problemTypeOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var problem struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decoding the problem document %q: %v", w.Body.String(), err)
	}
	return problem.Type
}

func invitationJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var answered struct {
		Invitation map[string]any `json:"invitation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decoding the answer %q: %v", w.Body.String(), err)
	}
	return answered.Invitation
}

// TestIssuingARoleTheCallerCannotManageIsForbidden checks that a caller who may manage memberships but not roles gets 403 forbidden.
func TestIssuingARoleTheCallerCannotManageIsForbidden(t *testing.T) {
	h := newHarness(t)
	h.rolePolicy.grantErr = authn.ErrForbidden
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com", "role": "editor"})

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if got := problemTypeOf(t, w); got != "urn:speed:authn:forbidden" {
		t.Errorf("problem type = %q, want urn:speed:authn:forbidden", got)
	}
	if h.rowCount() != 0 {
		t.Errorf("a row was written for a role the caller cannot manage")
	}
}

// TestIssuingAProtectedRoleAnswersItsOwnForbidden checks that a protected role answers its own 403 code.
func TestIssuingAProtectedRoleAnswersItsOwnForbidden(t *testing.T) {
	h := newHarness(t)
	h.rolePolicy.grantErr = authn.ErrRoleProtected
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com", "role": "tenant-admin"})

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if got := problemTypeOf(t, w); got != "urn:speed:authn:invitation-role-protected" {
		t.Errorf("problem type = %q, want urn:speed:authn:invitation-role-protected", got)
	}
	if h.rowCount() != 0 {
		t.Errorf("a row was written for a protected role")
	}
}

// TestIssuingAnUnknownRoleAnswersBadRequest checks that a role the tenant does not have is a 400 of its own.
func TestIssuingAnUnknownRoleAnswersBadRequest(t *testing.T) {
	h := newHarness(t)
	h.rolePolicy.grantErr = authn.ErrRoleUnknown
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com", "role": "ghost"})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if got := problemTypeOf(t, w); got != "urn:speed:authn:invitation-role-unknown" {
		t.Errorf("problem type = %q, want urn:speed:authn:invitation-role-unknown", got)
	}
	if h.rowCount() != 0 {
		t.Errorf("a row was written for a role the tenant does not have")
	}
}

// TestIssuingARoleWithoutThePolicyFailsClosed checks that an issuance with a role and no policy answers 503.
func TestIssuingARoleWithoutThePolicyFailsClosed(t *testing.T) {
	h := newHarness(t)
	h.p.rolePolicy = nil
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com", "role": "editor"})

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", w.Code, w.Body.String())
	}
	if h.rowCount() != 0 {
		t.Errorf("a row was written without an invitation role policy")
	}
}

// TestIssuingWithoutARoleDoesNotAskTheRolePolicy checks that an issuance without a role asks no policy.
func TestIssuingWithoutARoleDoesNotAskTheRolePolicy(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	if calls := h.rolePolicy.issueCalls(); len(calls) != 0 {
		t.Errorf("the role policy was asked %d time(s) for an invitation without a role", len(calls))
	}
}

// TestTheManagementAndPublicViewsCarryTheRole checks that every view and the acceptance answer carry the role.
func TestTheManagementAndPublicViewsCarryTheRole(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com", "role": "editor"})
	if w.Code != http.StatusCreated {
		t.Fatalf("issuing: status = %d: %s", w.Code, w.Body.String())
	}
	if got := invitationJSON(t, w)["role"]; got != "editor" {
		t.Errorf("the issue answer's role = %v, want editor", got)
	}

	list := h.request(http.MethodGet, pathAdmin, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("listing: status = %d: %s", list.Code, list.Body.String())
	}
	var page struct {
		Invitations []map[string]any `json:"invitations"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil || len(page.Invitations) != 1 {
		t.Fatalf("decoding the list %q: %v", list.Body.String(), err)
	}
	if got := page.Invitations[0]["role"]; got != "editor" {
		t.Errorf("the list's role = %v, want editor", got)
	}

	var issued struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &issued); err != nil {
		t.Fatalf("decoding the issue answer: %v", err)
	}
	view := h.request(http.MethodGet, "/api/v1/auth/invitations/"+issued.Token, nil)
	if view.Code != http.StatusOK {
		t.Fatalf("public read: status = %d: %s", view.Code, view.Body.String())
	}
	var public struct {
		Invitation map[string]any `json:"invitation"`
	}
	if err := json.Unmarshal(view.Body.Bytes(), &public); err != nil {
		t.Fatalf("decoding the public read: %v", err)
	}
	if got := public.Invitation["role"]; got != "editor" {
		t.Errorf("the public read's role = %v, want editor", got)
	}

	h.acceptAsHTTP("acct-alice", "alice@example.com")
	accepted := h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]any{})
	if accepted.Code != http.StatusCreated {
		t.Fatalf("accepting: status = %d: %s", accepted.Code, accepted.Body.String())
	}
	var acceptBody map[string]any
	if err := json.Unmarshal(accepted.Body.Bytes(), &acceptBody); err != nil {
		t.Fatalf("decoding the acceptance: %v", err)
	}
	if got := acceptBody["role"]; got != "editor" {
		t.Errorf("the acceptance answer's role = %v, want editor", got)
	}
}

// TestTheViewsOmitTheRoleKeyWhenThereIsNone checks that no role means the key is absent, never null, in every view.
func TestTheViewsOmitTheRoleKeyWhenThereIsNone(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusCreated {
		t.Fatalf("issuing: status = %d: %s", w.Code, w.Body.String())
	}
	if _, present := invitationJSON(t, w)["role"]; present {
		t.Errorf("the issue answer carries a role key without a role: %s", w.Body.String())
	}
	var issued struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &issued); err != nil {
		t.Fatalf("decoding the issue answer: %v", err)
	}
	view := h.request(http.MethodGet, "/api/v1/auth/invitations/"+issued.Token, nil)
	var public struct {
		Invitation map[string]any `json:"invitation"`
	}
	if err := json.Unmarshal(view.Body.Bytes(), &public); err != nil {
		t.Fatalf("decoding the public read: %v", err)
	}
	if _, present := public.Invitation["role"]; present {
		t.Errorf("the public read carries a role key without a role: %s", view.Body.String())
	}

	h.acceptAsHTTP("acct-alice", "alice@example.com")
	accepted := h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]any{})
	var acceptBody map[string]any
	if err := json.Unmarshal(accepted.Body.Bytes(), &acceptBody); err != nil {
		t.Fatalf("decoding the acceptance: %v", err)
	}
	if _, present := acceptBody["role"]; present {
		t.Errorf("the acceptance answer carries a role key without a role: %s", accepted.Body.String())
	}
}

// TestAcceptBindsTheInvitationsRole checks that the acceptance binds the invitation's role to the accepting account.
func TestAcceptBindsTheInvitationsRole(t *testing.T) {
	h := newHarness(t)
	issued := h.issueRole(tenant, "alice@example.com", "editor")
	accepted, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token})
	if err != nil {
		t.Fatalf("accepting: %v", err)
	}
	if accepted.Role != "editor" {
		t.Errorf("accepted role = %q, want editor", accepted.Role)
	}
	written := h.roleBinder.written()
	if len(written) != 1 || written[0].Tenant != tenant || written[0].Account != "acct-alice" || written[0].Role != "editor" {
		t.Errorf("bindings = %+v, want one editor binding for acct-alice in %s", written, tenant)
	}
	if !h.roleBinder.txSeen {
		t.Error("the binding was not written through the acceptance's transaction handle")
	}
	asked := h.rolePolicy.acceptCalls()
	if len(asked) != 1 || asked[0].Tenant != tenant || asked[0].Role != "editor" || asked[0].Account != string(adminID) {
		t.Errorf("the acceptance asked %+v, want one editor judgement for %q in %s", asked, adminID, tenant)
	}
}

// TestAcceptKeepsAnExistingMembershipAndStillBindsTheRole checks that an existing member keeps the source and exemption and still gets the role.
func TestAcceptKeepsAnExistingMembershipAndStillBindsTheRole(t *testing.T) {
	h := newHarness(t)
	issued := h.issueRole(tenant, "alice@example.com", "editor")
	h.members.hold(authn.Membership{
		Account: "acct-alice", Tenant: tenant, Active: true, Source: "admin", SSOExempt: true,
	})

	accepted, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token})
	if err != nil {
		t.Fatalf("accepting: %v", err)
	}
	if accepted.Source != "admin" || !accepted.SSOExempt || !accepted.Active {
		t.Errorf("membership = %+v, want the existing source, exemption and activity", accepted.Membership)
	}
	if written := h.roleBinder.written(); len(written) != 1 || written[0].Role != "editor" {
		t.Errorf("bindings = %+v, want the invitation's role bound", written)
	}
}

// TestAcceptRefusesARoleThatCanNoLongerBeGranted checks that a role that can no longer be granted revokes the invitation and writes nothing.
func TestAcceptRefusesARoleThatCanNoLongerBeGranted(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "the role is gone", err: authn.ErrRoleUnknown},
		{name: "the inviter lost a permission", err: authn.ErrRoleProtected},
		{name: "the inviter is no longer an active member", err: authn.ErrForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.rolePolicy.stillErr = tc.err
			h.rolePolicy.stillErrFor = string(adminID)
			issued := h.issueRole(tenant, "alice@example.com", "editor")

			_, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token})
			if !errors.Is(err, ErrInvitationRoleWithdrawn) {
				t.Fatalf("accepting gave %v, want ErrInvitationRoleWithdrawn", err)
			}
			row := h.row(issued.Invitation.ID)
			if row.Status != statusRevoked || row.RevokedAt == nil {
				t.Errorf("the row reads status %d and revoked_at %v, want revoked with a time", row.Status, row.RevokedAt)
			}
			if row.AcceptedAt != nil {
				t.Errorf("accepted_at = %v, want nil", row.AcceptedAt)
			}
			if h.members.count != 0 {
				t.Errorf("the refused acceptance wrote %d membership(s), want none", h.members.count)
			}
			if written := h.roleBinder.written(); len(written) != 0 {
				t.Errorf("the refused acceptance wrote bindings %+v, want none", written)
			}
			assertRevokedAudit(t, h, issued.Invitation.ID, authn.ReasonInvitationRoleWithdrawn)
		})
	}
}

// TestAcceptKeepsTheInvitationWhenTheRoleReviewIsUnavailable checks that a review that cannot answer leaves the invitation pending and answers unavailable.
func TestAcceptKeepsTheInvitationWhenTheRoleReviewIsUnavailable(t *testing.T) {
	h := newHarness(t)
	h.rolePolicy.stillErr = errors.New("the policy store is down")
	h.rolePolicy.stillErrFor = string(adminID)
	issued := h.issueRole(tenant, "alice@example.com", "editor")

	_, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token})
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("accepting gave %v, want ErrUnavailable", err)
	}
	row := h.row(issued.Invitation.ID)
	if row.Status != statusPending || row.RevokedAt != nil {
		t.Errorf("the row reads status %d and revoked_at %v, want pending with no revocation", row.Status, row.RevokedAt)
	}
	for _, entry := range h.audit.entries() {
		if entry.Action == actionRevoke && entry.Object == issued.Invitation.ID {
			t.Errorf("the unavailable review wrote a revocation audit row: %+v", entry)
		}
	}
	if h.members.count != 0 {
		t.Errorf("the failed acceptance wrote %d membership(s), want none", h.members.count)
	}
	if written := h.roleBinder.written(); len(written) != 0 {
		t.Errorf("the failed acceptance wrote bindings %+v, want none", written)
	}
}

// TestAcceptKeepsTheInvitationWhenTheInviterCannotBeRead checks that an unreadable inviter leaves the invitation pending.
func TestAcceptKeepsTheInvitationWhenTheInviterCannotBeRead(t *testing.T) {
	h := newHarness(t)
	issued := h.issueRole(tenant, "alice@example.com", "editor")
	h.accounts.failsFor(adminID, errInviterUnreadable)

	_, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token})
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("accepting gave %v, want ErrUnavailable", err)
	}
	row := h.row(issued.Invitation.ID)
	if row.Status != statusPending || row.RevokedAt != nil {
		t.Errorf("the row reads status %d and revoked_at %v, want pending with no revocation", row.Status, row.RevokedAt)
	}
	for _, entry := range h.audit.entries() {
		if entry.Action == actionRevoke && entry.Object == issued.Invitation.ID {
			t.Errorf("the unreadable inviter wrote a revocation audit row: %+v", entry)
		}
	}
	if h.members.count != 0 {
		t.Errorf("the failed acceptance wrote %d membership(s), want none", h.members.count)
	}
	if written := h.roleBinder.written(); len(written) != 0 {
		t.Errorf("the failed acceptance wrote bindings %+v, want none", written)
	}
}

// TestAcceptRevokesTheInvitationWhenTheInviterCannotStand checks that a gone or disabled inviter withdraws the invitation.
func TestAcceptRevokesTheInvitationWhenTheInviterCannotStand(t *testing.T) {
	for name, script := range map[string]func(*fakeAccounts){
		"the inviter account no longer exists": func(a *fakeAccounts) { a.gone(adminID) },
		"the inviter account was disabled":     func(a *fakeAccounts) { a.disabled(adminID) },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			issued := h.issueRole(tenant, "alice@example.com", "editor")
			script(h.accounts)

			_, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token})
			if !errors.Is(err, ErrInvitationRoleWithdrawn) {
				t.Fatalf("accepting gave %v, want ErrInvitationRoleWithdrawn", err)
			}
			row := h.row(issued.Invitation.ID)
			if row.Status != statusRevoked || row.RevokedAt == nil {
				t.Errorf("the row reads status %d and revoked_at %v, want revoked with a time", row.Status, row.RevokedAt)
			}
			if h.members.count != 0 {
				t.Errorf("the refused acceptance wrote %d membership(s), want none", h.members.count)
			}
			if written := h.roleBinder.written(); len(written) != 0 {
				t.Errorf("the refused acceptance wrote bindings %+v, want none", written)
			}
			assertRevokedAudit(t, h, issued.Invitation.ID, authn.ReasonInvitationRoleWithdrawn)
		})
	}
}

// TestAnInviterWithoutAnAccountIsNeverRead checks that a host command and a static subject are never read from the account store.
func TestAnInviterWithoutAnAccountIsNeverRead(t *testing.T) {
	t.Run("a host command's invitation", func(t *testing.T) {
		h := newHarness(t)
		issued, err := h.p.Issue(context.Background(), IssueInvite{
			Tenant: tenant, Email: "alice@example.com", Role: "editor",
		})
		if err != nil {
			t.Fatalf("issuing as a host command: %v", err)
		}
		if issued.Invitation.InvitedBy != "" {
			t.Fatalf("invited_by = %q, want the host command's empty issuer", issued.Invitation.InvitedBy)
		}
		before := h.accounts.getCountOf("")
		if _, err = h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token}); err != nil {
			t.Fatalf("accepting: %v", err)
		}
		if after := h.accounts.getCountOf(""); after != before {
			t.Errorf("the acceptance read an empty inviter from the account store %d time(s), want none", after-before)
		}
	})

	t.Run("a static subject", func(t *testing.T) {
		h := newHarness(t)
		token, _ := h.seedStaticInvitation()
		before := h.accounts.getCountOf("static.ops")
		if _, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: token}); err != nil {
			t.Fatalf("accepting: %v", err)
		}
		if after := h.accounts.getCountOf("static.ops"); after != before {
			t.Errorf("the acceptance read the account store %d time(s), want none for a static subject", after-before)
		}
		review := h.rolePolicy.acceptCalls()
		if len(review) != 1 || review[0].Tenant != tenant || review[0].Account != "static.ops" || review[0].Role != "editor" {
			t.Errorf("the static subject's review asked %+v, want one editor judgement for static.ops in %s", review, tenant)
		}
	})

	t.Run("a static subject whose role can no longer be granted", func(t *testing.T) {
		h := newHarness(t)
		h.rolePolicy.stillErr = authn.ErrRoleUnknown
		h.rolePolicy.stillErrFor = "static.ops"
		token, id := h.seedStaticInvitation()

		if _, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: token}); !errors.Is(err, ErrInvitationRoleWithdrawn) {
			t.Fatalf("accepting gave %v, want ErrInvitationRoleWithdrawn", err)
		}
		if row := h.row(id); row.Status != statusRevoked || row.RevokedAt == nil {
			t.Errorf("the row reads status %d and revoked_at %v, want revoked with a time", row.Status, row.RevokedAt)
		}
		if h.members.count != 0 {
			t.Errorf("the refused acceptance wrote %d membership(s), want none", h.members.count)
		}
		if written := h.roleBinder.written(); len(written) != 0 {
			t.Errorf("the refused acceptance wrote bindings %+v, want none", written)
		}
		assertRevokedAudit(t, h, id, authn.ReasonInvitationRoleWithdrawn)
	})
}

func (h *harness) seedStaticInvitation() (token, id string) {
	h.t.Helper()
	minted, err := newToken()
	if err != nil {
		h.t.Fatalf("minting a token: %v", err)
	}
	now := h.clock.read()
	row := invitationRow{
		ID: "inv-static", TenantID: tenant, Email: "alice@example.com",
		TokenDigest: tokenDigest(minted), Status: statusPending,
		InvitedBy: "static.ops", Role: "editor",
		ExpiresAt: utc(now.Add(time.Hour)), LastSentAt: utc(now),
		CreatedAt: utc(now), UpdatedAt: utc(now),
	}
	if createErr := h.handle.Create(&row).Error; createErr != nil {
		h.t.Fatalf("seeding the static subject's invitation: %v", createErr)
	}
	return minted, row.ID
}

// TestAnIdentityWithoutAnAccountIsNotAHost checks that no identity is a host command and an identity with no account is refused.
func TestAnIdentityWithoutAnAccountIsNotAHost(t *testing.T) {
	if _, host, err := issuerOf(authn.Identity{}, false); err != nil || !host {
		t.Errorf("issuerOf(no identity) = (host=%v, err=%v), want a host command", host, err)
	}
	if _, host, err := issuerOf(authn.Identity{}, true); err == nil || host {
		t.Errorf("issuerOf(an identity with no account) = (host=%v, err=%v), want it refused", host, err)
	}
}

func assertRevokedAudit(t *testing.T, h *harness, invitationID, reason string) {
	t.Helper()
	var found int
	for _, row := range h.audit.entries() {
		if row.Action != actionRevoke || row.Object != invitationID {
			continue
		}
		found++
		if row.Outcome != outcomeSuccess {
			t.Errorf("the revocation audit's outcome = %q, want success", row.Outcome)
		}
		if row.Detail[detailTenant] != tenant {
			t.Errorf("the revocation audit's tenant = %q, want %q", row.Detail[detailTenant], tenant)
		}
		if row.Detail[detailActionID] == "" {
			t.Error("the revocation audit carries no action identifier")
		}
		if row.Detail[detailReason] != reason {
			t.Errorf("the revocation audit's reason = %q, want %q", row.Detail[detailReason], reason)
		}
	}
	if found != 1 {
		t.Errorf("the revocation left %d audit row(s), want exactly one; rows = %+v",
			found, h.audit.entries())
	}
}

// TestAHostCommandIssuesAnInvitationWithARole checks that a host command issues and accepts a role without an inviter account.
func TestAHostCommandIssuesAnInvitationWithARole(t *testing.T) {
	h := newHarness(t)
	issued, err := h.p.Issue(context.Background(), IssueInvite{
		Tenant: tenant, Email: "alice@example.com", Role: "editor",
	})
	if err != nil {
		t.Fatalf("a host command issuing an invitation with a role: %v", err)
	}
	if issued.Invitation.Role != "editor" {
		t.Errorf("the invitation's role = %q, want editor", issued.Invitation.Role)
	}
	if issued.Invitation.InvitedBy != "" {
		t.Errorf("the invitation's inviter = %q, want empty for a host command", issued.Invitation.InvitedBy)
	}
	asked := h.rolePolicy.issueCalls()
	if len(asked) != 1 || asked[0].Account != "" || asked[0].Role != "editor" {
		t.Errorf("the issue judgement was asked %+v, want one editor judgement with no account", asked)
	}

	accepted, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token})
	if err != nil {
		t.Fatalf("accepting a host-issued invitation: %v", err)
	}
	if accepted.Role != "editor" {
		t.Errorf("accepted role = %q, want editor", accepted.Role)
	}
	review := h.rolePolicy.acceptCalls()
	if len(review) != 1 || review[0].Account != "" || review[0].Role != "editor" {
		t.Errorf("the acceptance review was asked %+v, want one editor judgement with no inviter", review)
	}
	if written := h.roleBinder.written(); len(written) != 1 || written[0].Role != "editor" {
		t.Errorf("bindings = %+v, want the invitation's role bound", written)
	}
}

// TestAcceptWritesTheAcceptanceAudit checks that one audit row per acceptance names the tenant, the role when there is one and the inviter.
func TestAcceptWritesTheAcceptanceAudit(t *testing.T) {
	h := newHarness(t)
	issued := h.issueRole(tenant, "alice@example.com", "editor")
	if _, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token}); err != nil {
		t.Fatalf("accepting: %v", err)
	}
	var found int
	for _, row := range h.audit.entries() {
		if row.Action != actionAccept {
			continue
		}
		found++
		if row.Object != issued.Invitation.ID {
			t.Errorf("audit object = %q, want the invitation identifier", row.Object)
		}
		if row.Outcome != outcomeSuccess {
			t.Errorf("audit outcome = %q, want success", row.Outcome)
		}
		if row.Detail[detailTenant] != tenant {
			t.Errorf("audit tenant = %q, want %q", row.Detail[detailTenant], tenant)
		}
		if row.Detail[detailRole] != "editor" {
			t.Errorf("audit role = %q, want editor", row.Detail[detailRole])
		}
		if row.Detail[detailInvitedBy] != string(adminID) {
			t.Errorf("audit inviter = %q, want %q", row.Detail[detailInvitedBy], adminID)
		}
	}
	if found != 1 {
		t.Errorf("the acceptance wrote %d %s audit rows, want exactly one; rows = %+v",
			found, actionAccept, h.audit.entries())
	}
}

// TestIssueAuditCarriesTheRole checks that the issue audit names the role when there is one and omits it otherwise.
func TestIssueAuditCarriesTheRole(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()
	if w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com", "role": "editor"}); w.Code != http.StatusCreated {
		t.Fatalf("issuing with a role: status = %d: %s", w.Code, w.Body.String())
	}
	var withRole map[string]string
	for _, row := range h.audit.entries() {
		if row.Action == actionIssue {
			withRole = row.Detail
		}
	}
	if withRole[detailRole] != "editor" {
		t.Errorf("the issue audit's role = %q, want editor", withRole[detailRole])
	}

	if w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "bob@example.com"}); w.Code != http.StatusCreated {
		t.Fatalf("issuing without a role: status = %d: %s", w.Code, w.Body.String())
	}
	var withoutRole map[string]string
	for _, row := range h.audit.entries() {
		if row.Action == actionIssue {
			withoutRole = row.Detail
		}
	}
	if _, present := withoutRole[detailRole]; present {
		t.Errorf("the issue audit carries a role key without a role: %+v", withoutRole)
	}
}

// TestAcceptanceAuditOmitsTheRoleWhenThereIsNone checks that an acceptance of an invitation without a role names no role.
func TestAcceptanceAuditOmitsTheRoleWhenThereIsNone(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "alice@example.com")
	if _, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token}); err != nil {
		t.Fatalf("accepting: %v", err)
	}
	for _, row := range h.audit.entries() {
		if row.Action != actionAccept {
			continue
		}
		if _, present := row.Detail[detailRole]; present {
			t.Errorf("the acceptance audit carries a role key without a role: %+v", row.Detail)
		}
	}
}

// TestAnAcceptanceWithoutAnAuditDoesNotHappen checks that an audit row that cannot land means the acceptance is not carried out.
func TestAnAcceptanceWithoutAnAuditDoesNotHappen(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "alice@example.com")
	h.audit.fails(errors.New("the audit store is down"))

	caller := h.acceptCtx("acct-alice", "alice@example.com")
	_, err := h.p.Accept(caller, AcceptInvite{Token: issued.Token})
	var fault speedhttp.Fault
	if !errors.As(err, &fault) || fault.Status != http.StatusServiceUnavailable {
		t.Fatalf("accepting with a failing audit gave %v, want the 503 of an audit that did not land", err)
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusPending {
		t.Errorf("status = %d, want pending: the acceptance was not carried out", got)
	}
	if h.members.count != 0 {
		t.Errorf("the acceptance wrote %d membership(s) without an audit row", h.members.count)
	}
}

// TestTheAcceptanceRouteAnswersAWithdrawnRoleAsTerminal checks that a withdrawn role answers 409 invitation-terminal.
func TestTheAcceptanceRouteAnswersAWithdrawnRoleAsTerminal(t *testing.T) {
	h := newHarness(t)
	issued := h.issueRole(tenant, "alice@example.com", "editor")
	h.rolePolicy.stillErr = authn.ErrRoleUnknown
	h.rolePolicy.stillErrFor = string(adminID)
	h.acceptAsHTTP("acct-alice", "alice@example.com")

	w := h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]any{})
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
	if got := problemTypeOf(t, w); got != "urn:speed:authn:invitation-terminal" {
		t.Errorf("problem type = %q, want urn:speed:authn:invitation-terminal", got)
	}
	if row := h.row(issued.Invitation.ID); row.Status != statusRevoked {
		t.Errorf("status = %d, want revoked", row.Status)
	}
}

type failingTenantNames struct{ err error }

func (f failingTenantNames) TenantNameOf(context.Context, string) (string, error) {
	return "", f.err
}

// TestARoleWithdrawalIsAuditedWhenTheTenantNameIsUnreadable checks that the revocation audit lands with the tenant identifier when the name cannot be read.
func TestARoleWithdrawalIsAuditedWhenTheTenantNameIsUnreadable(t *testing.T) {
	h := newHarness(t)
	h.p.tenantNames = failingTenantNames{err: errors.New("the name store is down")}
	h.rolePolicy.stillErr = authn.ErrRoleUnknown
	h.rolePolicy.stillErrFor = string(adminID)
	issued := h.issueRole(tenant, "alice@example.com", "editor")

	if _, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token}); !errors.Is(err, ErrInvitationRoleWithdrawn) {
		t.Fatalf("accepting gave %v, want ErrInvitationRoleWithdrawn", err)
	}
	assertRevokedAudit(t, h, issued.Invitation.ID, authn.ReasonInvitationRoleWithdrawn)
}
