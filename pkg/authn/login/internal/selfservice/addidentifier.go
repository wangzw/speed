package selfservice

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// The kinds the self-service add accepts. A phone is not one: it is added
// through POST /phone and POST /phone/verify, which prove it by SMS.
const (
	identifierKindUsername = "username"
	identifierKindEmail    = "email"
)

// The states the add answers with. added says the identifier is written;
// pending says a code was sent and the answer is still to come.
const (
	identifierStateAdded   = "added"
	identifierStatePending = "pending"
)

// The email-code loop. A code is a six-digit number; a record holds at most this
// many attempts before it is spent and the caller has to ask for another code,
// which is what keeps a six-digit code from being guessed over its lifetime.
const (
	emailCodeDigits  = 6
	maxEmailAttempts = 5

	// emailKeyPrefix starts the key of a pending email verification. The key is
	// the prefix and the SHA-256 of the session that started it, so a session
	// holds at most one record at a time and cannot name another session's.
	emailKeyPrefix = "authn:email:"
)

// emailAddress is the form an address is accepted in: ASCII, exactly one at
// sign, at most 254 bytes. The account module owns the authoritative
// normalization; this is the shape the code is sent to, checked before a message
// is paid for.
var emailAddress = regexp.MustCompile(`^[^@\s]+@[^@\s]+$`)

// emailRecord is what a pending email verification keeps in the key-value store:
// the account it was started for, the address the code was sent to, the digest of
// the code, when it was sent and how many attempts it has taken. The client
// submits only the code, so it cannot choose which address is verified.
type emailRecord struct {
	Account    string `json:"account"`
	Email      string `json:"email"`
	CodeDigest string `json:"code_digest"`
	SentAt     int64  `json:"sent_at"`
	ExpiresAt  int64  `json:"expires_at"`
	Attempts   int    `json:"attempts"`
}

// emailKey is the key of a session's pending email verification.
func emailKey(session authn.SessionID) string {
	sum := sha256.Sum256([]byte(session))
	return emailKeyPrefix + hex.EncodeToString(sum[:])
}

// emailDigest is the digest of a code, salted with the session the code was
// drawn for. The session itself is never stored, only its SHA-256 as the key, so
// a reader of the store cannot recompute the digest of a guessed code and tell a
// stop attempt from a wrong one.
func emailDigest(session authn.SessionID, code string) string {
	sum := sha256.Sum256([]byte(string(session) + "\x00" + code))
	return hex.EncodeToString(sum[:])
}

// emailCodeMatches compares a stored digest with the digest of a submitted code
// in constant time.
func emailCodeMatches(want, got string) bool {
	w, err1 := hex.DecodeString(want)
	g, err2 := hex.DecodeString(got)
	if err1 != nil || err2 != nil || len(w) != len(g) {
		return false
	}
	return subtle.ConstantTimeCompare(w, g) == 1
}

// emailCode draws a six-digit code from the system's entropy source.
func emailCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// normalizeEmail lowercases and trims an address and reports whether it is one
// this loop will send to.
func normalizeEmail(raw string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" || len(v) > 254 || !emailAddress.MatchString(v) {
		return "", false
	}
	for _, r := range v {
		if r > 127 {
			return "", false
		}
	}
	return v, true
}

// addIdentifierRequest is the body of POST /identifiers.
type addIdentifierRequest struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// addIdentifierResponse is the answer of POST /identifiers and, with the state
// added, of POST /identifiers/verify: what the call did, and for a pending email
// how long the code lives and how soon another may be asked for.
type addIdentifierResponse struct {
	State       string `json:"state"`
	ExpiresIn   int64  `json:"expires_in,omitempty"`
	ResendAfter int64  `json:"resend_after,omitempty"`
}

// addIdentifier answers POST /identifiers: it adds one login identifier the
// caller chooses. A username is a login name and carries no ownership to prove,
// so it is written at once, recorded as the account's own assertion. An email is
// a contact address, so it is added only once its holder has proved it: a code
// is sent to it and the answer comes back through POST /identifiers/verify,
// where the identifier is written with the proof.
//
// The write is held to the same freshness as every other change of what proves
// the account, refuses an impersonation, and ends like the phone routes: the
// current session gets a fresh token and the account's other sessions are
// revoked, because a new login name is a new way into the account.
func (r *routes) addIdentifier(c *gin.Context) {
	who, ok := r.authorize(c, "")
	if !ok {
		return
	}
	var req addIdentifierRequest
	if !mount.Decode(c, &req) {
		return
	}
	ctx := c.Request.Context()
	switch req.Kind {
	case identifierKindUsername:
		if strings.TrimSpace(req.Value) == "" {
			mount.Fail(c, mount.CodeBadRequest)
			return
		}
		if err := r.deps.Admin.AddIdentifier(ctx, who.account, authn.IdentifierUsername, req.Value,
			authn.Verification{Via: authn.VerifiedViaSelf}); err != nil {
			r.fail(c, err)
			return
		}
		if r.settle(c, who, authn.ReasonSelf) {
			c.JSON(http.StatusOK, addIdentifierResponse{State: identifierStateAdded})
		}
	case identifierKindEmail:
		r.addEmail(c, who, req.Value)
	default:
		mount.Fail(c, mount.CodeBadRequest)
	}
}

// addEmail starts the email branch of POST /identifiers: it sends a code to the
// address and keeps what is needed to check it. A deployment with no email
// channel has no way to prove an address, so it answers that the route is
// unavailable; the code reaches the address and nothing the client reads says
// whether it left.
func (r *routes) addEmail(c *gin.Context, who caller, raw string) {
	ctx := c.Request.Context()
	if r.deps.EmailSender == nil {
		logWarn(ctx, "an email identifier was asked for on a deployment with no email channel", nil)
		mount.Unavailable(c)
		return
	}
	email, ok := normalizeEmail(raw)
	if !ok {
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	key := emailKey(who.session)
	ttl := r.durations.get(ctx, keyEmailCodeTTL, defaultEmailCodeTTL, minEmailCodeTTL, maxEmailCodeTTL)
	resend := r.durations.get(ctx, keyEmailResendInterval, defaultEmailResendInterval, minEmailResendInterval, maxEmailResendInterval)

	// Another code within the interval is a 429, so a client cannot turn the
	// route into a flood of messages to one address.
	pendingRaw, present, err := r.deps.KV.Get(ctx, key)
	if err != nil {
		logWarn(ctx, "the store of pending email verifications cannot be read", err)
		mount.Unavailable(c)
		return
	}
	if present {
		var prior emailRecord
		if json.Unmarshal(pendingRaw, &prior) == nil && prior.Account == string(who.account) {
			if elapsed := r.deps.Clock.Now().Sub(time.Unix(prior.SentAt, 0)); elapsed < resend {
				mount.Throttled(c, resend-elapsed)
				return
			}
		}
	}

	code, err := emailCode()
	if err != nil {
		logWarn(ctx, "an email verification code could not be drawn", err)
		mount.Unavailable(c)
		return
	}
	if err = r.deps.EmailSender.SendCode(ctx, email, code, ttl, smsLocale(ctx)); err != nil {
		r.fail(c, err)
		return
	}
	now := r.deps.Clock.Now()
	rec := emailRecord{
		Account: string(who.account), Email: email, CodeDigest: emailDigest(who.session, code),
		SentAt: now.Unix(), ExpiresAt: now.Add(ttl).Unix(), Attempts: 0,
	}
	value, err := json.Marshal(rec)
	if err != nil {
		mount.Unavailable(c)
		return
	}
	if err = r.deps.KV.Set(ctx, key, value, ttl); err != nil {
		logWarn(ctx, "the store of pending email verifications cannot be written", err)
		mount.Unavailable(c)
		return
	}
	c.JSON(http.StatusOK, addIdentifierResponse{
		State: identifierStatePending, ExpiresIn: seconds(ttl), ResendAfter: seconds(resend),
	})
}

// verifyIdentifierRequest is the body of POST /identifiers/verify.
type verifyIdentifierRequest struct {
	Code string `json:"code"`
}

// verifyIdentifier answers POST /identifiers/verify: it checks the code sent to
// the address and, on a match, writes the address as a verified identifier and
// publishes it through the account module. The record of the session is read
// first; a missing one, one that names another account, or one that does not
// parse is a flow-invalid, so nothing about another session's pending address
// leaks. A code that does not match counts an attempt, and the record is spent
// once the attempts are used up, so a six-digit code cannot be guessed over its
// lifetime.
func (r *routes) verifyIdentifier(c *gin.Context) {
	who, ok := r.authorize(c, "")
	if !ok {
		return
	}
	var req verifyIdentifierRequest
	if !mount.Decode(c, &req) {
		return
	}
	if req.Code == "" || len(req.Code) > maxCodeLen {
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	ctx := c.Request.Context()
	key := emailKey(who.session)
	raw, present, err := r.deps.KV.Get(ctx, key)
	if err != nil {
		logWarn(ctx, "the store of pending email verifications cannot be read", err)
		mount.Unavailable(c)
		return
	}
	var rec emailRecord
	if !present || json.Unmarshal(raw, &rec) != nil || rec.Account != string(who.account) ||
		rec.Email == "" || rec.CodeDigest == "" {
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
		return
	}
	if !emailCodeMatches(rec.CodeDigest, emailDigest(who.session, req.Code)) {
		countEmailAttempt(ctx, r, key, rec)
		mount.Unauthorized(c, r.endpoint, mount.CodeLoginFailed)
		return
	}

	bindErr := r.deps.Admin.AddIdentifier(ctx, who.account, authn.IdentifierEmail, rec.Email,
		authn.Verification{Via: authn.VerifiedViaEmailCode})
	if err := r.deps.KV.Delete(ctx, key); err != nil {
		logWarn(ctx, "a consumed email verification could not be deleted", err)
	}
	if bindErr != nil {
		r.fail(c, bindErr)
		return
	}
	if r.settle(c, who, authn.ReasonSelf) {
		c.JSON(http.StatusOK, addIdentifierResponse{State: identifierStateAdded})
	}
}

// countEmailAttempt records one failed attempt against a pending record, and
// spends the record once the attempts are used up. A store that cannot be written
// leaves the record as it was, so a failed count never lets the attempt limit
// grow; the next attempt starts from the same count.
func countEmailAttempt(ctx context.Context, r *routes, key string, rec emailRecord) {
	rec.Attempts++
	if rec.Attempts >= maxEmailAttempts {
		if err := r.deps.KV.Delete(ctx, key); err != nil {
			logWarn(ctx, "a spent email verification could not be deleted", err)
		}
		return
	}
	value, err := json.Marshal(rec)
	if err != nil {
		return
	}
	ttl := time.Unix(rec.ExpiresAt, 0).Sub(r.deps.Clock.Now())
	if ttl <= 0 {
		if err := r.deps.KV.Delete(ctx, key); err != nil {
			logWarn(ctx, "an expired email verification could not be deleted", err)
		}
		return
	}
	if err := r.deps.KV.Set(ctx, key, value, ttl); err != nil {
		logWarn(ctx, "the attempt count of an email verification cannot be written", err)
	}
}
