package token

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

func (r *rig) accessFor(session authn.SessionID, clientID string) string {
	r.t.Helper()
	in := normalIssue(session)
	in.ClientID = clientID
	access, err := r.p.Issue(context.Background(), in)
	if err != nil {
		r.t.Fatalf("Issue: %v", err)
	}
	return access.Value
}

func (r *rig) closeStore() {
	r.t.Helper()
	sqlDB, err := r.p.handle.DB()
	if err != nil {
		r.t.Fatalf("taking the database handle: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		r.t.Fatalf("closing the database: %v", err)
	}
}

// TestRevokeOfARefreshTokenRevokesItsSession checks that revoke of a refresh
// token revokes its session.
func TestRevokeOfARefreshTokenRevokesItsSession(t *testing.T) {
	r := newRig(t)
	id, refresh := r.issueRefresh()
	if err := r.p.Revoke(context.Background(), refresh.Value); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if got := r.sessions.revoked; len(got) != 1 || got[0] != id {
		t.Errorf("Revoke revoked %v, want the refresh token's session %s", got, id)
	}
}

// TestRevokeOfAnOrdinaryAccessTokenRevokesItsSession checks that revoke of an
// ordinary access token revokes its session.
func TestRevokeOfAnOrdinaryAccessTokenRevokesItsSession(t *testing.T) {
	r := newRig(t)
	id, _ := r.issueRefresh()
	access := r.accessFor(id, "")
	if err := r.p.Revoke(context.Background(), access); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if got := r.sessions.revoked; len(got) != 1 || got[0] != id {
		t.Errorf("Revoke revoked %v, want the token's session %s", got, id)
	}
}

// TestRevokeOfAServiceTokenIsANoOp checks that revoke of a service token is a
// no op.
func TestRevokeOfAServiceTokenIsANoOp(t *testing.T) {
	r := newRig(t)
	service := authn.SessionID("sess-service-1")
	r.sessions.put(service, authn.SessionStatus{Active: true, Account: account, Kind: authn.SessionService})
	access := r.accessFor(service, string(account))
	if err := r.p.Revoke(context.Background(), access); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if len(r.sessions.revoked) != 0 {
		t.Errorf("a service token revoked %v, want nothing", r.sessions.revoked)
	}
}

// TestRevokeOfAnImpersonationTokenEndsTheRecordOnly checks that revoke of an
// impersonation token ends the record only.
func TestRevokeOfAnImpersonationTokenEndsTheRecordOnly(t *testing.T) {
	r := newRig(t)
	admin := authn.SessionID("sess-admin-1")
	r.sessions.put(admin, authn.SessionStatus{Active: true, Account: "acct-admin", Kind: authn.SessionToken})
	in := normalIssue(admin)
	in.Actor = "acct-admin"
	in.ImpersonationID = "grant-1"
	r.activeRecord("grant-1", epoch.Add(defaults.AccessTTL))
	access, err := r.p.Issue(context.Background(), in)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := r.p.Revoke(context.Background(), access.Value); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if got := r.imp.revoked; len(got) != 1 || got[0] != "grant-1" {
		t.Errorf("Revoke ended %v, want the impersonation record grant-1", got)
	}
	if len(r.sessions.revoked) != 0 {
		t.Errorf("revoking an impersonation token revoked the administrator's session: %v", r.sessions.revoked)
	}
}

// TestRevokeOfAnUnrecognisableTokenIsANoOpAndAlwaysSucceeds checks that
// revoke of an unrecognisable token is a no op and always succeeds.
func TestRevokeOfAnUnrecognisableTokenIsANoOpAndAlwaysSucceeds(t *testing.T) {
	r := newRig(t)
	for _, presented := range []string{
		"",
		"not-a-token",
		"spd_rt_" + strings.Repeat("Z", 43),
		"a.b.c",
		strings.Repeat("x", maxAccessTokenBytes+1),
	} {
		if err := r.p.Revoke(context.Background(), presented); err != nil {
			t.Errorf("Revoke(%q) = %v, want nil", presented, err)
		}
	}
	if len(r.sessions.revoked) != 0 || len(r.imp.revoked) != 0 {
		t.Errorf("an unrecognised token acted: sessions %v, impersonation %v",
			r.sessions.revoked, r.imp.revoked)
	}
}

// TestRevokeDoesNotRevokeForATokenOfAnotherKey holds that a token whose
// signature does not hold is a no-op: a revocation never trusts an unsigned
// payload.
func TestRevokeDoesNotRevokeForATokenOfAnotherKey(t *testing.T) {
	r := newRig(t)
	id, _ := r.issueRefresh()
	other := loadedSet(t, algES256)
	payload, err := tokenClaims{
		Issuer: issuer, Subject: string(account), Audience: []string{issuer},
		Expiry: epoch.Add(600).Unix(), IssuedAt: epoch.Unix(), NotBefore: epoch.Unix(),
		ID: "jti", Session: string(id), ClientID: clientIDDefault,
	}.marshal()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sign(*other.signing, accessTokenType, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.p.Revoke(context.Background(), raw); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if len(r.sessions.revoked) != 0 {
		t.Errorf("a token of another key revoked %v, want nothing", r.sessions.revoked)
	}
}

// TestRevokeOfARefreshTokenRefusesWhenTheTableIsUnreachable holds the failure
// direction of a revocation: a dependency that cannot answer is ErrUnavailable,
// never the success a presenter would read as "the token is gone".
func TestRevokeOfARefreshTokenRefusesWhenTheTableIsUnreachable(t *testing.T) {
	r := newRig(t)
	_, refresh := r.issueRefresh()
	r.closeStore()

	if err := r.p.Revoke(context.Background(), refresh.Value); !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("Revoke = %v, want ErrUnavailable", err)
	}
	if len(r.sessions.revoked) != 0 {
		t.Errorf("a revocation that could not reach the table revoked %v", r.sessions.revoked)
	}
}

// TestRevokeOfARefreshTokenRefusesWhenTheSessionWriteFails holds that a
// revocation whose session write fails is not reported as success: the session
// stays live, so the caller must not be told it was revoked.
func TestRevokeOfARefreshTokenRefusesWhenTheSessionWriteFails(t *testing.T) {
	r := newRig(t)
	_, refresh := r.issueRefresh()
	r.sessions.revokeErr = errors.New("token test: the session store is down")

	if err := r.p.Revoke(context.Background(), refresh.Value); !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("Revoke = %v, want ErrUnavailable", err)
	}
	if len(r.sessions.revoked) != 0 {
		t.Errorf("a failed write recorded a revocation: %v", r.sessions.revoked)
	}
}

// TestRevokeOfAnAccessTokenRefusesWhenTheSessionStoreIsUnreachable holds the
// same direction on the access-token path: the session read that decides the
// kind cannot answer, so the revocation refuses rather than revoke blindly.
func TestRevokeOfAnAccessTokenRefusesWhenTheSessionStoreIsUnreachable(t *testing.T) {
	r := newRig(t)
	id, _ := r.issueRefresh()
	access := r.accessFor(id, "")
	r.sessions.freshErr = errors.New("token test: the session store is down")

	if err := r.p.Revoke(context.Background(), access); !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("Revoke = %v, want ErrUnavailable", err)
	}
	if len(r.sessions.revoked) != 0 {
		t.Errorf("a revocation that could not read the session revoked %v", r.sessions.revoked)
	}
}

// TestRevokeOfAnImpersonationTokenRefusesWhenTheRecordStoreIsUnreachable holds
// that ending an impersonation record that cannot be reached is ErrUnavailable,
// not a success the still-live record contradicts.
func TestRevokeOfAnImpersonationTokenRefusesWhenTheRecordStoreIsUnreachable(t *testing.T) {
	r := newRig(t)
	admin := authn.SessionID("sess-admin-1")
	r.sessions.put(admin, authn.SessionStatus{Active: true, Account: "acct-admin", Kind: authn.SessionToken})
	in := normalIssue(admin)
	in.Actor = "acct-admin"
	in.ImpersonationID = "grant-1"
	r.activeRecord("grant-1", epoch.Add(defaults.AccessTTL))
	access, err := r.p.Issue(context.Background(), in)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	r.imp.err = errors.New("token test: the impersonation store is down")

	if err := r.p.Revoke(context.Background(), access.Value); !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("Revoke = %v, want ErrUnavailable", err)
	}
	if len(r.sessions.revoked) != 0 {
		t.Errorf("revoking an impersonation token revoked the administrator's session: %v", r.sessions.revoked)
	}
}

// TestRevokeOfATokenForAnAbsentSessionStillSucceeds holds the other half: a
// session the token names that is already gone is a no-op success, so a token
// that outlived its session does not turn a revocation into a 503.
func TestRevokeOfATokenForAnAbsentSessionStillSucceeds(t *testing.T) {
	r := newRig(t)
	access := r.accessFor(authn.SessionID("sess-gone"), "")
	if err := r.p.Revoke(context.Background(), access); err != nil {
		t.Fatalf("Revoke = %v, want nil", err)
	}
	if len(r.sessions.revoked) != 0 {
		t.Errorf("a no-op revocation acted: %v", r.sessions.revoked)
	}
}
