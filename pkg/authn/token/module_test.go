package token

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// requirementTokens are the capability tokens a module requires, in order.
func requirementTokens(reqs []core.Requirement) []any {
	out := make([]any, len(reqs))
	for i, r := range reqs {
		out[i] = r.Token
	}
	return out
}

func TestTheModuleDeclaresWhatTheDesignSays(t *testing.T) {
	m := Module()
	if m.Name != ModuleName {
		t.Fatalf("the module name is %q, want %q", m.Name, ModuleName)
	}
	if ConfigNamespace != ModuleName {
		t.Errorf("the configuration namespace is %q, want %q", ConfigNamespace, ModuleName)
	}

	wantRequires := []any{
		(*db.Database)(nil),
		(*mount.Mounter)(nil),
		(*authn.Accounts)(nil),
		(*authn.Sessions)(nil),
		(*authn.Passwords)(nil),
		(*authn.Throttle)(nil),
		(*settings.Store)(nil),
		(*log.Logger)(nil),
	}
	if got := requirementTokens(m.Requires); !reflect.DeepEqual(got, wantRequires) {
		t.Errorf("Requires is %v, want %v", got, wantRequires)
	}
	for _, r := range m.Requires {
		switch r.Token {
		case core.Token((*settings.Store)(nil)):
			if !r.Optional {
				t.Errorf("the settings requirement is not optional; a command that names no settings module has to still start")
			}
			continue
		case core.Token((*log.Logger)(nil)):
			if !r.Optional {
				t.Errorf("the logging requirement is not optional; a command that names no logging module has to still start")
			}
			continue
		}
		if r.Optional {
			t.Errorf("the requirement %v is optional, and the design requires it", r.Token)
		}
	}

	wantProvides := []core.Provision{
		{Token: (*authn.Tokens)(nil), Exclusive: true},
		{Token: (*authn.Verifier)(nil)},
	}
	if !reflect.DeepEqual(m.Provides, wantProvides) {
		t.Errorf("Provides is %v, want %v", m.Provides, wantProvides)
	}
}

func TestStartWithoutTheTableIsNotMigrated(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "tokens.db") + "?_pragma=busy_timeout(5000)"
	database := dbtest.SQLite(t, dsn, nil)
	p := newProduct(resolved{algorithm: algES256}, loadedSet(t, algES256), database, mounttest.New(t), nil, nil, nil, nil)

	err := p.start(t.Context())
	if !errors.Is(err, authn.ErrNotMigrated) {
		t.Fatalf("starting with no table gave %v, want ErrNotMigrated", err)
	}
	if !strings.Contains(err.Error(), tableName) {
		t.Errorf("the refusal %q does not name the table", err)
	}

	if err := p.handle.Exec("CREATE TABLE " + tableName + " (id TEXT PRIMARY KEY)").Error; err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	if err := p.start(t.Context()); err != nil {
		t.Fatalf("starting with the table present: %v", err)
	}
	p.stop(t.Context())
}

// TestStartLaunchesTheSweepAndStopEndsIt holds the module wiring: a successful
// start leaves the refresh-token sweep running, and stop ends it. Without the
// call from start the table would still be probed but the sweep would never run.
func TestStartLaunchesTheSweepAndStopEndsIt(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "tokens.db") + "?_pragma=busy_timeout(5000)"
	database := dbtest.SQLite(t, dsn, nil)
	p := newProduct(resolved{algorithm: algES256}, loadedSet(t, algES256), database, mounttest.New(t), nil, nil, nil, nil)
	if err := p.handle.Exec("CREATE TABLE " + tableName + " (id TEXT PRIMARY KEY)").Error; err != nil {
		t.Fatalf("creating the table: %v", err)
	}

	if err := p.start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	sweepMu.Lock()
	_, running := sweepRunning[p]
	sweepMu.Unlock()
	if !running {
		t.Error("start did not launch the refresh-token sweep")
	}

	p.stop(t.Context())
	sweepMu.Lock()
	_, still := sweepRunning[p]
	sweepMu.Unlock()
	if still {
		t.Error("stop left the refresh-token sweep running")
	}
}

// TestWireMountsTheTokenEndpoints holds the module wiring: wire mounts the token
// and revocation endpoints, so an assembly that reaches Init answers POST
// B/token. Without the call from wire the key set would still mount and the
// endpoints would answer 404.
func TestWireMountsTheTokenEndpoints(t *testing.T) {
	r := newRig(t)
	m := mounttest.New(t)
	r.p.mounter = m
	if err := r.p.wire(core.New()); err != nil {
		t.Fatalf("wire: %v", err)
	}

	token := form(m, m.BaseIn("")+tokenPath, url.Values{"grant_type": {"not-a-grant"}})
	if token.Code == http.StatusNotFound {
		t.Fatalf("POST %s answered 404; wire did not mount the token endpoint", tokenPath)
	}
	if token.Code != http.StatusBadRequest {
		t.Errorf("POST %s with an unknown grant answered %d, want 400", tokenPath, token.Code)
	}
	if revoke := form(m, m.BaseIn("")+revokePath, url.Values{}); revoke.Code != http.StatusOK {
		t.Errorf("POST %s answered %d, want 200", revokePath, revoke.Code)
	}
}

func TestTheModuleNamesItsStableSurfaceAsTheDesignFixesIt(t *testing.T) {
	if backendName != "token" {
		t.Errorf("the backend name is %q, want token", backendName)
	}
	if tokenPath != "/token" || revokePath != "/token/revoke" || jwksPath != "/jwks.json" {
		t.Errorf("the routes are %q, %q and %q", tokenPath, revokePath, jwksPath)
	}
	if refreshTokenPrefix != "spd_rt_" {
		t.Errorf("the refresh-token prefix is %q, want spd_rt_", refreshTokenPrefix)
	}

	wantClaims := []string{
		"iss", "sub", "aud", "exp", "nbf", "iat", "jti", "sid",
		"amr", "acr", "auth_time", "act", "imp", "imp_tenant", "client_id", "typ",
	}
	if len(reservedClaims) != len(wantClaims) {
		t.Errorf("the reserved claim set holds %d names, want %d", len(reservedClaims), len(wantClaims))
	}
	for _, name := range wantClaims {
		if _, ok := reservedClaims[name]; !ok {
			t.Errorf("the reserved claim set is missing %q", name)
		}
	}
}

func TestAContributorThatReturnsAReservedClaimFailsTheIssue(t *testing.T) {
	if err := checkContributorClaims(map[string]any{"role": "admin"}); err != nil {
		t.Errorf("an ordinary claim was refused: %v", err)
	}
	if err := checkContributorClaims(nil); err != nil {
		t.Errorf("an empty claim set was refused: %v", err)
	}
	for _, name := range []string{"sub", "exp", "imp", "typ"} {
		err := checkContributorClaims(map[string]any{name: "x"})
		if !errors.Is(err, authn.ErrReservedClaim) {
			t.Errorf("the reserved claim %q gave %v, want ErrReservedClaim", name, err)
		}
	}
}

func TestMarshalMergesAnExtraClaimAndRefusesAReservedOne(t *testing.T) {
	c := tokenClaims{
		Issuer:   "iss",
		Subject:  "acct-1",
		Audience: []string{"aud"},
		Expiry:   100,
		IssuedAt: 50,
		ID:       "jti-1",
		Session:  "sess-1",
		ClientID: "speed",
		Extra:    map[string]any{"role": "admin"},
	}
	body, err := c.marshal()
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	var merged map[string]any
	if err := json.Unmarshal(body, &merged); err != nil {
		t.Fatalf("the claims are not a JSON object: %v", err)
	}
	if merged["sub"] != "acct-1" || merged["sid"] != "sess-1" {
		t.Errorf("the standard claims are %v", merged)
	}
	if merged["role"] != "admin" {
		t.Errorf("the contributor's claim did not reach the token: %v", merged)
	}
	if _, ok := merged["typ"]; ok {
		t.Errorf("the module wrote a typ claim, which is a header: %v", merged)
	}

	c.Extra = map[string]any{"exp": 1}
	if _, err := c.marshal(); !errors.Is(err, authn.ErrReservedClaim) {
		t.Errorf("marshal with a reserved extra gave %v, want ErrReservedClaim", err)
	}
}

// TestIsMissingTableRecognisesBothDialects holds the table probe's message
// match: SQLite's and PostgreSQL's wording for a missing table both read as the
// table being absent, and an unrelated error does not.
func TestIsMissingTableRecognisesBothDialects(t *testing.T) {
	for _, text := range []string{
		"no such table: " + tableName,
		`relation "` + tableName + `" does not exist`,
	} {
		if !isMissingTable(errors.New(text)) {
			t.Errorf("isMissingTable(%q) = false, want true", text)
		}
	}
	if isMissingTable(nil) {
		t.Error("isMissingTable(nil) = true, want false")
	}
	if isMissingTable(errors.New("connection refused")) {
		t.Error("isMissingTable of an unrelated error = true, want false")
	}
}

// TestTheStableInstrumentAndTokenNamesArePinned holds the literals a dashboard
// and an external reader depend on: the instrument names and the refresh token's
// randomness, which a rename or a weaker size would otherwise change unnoticed.
func TestTheStableInstrumentAndTokenNamesArePinned(t *testing.T) {
	if operationsInstrument != "authn.token.operations" {
		t.Errorf("the operations instrument is %q, want authn.token.operations", operationsInstrument)
	}
	if auditFailuresInstrument != "authn.audit.write-failures" {
		t.Errorf("the audit-failures instrument is %q, want authn.audit.write-failures", auditFailuresInstrument)
	}
	if tokenBytes != 32 {
		t.Errorf("the refresh token carries %d random bytes, want 32", tokenBytes)
	}
}

// TestImportingThePackageRegistersTheModule holds the module registration:
// importing the package puts the token module in the process registry, which is
// what makes it part of a binary's available set.
func TestImportingThePackageRegistersTheModule(t *testing.T) {
	got, ok := core.ProcessRegistry.Lookup(ModuleName)
	if !ok {
		t.Fatalf("the process registry holds no module named %s", ModuleName)
	}
	if got.Name != ModuleName {
		t.Errorf("the registry holds %q, want %q", got.Name, ModuleName)
	}
}

func TestARefreshTokenHasThePrefixAndIsStoredAsItsDigest(t *testing.T) {
	token, err := newRefreshToken()
	if err != nil {
		t.Fatalf("newRefreshToken: %v", err)
	}
	if !strings.HasPrefix(token, refreshTokenPrefix) {
		t.Fatalf("the token %q does not start with %q", token, refreshTokenPrefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, refreshTokenPrefix))
	if err != nil {
		t.Fatalf("the body is not base64url without padding: %v", err)
	}
	if len(raw) != tokenBytes {
		t.Errorf("the body is %d bytes of randomness, want %d", len(raw), tokenBytes)
	}
	again, err := newRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	if token == again {
		t.Error("two refresh tokens came out equal")
	}

	sum := sha256.Sum256([]byte(token))
	if got := digestRefreshToken(token); got != hex.EncodeToString(sum[:]) {
		t.Errorf("the digest is %q, want the hex SHA-256 %q", got, hex.EncodeToString(sum[:]))
	}
	if digestRefreshToken(token) == token {
		t.Error("the digest is the token itself")
	}
}
