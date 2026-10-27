package token

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
)

// issuedClaims parses an access token of the rig's set, verifies its signature
// and returns the claims.
func (r *rig) issuedClaims(raw string) map[string]any {
	r.t.Helper()
	parsed, err := parseSigned(raw, r.keys.algorithms)
	if err != nil {
		r.t.Fatalf("parsing the token: %v", err)
	}
	var claims map[string]any
	if err := parsed.verify(r.keys.signing.pub, &claims); err != nil {
		r.t.Fatalf("verifying the token: %v", err)
	}
	return claims
}

// normalIssue is the ordinary issue request of a login session.
func normalIssue(session authn.SessionID) authn.IssueToken {
	return authn.IssueToken{
		Account:   account,
		Session:   session,
		Methods:   []authn.Method{authn.MethodPassword},
		Assurance: authn.AssuranceSingle,
		AuthTime:  epoch,
	}
}

func TestTheIssuedClaimSetIsTheDesignsTable(t *testing.T) {
	r := newRig(t)
	session := authn.SessionID("sess-1")
	access, err := r.p.Issue(context.Background(), normalIssue(session))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !access.ExpiresAt.Equal(epoch.Add(defaults.AccessTTL)) {
		t.Errorf("ExpiresAt is %v, want %v", access.ExpiresAt, epoch.Add(defaults.AccessTTL))
	}
	claims := r.issuedClaims(access.Value)

	if claims["iss"] != issuer {
		t.Errorf("iss is %v, want %s", claims["iss"], issuer)
	}
	if claims["sub"] != string(account) {
		t.Errorf("sub is %v, want %s", claims["sub"], account)
	}
	if aud, ok := claims["aud"].([]any); !ok || len(aud) != 1 || aud[0] != issuer {
		t.Errorf("aud is %v, want [%s]", claims["aud"], issuer)
	}
	iat := int64(claims["iat"].(float64))
	exp := int64(claims["exp"].(float64))
	if exp-iat != int64(defaults.AccessTTL.Seconds()) {
		t.Errorf("exp - iat is %d, want %d", exp-iat, int64(defaults.AccessTTL.Seconds()))
	}
	if int64(claims["nbf"].(float64)) != iat || iat != epoch.Unix() {
		t.Errorf("nbf/iat is %v/%v, want %d", claims["nbf"], claims["iat"], epoch.Unix())
	}
	jti, err := uuid.Parse(claims["jti"].(string))
	if err != nil || jti.Version() != 7 {
		t.Errorf("jti is %v, want a UUIDv7", claims["jti"])
	}
	if claims["sid"] != string(session) {
		t.Errorf("sid is %v, want %s", claims["sid"], session)
	}
	if amr, ok := claims["amr"].([]any); !ok || len(amr) != 1 || amr[0] != string(authn.MethodPassword) {
		t.Errorf("amr is %v, want [%s]", claims["amr"], authn.MethodPassword)
	}
	if claims["acr"] != "1" {
		t.Errorf("acr is %v, want 1", claims["acr"])
	}
	if int64(claims["auth_time"].(float64)) != epoch.Unix() {
		t.Errorf("auth_time is %v, want %d", claims["auth_time"], epoch.Unix())
	}
	if claims["client_id"] != clientIDDefault {
		t.Errorf("client_id is %v, want %s", claims["client_id"], clientIDDefault)
	}
	for _, absent := range []string{"act", "imp", "imp_tenant", "role"} {
		if _, ok := claims[absent]; ok {
			t.Errorf("an ordinary token carries %q: %v", absent, claims[absent])
		}
	}
}

func TestAnImpersonationTokenCarriesTheOperatorsLevelAndTheActor(t *testing.T) {
	r := newRig(t)
	in := normalIssue(authn.SessionID("sess-1"))
	in.Actor = "acct-admin"
	in.Assurance = authn.AssuranceMulti
	in.ImpersonationID = "grant-1"
	in.Scope = "tenant-a"
	in.AuthTime = epoch
	r.activeRecord("grant-1", epoch.Add(time.Hour))

	access, err := r.p.Issue(context.Background(), in)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims := r.issuedClaims(access.Value)
	if claims["acr"] != "2" {
		t.Errorf("an impersonation token's acr is %v, want the operator session's 2", claims["acr"])
	}
	if claims["sub"] != string(account) {
		t.Errorf("sub is %v, want the impersonated account", claims["sub"])
	}
	act, ok := claims["act"].(map[string]any)
	if !ok || act["sub"] != "acct-admin" {
		t.Errorf("act is %v, want the actor", claims["act"])
	}
	if claims["imp"] != "grant-1" {
		t.Errorf("imp is %v, want grant-1", claims["imp"])
	}
	if claims["imp_tenant"] != "tenant-a" {
		t.Errorf("imp_tenant is %v, want tenant-a", claims["imp_tenant"])
	}
}

// TestAnImpersonationTokenExpiresNoLaterThanItsRecord holds the cap on the
// issuing path: a record with less time left than the access-ttl setting makes
// the token expire with the record, so an offline verifier reading exp alone
// cannot extend the impersonation past its own end.
func TestAnImpersonationTokenExpiresNoLaterThanItsRecord(t *testing.T) {
	r := newRig(t)
	remaining := 3 * time.Minute
	r.activeRecord("grant-1", epoch.Add(remaining))

	in := normalIssue(authn.SessionID("sess-1"))
	in.Actor = "acct-admin"
	in.ImpersonationID = "grant-1"
	in.Scope = "tenant-a"
	access, err := r.p.Issue(context.Background(), in)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if want := epoch.Add(remaining); !access.ExpiresAt.Equal(want) {
		t.Errorf("the impersonation token expires at %v, want the record's end %v", access.ExpiresAt, want)
	}
	claims := r.issuedClaims(access.Value)
	iat := int64(claims["iat"].(float64))
	exp := int64(claims["exp"].(float64))
	if exp-iat != int64(remaining.Seconds()) {
		t.Errorf("exp - iat is %d, want the record's %d remaining seconds", exp-iat, int64(remaining.Seconds()))
	}
}

// TestAnImpersonationTokenIsRefusedForARecordThatDoesNotHold holds the failure
// direction of the cap: no remaining time can be read from a record that is not
// active, and an assembly with no impersonation capability cannot read one at
// all, so neither issues a token.
func TestAnImpersonationTokenIsRefusedForARecordThatDoesNotHold(t *testing.T) {
	in := normalIssue(authn.SessionID("sess-1"))
	in.Actor = "acct-admin"
	in.ImpersonationID = "grant-1"

	r := newRig(t)
	if _, err := r.p.Issue(context.Background(), in); !errors.Is(err, authn.ErrNotImpersonating) {
		t.Fatalf("an inactive record gave %v, want ErrNotImpersonating", err)
	}

	noCapability := newRig(t)
	noCapability.p.impersonation = nil
	if _, err := noCapability.p.Issue(context.Background(), in); !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("an assembly with no impersonation capability gave %v, want ErrUnavailable", err)
	}
}

func TestAContributorThatReturnsAReservedClaimOrAnErrorFailsTheIssue(t *testing.T) {
	ctx := context.Background()
	t.Run("a reserved name", func(t *testing.T) {
		r := newRig(t)
		r.p.claims = []authn.ClaimsContributor{contributor{claims: map[string]any{"sub": "x"}}}
		if _, err := r.p.Issue(ctx, normalIssue("sess-1")); !errors.Is(err, authn.ErrReservedClaim) {
			t.Fatalf("a contributor's reserved claim gave %v, want ErrReservedClaim", err)
		}
	})
	t.Run("an error", func(t *testing.T) {
		r := newRig(t)
		r.p.claims = []authn.ClaimsContributor{contributor{err: errors.New("nope")}}
		if _, err := r.p.Issue(ctx, normalIssue("sess-1")); err == nil {
			t.Fatal("a contributor's error did not fail the issue")
		}
	})
	t.Run("an ordinary claim is merged", func(t *testing.T) {
		r := newRig(t)
		r.p.claims = []authn.ClaimsContributor{contributor{claims: map[string]any{"role": "admin"}}}
		access, err := r.p.Issue(ctx, normalIssue("sess-1"))
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		if got := r.issuedClaims(access.Value); got["role"] != "admin" {
			t.Errorf("the contributor's claim did not reach the token: %v", got)
		}
	})
}

func TestAContributorSeesTheIssuesOwnFacts(t *testing.T) {
	r := newRig(t)
	var seen authn.Principal
	r.p.claims = []authn.ClaimsContributor{contributor{seen: &seen}}

	in := normalIssue("sess-1")
	in.Actor = authn.AccountID("acct-ops")
	in.Scope = "acme"
	if _, err := r.p.Issue(context.Background(), in); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if seen.Account != in.Account || seen.Actor != in.Actor || seen.Session != in.Session {
		t.Errorf("the contributor saw %s/%s/%s, want %s/%s/%s",
			seen.Account, seen.Actor, seen.Session, in.Account, in.Actor, in.Session)
	}
	if seen.Assurance != in.Assurance || !seen.AuthTime.Equal(in.AuthTime) {
		t.Errorf("the contributor saw assurance %v at %s, want %v at %s",
			seen.Assurance, seen.AuthTime, in.Assurance, in.AuthTime)
	}
	if seen.Scope != in.Scope {
		t.Errorf("the contributor saw scope %q, want %q", seen.Scope, in.Scope)
	}
	if !slices.Equal(seen.Methods, in.Methods) {
		t.Errorf("the contributor saw methods %v, want %v", seen.Methods, in.Methods)
	}
}

func TestAnOverLongLifetimeIsRefusedAndAZeroOneTakesTheSetting(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()

	in := normalIssue("sess-1")
	in.TTL = 2 * time.Hour
	if _, err := r.p.Issue(ctx, in); err == nil {
		t.Fatal("a two-hour access token was issued, and exp - iat may not pass one hour")
	}

	r.settings.set(accessTTLPath, 30*time.Minute)
	in.TTL = 0
	access, err := r.p.Issue(ctx, in)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !access.ExpiresAt.Equal(epoch.Add(30 * time.Minute)) {
		t.Errorf("a zero TTL gave expiry %v, want the access-ttl setting's 30m", access.ExpiresAt)
	}
}

func TestIssueWithoutASigningKeyIsErrNoSigningKey(t *testing.T) {
	r := newRig(t)
	r.p.keys.signing = nil
	if _, err := r.p.Issue(context.Background(), normalIssue("sess-1")); !errors.Is(err, authn.ErrNoSigningKey) {
		t.Fatalf("Issue with no signing key gave %v, want ErrNoSigningKey", err)
	}
}

// contributor is a ClaimsContributor a test answers with.
type contributor struct {
	claims map[string]any
	err    error
	seen   *authn.Principal // where Claims records the principal it was handed
}

func (c contributor) Claims(_ context.Context, p authn.Principal) (map[string]any, error) {
	if c.seen != nil {
		*c.seen = p
	}
	return c.claims, c.err
}

// signSample signs a minimal access token with the set's signing key, for the
// rotation drill.
func signSample(t *testing.T, set *keySet) string {
	t.Helper()
	payload, err := tokenClaims{
		Issuer:    issuer,
		Subject:   "acct-alice",
		Audience:  []string{issuer},
		Expiry:    epoch.Add(time.Minute).Unix(),
		NotBefore: epoch.Unix(),
		IssuedAt:  epoch.Unix(),
		ID:        "jti",
		Session:   "sess-1",
		ClientID:  clientIDDefault,
	}.marshal()
	if err != nil {
		t.Fatalf("marshalling the claims: %v", err)
	}
	raw, err := sign(*set.signing, accessTokenType, payload)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return raw
}

// verifies reports whether a token's signature holds against a set's key of the
// given kid.
func verifies(raw string, set *keySet, kid string) bool {
	parsed, err := parseSigned(raw, set.algorithms)
	if err != nil {
		return false
	}
	var key *keyEntry
	for i := range set.entries {
		if set.entries[i].kid == kid {
			key = &set.entries[i]
			break
		}
	}
	if key == nil {
		return false
	}
	return parsed.verify(key.pub, &tokenClaims{}) == nil
}

func jwksKIDs(t *testing.T, set *keySet) map[string]bool {
	t.Helper()
	body, err := marshalJWKS(set.entries)
	if err != nil {
		t.Fatalf("marshalling the key set: %v", err)
	}
	var decoded jwksBody
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decoding the key set: %v", err)
	}
	out := map[string]bool{}
	for _, k := range decoded.Keys {
		out[k["kid"].(string)] = true
	}
	return out
}

// TestAKeyRotationRollsOverInTwoPhases walks the four-step rotation: publish a
// new key for verification, switch the signing key to it, then retire the old
// one. Throughout, a token signed by a key that is still in the set verifies and
// one signed by a retired key does not.
func TestAKeyRotationRollsOverInTwoPhases(t *testing.T) {
	a := generatedKey(t, algES256)
	b := generatedKey(t, algES256)

	signA, _, err := loadKeySet(resolved{algorithm: algES256, signingFile: a})
	if err != nil {
		t.Fatal(err)
	}
	// Step 1: B is added to every replica's verification files while A still signs.
	signAverifyAB, _, err := loadKeySet(resolved{algorithm: algES256, signingFile: a, verifyFiles: []string{b}})
	if err != nil {
		t.Fatal(err)
	}
	// Step 3: the signing key moves to B and A stays for verification.
	signBverifyA, _, err := loadKeySet(resolved{algorithm: algES256, signingFile: b, verifyFiles: []string{a}})
	if err != nil {
		t.Fatal(err)
	}
	// Step 4: A is retired.
	signB, _, err := loadKeySet(resolved{algorithm: algES256, signingFile: b})
	if err != nil {
		t.Fatal(err)
	}

	tokenA := signSample(t, signA)
	tokenB := signSample(t, signBverifyA)

	kidA := signA.signing.kid
	kidB := signB.signing.kid
	if kidA == kidB {
		t.Fatal("the two keys share a kid")
	}

	// A token signed by A keeps verifying while A is trusted, and stops once A
	// is retired.
	for _, set := range []*keySet{signA, signAverifyAB, signBverifyA} {
		if !verifies(tokenA, set, kidA) {
			t.Error("a token signed by A did not verify while A is in the set")
		}
	}
	if verifies(tokenA, signB, kidA) {
		t.Error("a token signed by A verified after A was retired")
	}
	// The token signed by B verifies wherever B is published.
	for _, set := range []*keySet{signAverifyAB, signBverifyA, signB} {
		if !verifies(tokenB, set, kidB) {
			t.Error("a token signed by B did not verify where B is published")
		}
	}
	// The published set carries both kids while the rotation is in flight.
	if kids := jwksKIDs(t, signBverifyA); !kids[kidA] || !kids[kidB] {
		t.Errorf("the JWKS during the rotation holds %v, want both kids", kids)
	}
	if kids := jwksKIDs(t, signB); kids[kidA] {
		t.Errorf("the JWKS after retirement still holds the retired kid: %v", kids)
	}
}

// The stand-in modules that provide the six capabilities the token module
// requires, so a case can drive the real registry without bringing in the
// modules that deliver them.
const (
	dbStandIn        = "stand-in.db"
	mountStandIn     = "stand-in.mount"
	accountsStandIn  = "stand-in.accounts"
	sessionsStandIn  = "stand-in.sessions"
	passwordsStandIn = "stand-in.passwords"
	throttleStandIn  = "stand-in.throttle"
)

// standIn is a module that declares the capability token, delivers without a New
// so the case supplies the instance.
func standIn(name string, token core.Token) core.Module {
	return core.Module{Name: name, Provides: []core.Provision{{Token: token}}}
}

// TestTheModuleDeliversTheTokenSurfaceThroughTheRegistry drives the real
// registry through this module's descriptor: it arrives at Init with the product
// constructed, resolves the token surface from it and issues through it. It is
// what holds that the registration delivers the interface the design names, and
// that New reads the configuration and loads the keys.
func TestTheModuleDeliversTheTokenSurfaceThroughTheRegistry(t *testing.T) {
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "tokens.db")+"?_pragma=busy_timeout(5000)", nil)
	mounter := mounttest.New(t)
	keyPath := generatedKey(t, algES256)
	body := `{"authn":{"token":{"issuer":"https://auth.example.com","algorithm":"ES256","signing-key-file":"` +
		keyPath + `"}}}`
	reader := authntest.ConfigReaderAll(t, body, Module(), gate.Module())

	reg := core.New()
	reg.Register(Module())
	reg.Register(standIn(dbStandIn, (*db.Database)(nil)))
	reg.Register(standIn(mountStandIn, (*mount.Mounter)(nil)))
	reg.Register(standIn(accountsStandIn, (*authn.Accounts)(nil)))
	reg.Register(standIn(sessionsStandIn, (*authn.Sessions)(nil)))
	reg.Register(standIn(passwordsStandIn, (*authn.Passwords)(nil)))
	reg.Register(standIn(throttleStandIn, (*authn.Throttle)(nil)))
	reg.Register(config.Module())
	reg.Supply(database, dbStandIn)
	reg.Supply(mounter, mountStandIn)
	reg.Supply(&fakeAccounts{}, accountsStandIn)
	reg.Supply(newFakeSessions(), sessionsStandIn)
	reg.Supply(newFakePasswords(), passwordsStandIn)
	reg.Supply(newFakeThrottle(), throttleStandIn)
	reg.Supply(reader, config.ModuleName)

	var delivered authn.Tokens
	err := reg.Drive(t.Context(), core.Drive{
		Depth: core.StageInit,
		Participants: []string{
			ModuleName, dbStandIn, mountStandIn, accountsStandIn, sessionsStandIn,
			passwordsStandIn, throttleStandIn, config.ModuleName,
		},
		Action: func(_ context.Context, reg *core.Registry) error {
			var err error
			delivered, err = core.Resolve[authn.Tokens](reg)
			return err
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if delivered == nil {
		t.Fatal("the registry resolved no token surface")
	}
	access, err := delivered.Issue(t.Context(), normalIssue("sess-1"))
	if err != nil {
		t.Fatalf("Issue through the registry assembly: %v", err)
	}
	if access.Value == "" {
		t.Error("no access token came out of the registry assembly")
	}
}

// TestAZeroAssuranceIsIssuedAsSingle holds the acr closed set: a caller that
// states no assurance (the zero value) yields acr "1", never "0".
func TestAZeroAssuranceIsIssuedAsSingle(t *testing.T) {
	r := newRig(t)
	in := normalIssue("sess-1")
	in.Assurance = authn.AssuranceNone
	access, err := r.p.Issue(context.Background(), in)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if claims := r.issuedClaims(access.Value); claims["acr"] != "1" {
		t.Errorf("a token issued at the zero assurance carries acr %v, want 1", claims["acr"])
	}
}

// TestTheAccessTTLBoundaryIsExact holds the lifetime bound's boundary: exactly
// one hour is issued and one nanosecond past it is refused.
func TestTheAccessTTLBoundaryIsExact(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	in := normalIssue("sess-1")

	in.TTL = time.Hour
	if _, err := r.p.Issue(ctx, in); err != nil {
		t.Fatalf("a one-hour access token gave %v, want it issued", err)
	}
	in.TTL = time.Hour + time.Nanosecond
	if _, err := r.p.Issue(ctx, in); !errors.Is(err, authn.ErrInvalidArgument) {
		t.Fatalf("a token just over an hour gave %v, want ErrInvalidArgument", err)
	}
}

// TestTheRevokeLengthBoundaryActsOnATokenOfExactlyTheLimit holds the revoke
// path's length guard at its boundary: a token of exactly maxAccessTokenBytes is
// recognised, so the comparison is not one off.
func TestTheRevokeLengthBoundaryActsOnATokenOfExactlyTheLimit(t *testing.T) {
	r := newRig(t)
	id, _ := r.issueRefresh()
	raw := r.sizedAccessToken(t, id, maxAccessTokenBytes)
	if len(raw) != maxAccessTokenBytes {
		t.Fatalf("the built token is %d bytes, want %d", len(raw), maxAccessTokenBytes)
	}
	claims, ok := r.p.revokeClaims(raw)
	if !ok {
		t.Fatalf("a token of exactly %d bytes was not recognised", maxAccessTokenBytes)
	}
	if claims.Session != string(id) {
		t.Errorf("the recognised claims name session %q, want %s", claims.Session, id)
	}
}

// sizedAccessToken signs an access token for session whose compact form is
// exactly size bytes, padding the claims with an ignored field.
func (r *rig) sizedAccessToken(t *testing.T, session authn.SessionID, size int) string {
	t.Helper()
	claimsFor := func(pad int) tokenClaims {
		c := tokenClaims{
			Issuer: issuer, Subject: string(account), Audience: []string{issuer},
			Expiry: epoch.Add(10 * time.Minute).Unix(), IssuedAt: epoch.Unix(), NotBefore: epoch.Unix(),
			ID: "jti", Session: string(session), ClientID: clientIDDefault,
		}
		c.Extra = map[string]any{"pad": strings.Repeat("a", pad)}
		return c
	}
	signClaims := func(pad int) string {
		payload, err := claimsFor(pad).marshal()
		if err != nil {
			t.Fatalf("marshalling claims: %v", err)
		}
		raw, err := sign(*r.p.keys.signing, accessTokenType, payload)
		if err != nil {
			t.Fatalf("signing: %v", err)
		}
		return raw
	}
	sample := signClaims(0)
	firstDot := strings.Index(sample, ".")
	lastDot := strings.LastIndex(sample, ".")
	headerLen := firstDot
	sigLen := len(sample) - lastDot - 1
	base, err := claimsFor(0).marshal()
	if err != nil {
		t.Fatalf("marshalling claims: %v", err)
	}
	for pad := 0; pad <= size+64; pad++ {
		seg := rawB64Len(len(base) + pad)
		if headerLen+1+seg+1+sigLen == size {
			raw := signClaims(pad)
			if len(raw) != size {
				t.Fatalf("the built token is %d bytes, want %d", len(raw), size)
			}
			return raw
		}
	}
	t.Fatalf("no access token of exactly %d bytes could be built", size)
	return ""
}
