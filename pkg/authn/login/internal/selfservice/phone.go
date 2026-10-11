package selfservice

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
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

// startPhoneRequest is the body of POST /phone.
type startPhoneRequest struct {
	Phone string `json:"phone"`
}

// startPhoneResponse is the answer of POST /phone: how long the code lives and
// how soon another may be asked for, in seconds. It carries neither the
// challenge nor anything that tells whether the number is already another
// account's.
type startPhoneResponse struct {
	ExpiresIn   int64 `json:"expires_in"`
	ResendAfter int64 `json:"resend_after"`
}

// startPhone answers POST /phone: it sends a code to the number and keeps what is
// needed to check it in the store, replacing the session's earlier record.
//
// The number is not looked up. A number another account holds is sent a code
// exactly as one nobody holds is, and the conflict surfaces only when the
// identifier is written after the code was verified, so the answer discloses
// nothing about who holds which number.
//
// A store that cannot be read is a 503 before the code is sent, because a code
// that cannot be verified is a message paid for and sent to no use.
func (r *routes) startPhone(c *gin.Context) {
	who, ok := r.authorize(c, "")
	if !ok {
		return
	}
	var req startPhoneRequest
	if !mount.Decode(c, &req) {
		return
	}
	if !e164.MatchString(req.Phone) {
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	ctx := c.Request.Context()
	key := phoneKey(who.session)
	if _, _, err := r.deps.KV.Get(ctx, key); err != nil {
		logWarn(ctx, "the store of pending phone verifications cannot be read", err)
		mount.Unavailable(c)
		return
	}
	codeTTL := r.durations.get(ctx, keyCodeTTL, defaultCodeTTL, minCodeTTL, maxCodeTTL)
	resend := r.durations.get(ctx, keyResendInterval, defaultResendInterval, minResendInterval, maxResendInterval)

	sentAt := r.deps.Clock.Now()
	challenge, err := r.deps.SMSCodes.Send(ctx, authn.SendCode{
		Purpose: authn.SMSPurposeEnroll,
		Phone:   req.Phone,
		Account: who.account,
		Deliver: true,
	})
	if err != nil {
		r.fail(c, err)
		return
	}
	// The record does not outlive the challenge row, whose lifetime began when
	// the code was written, a little before now.
	ttl := codeTTL - r.deps.Clock.Now().Sub(sentAt)
	value, err := json.Marshal(phoneRecord{Account: string(who.account), Phone: req.Phone, ChallengeID: challenge})
	if err != nil || ttl <= 0 {
		mount.Unavailable(c)
		return
	}
	if err := r.deps.KV.Set(ctx, key, value, ttl); err != nil {
		logWarn(ctx, "the store of pending phone verifications cannot be written", err)
		mount.Unavailable(c)
		return
	}
	c.JSON(http.StatusOK, startPhoneResponse{ExpiresIn: seconds(codeTTL), ResendAfter: seconds(resend)})
}

// verifyPhoneRequest is the body of POST /phone/verify.
type verifyPhoneRequest struct {
	Code string `json:"code"`
}

// verifyPhone answers POST /phone/verify. The order is the one the back-off
// needs: the record of the session is read, the number's back-off is checked,
// and only then is the code compared, so a number in back-off is not offered to
// a guess. A rejected code is counted against the number, an accepted one clears
// it, and an answer that could not be had (the store, the back-off, the
// verifier) is neither: it is a 503 and the attempt is not counted.
//
// An accepted code binds the number to the account as a verified identifier,
// where the account module replaces a phone the account already has and refuses
// a number another account holds. The record is deleted once the code has been
// accepted, whether or not the binding succeeded, because the challenge is
// consumed and the record cannot be used again.
func (r *routes) verifyPhone(c *gin.Context) {
	who, ok := r.authorize(c, "")
	if !ok {
		return
	}
	var req verifyPhoneRequest
	if !mount.Decode(c, &req) {
		return
	}
	if req.Code == "" || len(req.Code) > maxCodeLen {
		mount.Fail(c, mount.CodeBadRequest)
		return
	}
	ctx := c.Request.Context()
	key := phoneKey(who.session)
	raw, present, err := r.deps.KV.Get(ctx, key)
	if err != nil {
		logWarn(ctx, "the store of pending phone verifications cannot be read", err)
		mount.Unavailable(c)
		return
	}
	var rec phoneRecord
	if !present || json.Unmarshal(raw, &rec) != nil || rec.Account != string(who.account) ||
		rec.Phone == "" || rec.ChallengeID == "" {
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
		return
	}

	throttleKey := phoneThrottleKey(rec.Phone)
	wait, err := r.deps.Throttle.Check(ctx, authn.ScopePhone, throttleKey)
	if err != nil {
		logWarn(ctx, "the back-off of a phone number cannot be read", err)
		mount.Unavailable(c)
		return
	}
	if wait > 0 {
		mount.Throttled(c, wait)
		return
	}

	switch err := r.deps.SMSCodes.Verify(ctx, rec.ChallengeID, req.Code); {
	case err == nil:
	case errors.Is(err, authn.ErrInvalidCredentials):
		engaged, failErr := r.deps.Throttle.Fail(ctx, authn.ScopePhone, throttleKey)
		if failErr != nil {
			logWarn(ctx, "the failure of a phone code cannot be counted", failErr)
			mount.Unavailable(c)
			return
		}
		if engaged {
			r.engaged(ctx, authn.ScopePhone)
		}
		mount.Unauthorized(c, r.endpoint, mount.CodeLoginFailed)
		return
	default:
		r.fail(c, err)
		return
	}
	if err := r.deps.Throttle.Succeed(ctx, authn.ScopePhone, throttleKey); err != nil {
		logWarn(ctx, "the back-off of a phone number cannot be cleared", err)
	}

	bindErr := r.deps.Admin.AddIdentifier(ctx, who.account, authn.IdentifierPhone, rec.Phone,
		authn.Verification{Via: authn.VerifiedViaSMSCode})
	if err := r.deps.KV.Delete(ctx, key); err != nil {
		logWarn(ctx, "a consumed phone verification could not be deleted", err)
	}
	if bindErr != nil {
		r.fail(c, bindErr)
		return
	}
	if r.settle(c, who, authn.ReasonSelf) {
		c.Status(http.StatusNoContent)
	}
}
