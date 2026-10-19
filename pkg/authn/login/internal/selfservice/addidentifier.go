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

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
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

// The email-code loop. A code is a six-digit number; a pending verification is
// spent after at most this many failed attempts, and the caller has to ask for
// another code, which is what keeps a six-digit code from being guessed over its
// lifetime.
const (
	emailCodeDigits  = 6
	maxEmailAttempts = 5

	// emailKeyPrefix starts the key of a pending email verification. The key is
	// the prefix and the SHA-256 of the session that started it, so a session
	// holds at most one record at a time and cannot name another session's.
	emailKeyPrefix = "authn:email:"

	// emailAttemptKeyPrefix starts the key of a pending verification's failed
	// attempts. The rest of the key is the digest of the code the attempts are
	// counted against, so each code counts from zero on a key of its own and the
	// count of one code never bleeds into the next. It is a key of its own so an
	// attempt is counted by an atomic increment: a count kept inside the record
	// made every wrong code a read-modify-write of that record, which concurrent
	// wrong codes race through and lose, and a lost count is a bound that no
	// longer holds.
	emailAttemptKeyPrefix = "authn:email-attempts:"

	// emailResendKeyPrefix starts the key of an address's resend window. The
	// rest of the key is the SHA-256 of the address's normalized form, so the
	// window is held against the address and shared across sessions and accounts:
	// a second session cannot turn the route into a flood of messages to one
	// address.
	emailResendKeyPrefix = "authn:email-resend:"
)

// emailAddress is the form an address is accepted in: ASCII, exactly one at
// sign, at most 254 bytes. The account module owns the authoritative
// normalization; this is the shape the code is sent to, checked before a message
// is paid for.
var emailAddress = regexp.MustCompile(`^[^@\s]+@[^@\s]+$`)

// emailRecord is what a pending email verification keeps in the key-value store:
// the account it was started for, the address the code was sent to, the digest of
// the code and when it expires. The client submits only the code, so it cannot
// choose which address is verified.
type emailRecord struct {
	Account    string `json:"account"`
	Email      string `json:"email"`
	CodeDigest string `json:"code_digest"`
	ExpiresAt  int64  `json:"expires_at"`
}

// emailResendMark is what an address's resend key holds: the instant a code was
// sent to it, so a throttled caller is told how long to wait.
type emailResendMark struct {
	SentAt int64 `json:"sent_at"`
}

// emailKey is the key of a session's pending email verification.
func emailKey(session authn.SessionID) string {
	sum := sha256.Sum256([]byte(session))
	return emailKeyPrefix + hex.EncodeToString(sum[:])
}

// emailAttemptKey is the key of a code's failed-attempt count. The digest names
// the code, so a fresh code starts from zero on a key of its own.
func emailAttemptKey(codeDigest string) string {
	return emailAttemptKeyPrefix + codeDigest
}

// emailResendKey is the resend-window key of an address: the SHA-256 of its
// normalized form, shared across the sessions and accounts that name it.
func emailResendKey(email string) string {
	sum := sha256.Sum256([]byte(email))
	return emailResendKeyPrefix + hex.EncodeToString(sum[:])
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
	bound := new(big.Int).Exp(big.NewInt(10), big.NewInt(emailCodeDigits), nil)
	n, err := rand.Int(rand.Reader, bound)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", emailCodeDigits, n.Int64()), nil
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
func (r *routes) addIdentifier(ctx context.Context, in addIdentifierInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	who, ok := r.authorize(c, "")
	if !ok {
		return c.result()
	}
	switch in.Kind {
	case identifierKindUsername:
		if strings.TrimSpace(in.Value) == "" {
			c.fail(mount.CodeBadRequest)
			return c.result()
		}
		if err := r.deps.Admin.AddIdentifier(c.ctx, who.account, authn.IdentifierUsername, in.Value,
			authn.Verification{Via: authn.VerifiedViaSelf}); err != nil {
			r.fail(c, err)
			return c.result()
		}
		if r.settle(c, who, authn.ReasonSelf) {
			c.json(http.StatusOK, addIdentifierResponse{State: identifierStateAdded})
		}
	case identifierKindEmail:
		r.addEmail(c, who, in.Value)
	default:
		c.fail(mount.CodeBadRequest)
	}
	return c.result()
}

// addEmail starts the email branch of POST /identifiers: it sends a code to the
// address and keeps what is needed to check it. A deployment with no email
// channel has no way to prove an address, so it answers that the route is
// unavailable; the code reaches the address and nothing the client reads says
// whether it left.
func (r *routes) addEmail(c *call, who caller, raw string) {
	if r.deps.EmailSender == nil {
		logWarn(c.ctx, "an email identifier was asked for on a deployment with no email channel", nil)
		c.unavailable()
		return
	}
	email, ok := normalizeEmail(raw)
	if !ok {
		c.fail(mount.CodeBadRequest)
		return
	}
	key := emailKey(who.session)
	ttl := r.durations.get(c.ctx, keyEmailCodeTTL, defaultEmailCodeTTL, minEmailCodeTTL, maxEmailCodeTTL)
	resend := r.durations.get(c.ctx, keyEmailResendInterval, defaultEmailResendInterval, minEmailResendInterval, maxEmailResendInterval)

	// Another code within the interval is a 429, so a client cannot turn the
	// route into a flood of messages to one address. The window is held against
	// the address rather than the session: the claim is one atomic step, so a
	// second session — or a second account — naming the same address still meets
	// it. A store that cannot be claimed is a 503 before the code is drawn,
	// because a code that cannot be recorded is a message paid for and sent to no
	// use.
	now := r.deps.Clock.Now()
	resendKey := emailResendKey(email)
	mark, err := json.Marshal(emailResendMark{SentAt: now.Unix()})
	if err != nil {
		c.unavailable()
		return
	}
	claimed, err := r.deps.KV.SetIfAbsent(c.ctx, resendKey, mark, resend)
	if err != nil {
		logWarn(c.ctx, "the resend window of an email address cannot be claimed", err)
		c.unavailable()
		return
	}
	if !claimed {
		c.throttled(r.resendWait(c.ctx, resendKey, resend))
		return
	}

	code, err := emailCode()
	if err != nil {
		logWarn(c.ctx, "an email verification code could not be drawn", err)
		r.releaseResend(c.ctx, resendKey)
		c.unavailable()
		return
	}
	if err = r.deps.EmailSender.SendCode(c.ctx, email, code, ttl, smsLocale(c.ctx)); err != nil {
		// The message did not leave, so the address is not held to the interval:
		// the claim is given back and the caller may ask again at once.
		r.releaseResend(c.ctx, resendKey)
		r.fail(c, err)
		return
	}
	rec := emailRecord{
		Account: string(who.account), Email: email, CodeDigest: emailDigest(who.session, code),
		ExpiresAt: now.Add(ttl).Unix(),
	}
	value, err := json.Marshal(rec)
	if err != nil {
		c.unavailable()
		return
	}
	if err = r.deps.KV.Set(c.ctx, key, value, ttl); err != nil {
		logWarn(c.ctx, "the store of pending email verifications cannot be written", err)
		c.unavailable()
		return
	}
	c.json(http.StatusOK, addIdentifierResponse{
		State: identifierStatePending, ExpiresIn: int(seconds(ttl)), ResendAfter: int(seconds(resend)),
	})
}

// resendWait is the time left of an address's resend window, read back from the
// claim so a throttled caller is told how long to wait. A claim that cannot be
// read, or does not parse, reports the whole interval, which never promises the
// caller less than the truth.
func (r *routes) resendWait(ctx context.Context, resendKey string, resend time.Duration) time.Duration {
	raw, present, err := r.deps.KV.Get(ctx, resendKey)
	if err != nil || !present {
		return resend
	}
	var mark emailResendMark
	if json.Unmarshal(raw, &mark) != nil {
		return resend
	}
	if elapsed := r.deps.Clock.Now().Sub(time.Unix(mark.SentAt, 0)); elapsed > 0 && elapsed < resend {
		return resend - elapsed
	}
	return resend
}

// releaseResend gives back an address's resend claim when no code was sent, so a
// message that did not leave does not hold the address to the interval.
func (r *routes) releaseResend(ctx context.Context, resendKey string) {
	if err := r.deps.KV.Delete(ctx, resendKey); err != nil {
		logWarn(ctx, "a resend claim of an email address could not be released", err)
	}
}

// verifyIdentifier answers POST /identifiers/verify: it checks the code sent to
// the address and, on a match, writes the address as a verified identifier and
// publishes it through the account module. The record of the session is read
// first; a missing one, one that names another account, or one that does not
// parse is a flow-invalid, so nothing about another session's pending address
// leaks. A code that does not match counts an attempt, and the record is spent
// once the attempts are used up, so a six-digit code cannot be guessed over its
// lifetime.
func (r *routes) verifyIdentifier(ctx context.Context, in verifyIdentifierInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	who, ok := r.authorize(c, "")
	if !ok {
		return c.result()
	}
	if in.Code == "" || len(in.Code) > maxCodeLen {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	key := emailKey(who.session)
	raw, present, err := r.deps.KV.Get(c.ctx, key)
	if err != nil {
		logWarn(c.ctx, "the store of pending email verifications cannot be read", err)
		c.unavailable()
		return c.result()
	}
	var rec emailRecord
	if !present || json.Unmarshal(raw, &rec) != nil || rec.Account != string(who.account) ||
		rec.Email == "" || rec.CodeDigest == "" {
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}
	attemptKey := emailAttemptKey(rec.CodeDigest)
	if !emailCodeMatches(rec.CodeDigest, emailDigest(who.session, in.Code)) {
		r.countEmailAttempt(c.ctx, key, attemptKey, rec.ExpiresAt)
		c.unauthorized(mount.CodeLoginFailed)
		return c.result()
	}

	bindErr := r.deps.Admin.AddIdentifier(c.ctx, who.account, authn.IdentifierEmail, rec.Email,
		authn.Verification{Via: authn.VerifiedViaEmailCode})
	if err := r.deps.KV.Delete(c.ctx, key); err != nil {
		logWarn(c.ctx, "a consumed email verification could not be deleted", err)
	}
	if err := r.deps.KV.Delete(c.ctx, attemptKey); err != nil {
		logWarn(c.ctx, "the attempt count of a consumed email verification could not be deleted", err)
	}
	if bindErr != nil {
		r.fail(c, bindErr)
		return c.result()
	}
	if r.settle(c, who, authn.ReasonSelf) {
		c.json(http.StatusOK, addIdentifierResponse{State: identifierStateAdded})
	}
	return c.result()
}

// countEmailAttempt records one failed attempt against a pending verification,
// and spends the record once the attempts are used up. The count lives on a key
// of its own and is advanced by an atomic increment, so concurrent wrong codes
// each count: a count read out of the record, added to and written back would
// let them read the same value, each write back one more, and lose every count
// but one, and a lost count is a bound that no longer holds. The record is the
// thing spent; the count ages out with the code it belongs to. A store that
// cannot be written leaves the count as it was, so the next attempt starts from
// the same place.
func (r *routes) countEmailAttempt(ctx context.Context, key, attemptKey string, expiresAt int64) {
	ttl := time.Unix(expiresAt, 0).Sub(r.deps.Clock.Now())
	if ttl <= 0 {
		if err := r.deps.KV.Delete(ctx, key); err != nil {
			logWarn(ctx, "an expired email verification could not be deleted", err)
		}
		return
	}
	attempts, err := r.deps.KV.IncrByFloatWithTTL(ctx, attemptKey, 1, ttl)
	if err != nil {
		logWarn(ctx, "the attempt count of an email verification cannot be written", err)
		return
	}
	if attempts >= maxEmailAttempts {
		if err := r.deps.KV.Delete(ctx, key); err != nil {
			logWarn(ctx, "a spent email verification could not be deleted", err)
		}
		if err := r.deps.KV.Delete(ctx, attemptKey); err != nil {
			logWarn(ctx, "the spent attempt count of an email verification could not be deleted", err)
		}
	}
}
