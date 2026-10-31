//go:build review

package main

// The review build's impersonation fixture, held to the product's own seams.
//
// The first case seeds the review database, opens it through the real database,
// account and multi-factor code, and asserts that the operator's TOTP factor is
// present, backdated past authn.mfa.sensitive-min-age and reported as seasoned
// by the register's own statement. It reads the seed back through the encrypted
// serializer, computes a code from it, and signs in through the real login flow
// with a password and that code. A renamed column, a changed status or a broken
// serializer breaks one of those steps.
//
// The second case uses the same seeded state: after a password-and-TOTP sign-in,
// a step-up flow offers the SMS factor and not the TOTP one, which is the branch
// the impersonation journey needs to replay its start with a method the session
// did not already prove.

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
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	authnaccount "github.com/vislake/speed/pkg/authn/account"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/mfa"
	mfatotp "github.com/vislake/speed/pkg/authn/mfa/totp"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
)

// The review operator the fixture seeds. They come from review/sqlite.yaml; the
// password is the development credential the file publishes. The account signs
// in with its username: the seeded email is an assertion the administrator made,
// not a way the holder chose, so it is not a login name.
const (
	reviewOperatorUsername = "operator"
	reviewOperatorEmail    = "operator@demo.test"
	reviewOperatorPassword = "operator-dev-secret"
)

// reviewImpersonationSecretEpoch is the TOTP period the factor is generated
// with; a code is computed for the current step.
const reviewImpersonationSecretEpoch = 30

// TestReviewImpersonationTOTPFactorIsSeasonedAndSignsIn is the guard the
// fixture exists for: the seeded factor is real. The factor and its seed are
// read through the product's own seams, the register reports the factor as
// seasoned, and a password-and-TOTP login completes with a code computed from
// the stored secret.
func TestReviewImpersonationTOTPFactorIsSeasonedAndSignsIn(t *testing.T) {
	base, token := reviewImpersonationSession(t)
	if token == "" {
		t.Fatal("the password-and-TOTP sign-in handed out no access token")
	}
	// The sign-in already exercised the seeded secret; a second, authenticated
	// read holds the session it produced.
	res := send(t, &http.Client{Timeout: 10 * time.Second}, http.MethodGet,
		base+"/api/v1/auth/me", nil, map[string]string{"Authorization": "Bearer " + token})
	if res.status != http.StatusOK {
		t.Fatalf("reading the signed-in profile answered %d:\n%s", res.status, res.body)
	}
}

// TestReviewImpersonationStepUpOffersSMS is the second half: after a
// password-and-TOTP sign-in the session has proved TOTP, so a step-up offers a
// kind it has not proved. The SMS factor must be on the list and TOTP must not.
func TestReviewImpersonationStepUpOffersSMS(t *testing.T) {
	base, token := reviewImpersonationSession(t)
	res := send(t, &http.Client{Timeout: 10 * time.Second}, http.MethodPost,
		base+"/api/v1/auth/flows/authenticated",
		map[string]any{"purpose": "step-up"},
		map[string]string{"Authorization": "Bearer " + token})
	if res.status != http.StatusOK {
		t.Fatalf("starting the step-up answered %d:\n%s", res.status, res.body)
	}
	var flow reviewFlowReply
	if err := json.Unmarshal(res.body, &flow); err != nil {
		t.Fatalf("the step-up answer is not JSON: %v\n%s", err, res.body)
	}
	if flow.Next.Kind != "factor" {
		t.Fatalf("the step-up offered kind %q, want a second-factor step:\n%s", flow.Next.Kind, res.body)
	}
	kinds := make([]string, 0, len(flow.Next.Factors))
	for _, factor := range flow.Next.Factors {
		kinds = append(kinds, factor.Kind)
	}
	if slices.Contains(kinds, string(authn.FactorTOTP)) {
		t.Errorf("the step-up offered the TOTP factor the session already proved: %v", kinds)
	}
	if !slices.Contains(kinds, string(authn.FactorSMS)) {
		t.Errorf("the step-up offered %v, want the SMS factor", kinds)
	}
}

// TestReviewImpersonationConfigurationSetsTheFloor holds the configuration the
// fixture relies on: the freshness window is lowered to its one-minute floor and
// the factor age stays at one hour, so the walkthrough reaches the step-up
// branch without waiting out the declared defaults.
func TestReviewImpersonationConfigurationSetsTheFloor(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("review", "sqlite.yaml"))
	if err != nil {
		t.Fatalf("reading the review configuration: %v", err)
	}
	body := string(raw)
	for _, want := range []struct {
		key, value string
	}{
		{"authn.impersonation.max-auth-age", `"1m"`},
		{"authn.mfa.sensitive-min-age", `"1h"`},
	} {
		entry := want.key + "\"\n      value: " + want.value
		if !strings.Contains(body, entry) {
			t.Errorf("the review configuration does not set %s to %s", want.key, want.value)
		}
	}
}

// reviewImpersonationSession seeds a fresh review database, asserts the seeded
// factor through the product's seams, serves it, and signs the operator in with
// a password and a code computed from the seeded TOTP secret.
func reviewImpersonationSession(t *testing.T) (base, token string) {
	t.Helper()
	binary := reviewBinary(t)
	ports := freePorts(t, listenerCount)
	configPath := reviewConfig(t,
		fmt.Sprintf("127.0.0.1:%d", ports[backendListener]))
	config := "file://" + configPath
	seedReview(t, binary, config)
	secret := reviewSeededTOTPSecret(t, configPath)

	serve := startReview(t, binary, config)
	serve.waitReady(t)

	base = fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	token = signInWithTOTP(t, base, reviewOperatorUsername, reviewOperatorPassword, secret)
	return base, token
}

// reviewSeededTOTPSecret opens the seeded database through the real database and
// account code and asserts the fixture through the multi-factor and TOTP seams:
// the operator holds an active TOTP factor activated at least the sensitive
// minimum age ago, the register reports it as seasoned, and its seed reads back
// through the encrypted serializer. It answers the plaintext seed.
func reviewSeededTOTPSecret(t *testing.T, configPath string) string {
	t.Helper()
	var secret string
	withReviewDatabase(t, configPath, func(ctx context.Context, database db.Database, accounts authn.Accounts) {
		id, err := accounts.Lookup(ctx, authn.IdentifierEmail, reviewOperatorEmail)
		if err != nil {
			t.Fatalf("looking up the seeded operator: %v", err)
		}
		record := reviewActiveTOTPFactor(t, ctx, database, id)
		if age := time.Since(record.ActivatedAt); age < time.Hour {
			t.Errorf("the seeded TOTP factor was activated %s ago, want at least the one-hour sensitive minimum",
				age.Round(time.Second))
		}
		seasoned, err := mfa.ReviewSeasoned(ctx, database, id, time.Hour)
		if err != nil {
			t.Fatalf("reading the register's seasoned answer: %v", err)
		}
		if !seasoned {
			t.Error("the register does not report the seeded TOTP factor as seasoned")
		}
		var found bool
		secret, found, err = mfatotp.ReviewSecret(ctx, database, record.ID)
		if err != nil {
			t.Fatalf("reading the seeded TOTP seed through the encrypted serializer: %v", err)
		}
		if !found || secret == "" {
			t.Fatalf("the seeded TOTP factor %s holds no seed", record.ID)
		}
		if secret != reviewOperatorTOTPSeed {
			t.Fatalf("the TOTP seed does not read back as the fixture secret the seed wrote")
		}
	})
	return secret
}

// reviewActiveTOTPFactor finds the operator's active TOTP factor through the
// register's own read.
func reviewActiveTOTPFactor(t *testing.T, ctx context.Context, database db.Database,
	id authn.AccountID,
) authn.FactorRecord {
	t.Helper()
	factors, err := mfa.ReviewFactors(ctx, database, id)
	if err != nil {
		t.Fatalf("listing the seeded factors: %v", err)
	}
	for _, factor := range factors {
		if factor.Kind == authn.FactorTOTP && factor.Status == authn.FactorActive {
			return factor
		}
	}
	t.Fatalf("the seeded operator holds no active TOTP factor: %+v", factors)
	return authn.FactorRecord{}
}

// withReviewDatabase opens the database the review configuration names through
// the real database and account modules and runs check while the assembly is up:
// a read after Drive returns would find the handle released. The encryption key
// is read from the configuration file, so a case cannot silently use a different
// one from the seed run.
func withReviewDatabase(t *testing.T, configPath string,
	check func(context.Context, db.Database, authn.Accounts),
) {
	t.Helper()
	body := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q,"encryption-key":%q}},"authn":{"account":{}}}`,
		filepath.Join(filepath.Dir(configPath), "review.db"), reviewEncryptionKey(t, configPath))
	reader := authntest.ConfigReaderAll(t, body, sqlite.Module(), authnaccount.Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(sqlite.Module())
	reg.Register(authnaccount.Module())
	reg.Supply(reader, config.ModuleName)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{sqlite.ModuleName, authnaccount.ModuleName},
		Action: func(ctx context.Context, reg *core.Registry) error {
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return err
			}
			accounts, err := core.Resolve[authn.Accounts](reg)
			if err != nil {
				return err
			}
			// The handle is released here rather than left to the process, so
			// the serve that follows opens the same file with nothing else
			// holding it.
			if sqlDB, dbErr := database.DB().DB(); dbErr == nil {
				defer func() { _ = sqlDB.Close() }()
			}
			check(ctx, database, accounts)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("reading the seeded database: %v", err)
	}
}

// reviewEncryptionKey reads the database encryption key out of the review
// configuration, so the case opens the seeded rows with the key they were
// sealed under.
func reviewEncryptionKey(t *testing.T, configPath string) string {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("reading the written review configuration: %v", err)
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
	t.Fatalf("the review configuration carries no database encryption key")
	return ""
}

// reviewFlowReply is the part of a flow reply the cases read.
type reviewFlowReply struct {
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
	flow := decodeReviewFlow(t, start.body)
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
	flow = decodeReviewFlow(t, challenged.body)
	if flow.Next.Kind != "challenge" {
		t.Fatalf("the TOTP challenge answered kind %q, want a code prompt:\n%s",
			flow.Next.Kind, challenged.body)
	}

	code := reviewTOTPCode(t, secret, time.Now())
	verified := send(t, client, http.MethodPost, base+"/api/v1/auth/flows/answer", map[string]any{
		"seq":   flow.Seq,
		"step":  "factor",
		"input": map[string]string{"factor": factorID, "code": code},
	}, nil)
	if verified.status != http.StatusOK {
		t.Fatalf("verifying the TOTP code answered %d:\n%s", verified.status, verified.body)
	}
	flow = decodeReviewFlow(t, verified.body)
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

// decodeReviewFlow parses a flow reply and fails the case when the body is not
// one.
func decodeReviewFlow(t *testing.T, body []byte) reviewFlowReply {
	t.Helper()
	var flow reviewFlowReply
	if err := json.Unmarshal(body, &flow); err != nil {
		t.Fatalf("a flow reply is not JSON: %v\n%s", err, body)
	}
	return flow
}

// reviewTOTPCode computes the six-digit code for secret at at, by the
// parameters the factor registers with: HMAC-SHA1, a thirty-second step and six
// digits. The secret is the base32 seed the register stored.
func reviewTOTPCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		t.Fatalf("decoding the seeded TOTP secret: %v", err)
	}
	counter := uint64(at.Unix() / reviewImpersonationSecretEpoch)
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
