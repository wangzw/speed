package main

// Shared helpers for the cases that drive the demo server over the e2e
// configuration (server/e2e/config.yaml). The program is the ordinary binary
// TestMain built; the e2e build tag changes only the embedded front end.

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	authnaccount "github.com/vislake/speed/pkg/authn/account"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/mfa/mfatest"
	"github.com/vislake/speed/pkg/authn/provisioning/provisioningtest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
)

// The operator the e2e configuration seeds. The password is the development
// credential the file publishes. The account signs in with its username: the
// seeded email is an assertion the administrator made, not a way the holder
// chose, so it is not a login name.
const (
	e2eOperatorUsername = "operator"
	e2eOperatorEmail    = "operator@demo.test"
	e2eOperatorPassword = "operator-dev-secret"

	// e2eOperatorTOTPSeed is the base32 seed the cases write for the
	// operator's TOTP factor. It is fixed on purpose: a person walking the
	// journey imports it into an authenticator app (issuer Speed Demo, account
	// the operator id, SHA-1, six digits, thirty-second period).
	e2eOperatorTOTPSeed = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"

	// e2eOperatorPhone is the phone the SMS factor binds in the cases that
	// need a second kind.
	e2eOperatorPhone = "+15551234567"

	// e2eImpersonationSecretEpoch is the TOTP period the factor is generated
	// with; a code is computed for the current step.
	e2eImpersonationSecretEpoch = 30
)

// e2eOperatorFactorAge is how far into the past the cases activate the
// operator's TOTP factor. It is well past authn.mfa.sensitive-min-age, which the
// e2e configuration lowers to its one-hour floor, so the impersonation gate
// reads the factor as seasoned without an hour of waiting.
const e2eOperatorFactorAge = 2 * time.Hour

// e2eBinary answers the program the cases drive. TestMain builds it once;
// there is no tagged variant.
func e2eBinary(t *testing.T) string {
	t.Helper()
	return hostBinary
}

// e2eConfig writes the e2e configuration with the listener address a case
// chooses and with its relative paths under a directory of the case's own: the
// file's own values would bind a fixed port and write into the source tree. The
// token signing key is minted at the path the written file names.
func e2eConfig(t *testing.T, backendAddress string) string {
	t.Helper()
	root := t.TempDir()
	body := e2eConfigBody(t, root, backendAddress,
		filepath.Join(root, "e2e.db"))
	path := filepath.Join(root, "e2e.yaml")
	if writeErr := os.WriteFile(path, []byte(body), 0o600); writeErr != nil {
		t.Fatalf("writing the configuration: %v", writeErr)
	}
	return path
}

// e2eConfigBody returns the e2e configuration with the listener address a
// case chooses, its token signing key and object store under root, and the
// database locator the caller gives. It is the assembly's own file —
// server/e2e/config.yaml — because every module it names reads its
// configuration from it: a case that runs the assembly over a baseline template
// starts modules whose required items have no value.
func e2eConfigBody(t *testing.T, root, backendAddress, dsn string) string {
	t.Helper()
	document, err := os.ReadFile(filepath.Join("e2e", "config.yaml"))
	if err != nil {
		t.Fatalf("reading the e2e configuration: %v", err)
	}
	key := filepath.Join(root, "token-signing-key.pem")
	if err := ensureSigningKey(key); err != nil {
		t.Fatalf("minting the case's token signing key: %v", err)
	}
	body := string(document)
	for _, edit := range [][2]string{
		{`address: "127.0.0.1:8080"`, fmt.Sprintf("address: %q", backendAddress)},
		// The needles carry the YAML key: the file's header comments name the
		// same paths, and replacing the first occurrence would leave the
		// configured locator pointing into the source tree.
		{`dsn: "./var/e2e.db"`, fmt.Sprintf("dsn: %q", dsn)},
		{`signing-key-file: var/token-signing-key.pem`, fmt.Sprintf("signing-key-file: %q", key)},
		{`root: "./var/objects"`, fmt.Sprintf("root: %q", filepath.Join(root, "objects"))},
	} {
		if !strings.Contains(body, edit[0]) {
			t.Fatalf("the e2e configuration holds no %q to replace", edit[0])
		}
		body = strings.Replace(body, edit[0], edit[1], 1)
	}
	return body
}

// seedE2E runs the seed command over the configuration the case wrote, so
// the run that follows finds the rows the assembly needs.
func seedE2E(t *testing.T, binary, config string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), commandLimit())
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--config", config, "sqlite", seedCommand)
	cmd.Env = childEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seeding the e2e configuration: %v\n%s", err, out)
	}
}

// startE2E runs the serve command the way demo_test's start runs it: through
// the same child type, so its standard error is captured the same way and a
// bind failure is recognisable on both.
func startE2E(t *testing.T, binary, config string) *process {
	t.Helper()
	return startProcess(t, binary, "", []string{"--config", config, "sqlite", serveCommand}, nil)
}

// seedE2EOperatorFactors writes the operator's TOTP and SMS factors through
// the mfa test-support package, aged past the sensitive minimum, and answers the
// plaintext TOTP seed. A factor enrolled through the product is activated now; a
// backdated row is what the impersonation journey needs.
func seedE2EOperatorFactors(t *testing.T, configPath string) string {
	t.Helper()
	var secret string
	withE2EDatabase(t, configPath,
		func(ctx context.Context, database db.Database, accounts authn.Accounts, admin authn.AccountAdmin) {
			id, err := accounts.Lookup(ctx, authn.IdentifierEmail, e2eOperatorEmail)
			if err != nil {
				t.Fatalf("looking up the seeded operator: %v", err)
			}
			// The impersonation start requires a proved contact on the initiator.
			// Prove the address the seed wrote and a phone number, the same writes
			// a completed code flow performs.
			proveE2EIdentifier(t, ctx, accounts, admin, id, authn.IdentifierEmail, e2eOperatorEmail)
			proveE2EIdentifier(t, ctx, accounts, admin, id, authn.IdentifierPhone, e2eOperatorPhone)

			now := time.Now().UTC()
			factorID, err := mfatest.SeedTOTPFactor(ctx, database, id, now.Add(-e2eOperatorFactorAge),
				e2eOperatorTOTPSeed)
			if err != nil {
				t.Fatalf("seeding the operator's TOTP factor: %v", err)
			}
			if _, seedErr := mfatest.SeedActiveFactor(ctx, database, id, authn.FactorSMS, now); seedErr != nil {
				t.Fatalf("seeding the operator's SMS factor: %v", seedErr)
			}
			seasoned, err := mfatest.Seasoned(ctx, database, id, time.Hour)
			if err != nil {
				t.Fatalf("reading the register's seasoned answer: %v", err)
			}
			if !seasoned {
				t.Error("the register does not report the backdated TOTP factor as seasoned")
			}
			got, found, err := mfatest.TOTPSecret(ctx, database, factorID)
			if err != nil {
				t.Fatalf("reading the TOTP seed back: %v", err)
			}
			if !found || got != e2eOperatorTOTPSeed {
				t.Fatalf("the TOTP seed reads back as %q (found %v), want the fixture secret", got, found)
			}
			secret = got
		})
	return secret
}

// proveE2EIdentifier proves one identifier on the account through the
// account module's own AddIdentifier, the write a completed code flow performs.
func proveE2EIdentifier(t *testing.T, ctx context.Context, accounts authn.Accounts,
	admin authn.AccountAdmin, id authn.AccountID, kind authn.IdentifierKind, value string,
) {
	t.Helper()
	held, err := accounts.Identifiers(ctx, id)
	if err != nil {
		t.Fatalf("reading the operator's identifiers: %v", err)
	}
	for _, identifier := range held {
		if identifier.Kind == kind && identifier.Value == value &&
			(authn.Verification{Via: identifier.VerifiedVia}).Proves() {
			return
		}
	}
	via := authn.Verification{Via: authn.VerifiedViaEmailCode}
	if kind == authn.IdentifierPhone {
		via = authn.Verification{Via: authn.VerifiedViaSMSCode}
	}
	if err := admin.AddIdentifier(ctx, id, kind, value, via); err != nil {
		t.Fatalf("proving the operator's %s %q: %v", kind, value, err)
	}
}

// withE2EDatabase opens the database the e2e configuration names through
// the real database and account modules and runs check while the assembly is
// up: a read after Drive returns would find the handle released. The encryption
// key is read from the configuration file, so a case cannot silently use a
// different one from the seed run.
func withE2EDatabase(t *testing.T, configPath string,
	check func(context.Context, db.Database, authn.Accounts, authn.AccountAdmin),
) {
	t.Helper()
	body := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q,"encryption-key":%q}},"authn":{"account":{}}}`,
		filepath.Join(filepath.Dir(configPath), "e2e.db"), e2eEncryptionKey(t, configPath))
	reader := authntest.ConfigReaderAll(t, body, sqlite.Module(), authnaccount.Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(sqlite.Module())
	reg.Register(authnaccount.Module())
	// The account module requires a provisioning seam. This case drives the
	// account store alone; the stand-in records the calls and writes nothing.
	reg.Register(provisioningtest.Module())
	reg.Supply(reader, config.ModuleName)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{sqlite.ModuleName, authnaccount.ModuleName, provisioningtest.ModuleName},
		Action: func(ctx context.Context, reg *core.Registry) error {
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return err
			}
			accounts, err := core.Resolve[authn.Accounts](reg)
			if err != nil {
				return err
			}
			admin, err := core.Resolve[authn.AccountAdmin](reg)
			if err != nil {
				return err
			}
			// The handle is released here rather than left to the process, so
			// the serve that follows opens the same file with nothing else
			// holding it.
			if sqlDB, dbErr := database.DB().DB(); dbErr == nil {
				defer func() { _ = sqlDB.Close() }()
			}
			check(ctx, database, accounts, admin)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("reading the seeded database: %v", err)
	}
}

// e2eEncryptionKey reads the database encryption key out of the e2e
// configuration, so the case opens the seeded rows with the key they were
// sealed under.
func e2eEncryptionKey(t *testing.T, configPath string) string {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("reading the written e2e configuration: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		value, ok := strings.CutPrefix(trimmed, "encryption-key:")
		if !ok {
			continue
		}
		key := strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" {
			return key
		}
	}
	t.Fatalf("the e2e configuration carries no database encryption key")
	return ""
}

// e2eFlowReply is the part of a flow reply the cases read.
type e2eFlowReply struct {
	Seq   int    `json:"seq"`
	State string `json:"state"`
	Next  struct {
		Kind    string `json:"kind"`
		Factors []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"factors"`
	} `json:"next"`
}

// signInWithTOTP runs a password login completed with the TOTP factor and
// answers the access token. The code is computed from secret at the moment of
// the verification step.
func signInWithTOTP(t *testing.T, base, identifier, password, secret string) string {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("building a cookie jar: %v", err)
	}
	client := &http.Client{Timeout: 10 * time.Second, Jar: jar}

	start := send(t, client, http.MethodPost, base+"/api/v1/auth/flows", map[string]any{
		"method":   "password",
		"delivery": "token",
		"binding":  "cookie",
		"input":    map[string]string{"identifier": identifier, "password": password},
	}, nil)
	if start.status != http.StatusOK {
		t.Fatalf("starting the password login answered %d:\n%s", start.status, start.body)
	}
	flow := decodeE2EFlow(t, start.body)
	if flow.Next.Kind != "factor" {
		t.Fatalf("the password step offered kind %q, want a second-factor step:\n%s",
			flow.Next.Kind, start.body)
	}
	var factorID string
	for _, factor := range flow.Next.Factors {
		if factor.Kind == string(authn.FactorTOTP) {
			factorID = factor.ID
		}
	}
	if factorID == "" {
		t.Fatalf("the login offered no TOTP factor:\n%s", start.body)
	}

	challenged := send(t, client, http.MethodPost, base+"/api/v1/auth/flows/answer", map[string]any{
		"seq":   flow.Seq,
		"step":  "factor",
		"input": map[string]string{"factor": factorID},
	}, nil)
	if challenged.status != http.StatusOK {
		t.Fatalf("starting the TOTP challenge answered %d:\n%s", challenged.status, challenged.body)
	}
	flow = decodeE2EFlow(t, challenged.body)
	if flow.Next.Kind != "challenge" {
		t.Fatalf("the TOTP challenge answered kind %q, want a code prompt:\n%s",
			flow.Next.Kind, challenged.body)
	}

	code := e2eTOTPCode(t, secret, time.Now())
	verified := send(t, client, http.MethodPost, base+"/api/v1/auth/flows/answer", map[string]any{
		"seq":   flow.Seq,
		"step":  "factor",
		"input": map[string]string{"factor": factorID, "code": code},
	}, nil)
	if verified.status != http.StatusOK {
		t.Fatalf("verifying the TOTP code answered %d:\n%s", verified.status, verified.body)
	}
	flow = decodeE2EFlow(t, verified.body)
	if flow.Next.Kind != "complete" {
		t.Fatalf("the TOTP verification answered kind %q, want a complete step:\n%s",
			flow.Next.Kind, verified.body)
	}

	done := send(t, client, http.MethodPost, base+"/api/v1/auth/flows/answer", map[string]any{
		"seq":  flow.Seq,
		"step": "complete",
	}, nil)
	if done.status != http.StatusOK {
		t.Fatalf("completing the login answered %d:\n%s", done.status, done.body)
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(done.body, &token); err != nil {
		t.Fatalf("the login answer is not JSON: %v\n%s", err, done.body)
	}
	if token.AccessToken == "" {
		t.Fatalf("the login handed out no access token:\n%s", done.body)
	}
	return token.AccessToken
}

// decodeE2EFlow parses a flow reply and fails the case when the body is not
// one.
func decodeE2EFlow(t *testing.T, body []byte) e2eFlowReply {
	t.Helper()
	var flow e2eFlowReply
	if err := json.Unmarshal(body, &flow); err != nil {
		t.Fatalf("a flow reply is not JSON: %v\n%s", err, body)
	}
	return flow
}

// e2eTOTPCode computes the six-digit code for secret at at, by the
// parameters the factor registers with: HMAC-SHA1, a thirty-second step and six
// digits. The secret is the base32 seed the register stored.
func e2eTOTPCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		t.Fatalf("decoding the seeded TOTP secret: %v", err)
	}
	counter := uint64(at.Unix() / e2eImpersonationSecretEpoch)
	var counterBytes [8]byte
	binary.BigEndian.PutUint64(counterBytes[:], counter)
	mac := hmac.New(sha1.New, key)
	if _, err := mac.Write(counterBytes[:]); err != nil {
		t.Fatalf("computing the TOTP digest: %v", err)
	}
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	code := (uint32(digest[offset])&0x7f)<<24 |
		uint32(digest[offset+1])<<16 |
		uint32(digest[offset+2])<<8 |
		uint32(digest[offset+3])
	return fmt.Sprintf("%06d", code%1000000)
}
