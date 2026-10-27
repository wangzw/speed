package login

import (
	"net/http"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// impersonatedAuthenticated puts an impersonating identity on the request and
// the operator's session status behind it: the session belongs to the actor and
// names the record, while the identity's account is the target.
func (r *rig) impersonatedAuthenticated(actor, target authn.AccountID, session authn.SessionID) {
	r.t.Helper()
	now := time.Now()
	r.m.As(
		authntest.WithAccount(target),
		authntest.WithActor(actor),
		authntest.WithSession(session),
		authntest.WithScope("tenant-a"),
		authntest.WithMethods(authn.MethodPassword, authn.MethodTOTP),
		authntest.WithAssurance(authn.AssuranceMulti),
		authntest.WithAuthTime(now),
	)
	r.sessions.status = authn.SessionStatus{
		Active: true, Account: actor, Kind: authn.SessionBrowser,
		Methods: []authn.Method{authn.MethodPassword, authn.MethodTOTP},
		Assurance: authn.AssuranceMulti, AuthTime: now,
		ImpersonationID: "grant-1",
	}
}

// TestAnImpersonatingIdentityStartsAnAuthenticatedFlow holds that the route on
// an already authenticated identity is open to an impersonating identity: the
// session it was started on belongs to the operator and names the record, and
// the flow is recorded for the effective account.
func TestAnImpersonatingIdentityStartsAnAuthenticatedFlow(t *testing.T) {
	r := newRig(t, &fakeMethod{name: "password"})
	r.impersonatedAuthenticated("acct-admin", "acct-target", "sid-op")

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"enroll","input":{"kind":"totp"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated while impersonating = %d: %s", start.Code, start.Body.String())
	}
	if len(r.mfa.enrolls) != 1 {
		t.Errorf("MFA.Enroll ran %d times, want the enrollment to start", len(r.mfa.enrolls))
	}
}
