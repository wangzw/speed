package selfservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
)

// phoneKeyPrefix starts the key of a pending phone verification. The key is the
// prefix and the SHA-256 of the session that started it, in hexadecimal, so a
// session holds at most one record at a time and cannot name another session's.
const phoneKeyPrefix = "authn:phone:"

// maxCodeLen is the longest verification code a request may carry. The code is a
// number of at most eight digits; anything longer is not one, and is refused
// before it is compared or counted as a failed attempt.
const maxCodeLen = 32

// e164 is the form a phone number is accepted in. The number is not parsed and
// not completed: the client writes it in international form.
var e164 = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// phoneRecord is what a pending verification keeps in the key-value store: the
// account it was started for, the number the code was sent to and the challenge
// holding the code. The client submits only the code, so it cannot choose which
// number is verified.
type phoneRecord struct {
	Account     string `json:"account"`
	Phone       string `json:"phone"`
	ChallengeID string `json:"challenge_id"`
}

// phoneKey is the key of a session's pending verification.
func phoneKey(session authn.SessionID) string {
	sum := sha256.Sum256([]byte(session))
	return phoneKeyPrefix + hex.EncodeToString(sum[:])
}

// phoneThrottleKey is the back-off key of a number: the SHA-256 of its E.164
// form, the same one authn.sms and the SMS login method use.
func phoneThrottleKey(phone string) string {
	sum := sha256.Sum256([]byte(phone))
	return hex.EncodeToString(sum[:])
}

// smsLocale is the language the request carries, passed to the SMS module as the
// message language and resolved there against the languages the carrier
// declares. It is the locale the i18n layer settled into the context, or the
// empty string when the request named none, which the SMS module reads as the
// run's configured default.
func smsLocale(ctx context.Context) string {
	locale, ok := i18n.FromContext(ctx)
	if !ok {
		return ""
	}
	return string(locale)
}

// startPhone answers POST /phone: it sends a code to the number and keeps what is
// needed to check it in the store, replacing the session's earlier record.
//
// Registering a number is a contact act, not a change of how the account signs
// in, so the route asks only for an identity and a session: no fresh
// authentication is asked for. The number is not looked up: a number another
// account holds is sent a code exactly as one nobody holds is, and the conflict
// surfaces only when the identifier is written after the code was verified, so
// the answer discloses nothing about who holds which number.
//
// A store that cannot be read is a 503 before the code is sent, because a code
// that cannot be verified is a message paid for and sent to no use.
func (r *routes) startPhone(ctx context.Context, in startPhoneInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	who, ok := r.identity(c)
	if !ok {
		return c.result()
	}
	if !e164.MatchString(in.Phone) {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	key := phoneKey(who.session)
	if _, _, err := r.deps.KV.Get(c.ctx, key); err != nil {
		logWarn(c.ctx, "the store of pending phone verifications cannot be read", err)
		c.unavailable()
		return c.result()
	}
	codeTTL := r.durations.get(c.ctx, keyCodeTTL, defaultCodeTTL, minCodeTTL, maxCodeTTL)
	resend := r.durations.get(c.ctx, keyResendInterval, defaultResendInterval, minResendInterval, maxResendInterval)

	sentAt := r.deps.Clock.Now()
	challenge, err := r.deps.SMSCodes.Send(c.ctx, authn.SendCode{
		Purpose: authn.SMSPurposeEnroll,
		Phone:   in.Phone,
		Account: who.account,
		Locale:  smsLocale(c.ctx),
		Deliver: true,
		Source:  c.clientIP,
		Proof:   in.HumanProof,
	})
	if err != nil {
		r.fail(c, err)
		return c.result()
	}
	// The record does not outlive the challenge row, whose lifetime began when
	// the code was written, a little before now.
	ttl := codeTTL - r.deps.Clock.Now().Sub(sentAt)
	value, err := json.Marshal(phoneRecord{Account: string(who.account), Phone: in.Phone, ChallengeID: challenge})
	if err != nil || ttl <= 0 {
		c.unavailable()
		return c.result()
	}
	if err := r.deps.KV.Set(c.ctx, key, value, ttl); err != nil {
		logWarn(c.ctx, "the store of pending phone verifications cannot be written", err)
		c.unavailable()
		return c.result()
	}
	c.json(http.StatusOK, startPhoneResponse{ExpiresIn: int(seconds(codeTTL)), ResendAfter: int(seconds(resend))})
	return c.result()
}

// verifyPhone answers POST /phone/verify. The order is the one the back-off
// needs: the record of the session is read, the number's back-off is checked,
// and only then is the code compared, so a number in back-off is not offered to
// a guess. The freshness a protected consequence calls for is decided before the
// code is compared as well, because a verification consumes its single-use
// challenge on a pass: a step-up refusal after the comparison would spend the
// code and leave the replay the client makes after stepping up with nothing. A
// rejected code is counted against the number, an accepted one clears it, and an
// answer that could not be had (the store, the back-off, the verifier) is
// neither: it is a 503 and the attempt is not counted.
//
// An accepted code binds the number to the account as a verified identifier,
// where the account module replaces a phone the account already has and refuses
// a number another account holds. The record is deleted once the code has been
// accepted, whether or not the binding succeeded, because the challenge is
// consumed and the record cannot be used again.
//
// Proving the number is not making it a way to sign in: the write asks for no
// freshness and settles nothing, neither rotating the session nor revoking the
// account's other sessions.
func (r *routes) verifyPhone(ctx context.Context, in verifyPhoneInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	who, ok := r.identity(c)
	if !ok {
		return c.result()
	}
	if in.Code == "" || len(in.Code) > maxCodeLen {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	key := phoneKey(who.session)
	raw, present, err := r.deps.KV.Get(c.ctx, key)
	if err != nil {
		logWarn(c.ctx, "the store of pending phone verifications cannot be read", err)
		c.unavailable()
		return c.result()
	}
	var rec phoneRecord
	if !present || json.Unmarshal(raw, &rec) != nil || rec.Account != string(who.account) ||
		rec.Phone == "" || rec.ChallengeID == "" {
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}

	throttleKey := phoneThrottleKey(rec.Phone)
	wait, err := r.deps.Throttle.Check(c.ctx, authn.ScopePhone, throttleKey)
	if err != nil {
		logWarn(c.ctx, "the back-off of a phone number cannot be read", err)
		c.unavailable()
		return c.result()
	}
	if wait > 0 {
		c.throttled(wait)
		return c.result()
	}

	// Proving a number asks for no freshness by itself; replacing the number an
	// active SMS factor sends its codes to does, because that would move the
	// factor's target. The check runs before the code is compared, so a step-up
	// refusal leaves the single-use challenge unspent and the code replayable.
	needsFresh, proofErr := r.contactProofNeedsFreshness(c.ctx, who.account, authn.IdentifierPhone, rec.Phone)
	if proofErr != nil {
		r.fail(c, proofErr)
		return c.result()
	}
	if needsFresh {
		id, found := authn.FromContext(c.ctx)
		if !found {
			c.unauthorized("")
			return c.result()
		}
		if !r.fresh(c, id, "") {
			return c.result()
		}
	}

	switch err := r.deps.SMSCodes.Verify(c.ctx, rec.ChallengeID, in.Code); {
	case err == nil:
	case errors.Is(err, authn.ErrInvalidCredentials):
		engaged, failErr := r.deps.Throttle.Fail(c.ctx, authn.ScopePhone, throttleKey)
		if failErr != nil {
			logWarn(c.ctx, "the failure of a phone code cannot be counted", failErr)
			c.unavailable()
			return c.result()
		}
		if engaged {
			r.engaged(c.ctx, authn.ScopePhone)
		}
		c.unauthorized(mount.CodeLoginFailed)
		return c.result()
	default:
		r.fail(c, err)
		return c.result()
	}
	if err := r.deps.Throttle.Succeed(c.ctx, authn.ScopePhone, throttleKey); err != nil {
		logWarn(c.ctx, "the back-off of a phone number cannot be cleared", err)
	}

	bindErr := r.deps.Admin.AddIdentifier(c.ctx, who.account, authn.IdentifierPhone, rec.Phone,
		authn.Verification{Via: authn.VerifiedViaSMSCode})
	if err := r.deps.KV.Delete(c.ctx, key); err != nil {
		logWarn(c.ctx, "a consumed phone verification could not be deleted", err)
	}
	if bindErr != nil {
		r.fail(c, bindErr)
		return c.result()
	}
	c.empty(http.StatusNoContent)
	return c.result()
}
