package token

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// serviceSession puts a live service session in the store and points the
// password checker at it, so a case reaches the issuing half of the grant.
func (r *rig) serviceSession() authn.SessionID {
	id := authn.SessionID("sess-service-1")
	r.sessions.put(id, authn.SessionStatus{Active: true, Account: account, Kind: authn.SessionService})
	r.sessions.forResult = id
	return id
}

func TestClientCredentialsThrottleKeyIsThePresentedClientID(t *testing.T) {
	r := newRig(t)
	r.throttle.retry = 30 * time.Second
	const client = "client-xyz"

	_, err := r.p.clientCredentials(context.Background(), client, "spd_cs_whatever")
	if !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("a throttled client credential gave %v, want ErrThrottled", err)
	}
	var throttled *authn.ThrottledError
	if !errors.As(err, &throttled) || throttled.RetryAfter != 30*time.Second {
		t.Errorf("the throttle error is %v, want a 30s RetryAfter", err)
	}
	if len(r.throttle.checkKeys) != 1 || r.throttle.checkKeys[0] != client {
		t.Errorf("the back-off key is %v, want the presented client_id %q", r.throttle.checkKeys, client)
	}
	if r.passwords.calls != 0 {
		t.Error("a throttled request still verified the secret")
	}
}

func TestAnUnknownClientAndAWrongSecretAnswerAlike(t *testing.T) {
	r := newRig(t)
	r.passwords.verify = func(string, string) (authn.AccountID, string, time.Time, error) {
		return "", "", time.Time{}, authn.ErrInvalidCredentials
	}
	ctx := context.Background()

	_, unknown := r.p.clientCredentials(ctx, "client-unknown", "spd_cs_whatever")
	_, wrong := r.p.clientCredentials(ctx, "client-known", "spd_cs_wrong")
	if !errors.Is(unknown, authn.ErrInvalidCredentials) || !errors.Is(wrong, authn.ErrInvalidCredentials) {
		t.Fatalf("the answers are %v and %v, want the same invalid-credential error", unknown, wrong)
	}
	if unknown.Error() != wrong.Error() {
		t.Errorf("an unknown client and a wrong secret read differently: %q vs %q", unknown, wrong)
	}
	if len(r.throttle.failedKeys) != 2 {
		t.Errorf("the failed attempts were counted as %v, want two", r.throttle.failedKeys)
	}
	for i, want := range []string{"client-unknown", "client-known"} {
		if r.throttle.failedKeys[i] != want {
			t.Errorf("failedKeys[%d] = %q, want %q", i, r.throttle.failedKeys[i], want)
		}
	}
}

func TestClientCredentialsIssuesOnlyAnAccessToken(t *testing.T) {
	r := newRig(t)
	session := r.serviceSession()
	r.passwords.verify = func(clientID, secret string) (authn.AccountID, string, time.Time, error) {
		return account, "secret-1", time.Time{}, nil
	}

	access, err := r.p.clientCredentials(context.Background(), string(account), "spd_cs_whatever")
	if err != nil {
		t.Fatalf("clientCredentials: %v", err)
	}
	if r.sessions.forCalls != 1 {
		t.Errorf("ForCredential was called %d times, want once", r.sessions.forCalls)
	}
	if got := r.throttle.succeeded; len(got) != 1 || got[0] != string(account) {
		t.Errorf("the success cleared %v, want the client key", got)
	}

	claims := r.issuedClaims(access.Value)
	if claims["sid"] != string(session) {
		t.Errorf("sid is %v, want the service session %s", claims["sid"], session)
	}
	if amr, ok := claims["amr"].([]any); !ok || len(amr) != 1 || amr[0] != string(authn.MethodClientSecret) {
		t.Errorf("amr is %v, want [%s]", claims["amr"], authn.MethodClientSecret)
	}
	if claims["acr"] != "1" {
		t.Errorf("acr is %v, want 1", claims["acr"])
	}
	if claims["client_id"] != string(account) {
		t.Errorf("client_id is %v, want the account identifier", claims["client_id"])
	}
	if !access.ExpiresAt.Equal(epoch.Add(defaults.AccessTTL)) {
		t.Errorf("the access token expires at %v, want %v", access.ExpiresAt, epoch.Add(defaults.AccessTTL))
	}
}

// TestClientCredentialsOfARevokedSecretIsRefused holds that a revoked secret's
// session refuses the exchange rather than minting a token.
func TestClientCredentialsOfARevokedSecretIsRefused(t *testing.T) {
	r := newRig(t)
	r.passwords.verify = func(string, string) (authn.AccountID, string, time.Time, error) {
		return account, "secret-1", time.Time{}, nil
	}
	r.sessions.forErr = authn.ErrSessionRevoked
	if _, err := r.p.clientCredentials(context.Background(), string(account), "spd_cs_whatever"); !errors.Is(err, authn.ErrSessionRevoked) {
		t.Fatalf("a revoked secret gave %v, want ErrSessionRevoked", err)
	}
}

// TestClientCredentialsWithNoSigningKeyIsErrNoSigningKey holds the failure
// direction: with no signing key the grant cannot issue, and says so.
func TestClientCredentialsWithNoSigningKeyIsErrNoSigningKey(t *testing.T) {
	r := newRig(t)
	r.serviceSession()
	r.passwords.verify = func(string, string) (authn.AccountID, string, time.Time, error) {
		return account, "secret-1", time.Time{}, nil
	}
	r.p.keys.signing = nil
	if _, err := r.p.clientCredentials(context.Background(), string(account), "spd_cs_whatever"); !errors.Is(err, authn.ErrNoSigningKey) {
		t.Fatalf("client credentials with no signing key gave %v, want ErrNoSigningKey", err)
	}
}

// TestAClientCredentialBackOffThatCannotAnswerIsUnavailable holds that a failed
// back-off check refuses the grant with ErrUnavailable rather than falling
// through to verify the secret: the throttle store being unreachable is not a
// reason to skip the check.
func TestAClientCredentialBackOffThatCannotAnswerIsUnavailable(t *testing.T) {
	r := newRig(t)
	r.throttle.checkErr = errors.New("token test: the back-off store is down")
	_, err := r.p.clientCredentials(context.Background(), "client-x", "spd_cs_whatever")
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("a back-off check that failed gave %v, want ErrUnavailable", err)
	}
	if r.passwords.calls != 0 {
		t.Error("a failed back-off check still verified the secret")
	}
	if len(r.throttle.failedKeys) != 0 {
		t.Errorf("a failed back-off check counted failures: %v", r.throttle.failedKeys)
	}
}

// TestASecretStoreOutageIsUnavailableNotAnInvalidCredential holds the failure
// direction: a store that cannot answer is ErrUnavailable, not a credential
// failure that would count against the client and read as a wrong secret.
func TestASecretStoreOutageIsUnavailableNotAnInvalidCredential(t *testing.T) {
	r := newRig(t)
	r.passwords.verify = func(string, string) (authn.AccountID, string, time.Time, error) {
		return "", "", time.Time{}, refreshUnavailable("reading a service secret", errors.New("down"))
	}
	_, err := r.p.clientCredentials(context.Background(), "client-x", "spd_cs_whatever")
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("a secret store outage gave %v, want ErrUnavailable", err)
	}
	if errors.Is(err, authn.ErrInvalidCredentials) {
		t.Error("a secret store outage read as an invalid credential")
	}
	if len(r.throttle.failedKeys) != 0 {
		t.Errorf("a store outage was counted against the client: %v", r.throttle.failedKeys)
	}
}

// TestTheSecretExpiryIsHandedToTheSessionStore holds that a real expiry is
// passed through as a non-nil pointer and an absent one as nil, so the service
// session is bound to the secret's lifetime only when it has one.
func TestTheSecretExpiryIsHandedToTheSessionStore(t *testing.T) {
	r := newRig(t)
	r.serviceSession()
	expiry := epoch.Add(48 * time.Hour)
	r.passwords.verify = func(string, string) (authn.AccountID, string, time.Time, error) {
		return account, "secret-1", expiry, nil
	}
	if _, err := r.p.clientCredentials(context.Background(), string(account), "spd_cs_whatever"); err != nil {
		t.Fatalf("clientCredentials: %v", err)
	}
	if r.sessions.forExpiry == nil || !r.sessions.forExpiry.Equal(expiry) {
		t.Errorf("ForCredential received expiry %v, want %v", r.sessions.forExpiry, expiry)
	}

	r = newRig(t)
	r.serviceSession()
	r.passwords.verify = func(string, string) (authn.AccountID, string, time.Time, error) {
		return account, "secret-1", time.Time{}, nil
	}
	if _, err := r.p.clientCredentials(context.Background(), string(account), "spd_cs_whatever"); err != nil {
		t.Fatalf("clientCredentials: %v", err)
	}
	if r.sessions.forExpiry != nil {
		t.Errorf("ForCredential received expiry %v for a secret with none, want nil", r.sessions.forExpiry)
	}
}
