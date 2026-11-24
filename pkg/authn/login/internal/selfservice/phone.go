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

const phoneKeyPrefix = "authn:phone:"

const maxCodeLen = 32

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

type phoneRecord struct {
	Account     string `json:"account"`
	Phone       string `json:"phone"`
	ChallengeID string `json:"challenge_id"`
}

func phoneKey(session authn.SessionID) string {
	sum := sha256.Sum256([]byte(session))
	return phoneKeyPrefix + hex.EncodeToString(sum[:])
}

func phoneThrottleKey(phone string) string {
	sum := sha256.Sum256([]byte(phone))
	return hex.EncodeToString(sum[:])
}

func smsLocale(ctx context.Context) string {
	locale, ok := i18n.FromContext(ctx)
	if !ok {
		return ""
	}
	return string(locale)
}

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
