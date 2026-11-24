package token

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/verdict"
)

// The fixture's fixed names and instant, around which every case builds its times.
var (
	vfyNow      = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	vfyAlice    = authn.AccountID("acct-alice")
	vfyBob      = authn.AccountID("acct-bob")
	vfyAdmin    = authn.AccountID("acct-admin")
	vfySession  = authn.SessionID("sess-1")
	vfyIssuer   = "https://auth.example.com"
	vfyAudience = "https://api.example.com"
)

// The product satisfies both interfaces, held at compile time.
var (
	_ authn.Verifier   = (*product)(nil)
	_ authn.Challenger = (*product)(nil)
)

// vfyClock is a clock a case reads and never moves beyond vfyNow.
type vfyClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *vfyClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

// vfyAccounts is the account snapshot store; err stands for an unreachable
// database.
type vfyAccounts struct {
	authn.Accounts
	accounts map[authn.AccountID]authn.Account
	err      error
}

func (s *vfyAccounts) Get(_ context.Context, id authn.AccountID) (authn.Account, error) {
	if s.err != nil {
		return authn.Account{}, s.err
	}
	a, ok := s.accounts[id]
	if !ok {
		return authn.Account{}, authn.ErrAccountNotFound
	}
	return a, nil
}

// vfySessions is the session store; err stands for an unreachable database.
type vfySessions struct {
	authn.Sessions
	status   authn.SessionStatus
	notFound bool
	err      error
}

func (s *vfySessions) Status(_ context.Context, _ authn.SessionID) (authn.SessionStatus, error) {
	switch {
	case s.err != nil:
		return authn.SessionStatus{}, s.err
	case s.notFound:
		return authn.SessionStatus{}, authn.ErrSessionNotFound
	}
	return s.status, nil
}

type vfyImpersonation struct {
	authn.Impersonation
	grant  authn.ImpersonationGrant
	active bool
	err    error
}

func (s *vfyImpersonation) Active(_ context.Context, _ string) (authn.ImpersonationGrant, bool, error) {
	return s.grant, s.active, s.err
}

type vfy struct {
	t    *testing.T
	p    *product
	set  *keySet
	st   *fakeSettings
	clk  *vfyClock
	acct *vfyAccounts
	sess *vfySessions
	imp  *vfyImpersonation
}

func newVfy(t *testing.T) *vfy { return newVfyWithSet(t, loadedSet(t, algES256)) }

func newVfyWithSet(t *testing.T, set *keySet) *vfy {
	t.Helper()
	clk := &vfyClock{at: vfyNow}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	acct := &vfyAccounts{accounts: map[authn.AccountID]authn.Account{
		vfyAlice: activeAccount(vfyAlice),
		vfyBob:   activeAccount(vfyBob),
		vfyAdmin: activeAccount(vfyAdmin),
	}}
	sess := &vfySessions{status: authn.SessionStatus{
		Active:    true,
		Account:   vfyAlice,
		Kind:      authn.SessionToken,
		Methods:   []authn.Method{authn.MethodPassword},
		Assurance: authn.AssuranceSingle,
		AuthTime:  vfyNow,
	}}
	imp := &vfyImpersonation{}
	store := newFakeSettings()
	p := &product{
		cfg: resolved{
			issuer:    vfyIssuer,
			audience:  []string{vfyAudience},
			clientID:  clientIDDefault,
			algorithm: set.algorithms[0],
		},
		keys:          set,
		logger:        logger,
		clock:         clk.Now,
		accounts:      acct,
		sessions:      sess,
		impersonation: imp,
	}
	p.settings = newSettingReader(logger)
	p.settings.store = store
	return &vfy{t: t, p: p, set: set, st: store, clk: clk, acct: acct, sess: sess, imp: imp}
}

func activeAccount(id authn.AccountID) authn.Account {
	return authn.Account{ID: id, Status: authn.StatusActive}
}

func (v *vfy) verify(token string) authn.Verdict {
	return v.p.Verify(context.Background(), authn.Credential{Scheme: schemeBearer, Value: token})
}

func (v *vfy) header() map[string]any {
	return map[string]any{"alg": algES256, "typ": accessTokenType, "kid": v.set.signing.kid}
}

func (v *vfy) sign(claims, header map[string]any) string {
	return vfyJWS(v.t, v.set.signing.signer, header, claims)
}

func (v *vfy) good() string { return v.sign(vfyClaims(), v.header()) }

func vfyClaims() map[string]any {
	return map[string]any{
		"iss":       vfyIssuer,
		"sub":       string(vfyAlice),
		"aud":       []string{vfyAudience},
		"exp":       vfyNow.Add(5 * time.Minute).Unix(),
		"nbf":       vfyNow.Unix(),
		"iat":       vfyNow.Unix(),
		"jti":       "jti-1",
		"sid":       string(vfySession),
		"amr":       []string{"pwd"},
		"acr":       "1",
		"auth_time": vfyNow.Unix(),
		"client_id": "speed",
	}
}

// vfyJWS signs a compact JWS over hdr and claims with key, in the key's own
// algorithm, so a case controls the header a token carries.
func vfyJWS(t *testing.T, key crypto.Signer, hdr, claims map[string]any) string {
	t.Helper()
	hb, err := json.Marshal(hdr)
	if err != nil {
		t.Fatalf("marshalling the header: %v", err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshalling the claims: %v", err)
	}
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	return signing + "." + vfySignature(t, key, signing)
}

func vfySignature(t *testing.T, key crypto.Signer, signing string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(signing))
	switch k := key.(type) {
	case *ecdsa.PrivateKey:
		r, s, err := ecdsa.Sign(rand.Reader, k, sum[:])
		if err != nil {
			t.Fatalf("signing: %v", err)
		}
		buf := make([]byte, 64)
		r.FillBytes(buf[:32])
		s.FillBytes(buf[32:])
		return base64.RawURLEncoding.EncodeToString(buf)
	case *rsa.PrivateKey:
		sig, err := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, sum[:])
		if err != nil {
			t.Fatalf("signing: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(sig)
	default:
		t.Fatalf("the key is a %T, which the test signer does not know", key)
		return ""
	}
}

func outcomeOf(v authn.Verdict) verdict.Outcome { return verdict.OutcomeOf(v) }
func principalOf(v authn.Verdict) authn.Principal {
	return verdict.PrincipalOf(v)
}
func causeOf(v authn.Verdict) error { return verdict.CauseOf(v) }

func requireDenied(t *testing.T, v authn.Verdict) {
	t.Helper()
	if got := outcomeOf(v); got != verdict.Denied {
		t.Fatalf("the verdict is %s, want denied", got)
	}
	if err := causeOf(v); !errors.Is(err, authn.ErrTokenInvalid) {
		t.Errorf("the cause is %v, which does not wrap ErrTokenInvalid", err)
	}
}

// TestTheBackendDeclaresWhatTheDesignFixesForIt checks the backend's name and
// accepted credential shapes.
func TestTheBackendDeclaresWhatTheDesignFixesForIt(t *testing.T) {
	v := newVfy(t)
	if got := v.p.Name(); got != "token" {
		t.Errorf("Name = %q, want token", got)
	}
	a := v.p.Accepts()
	if len(a.Schemes) != 1 || a.Schemes[0] != "bearer" {
		t.Errorf("Accepts.Schemes = %v, want [bearer]", a.Schemes)
	}
	if a.Accounts != "" {
		t.Errorf("Accepts.Accounts = %q, want the library namespace", a.Accounts)
	}
	if !a.Impersonation || !a.SSO {
		t.Errorf("Accepts = %+v, want impersonation and SSO declared", a)
	}
	if len(a.Cookies) != 0 {
		t.Errorf("Accepts.Cookies = %v, want none", a.Cookies)
	}
}

// TestTheChallengeNamesTheTokenOnARefusal names the token in
// error="invalid_token" on a refusal.
func TestTheChallengeNamesTheTokenOnARefusal(t *testing.T) {
	v := newVfy(t)
	got := v.p.Challenge("app", true)
	if len(got) != 1 || !strings.Contains(got[0], `error="invalid_token"`) || !strings.Contains(got[0], `realm="app"`) {
		t.Errorf("the refused challenge is %v, want a Bearer challenge with invalid_token", got)
	}
	clean := v.p.Challenge("app", false)
	if len(clean) != 1 || strings.Contains(clean[0], "error=") {
		t.Errorf("the unrejected challenge is %v, want no error", clean)
	}
}

// TestAGoodTokenAuthenticatesWithTheSessionFacts authenticates a good token with
// the account's and the session's facts.
func TestAGoodTokenAuthenticatesWithTheSessionFacts(t *testing.T) {
	v := newVfy(t)
	v.sess.status.Methods = []authn.Method{authn.MethodPassword, authn.MethodTOTP}
	v.sess.status.Assurance = authn.AssuranceMulti
	v.sess.status.Attributes = map[string]string{"idp.role": "staff"}
	v.acct.accounts[vfyAlice] = authn.Account{
		ID: vfyAlice, Status: authn.StatusActive, Attributes: map[string]string{"region": "eu"},
	}

	got := principalOf(v.verify(v.good()))
	if got.Account != vfyAlice || got.Session != vfySession {
		t.Errorf("the principal is %+v, want account %s and session %s", got, vfyAlice, vfySession)
	}
	if got.Actor != "" || got.Scope != "" {
		t.Errorf("a normal token carries an actor %q or scope %q", got.Actor, got.Scope)
	}
	if len(got.Methods) != 2 || got.Assurance != authn.AssuranceMulti {
		t.Errorf("the methods are %v and the assurance %v, want two methods and multi", got.Methods, got.Assurance)
	}
	if got.Attributes["region"] != "eu" || got.Attributes["idp.role"] != "staff" {
		t.Errorf("the attributes are %v, want the account's and the session's", got.Attributes)
	}
}

// TestATokenThatFailsAStepIsRefused refuses a token that fails any verifying step.
func TestATokenThatFailsAStepIsRefused(t *testing.T) {
	cases := []struct {
		name string
		make func(v *vfy) string
	}{
		{"longer than the limit", func(v *vfy) string { return strings.Repeat("a", maxTokenLen+1) }},
		{"not three segments", func(v *vfy) string { return "only.two" }},
		{"empty", func(v *vfy) string { return "" }},
		{"alg none", func(v *vfy) string {
			h := map[string]any{"alg": "none", "typ": accessTokenType, "kid": v.set.signing.kid}
			hb, _ := json.Marshal(h)
			cb, _ := json.Marshal(vfyClaims())
			return base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb) + "."
		}},
		{"an algorithm outside the allow-list", func(v *vfy) string {
			h := v.header()
			h["alg"] = "HS256"
			return v.sign(vfyClaims(), h)
		}},
		{"a typ that is not an access token", func(v *vfy) string {
			h := v.header()
			h["typ"] = "JWT"
			return v.sign(vfyClaims(), h)
		}},
		{"no typ", func(v *vfy) string {
			h := v.header()
			delete(h, "typ")
			return v.sign(vfyClaims(), h)
		}},
		{"a critical header extension", func(v *vfy) string {
			h := v.header()
			// "b64" is a critical name go-jose understands, so the library lets it
			// through and this backend's refusal of any crit is what catches it.
			h["crit"] = []string{"b64"}
			return v.sign(vfyClaims(), h)
		}},
		{"a kid that is not in the set", func(v *vfy) string {
			h := v.header()
			h["kid"] = "not-a-configured-key"
			return v.sign(vfyClaims(), h)
		}},
		{"no kid", func(v *vfy) string {
			h := v.header()
			delete(h, "kid")
			return v.sign(vfyClaims(), h)
		}},
		{"no exp", func(v *vfy) string {
			c := vfyClaims()
			delete(c, "exp")
			return v.sign(c, v.header())
		}},
		{"no iat", func(v *vfy) string {
			c := vfyClaims()
			delete(c, "iat")
			return v.sign(c, v.header())
		}},
		{"no jti", func(v *vfy) string {
			c := vfyClaims()
			delete(c, "jti")
			return v.sign(c, v.header())
		}},
		{"no sid", func(v *vfy) string {
			c := vfyClaims()
			delete(c, "sid")
			return v.sign(c, v.header())
		}},
		{"no sub", func(v *vfy) string {
			c := vfyClaims()
			delete(c, "sub")
			return v.sign(c, v.header())
		}},
		{"no client_id", func(v *vfy) string {
			c := vfyClaims()
			delete(c, "client_id")
			return v.sign(c, v.header())
		}},
		{"an issuer that does not match", func(v *vfy) string {
			c := vfyClaims()
			c["iss"] = "https://elsewhere.example.com"
			return v.sign(c, v.header())
		}},
		{"an audience that does not intersect", func(v *vfy) string {
			c := vfyClaims()
			c["aud"] = []string{"https://nobody.example.com"}
			return v.sign(c, v.header())
		}},
		{"an expiry more than an hour after it was issued", func(v *vfy) string {
			c := vfyClaims()
			c["exp"] = vfyNow.Add(2 * time.Hour).Unix()
			return v.sign(c, v.header())
		}},
		{"an expired token past the leeway", func(v *vfy) string {
			c := vfyClaims()
			c["exp"] = vfyNow.Add(-defaults.Leeway - time.Second).Unix()
			return v.sign(c, v.header())
		}},
		{"a not-yet-valid token past the leeway", func(v *vfy) string {
			c := vfyClaims()
			c["nbf"] = vfyNow.Add(defaults.Leeway + time.Second).Unix()
			return v.sign(c, v.header())
		}},
		{"a token issued in the future past the leeway", func(v *vfy) string {
			c := vfyClaims()
			c["iat"] = vfyNow.Add(defaults.Leeway + time.Second).Unix()
			return v.sign(c, v.header())
		}},
		{"a sub that is not an account identifier", func(v *vfy) string {
			c := vfyClaims()
			c["sub"] = "not a valid id"
			return v.sign(c, v.header())
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newVfy(t)
			requireDenied(t, v.verify(tc.make(v)))
		})
	}
}

// TestATokenSignedWithThePublicKeyAsAMACIsRefused refuses an HS256 token keyed on
// the RSA public key's bytes and labelled RS256.
func TestATokenSignedWithThePublicKeyAsAMACIsRefused(t *testing.T) {
	v := newVfyWithSet(t, loadedSet(t, algRS256))
	pub := v.set.signing.pub.(*rsa.PublicKey)
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshalling the public key: %v", err)
	}

	hdr := map[string]any{"alg": algRS256, "typ": accessTokenType, "kid": v.set.signing.kid}
	hb, _ := json.Marshal(hdr)
	cb, _ := json.Marshal(vfyClaims())
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	mac := hmac.New(sha256.New, der)
	mac.Write([]byte(signing))
	raw := signing + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	requireDenied(t, v.verify(raw))
}

// TestTheClockSkewWithinTheLeewayIsTolerated authenticates a token whose times
// sit within the leeway.
func TestTheClockSkewWithinTheLeewayIsTolerated(t *testing.T) {
	v := newVfy(t)
	c := vfyClaims()
	c["iat"] = vfyNow.Add(defaults.Leeway - time.Second).Unix()
	c["exp"] = vfyNow.Add(defaults.Leeway - time.Second).Unix()
	if got := outcomeOf(v.verify(v.sign(c, v.header()))); got != verdict.Authenticated {
		t.Errorf("a token within the leeway is %s, want authenticated", got)
	}
}

// TestAnAudienceSetMatchesOnAnyMember authenticates a token meeting any one of
// the configured audiences.
func TestAnAudienceSetMatchesOnAnyMember(t *testing.T) {
	v := newVfy(t)
	v.p.cfg.audience = []string{"https://one.example.com", vfyAudience}
	if got := outcomeOf(v.verify(v.good())); got != verdict.Authenticated {
		t.Errorf("a token meeting one of the audiences is %s, want authenticated", got)
	}
}

// TestALifetimeOfExactlyOneHourIsAccepted accepts a token whose lifetime is
// exactly one hour.
func TestALifetimeOfExactlyOneHourIsAccepted(t *testing.T) {
	v := newVfy(t)
	c := vfyClaims()
	c["exp"] = vfyNow.Add(time.Hour).Unix()
	if got := outcomeOf(v.verify(v.sign(c, v.header()))); got != verdict.Authenticated {
		t.Errorf("a token with a one-hour lifetime is %s, want authenticated", got)
	}
}

// TestTheAccountSnapshotRefusesADisabledAccountOrAnOlderToken refuses a disabled
// account, a token older than valid_after, and an unknown account.
func TestTheAccountSnapshotRefusesADisabledAccountOrAnOlderToken(t *testing.T) {
	v := newVfy(t)
	a := v.acct.accounts[vfyAlice]
	a.Status = authn.StatusDisabled
	v.acct.accounts[vfyAlice] = a
	requireDenied(t, v.verify(v.good()))

	v = newVfy(t)
	a = v.acct.accounts[vfyAlice]
	a.ValidAfter = vfyNow.Add(30 * time.Second)
	v.acct.accounts[vfyAlice] = a
	requireDenied(t, v.verify(v.good()))

	v = newVfy(t)
	requireDenied(t, v.verify(v.sign(withSub(vfyClaims(), "acct-ghost"), v.header())))
}

// TestTheSessionStepRefusesAnInvalidOrMisownedSession refuses an inactive,
// misowned or missing session.
func TestTheSessionStepRefusesAnInvalidOrMisownedSession(t *testing.T) {
	v := newVfy(t)
	v.sess.status.Active = false
	requireDenied(t, v.verify(v.good()))

	v = newVfy(t)
	v.sess.status.Account = vfyBob
	requireDenied(t, v.verify(v.good()))

	v = newVfy(t)
	v.sess.notFound = true
	requireDenied(t, v.verify(v.good()))
}

// TestAServiceTokenMustNameAServiceSession refuses a client-secret token on a
// session of any other kind.
func TestAServiceTokenMustNameAServiceSession(t *testing.T) {
	v := newVfy(t)
	c := vfyClaims()
	c["amr"] = []string{string(authn.MethodClientSecret)}
	v.sess.status.Kind = authn.SessionToken
	requireDenied(t, v.verify(v.sign(c, v.header())))

	v = newVfy(t)
	v.sess.status.Kind = authn.SessionService
	got := principalOf(v.verify(v.sign(c, v.header())))
	if len(got.Methods) != 1 || got.Methods[0] != authn.MethodClientSecret {
		t.Errorf("a service token's methods are %v, want [client-secret]", got.Methods)
	}
	if got.Assurance != authn.AssuranceSingle {
		t.Errorf("a service token's assurance is %v, want single", got.Assurance)
	}
	if !got.AuthTime.Equal(vfyNow) {
		t.Errorf("a service token's auth time is %v, want the token's iat %v", got.AuthTime, vfyNow)
	}
}

func (v *vfy) impersonationToken() string {
	c := vfyClaims()
	c["sub"] = string(vfyBob)
	c["act"] = map[string]any{"sub": string(vfyAdmin)}
	c["imp"] = "imp-1"
	c["imp_tenant"] = "acme"
	v.sess.status.Account = vfyAdmin
	v.imp.grant = authn.ImpersonationGrant{
		ID: "imp-1", Actor: vfyAdmin, Target: vfyBob, Tenant: "acme", SessionID: vfySession,
	}
	v.imp.active = true
	return v.sign(c, v.header())
}

// TestAnImpersonationTokenAuthenticatesAsTheTarget authenticates as the target
// under the operator session's facts.
func TestAnImpersonationTokenAuthenticatesAsTheTarget(t *testing.T) {
	v := newVfy(t)
	v.sess.status.Assurance = authn.AssuranceMulti
	v.sess.status.IDPAssured = true
	v.sess.status.Provider = "oidc:corp"
	got := principalOf(v.verify(v.impersonationToken()))
	if got.Account != vfyBob || got.Actor != vfyAdmin {
		t.Errorf("the principal is account %s actor %s, want %s acting as %s", got.Account, got.Actor, vfyAdmin, vfyBob)
	}
	if got.Scope != "acme" {
		t.Errorf("the scope is %q, want the record's tenant acme", got.Scope)
	}
	if got.Assurance != authn.AssuranceMulti || !got.IDPAssured || got.Provider != "oidc:corp" {
		t.Errorf("assurance %v, IDPAssured %v and provider %q, want the operator session's own facts", got.Assurance, got.IDPAssured, got.Provider)
	}
}

// TestAnImpersonationTokenThatDoesNotMatchItsRecordIsRefused refuses a token whose
// actor, target, record or session disagrees with its grant.
func TestAnImpersonationTokenThatDoesNotMatchItsRecordIsRefused(t *testing.T) {
	v := newVfy(t)
	tok := v.impersonationToken()
	v.imp.grant.Actor = vfyBob
	requireDenied(t, v.verify(tok))

	v = newVfy(t)
	tok = v.impersonationToken()
	v.imp.grant.Target = vfyAlice
	requireDenied(t, v.verify(tok))

	v = newVfy(t)
	tok = v.impersonationToken()
	v.imp.active = false
	requireDenied(t, v.verify(tok))

	v = newVfy(t)
	tok = v.impersonationToken()
	v.sess.status.Account = vfyBob
	requireDenied(t, v.verify(tok))
}

// TestAnImpersonationTokenWithoutTheCapabilityIsRefused refuses an impersonation
// token when the product has no impersonation store.
func TestAnImpersonationTokenWithoutTheCapabilityIsRefused(t *testing.T) {
	v := newVfy(t)
	tok := v.impersonationToken()
	v.p.impersonation = nil
	requireDenied(t, v.verify(tok))
}

// TestTheStatelessModeOnlyDropsTheSessionCheck drops the session check alone; the
// impersonation and account checks still run.
func TestTheStatelessModeOnlyDropsTheSessionCheck(t *testing.T) {
	v := newVfy(t)
	v.st.setBool(checkSessionPath, false)
	v.sess.status.Active = false
	v.sess.status.Methods = nil
	got := principalOf(v.verify(v.good()))
	if got.Account != vfyAlice {
		t.Errorf("a revoked session in the stateless mode gave %+v, want the token's account", got)
	}
	if len(got.Methods) != 1 || got.Methods[0] != authn.MethodPassword {
		t.Errorf("the methods are %v, want those of the token's amr", got.Methods)
	}

	v = newVfy(t)
	v.st.setBool(checkSessionPath, false)
	if got := outcomeOf(v.verify(v.impersonationToken())); got != verdict.Authenticated {
		t.Errorf("a valid impersonation token under the switch is %s, want authenticated", got)
	}

	v = newVfy(t)
	v.st.setBool(checkSessionPath, false)
	tok := v.impersonationToken()
	v.imp.active = false
	requireDenied(t, v.verify(tok))

	v = newVfy(t)
	v.st.setBool(checkSessionPath, false)
	a := v.acct.accounts[vfyAlice]
	a.Status = authn.StatusDisabled
	v.acct.accounts[vfyAlice] = a
	requireDenied(t, v.verify(v.good()))
}

// TestTheAccountSnapshotIsServedFromItsCacheWithinCheckTTL serves a cached account
// for check-ttl before a change to it is seen.
func TestTheAccountSnapshotIsServedFromItsCacheWithinCheckTTL(t *testing.T) {
	v := newVfy(t)
	if got := outcomeOf(v.verify(v.good())); got != verdict.Authenticated {
		t.Fatalf("the good token is %s, want authenticated", got)
	}
	a := v.acct.accounts[vfyAlice]
	a.Status = authn.StatusDisabled
	v.acct.accounts[vfyAlice] = a
	if got := outcomeOf(v.verify(v.good())); got != verdict.Authenticated {
		t.Errorf("within check-ttl the cached account is served, got %s", got)
	}
	v.clk.at = v.clk.at.Add(defaultCheckTTL + time.Second)
	requireDenied(t, v.verify(v.good()))
}

// TestTheImpersonationRecordIsServedFromItsCacheWithinCheckTTL serves a cached
// impersonation record for check-ttl.
func TestTheImpersonationRecordIsServedFromItsCacheWithinCheckTTL(t *testing.T) {
	v := newVfy(t)
	tok := v.impersonationToken()
	if got := outcomeOf(v.verify(tok)); got != verdict.Authenticated {
		t.Fatalf("the impersonation token is %s, want authenticated", got)
	}
	v.imp.active = false
	if got := outcomeOf(v.verify(tok)); got != verdict.Authenticated {
		t.Errorf("within check-ttl the cached record is served, got %s", got)
	}
	v.clk.at = v.clk.at.Add(defaultCheckTTL + time.Second)
	requireDenied(t, v.verify(tok))
}

// TestACachedIdentitySurvivesAnUnreachableStore authenticates a cache hit across a
// db outage and answers Unavailable on a cold read.
func TestACachedIdentitySurvivesAnUnreachableStore(t *testing.T) {
	v := newVfy(t)
	if got := outcomeOf(v.verify(v.good())); got != verdict.Authenticated {
		t.Fatalf("the good token is %s, want authenticated", got)
	}
	v.acct.err = errors.New("the database is not reachable")
	if got := outcomeOf(v.verify(v.good())); got != verdict.Authenticated {
		t.Errorf("a cached account served across a db outage is %s, want authenticated", got)
	}

	cold := newVfy(t)
	cold.acct.err = errors.New("the database is not reachable")
	if got := outcomeOf(cold.verify(cold.good())); got != verdict.Unavailable {
		t.Errorf("a cold read across a db outage is %s, want unavailable", got)
	}
}

// TestAnUnreachableStoreIsUnavailableNeverADenial answers Unavailable on an
// unreachable account, session or impersonation store.
func TestAnUnreachableStoreIsUnavailableNeverADenial(t *testing.T) {
	v := newVfy(t)
	v.acct.err = errors.New("the database is not reachable")
	if got := outcomeOf(v.verify(v.good())); got != verdict.Unavailable {
		t.Errorf("an unreachable account store is %s, want unavailable", got)
	}

	v = newVfy(t)
	v.sess.err = errors.New("the database is not reachable")
	if got := outcomeOf(v.verify(v.good())); got != verdict.Unavailable {
		t.Errorf("an unreachable session store is %s, want unavailable", got)
	}

	v = newVfy(t)
	tok := v.impersonationToken()
	v.imp.err = errors.New("the database is not reachable")
	if got := outcomeOf(v.verify(tok)); got != verdict.Unavailable {
		t.Errorf("an unreachable impersonation store is %s, want unavailable", got)
	}
}

func withSub(c map[string]any, sub string) map[string]any {
	c["sub"] = sub
	return c
}

// sizedToken builds a well-formed token whose compact form is exactly size bytes,
// padding the header and claims.
func sizedToken(t *testing.T, v *vfy, size int) string {
	t.Helper()
	// The signature length is fixed, so the compact length is arithmetic on the
	// raw-base64 sizes of the header and claims.
	sample := v.sign(vfyClaims(), v.header())
	sigLen := len(sample) - strings.LastIndex(sample, ".") - 1
	baseLen := func(fields map[string]any) int {
		fields["pad"] = ""
		body, err := json.Marshal(fields)
		if err != nil {
			t.Fatalf("marshalling: %v", err)
		}
		return len(body)
	}
	claimBase := baseLen(vfyClaims())
	headerBase := baseLen(v.header())
	lengthOf := func(claimsPad, headerPad int) int {
		return rawB64Len(claimBase+claimsPad) + 1 + rawB64Len(headerBase+headerPad) + 1 + sigLen
	}
	for claimsPad := 0; claimsPad <= size+64; claimsPad++ {
		if lengthOf(claimsPad, 0) > size+8 {
			break
		}
		for headerPad := 0; headerPad <= 8; headerPad++ {
			if lengthOf(claimsPad, headerPad) == size {
				c := vfyClaims()
				c["pad"] = strings.Repeat("a", claimsPad)
				h := v.header()
				h["pad"] = strings.Repeat("b", headerPad)
				tok := v.sign(c, h)
				if len(tok) != size {
					t.Fatalf("the built token is %d bytes, want %d", len(tok), size)
				}
				return tok
			}
		}
	}
	t.Fatalf("no well-formed token of exactly %d bytes could be built", size)
	return ""
}

func rawB64Len(n int) int {
	switch n % 3 {
	case 0:
		return n / 3 * 4
	case 1:
		return n/3*4 + 2
	default:
		return n/3*4 + 3
	}
}

// TestTheTokenLengthGuardIsExercisedOnAWellFormedToken accepts exactly maxTokenLen
// bytes and refuses one more.
func TestTheTokenLengthGuardIsExercisedOnAWellFormedToken(t *testing.T) {
	v := newVfy(t)
	atLimit := sizedToken(t, v, maxTokenLen)
	if got := outcomeOf(v.verify(atLimit)); got != verdict.Authenticated {
		t.Errorf("a valid token of exactly %d bytes is %s, want authenticated", maxTokenLen, got)
	}
	over := sizedToken(t, v, maxTokenLen+1)
	requireDenied(t, v.verify(over))
}

// TestEveryAccessTokenTypeSpellingIsAccepted takes both RFC 9068 typ spellings in
// any case.
func TestEveryAccessTokenTypeSpellingIsAccepted(t *testing.T) {
	for _, typ := range []string{
		accessTokenType, accessTokenTypeAlt, "AT+JWT", "Application/At+Jwt",
	} {
		t.Run(typ, func(t *testing.T) {
			v := newVfy(t)
			h := v.header()
			h["typ"] = typ
			if got := outcomeOf(v.verify(v.sign(vfyClaims(), h))); got != verdict.Authenticated {
				t.Errorf("a token whose typ is %q is %s, want authenticated", typ, got)
			}
		})
	}
}

// TestAMultiSignatureTokenIsRefused refuses a JWS carrying more than one signature.
func TestAMultiSignatureTokenIsRefused(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{}`))
	protected := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"at+jwt"}`))
	sig := base64.RawURLEncoding.EncodeToString([]byte("signature"))
	raw := `{"payload":"` + payload + `","signatures":[` +
		`{"protected":"` + protected + `","signature":"` + sig + `"},` +
		`{"protected":"` + protected + `","signature":"` + sig + `"}]}`
	if _, err := parseSigned(raw, []string{algES256}); err == nil {
		t.Error("a JWS carrying two signatures parsed as a single-signature token")
	}
	v := newVfy(t)
	requireDenied(t, v.verify(raw))
}

// TestTheMalformedInputsOfTheDisclosedPanicAreRefusedNotCrashed answers Denied,
// never a panic, on malformed input of the kind that makes go-jose panic.
func TestTheMalformedInputsOfTheDisclosedPanicAreRefusedNotCrashed(t *testing.T) {
	inputs := []string{
		"..",
		".a.",
		"e30.e30.",
		"!!!.???.###",
		"\x00\x01\x02.\x00.\x00",
		"eyJhbGciOiJFUzI1NiJ9.e30.%%%",
	}
	for _, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Verify panicked on %q: %v", in, r)
				}
			}()
			v := newVfy(t)
			if got := outcomeOf(v.verify(in)); got != verdict.Denied {
				t.Errorf("Verify(%q) = %s, want denied", in, got)
			}
		}()
	}
}

// TestATokenIssuedAtTheValidAfterInstantIsAccepted accepts a token whose iat equals
// the account's valid_after.
func TestATokenIssuedAtTheValidAfterInstantIsAccepted(t *testing.T) {
	v := newVfy(t)
	a := v.acct.accounts[vfyAlice]
	a.ValidAfter = vfyNow
	v.acct.accounts[vfyAlice] = a
	if got := outcomeOf(v.verify(v.good())); got != verdict.Authenticated {
		t.Errorf("a token issued at the account's valid-after instant is %s, want authenticated", got)
	}
}

// TestAnImpersonationTokenTakesTheOperatorsLevel takes the assurance, IdP
// declaration and provider from the operator session.
func TestAnImpersonationTokenTakesTheOperatorsLevel(t *testing.T) {
	v := newVfy(t)
	tok := v.impersonationToken()
	v.sess.status.Assurance = authn.AssuranceMulti
	v.sess.status.IDPAssured = true
	v.sess.status.Provider = "oidc:corp"
	got := principalOf(v.verify(tok))
	if got.Assurance != authn.AssuranceMulti || !got.IDPAssured || got.Provider != "oidc:corp" {
		t.Errorf("the impersonation is %v, IDPAssured %v, provider %q, want the operator session's own",
			got.Assurance, got.IDPAssured, got.Provider)
	}
}

// TestAStatelessImpersonationTokenReadsItsLevelFromTheToken reads the level from
// the token's acr, and the facts it does not carry as false and empty.
func TestAStatelessImpersonationTokenReadsItsLevelFromTheToken(t *testing.T) {
	v := newVfy(t)
	v.st.setBool(checkSessionPath, false)
	c := vfyClaims()
	c["sub"] = string(vfyBob)
	c["act"] = map[string]any{"sub": string(vfyAdmin)}
	c["imp"] = "imp-1"
	c["acr"] = "2"
	v.imp.grant = authn.ImpersonationGrant{
		ID: "imp-1", Actor: vfyAdmin, Target: vfyBob, Tenant: "acme", SessionID: vfySession,
	}
	v.imp.active = true
	got := principalOf(v.verify(v.sign(c, v.header())))
	if got.Assurance != authn.AssuranceMulti {
		t.Errorf("a stateless impersonation whose acr is 2 is %v, want multi", got.Assurance)
	}
	if got.IDPAssured || got.Provider != "" {
		t.Errorf("IDPAssured %v and provider %q, want false and empty: the token carries neither", got.IDPAssured, got.Provider)
	}
}

// TestAnImpersonationTakesItsMethodsFromTheSession reads the method set off the
// session, not off the token's amr.
func TestAnImpersonationTakesItsMethodsFromTheSession(t *testing.T) {
	v := newVfy(t)
	v.sess.status.Methods = []authn.Method{authn.MethodTOTP}
	got := principalOf(v.verify(v.impersonationToken()))
	if len(got.Methods) != 1 || got.Methods[0] != authn.MethodTOTP {
		t.Errorf("the impersonation principal's methods are %v, want the session's [totp]", got.Methods)
	}
}

// TestATokenWithNoAuthenticationMethodIsRefused refuses a principal that would
// carry no method, whether the set would come from the token or a session.
func TestATokenWithNoAuthenticationMethodIsRefused(t *testing.T) {
	v := newVfy(t)
	v.st.setBool(checkSessionPath, false)
	c := vfyClaims()
	c["amr"] = []string{}
	requireDenied(t, v.verify(v.sign(c, v.header())))

	v = newVfy(t)
	v.sess.status.Methods = nil
	requireDenied(t, v.verify(v.good()))
}

// TestTheStatelessModeReadsTheAssuranceFromTheToken reads acr "2" as multi and "1"
// as single when the session check is off.
func TestTheStatelessModeReadsTheAssuranceFromTheToken(t *testing.T) {
	v := newVfy(t)
	v.st.setBool(checkSessionPath, false)
	c := vfyClaims()
	c["acr"] = "2"
	if got := principalOf(v.verify(v.sign(c, v.header()))); got.Assurance != authn.AssuranceMulti {
		t.Errorf("a token whose acr is 2 is %v, want multi", got.Assurance)
	}
	c["acr"] = "1"
	if got := principalOf(v.verify(v.sign(c, v.header()))); got.Assurance != authn.AssuranceSingle {
		t.Errorf("a token whose acr is 1 is %v, want single", got.Assurance)
	}
}

// TestAZeroAssuranceReachesThePrincipalAsSingle hands a zero assurance over as
// single.
func TestAZeroAssuranceReachesThePrincipalAsSingle(t *testing.T) {
	v := newVfy(t)
	v.sess.status.Assurance = authn.AssuranceNone
	got := principalOf(v.verify(v.good()))
	if got.Assurance != authn.AssuranceSingle {
		t.Errorf("a session at the zero assurance gave a principal at %v, want single", got.Assurance)
	}
}

// TestTheSessionProviderAndIDPLevelReachThePrincipal carries the session's
// provider and IdP level onto the principal.
func TestTheSessionProviderAndIDPLevelReachThePrincipal(t *testing.T) {
	v := newVfy(t)
	v.sess.status.Provider = "oidc-x"
	v.sess.status.IDPAssured = true
	got := principalOf(v.verify(v.good()))
	if got.Provider != "oidc-x" || !got.IDPAssured {
		t.Errorf("the principal carries provider %q and idp-assured %v, want oidc-x and true",
			got.Provider, got.IDPAssured)
	}
}

// TestReservedAccountAttributesDoNotReachThePrincipal drops an account attribute
// under idp.* or authn.* and keeps an ordinary one.
func TestReservedAccountAttributesDoNotReachThePrincipal(t *testing.T) {
	v := newVfy(t)
	v.acct.accounts[vfyAlice] = authn.Account{
		ID: vfyAlice, Status: authn.StatusActive,
		Attributes: map[string]string{"idp.role": "spoofed", "authn.z": "spoofed", "region": "eu"},
	}
	got := principalOf(v.verify(v.good()))
	if _, ok := got.Attributes["idp.role"]; ok {
		t.Error("an account attribute under idp. was copied to the principal")
	}
	if _, ok := got.Attributes["authn.z"]; ok {
		t.Error("an account attribute under authn. was copied to the principal")
	}
	if got.Attributes["region"] != "eu" {
		t.Errorf("the ordinary attribute is %v, want region=eu", got.Attributes)
	}
}

// TestAPrincipalWithNoAttributesCarriesNil carries a nil attribute set, not an
// empty one.
func TestAPrincipalWithNoAttributesCarriesNil(t *testing.T) {
	v := newVfy(t)
	if got := principalOf(v.verify(v.good())); got.Attributes != nil {
		t.Errorf("a principal with no attributes carries %v, want nil", got.Attributes)
	}
}

// TestAMalformedSubjectIsRefusedBeforeAnyLookup refuses a malformed sub even when
// an account exists under that very identifier.
func TestAMalformedSubjectIsRefusedBeforeAnyLookup(t *testing.T) {
	v := newVfy(t)
	bad := authn.AccountID("not a valid id")
	v.acct.accounts[bad] = activeAccount(bad)
	v.sess.status.Account = bad
	c := vfyClaims()
	c["sub"] = string(bad)
	requireDenied(t, v.verify(v.sign(c, v.header())))
}

// TestAServiceTokenCarriesItsIssueInstantAsAuthTime carries the token's iat, not
// the verify instant, as auth time.
func TestAServiceTokenCarriesItsIssueInstantAsAuthTime(t *testing.T) {
	v := newVfy(t)
	v.sess.status.Kind = authn.SessionService
	v.sess.status.Account = vfyAlice
	c := vfyClaims()
	c["amr"] = []string{string(authn.MethodClientSecret)}
	tok := v.sign(c, v.header())
	v.clk.at = vfyNow.Add(time.Minute)
	got := principalOf(v.verify(tok))
	if !got.AuthTime.Equal(vfyNow) {
		t.Errorf("a service token's auth time is %v, want its iat %v", got.AuthTime, vfyNow)
	}
}

// TestATokenWhoseKeysRegisteredAlgorithmDiffersIsRefused refuses a token whose
// header algorithm differs from the one its selected key carries.
func TestATokenWhoseKeysRegisteredAlgorithmDiffersIsRefused(t *testing.T) {
	v := newVfy(t)
	for i := range v.set.entries {
		if v.set.entries[i].kid == v.set.signing.kid {
			v.set.entries[i].alg = algRS256
		}
	}
	requireDenied(t, v.verify(v.good()))
}

// TestTheLeewaySettingIsRead compares the token's times with the configured
// leeway, not the declared default.
func TestTheLeewaySettingIsRead(t *testing.T) {
	v := newVfy(t)
	store := newFakeSettings()
	store.set(leewayPath, leewayMax)
	v.p.settings.store = store
	c := vfyClaims()
	c["iat"] = vfyNow.Add(defaults.Leeway + time.Second).Unix()
	c["exp"] = vfyNow.Add(5 * time.Minute).Unix()
	if got := outcomeOf(v.verify(v.sign(c, v.header()))); got != verdict.Authenticated {
		t.Errorf("a token within the configured leeway is %s, want authenticated", got)
	}
}
