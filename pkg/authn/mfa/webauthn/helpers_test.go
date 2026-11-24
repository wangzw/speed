package webauthn

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	libwebauthn "github.com/go-webauthn/webauthn/webauthn"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/internal/dbtest"
)

const (
	testRPID   = "example.com"
	testOrigin = "https://example.com"
)

const acct = authn.AccountID("acct-1")

func testConfig() resolved {
	return resolved{rpID: testRPID, rpName: "speed", origins: []string{testOrigin}}
}

const sqliteDialect = "sqlite"

func applyMigrations(t *testing.T, h *gorm.DB, dialect string) {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, dialect)
	if err != nil {
		t.Fatalf("listing the %s migrations: %v", dialect, err)
	}
	for _, entry := range entries {
		sql, err := fs.ReadFile(migrationsFS, dialect+"/"+entry.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		for _, statement := range strings.Split(string(sql), ";") {
			if strings.TrimSpace(statement) == "" {
				continue
			}
			if err := h.Exec(statement).Error; err != nil {
				t.Fatalf("applying %s: %v", entry.Name(), err)
			}
		}
	}
}

type dialect struct {
	name string
	open func(t *testing.T) *gorm.DB
}

var dialects = []dialect{{
	name: sqliteDialect,
	open: func(t *testing.T) *gorm.DB {
		return dbtest.SQLite(t, filepath.Join(t.TempDir(), "webauthn.db"), nil).DB()
	},
}}

func openDialect(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	for _, d := range dialects {
		if d.name == dialect {
			h := d.open(t).Session(&gorm.Session{Logger: logger.Discard})
			applyMigrations(t, h, dialect)
			return h
		}
	}
	t.Fatalf("no dialect %q", dialect)
	return nil
}

func forEachDialect(t *testing.T, run func(t *testing.T, h *gorm.DB, dialect string)) {
	t.Helper()
	for _, d := range dialects {
		t.Run(d.name, func(t *testing.T) {
			run(t, openDialect(t, d.name), d.name)
		})
	}
}

type fakeMFA struct {
	authn.MFA
	mu      sync.Mutex
	factors map[authn.AccountID][]authn.FactorRecord
	kinds   []authn.FactorKind
}

func newFakeMFA() *fakeMFA {
	return &fakeMFA{factors: map[authn.AccountID][]authn.FactorRecord{}, kinds: []authn.FactorKind{authn.FactorWebAuthn}}
}

func (m *fakeMFA) set(account authn.AccountID, recs ...authn.FactorRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.factors[account] = recs
}

func (m *fakeMFA) Factors(_ context.Context, id authn.AccountID) ([]authn.FactorRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]authn.FactorRecord(nil), m.factors[id]...), nil
}

func (m *fakeMFA) Kinds() []authn.FactorKind { return append([]authn.FactorKind(nil), m.kinds...) }

type fakeAccounts struct {
	authn.Accounts
	usernames map[authn.AccountID]string
}

func (a *fakeAccounts) Identifiers(_ context.Context, id authn.AccountID) ([]authn.Identifier, error) {
	if u, ok := a.usernames[id]; ok {
		return []authn.Identifier{{Kind: authn.IdentifierUsername, Value: u}}, nil
	}
	return nil, nil
}

type rig struct {
	t        *testing.T
	p        *product
	h        *gorm.DB
	mfa      *fakeMFA
	accounts *fakeAccounts
}

func newRig(t *testing.T, h *gorm.DB) *rig {
	t.Helper()
	cfg := testConfig()
	wa, err := newRelyingParty(cfg)
	if err != nil {
		t.Fatalf("building the relying party: %v", err)
	}
	mfa := newFakeMFA()
	accounts := &fakeAccounts{usernames: map[authn.AccountID]string{acct: "alice"}}
	p := &product{
		register: &register{handle: h},
		accounts: accounts,
		mfa:      mfa,
		wa:       wa,
		config:   cfg,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return &rig{t: t, p: p, h: h, mfa: mfa, accounts: accounts}
}

func pending() authn.FactorRecord {
	return authn.FactorRecord{ID: "factor-1", Account: acct, Kind: authn.FactorWebAuthn, Status: authn.FactorPending}
}

type authenticator struct {
	key    *ecdsa.PrivateKey
	credID []byte
	rpID   string
	origin string
}

func newAuthenticator(t *testing.T) *authenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	cred := make([]byte, 32)
	if _, err := rand.Read(cred); err != nil {
		t.Fatalf("drawing a credential id: %v", err)
	}
	return &authenticator{key: key, credID: cred, rpID: testRPID, origin: testOrigin}
}

func (a *authenticator) coseKey(t *testing.T) []byte {
	t.Helper()
	point, err := a.key.PublicKey.Bytes()
	if err != nil {
		t.Fatalf("encoding the public key: %v", err)
	}
	if len(point) != 65 || point[0] != 4 {
		t.Fatalf("the public key encoded to %d bytes, want an uncompressed P-256 point", len(point))
	}
	data, err := webauthncbor.Marshal(map[int64]any{
		1:  int64(webauthncose.EllipticKey),
		3:  int64(webauthncose.AlgES256),
		-1: int64(webauthncose.P256),
		-2: point[1:33],
		-3: point[33:65],
	})
	if err != nil {
		t.Fatalf("encoding the COSE key: %v", err)
	}
	return data
}

func (a *authenticator) authData(t *testing.T, flags protocol.AuthenticatorFlags, count uint32, uv, attested bool) []byte {
	t.Helper()
	h := sha256.Sum256([]byte(a.rpID))
	out := append([]byte(nil), h[:]...)
	if uv {
		flags |= protocol.FlagUserVerified
	}
	out = append(out, byte(flags))
	out = binary.BigEndian.AppendUint32(out, count)
	if attested {
		out = append(out, make([]byte, 16)...)
		out = binary.BigEndian.AppendUint16(out, uint16(len(a.credID)))
		out = append(out, a.credID...)
		out = append(out, a.coseKey(t)...)
	}
	return out
}

func (a *authenticator) clientData(t *testing.T, ceremony, challenge string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"type":        ceremony,
		"challenge":   challenge,
		"origin":      a.origin,
		"crossOrigin": false,
	})
	if err != nil {
		t.Fatalf("encoding the client data: %v", err)
	}
	return data
}

func (a *authenticator) sign(t *testing.T, authData, clientDataJSON []byte) []byte {
	t.Helper()
	h := sha256.Sum256(clientDataJSON)
	signed := append(append([]byte(nil), authData...), h[:]...)
	digest := sha256.Sum256(signed)
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return sig
}

func (a *authenticator) registration(t *testing.T, challenge string, uv bool) []byte {
	t.Helper()
	authData := a.authData(t, protocol.FlagUserPresent|protocol.FlagAttestedCredentialData, 0, uv, true)
	cdj := a.clientData(t, "webauthn.create", challenge)
	att, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": authData})
	if err != nil {
		t.Fatalf("encoding the attestation object: %v", err)
	}
	return credentialBodyJSON(t, a.credID, map[string]any{
		"attestationObject": base64.RawURLEncoding.EncodeToString(att),
		"clientDataJSON":    base64.RawURLEncoding.EncodeToString(cdj),
	}, nil)
}

func (a *authenticator) assertion(t *testing.T, challenge string, count uint32, uv bool, handle []byte) []byte {
	t.Helper()
	authData := a.authData(t, protocol.FlagUserPresent, count, uv, false)
	cdj := a.clientData(t, "webauthn.get", challenge)
	sig := a.sign(t, authData, cdj)
	response := map[string]any{
		"authenticatorData": base64.RawURLEncoding.EncodeToString(authData),
		"clientDataJSON":    base64.RawURLEncoding.EncodeToString(cdj),
		"signature":         base64.RawURLEncoding.EncodeToString(sig),
	}
	if len(handle) > 0 {
		response["userHandle"] = base64.RawURLEncoding.EncodeToString(handle)
	}
	return credentialBodyJSON(t, a.credID, response, nil)
}

func tamperedAssertion(t *testing.T, a *authenticator, challenge string, count uint32) []byte {
	t.Helper()
	other := newAuthenticator(t)
	authData := a.authData(t, protocol.FlagUserPresent, count, true, false)
	cdj := a.clientData(t, "webauthn.get", challenge)
	sig := other.sign(t, authData, cdj)
	return credentialBodyJSON(t, a.credID, map[string]any{
		"authenticatorData": base64.RawURLEncoding.EncodeToString(authData),
		"clientDataJSON":    base64.RawURLEncoding.EncodeToString(cdj),
		"signature":         base64.RawURLEncoding.EncodeToString(sig),
	}, nil)
}

func credentialBodyJSON(t *testing.T, credID []byte, response map[string]any, extra map[string]any) []byte {
	t.Helper()
	id := base64.RawURLEncoding.EncodeToString(credID)
	body := map[string]any{
		"id":       id,
		"rawId":    id,
		"type":     "public-key",
		"response": response,
	}
	for k, v := range extra {
		body[k] = v
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encoding the response body: %v", err)
	}
	return data
}

type creationOptions struct {
	Challenge string `json:"challenge"`
	RP        struct {
		ID string `json:"id"`
	} `json:"rp"`
	User struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"user"`
	AuthenticatorSelection struct {
		ResidentKey        string `json:"residentKey"`
		RequireResidentKey *bool  `json:"requireResidentKey"`
		UserVerification   string `json:"userVerification"`
	} `json:"authenticatorSelection"`
	ExcludeCredentials []struct {
		ID string `json:"id"`
	} `json:"excludeCredentials"`
}

type assertionOptions struct {
	Challenge        string `json:"challenge"`
	UserVerification string `json:"userVerification"`
	AllowCredentials []struct {
		ID string `json:"id"`
	} `json:"allowCredentials"`
}

func decodeCreation(t *testing.T, v any) creationOptions {
	t.Helper()
	raw, ok := v.(json.RawMessage)
	if !ok {
		t.Fatalf("the options are %T, want json.RawMessage", v)
	}
	var out creationOptions
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decoding the creation options: %v", err)
	}
	return out
}

func decodeAssertion(t *testing.T, v any) assertionOptions {
	t.Helper()
	raw, ok := v.(json.RawMessage)
	if !ok {
		t.Fatalf("the options are %T, want json.RawMessage", v)
	}
	var out assertionOptions
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decoding the assertion options: %v", err)
	}
	return out
}

func (r *rig) enroll(t *testing.T, a *authenticator, profile string, passkey bool) authn.FactorRecord {
	t.Helper()
	ctx := context.Background()
	rec := pending()
	got, err := r.p.beginEnroll(ctx, rec, authn.StepInput{
		Action: authn.InputSubmit,
		Fields: map[string]string{fieldProfile: profile},
	})
	if err != nil {
		t.Fatalf("beginEnroll: %v", err)
	}
	if step.KindOf(got) != step.KindData {
		t.Fatalf("beginEnroll answered %v, want data", step.KindOf(got))
	}
	opts := decodeCreation(t, step.DataOf(got))
	a.rpID = opts.RP.ID
	a.origin = testOrigin
	body := a.registration(t, opts.Challenge, passkey)
	fin, err := r.p.finishEnroll(ctx, rec, authn.FlowState{Private: step.PrivateOf(got)},
		authn.StepInput{Action: authn.InputSubmit, Fields: map[string]string{fieldCredential: string(body)}})
	if err != nil {
		t.Fatalf("finishEnroll: %v", err)
	}
	if step.KindOf(fin) != step.KindEnrolled {
		t.Fatalf("finishEnroll answered %v, want enrolled", step.KindOf(fin))
	}
	return rec
}

func expiredCeremony(t *testing.T, challenge string, userID []byte) []byte {
	t.Helper()
	session := libwebauthn.SessionData{
		Challenge:        challenge,
		UserID:           userID,
		Expires:          time.Now().Add(-time.Hour),
		UserVerification: protocol.VerificationPreferred,
	}
	state, err := marshalCeremony("", &session)
	if err != nil {
		t.Fatalf("marshalling an expired ceremony: %v", err)
	}
	return state
}

func (r *rig) storedCount(t *testing.T, factorID string) (int64, bool) {
	t.Helper()
	row, found, err := r.p.register.getCredential(context.Background(), factorID)
	if err != nil {
		t.Fatalf("getCredential: %v", err)
	}
	if !found {
		t.Fatalf("the factor %s has no credential", factorID)
	}
	return row.SignCount, row.CloneWarning
}

type mapReader struct{ values map[string]any }

func (m mapReader) Decode(path string, target any) error {
	v, ok := m.values[path]
	if !ok {
		return nil
	}
	reflect.ValueOf(target).Elem().Set(reflect.ValueOf(v))
	return nil
}

func configReader(publicURL string) config.Reader {
	return mapReader{values: map[string]any{gateNamespace + "." + gateMountPath: gateMount{PublicURL: publicURL}}}
}

func configReaderFor(publicURL string, cfg moduleConfig) config.Reader {
	return mapReader{values: map[string]any{
		gateNamespace + "." + gateMountPath: gateMount{PublicURL: publicURL},
		ConfigNamespace:                     cfg,
	}}
}
