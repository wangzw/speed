package login

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/budgetalias"
	"github.com/vislake/speed/pkg/authn/internal/cookiename"
	"github.com/vislake/speed/pkg/authn/internal/identity"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

const (
	resetKeyPrefix = "authn:reset:"

	resetRecordVersion = 1

	resetStateSent       = "sent"
	resetStateVerified   = "verified"
	resetStateProcessing = "processing"
	resetStateFailed     = "failed"

	resetChannelEmail = "email"
	resetChannelSMS   = "sms"

	resetCodeDigits    = 6
	maxResetAttempts   = 5
	maxResetCodeLength = 32

	resetMailWorkers = 4

	actionPasswordReset = "authn.password.reset"
	outcomeAttempted    = "attempted"
	resetOutcomeDenied  = "denied"
)

var (
	resetE164  = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
	resetEmail = regexp.MustCompile(`^[^@\s]+@[^@\s]+$`)
)

type resetRecord struct {
	Version          int       `json:"v"`
	Channel          string    `json:"channel"`
	State            string    `json:"state"`
	Binding          string    `json:"binding"`
	IdentifierDigest string    `json:"identifier_digest"`
	Account          string    `json:"account,omitempty"`
	IdentifierID     string    `json:"identifier_id,omitempty"`
	Suppressed       bool      `json:"suppressed,omitempty"`
	ChallengeID      string    `json:"challenge_id,omitempty"`
	CodeDigest       string    `json:"code_digest,omitempty"`
	Attempts         int       `json:"attempts"`
	Sends            int       `json:"sends"`
	SentAt           int64     `json:"sent_at"`
	CodeExpiresAt    time.Time `json:"code_expires_at"`
	ExpiresAt        time.Time `json:"expires_at"`
	Locale           string    `json:"locale,omitempty"`
}

type resetMailer struct {
	slots chan struct{}
}

func newResetMailer(workers int) *resetMailer {
	if workers < 1 {
		workers = 1
	}
	return &resetMailer{slots: make(chan struct{}, workers)}
}

func (m *resetMailer) reserve() bool {
	if m == nil {
		return true
	}
	select {
	case m.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (m *resetMailer) release() {
	if m == nil {
		return
	}
	<-m.slots
}

func (r *routes) setResetCookie(c *call, token string, maxAge int) {
	c.setCookie(cookiename.Cookie(r.publicURL, cookiename.Spec{
		Stem:     cookiename.Reset,
		Value:    token,
		MaxAge:   maxAge,
		SameSite: http.SameSiteStrictMode,
	}))
}

func resetKey(token string) string { return resetKeyPrefix + tokenDigest(token) }

func decodeResetRecord(raw []byte) (resetRecord, error) {
	var rec resetRecord
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rec); err != nil {
		return resetRecord{}, err
	}
	if rec.Version != resetRecordVersion {
		return resetRecord{}, fmt.Errorf("unknown reset record version %d", rec.Version)
	}
	return rec, nil
}

// A record whose binding does not match or cannot be decoded is an invalid flow, not an outage.
func (r *routes) loadResetRecord(ctx context.Context, token string) (resetRecord, []byte, bool, error) {
	if token == "" {
		return resetRecord{}, nil, false, nil
	}
	raw, present, err := r.kv.Get(ctx, resetKey(token))
	if err != nil {
		return resetRecord{}, nil, false, fmt.Errorf("%s: reading a reset record: %w", ModuleName, err)
	}
	if !present {
		return resetRecord{}, nil, false, nil
	}
	rec, err := decodeResetRecord(raw)
	if err != nil {
		return resetRecord{}, nil, false, errFlowInvalid("the reset record cannot be read")
	}
	if !bindingMatches(token, rec.Binding) {
		return resetRecord{}, nil, false, errFlowInvalid("the reset binding does not match")
	}
	return rec, raw, true, nil
}

func (r *routes) storeResetRecord(ctx context.Context, token string, rec resetRecord, ttl time.Duration) ([]byte, error) {
	raw, err := json.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("%s: encoding a reset record: %w", ModuleName, err)
	}
	if err := r.kv.Set(ctx, resetKey(token), raw, ttl); err != nil {
		return nil, fmt.Errorf("%s: writing a reset record: %w", ModuleName, err)
	}
	return raw, nil
}

// A swap does not extend the record's lifetime; its deadline is fixed when the record is created.
func (r *routes) swapResetRecord(ctx context.Context, token string, old []byte, rec resetRecord) ([]byte, bool, error) {
	raw, err := json.Marshal(rec)
	if err != nil {
		return nil, false, fmt.Errorf("%s: encoding a reset record: %w", ModuleName, err)
	}
	swapped, err := r.kv.CompareAndSwap(ctx, resetKey(token), old, raw)
	if err != nil {
		return nil, false, fmt.Errorf("%s: swapping a reset record: %w", ModuleName, err)
	}
	return raw, swapped, nil
}

func (r *routes) deleteResetRecord(ctx context.Context, token string) error {
	if err := r.kv.Delete(ctx, resetKey(token)); err != nil {
		return fmt.Errorf("%s: removing a reset record: %w", ModuleName, err)
	}
	return nil
}

func normalizeResetIdentifier(channel, raw string) (string, bool) {
	switch channel {
	case resetChannelEmail:
		v := strings.ToLower(strings.TrimSpace(raw))
		if v == "" || len(v) > 254 || !resetEmail.MatchString(v) {
			return "", false
		}
		for _, r := range v {
			if r > 127 {
				return "", false
			}
		}
		return v, true
	case resetChannelSMS:
		if !resetE164.MatchString(raw) {
			return "", false
		}
		return raw, true
	}
	return "", false
}

func resetIdentifierDigest(token, channel, normalized string) string {
	sum := sha256.Sum256([]byte(tokenDigest(token) + "\x00" + channel + "\x00" + normalized))
	return hex.EncodeToString(sum[:])
}

func resetCodeDigest(token, code string) string {
	sum := sha256.Sum256([]byte(tokenDigest(token) + "\x00" + code))
	return hex.EncodeToString(sum[:])
}

func resetDigestMatches(want, got string) bool {
	w, err1 := hex.DecodeString(want)
	g, err2 := hex.DecodeString(got)
	if err1 != nil || err2 != nil || len(w) != len(g) || len(w) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare(w, g) == 1
}

func resetCode() (string, error) {
	bound := new(big.Int).Exp(big.NewInt(10), big.NewInt(resetCodeDigits), nil)
	n, err := rand.Int(rand.Reader, bound)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", resetCodeDigits, n.Int64()), nil
}

// resetDecoy is a local random digest, so a suppressed record never verifies and never calls the code verifier.
func resetDecoy() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (r *routes) resetEligible(ctx context.Context, channel, normalized string) (bool, authn.AccountID, string, error) {
	kind := authn.IdentifierEmail
	wantVia := authn.VerifiedViaEmailCode
	if channel == resetChannelSMS {
		kind = authn.IdentifierPhone
		wantVia = authn.VerifiedViaSMSCode
	}
	account, err := r.accounts.Lookup(ctx, kind, normalized)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound), errors.Is(err, authn.ErrInvalidIdentifier):
		return false, "", "", nil
	case err != nil:
		return false, "", "", err
	}
	acct, err := r.accounts.Get(ctx, account)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return false, "", "", nil
	case err != nil:
		return false, "", "", err
	}
	if acct.Kind != authn.KindUser || acct.Status != authn.StatusActive || !acct.PasswordSet {
		return false, "", "", nil
	}
	held, err := r.accounts.Identifiers(ctx, account)
	if err != nil {
		return false, "", "", err
	}
	for _, ident := range held {
		if ident.Kind == kind && ident.Value == normalized && ident.VerifiedVia == wantVia {
			return true, account, ident.ID, nil
		}
	}
	return false, "", "", nil
}

func (r *routes) resetRecordEligible(ctx context.Context, rec resetRecord) (bool, error) {
	account := authn.AccountID(rec.Account)
	if account == "" || rec.IdentifierID == "" {
		return false, nil
	}
	acct, err := r.accounts.Get(ctx, account)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return false, nil
	case err != nil:
		return false, err
	}
	if acct.Kind != authn.KindUser || acct.Status != authn.StatusActive || !acct.PasswordSet {
		return false, nil
	}
	held, err := r.accounts.Identifiers(ctx, account)
	if err != nil {
		return false, err
	}
	kind := authn.IdentifierEmail
	wantVia := authn.VerifiedViaEmailCode
	if rec.Channel == resetChannelSMS {
		kind = authn.IdentifierPhone
		wantVia = authn.VerifiedViaSMSCode
	}
	for _, ident := range held {
		if ident.ID == rec.IdentifierID && ident.Kind == kind && ident.VerifiedVia == wantVia {
			return true, nil
		}
	}
	return false, nil
}

func (r *routes) resetOpen(c *call) bool {
	if r.settings.passwordReset() != passwordResetOpen {
		c.forbidden(mount.CodePasswordResetClosed)
		return false
	}
	if r.audit == nil {
		log.FromContext(c.ctx).Warn(ModuleName + ": the anonymous password reset is not published because the " +
			"audit capability is absent, so the request is refused")
		c.unavailable()
		return false
	}
	return true
}

func (r *routes) resetChannelReady(c *call, channel string) bool {
	switch channel {
	case resetChannelEmail:
		if r.emailReset == nil {
			c.unavailable()
			return false
		}
	case resetChannelSMS:
		if r.smsCodes == nil {
			c.unavailable()
			return false
		}
		disabled, err := r.settings.disabledMethods()
		if err != nil {
			r.fail(c, err)
			return false
		}
		if slicesContains(disabled, resetChannelSMS) {
			c.forbidden(mount.CodePasswordResetClosed)
			return false
		}
		if support, ok := r.smsCodes.(authn.PurposeSupport); ok && !support.SupportsPurpose(authn.SMSPurposeReset) {
			c.unavailable()
			return false
		}
	default:
		c.fail(mount.CodeBadRequest)
		return false
	}
	return true
}

func (r *routes) resetChannelAvailable(channel string, disabled []string) bool {
	switch channel {
	case resetChannelEmail:
		return r.emailReset != nil
	case resetChannelSMS:
		if r.smsCodes == nil || slicesContains(disabled, resetChannelSMS) {
			return false
		}
		if support, ok := r.smsCodes.(authn.PurposeSupport); ok && !support.SupportsPurpose(authn.SMSPurposeReset) {
			return false
		}
		return true
	}
	return false
}

func (r *routes) passwordResetCapability(disabled []string) passwordResetView {
	out := passwordResetView{Methods: []string{}}
	for _, channel := range []string{resetChannelEmail, resetChannelSMS} {
		if r.resetChannelAvailable(channel, disabled) {
			out.Methods = append(out.Methods, channel)
		}
	}
	out.Enabled = r.settings.passwordReset() == passwordResetOpen && r.audit != nil && len(out.Methods) > 0
	return out
}

func slicesContains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func (r *routes) resetDurations(channel string) (codeTTL, resend time.Duration) {
	if channel == resetChannelSMS {
		return r.settings.smsCodeTTL(), r.settings.smsResend()
	}
	return r.settings.emailCodeTTL(), r.settings.emailResend()
}

func resetAnswer(c *call, status int, state string, expiresIn, resendAfter int) {
	c.json(status, passwordResetState{State: state, ExpiresIn: expiresIn, ResendAfter: resendAfter})
}

func resetSeconds(d time.Duration) int {
	return int((d + time.Second - 1) / time.Second)
}

func (r *routes) passwordResetStart(ctx context.Context, in passwordResetStartInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	channel := in.Channel
	if channel != resetChannelEmail && channel != resetChannelSMS {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	if !r.resetOpen(c) {
		return c.result()
	}
	if !r.resetChannelReady(c, channel) {
		return c.result()
	}
	normalized, ok := normalizeResetIdentifier(channel, in.Identifier)
	if !ok {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	if channel == resetChannelEmail && !r.checkHumanProof(c, in.HumanProof) {
		return c.result()
	}
	if !r.admitSends(c) {
		r.meters.resetOperation(c.ctx, resetOpSend, channel, outcomeThrottled)
		return c.result()
	}

	now := r.clock.Now()
	codeTTL, resend := r.resetDurations(channel)
	ticketTTL := r.settings.passwordResetTicketTTL()

	// A live record on the presented cookie decides resend versus fresh flow; a record being
	// completed is refused, so a start cannot race a password write on the same binding.
	token := ""
	oldToken := ""
	var oldRaw []byte
	var old *resetRecord
	if presented := c.cookieValue(r.resetCookie); presented != "" {
		rec, raw, present, err := r.loadResetRecord(c.ctx, presented)
		if err != nil {
			r.fail(c, err)
			return c.result()
		}
		if present {
			if rec.State == resetStateProcessing {
				c.unauthorized(mount.CodeFlowInvalid)
				return c.result()
			}
			token, oldToken, oldRaw, old = presented, presented, raw, &rec
		}
	}

	resendFlow := false
	if old != nil && token != "" {
		digest := resetIdentifierDigest(token, channel, normalized)
		if old.State == resetStateSent && old.Channel == channel &&
			resetDigestMatches(old.IdentifierDigest, digest) {
			elapsed := now.Sub(time.Unix(old.SentAt, 0))
			if elapsed < resend {
				r.meters.resetOperation(c.ctx, resetOpSend, channel, outcomeThrottled)
				c.throttled(resend - elapsed)
				return c.result()
			}
			// A code whose ticket cannot fit before the record's fixed deadline is not sent; the flow
			// restarts with a fresh binding.
			if !now.Add(codeTTL + ticketTTL).After(old.ExpiresAt) {
				resendFlow = true
			}
		}
	}
	if !resendFlow {
		fresh, err := newFlowToken()
		if err != nil {
			r.fail(c, err)
			return c.result()
		}
		token = fresh
	}
	digest := resetIdentifierDigest(token, channel, normalized)

	eligible, account, identifierID, err := r.resetEligible(c.ctx, channel, normalized)
	if err != nil {
		r.fail(c, err)
		return c.result()
	}

	deadline := now.Add(codeTTL + ticketTTL)
	if resendFlow {
		deadline = old.ExpiresAt
	}

	var (
		suppressed  = !eligible
		challengeID string
		codeDigest  string
		sends       int
	)
	if resendFlow {
		sends = old.Sends
	}
	var (
		emailSlot    bool
		emailReady   bool
		emailDeliver bool
		emailTo      string
		emailCode    string
		emailCodeTTL time.Duration
		emailLocale  string
	)
	defer func() {
		if !emailSlot {
			return
		}
		if emailDeliver {
			r.dispatchResetEmail(c.ctx, emailTo, emailCode, emailCodeTTL, emailLocale)
			return
		}
		r.resetMail.release()
	}()

	switch channel {
	case resetChannelSMS:
		if !suppressed && sends >= r.settings.perFlowSends() {
			suppressed = true
		}
		deliver := !suppressed
		sentID, sendErr := r.smsCodes.Send(c.ctx, authn.SendCode{
			Purpose: authn.SMSPurposeReset,
			Phone:   normalized,
			Locale:  localeOf(c),
			Deliver: deliver,
			Source:  c.clientIP,
			Proof:   in.HumanProof,
		})
		switch {
		case sendErr == nil:
			challengeID = sentID
		case errors.Is(sendErr, authn.ErrThrottled):
			suppressed, deliver = true, false
		case errors.Is(sendErr, authn.ErrUnsupportedRegion):
			r.meters.resetOperation(c.ctx, resetOpSend, channel, outcomeFailed)
			c.fail(mount.CodeUnsupportedRegion)
			return c.result()
		default:
			r.meters.resetOperation(c.ctx, resetOpSend, channel, outcomeUnavailable)
			r.fail(c, sendErr)
			return c.result()
		}
		if suppressed {
			decoy, derr := resetDecoy()
			if derr != nil {
				r.fail(c, derr)
				return c.result()
			}
			codeDigest, challengeID = decoy, ""
		}
		if deliver {
			sends++
			if !r.chargeSends(c, 1) {
				r.meters.resetOperation(c.ctx, resetOpSend, channel, outcomeThrottled)
				return c.result()
			}
		}

	case resetChannelEmail:
		if !r.resetMail.reserve() {
			r.meters.resetOperation(c.ctx, resetOpSend, channel, outcomeUnavailable)
			c.unavailable()
			return c.result()
		}
		emailSlot = true
		if !suppressed {
			resendKey := resetEmailResendKey(normalized)
			mark, merr := json.Marshal(struct {
				SentAt int64 `json:"sent_at"`
			}{SentAt: now.Unix()})
			if merr != nil {
				r.meters.resetOperation(c.ctx, resetOpSend, channel, outcomeUnavailable)
				c.unavailable()
				return c.result()
			}
			claimed, cerr := r.kv.SetIfAbsent(c.ctx, resendKey, mark, resend)
			if cerr != nil {
				r.meters.resetOperation(c.ctx, resetOpSend, channel, outcomeUnavailable)
				r.fail(c, fmt.Errorf("%w: %s: %w", authn.ErrUnavailable, ModuleName, cerr))
				return c.result()
			}
			if !claimed {
				suppressed = true
			} else {
				_, state := r.reserveResetEmailBudgets(c, now, normalized, account, c.clientIP)
				switch state {
				case resetBudgetOver:
					r.releaseResetEmailWindow(c.ctx, resendKey)
					suppressed = true
				case resetBudgetFailed:
					r.releaseResetEmailWindow(c.ctx, resendKey)
					r.meters.resetOperation(c.ctx, resetOpSend, channel, outcomeUnavailable)
					return c.result()
				}
			}
		}
		if suppressed && !eligible {
			if _, state := r.reserveResetEmailBudgets(c, now, normalized, "", c.clientIP); state == resetBudgetFailed {
				r.meters.resetOperation(c.ctx, resetOpSend, channel, outcomeUnavailable)
				return c.result()
			}
		}
		if suppressed {
			decoy, derr := resetDecoy()
			if derr != nil {
				r.fail(c, derr)
				return c.result()
			}
			codeDigest = decoy
			break
		}
		code, cerr := resetCode()
		if cerr != nil {
			r.meters.resetOperation(c.ctx, resetOpSend, channel, outcomeUnavailable)
			r.fail(c, cerr)
			return c.result()
		}
		codeDigest = resetCodeDigest(token, code)
		sends++
		emailReady = true
		emailTo, emailCode, emailCodeTTL, emailLocale = normalized, code, codeTTL, localeOf(c)
	}

	rec := resetRecord{
		Version:          resetRecordVersion,
		Channel:          channel,
		State:            resetStateSent,
		Binding:          tokenDigest(token),
		IdentifierDigest: digest,
		Suppressed:       suppressed,
		ChallengeID:      challengeID,
		CodeDigest:       codeDigest,
		Sends:            sends,
		SentAt:           now.Unix(),
		CodeExpiresAt:    now.Add(codeTTL),
		ExpiresAt:        deadline,
		Locale:           localeOf(c),
	}
	if eligible {
		rec.Account = string(account)
		rec.IdentifierID = identifierID
	}

	if resendFlow {
		if _, swapped, err := r.swapResetRecord(c.ctx, token, oldRaw, rec); err != nil || !swapped {
			if err != nil {
				r.meters.resetOperation(c.ctx, resetOpSend, channel, outcomeUnavailable)
				r.fail(c, err)
				return c.result()
			}
			c.unauthorized(mount.CodeFlowInvalid)
			return c.result()
		}
	} else {
		if _, err := r.storeResetRecord(c.ctx, token, rec, deadline.Sub(now)); err != nil {
			r.meters.resetOperation(c.ctx, resetOpSend, channel, outcomeUnavailable)
			r.fail(c, err)
			return c.result()
		}
		if old != nil && oldToken != token {
			if err := r.deleteResetRecord(c.ctx, oldToken); err != nil {
				log.FromContext(c.ctx).Warn(ModuleName+": a replaced reset record could not be removed",
					"cause", err.Error())
			}
		}
	}

	// The message is dispatched only after its record is durable; an early return releases the slot instead.
	emailDeliver = emailReady

	r.setResetCookie(c, token, resetSeconds(deadline.Sub(now)))
	outcome := resetSent
	if suppressed {
		outcome = resetSuppressed
	}
	r.meters.resetOperation(c.ctx, resetOpSend, channel, outcome)
	resetAnswer(c, http.StatusAccepted, resetStateSent, resetSeconds(codeTTL), resetSeconds(resend))
	return c.result()
}

func (r *routes) passwordResetCurrent(ctx context.Context, _ struct{}, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	if !r.resetOpen(c) {
		return c.result()
	}
	token := c.cookieValue(r.resetCookie)
	rec, _, present, err := r.loadResetRecord(c.ctx, token)
	if err != nil {
		r.fail(c, err)
		return c.result()
	}
	if !present {
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}
	if !r.resetChannelReady(c, rec.Channel) {
		return c.result()
	}
	codeTTL, resend := r.resetDurations(rec.Channel)
	switch rec.State {
	case resetStateSent:
		resetAnswer(c, http.StatusOK, resetStateSent, resetSeconds(codeTTL), resetSeconds(resend))
	case resetStateVerified:
		resetAnswer(c, http.StatusOK, resetStateVerified, resetSeconds(r.settings.passwordResetTicketTTL()), 0)
	default:
		c.unauthorized(mount.CodeFlowInvalid)
	}
	return c.result()
}

func (r *routes) passwordResetDelete(ctx context.Context, _ struct{}, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	token := c.cookieValue(r.resetCookie)
	if token != "" {
		if err := r.deleteResetRecord(c.ctx, token); err != nil {
			r.fail(c, err)
			return c.result()
		}
	}
	r.setResetCookie(c, "", -1)
	c.empty(http.StatusNoContent)
	return c.result()
}

func (r *routes) passwordResetVerify(ctx context.Context, in passwordResetVerifyInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	if !r.resetOpen(c) {
		return c.result()
	}
	token := c.cookieValue(r.resetCookie)
	rec, raw, present, err := r.loadResetRecord(c.ctx, token)
	if err != nil {
		r.fail(c, err)
		return c.result()
	}
	if !present {
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}
	if !r.resetChannelReady(c, rec.Channel) {
		return c.result()
	}
	if rec.State == resetStateFailed {
		r.meters.resetOperation(c.ctx, resetOpVerify, rec.Channel, outcomeFailed)
		c.unauthorized(mount.CodeLoginFailed)
		return c.result()
	}
	if rec.State == resetStateVerified {
		// A verified record answers the same code again as success, so a lost reply does not invalidate the reset.
		if in.Code == "" || len(in.Code) > maxResetCodeLength {
			c.fail(mount.CodeBadRequest)
			return c.result()
		}
		if !resetDigestMatches(rec.CodeDigest, resetCodeDigest(token, in.Code)) {
			r.meters.resetOperation(c.ctx, resetOpVerify, rec.Channel, outcomeFailed)
			c.unauthorized(mount.CodeLoginFailed)
			return c.result()
		}
		ticketTTL := r.settings.passwordResetTicketTTL()
		r.setResetCookie(c, token, resetSeconds(ticketTTL))
		r.meters.resetOperation(c.ctx, resetOpVerify, rec.Channel, resetVerified)
		resetAnswer(c, http.StatusOK, resetStateVerified, resetSeconds(ticketTTL), 0)
		return c.result()
	}
	if rec.State != resetStateSent {
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}
	if in.Code == "" || len(in.Code) > maxResetCodeLength {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	now := r.clock.Now()
	if now.After(rec.CodeExpiresAt) {
		r.meters.resetOperation(c.ctx, resetOpVerify, rec.Channel, outcomeFailed)
		c.unauthorized(mount.CodeLoginFailed)
		return c.result()
	}

	matched := false
	switch {
	case rec.Suppressed:
		matched = resetDigestMatches(rec.CodeDigest, resetCodeDigest(token, in.Code))
	case rec.Channel == resetChannelSMS:
		verr := r.smsCodes.Verify(c.ctx, rec.ChallengeID, in.Code)
		switch {
		case verr == nil:
			matched = true
		case errors.Is(verr, authn.ErrInvalidCredentials):
		case errors.Is(verr, authn.ErrThrottled), errors.Is(verr, authn.ErrUnavailable),
			errors.Is(verr, authn.ErrBusy):
			r.meters.resetOperation(c.ctx, resetOpVerify, rec.Channel, outcomeUnavailable)
			c.unavailable()
			return c.result()
		default:
			r.meters.resetOperation(c.ctx, resetOpVerify, rec.Channel, outcomeUnavailable)
			r.fail(c, verr)
			return c.result()
		}
	case rec.Channel == resetChannelEmail:
		matched = resetDigestMatches(rec.CodeDigest, resetCodeDigest(token, in.Code))
	}

	if matched {
		rec.State = resetStateVerified
		rec.Attempts = 0
		rec.CodeDigest = resetCodeDigest(token, in.Code)
		if _, swapped, err := r.swapResetRecord(c.ctx, token, raw, rec); err != nil || !swapped {
			if err != nil {
				r.meters.resetOperation(c.ctx, resetOpVerify, rec.Channel, outcomeUnavailable)
				r.fail(c, err)
				return c.result()
			}
			c.unauthorized(mount.CodeFlowInvalid)
			return c.result()
		}
		ticketTTL := r.settings.passwordResetTicketTTL()
		r.setResetCookie(c, token, resetSeconds(ticketTTL))
		r.meters.resetOperation(c.ctx, resetOpVerify, rec.Channel, resetVerified)
		resetAnswer(c, http.StatusOK, resetStateVerified, resetSeconds(ticketTTL), 0)
		return c.result()
	}

	rec.Attempts++
	if rec.Attempts >= maxResetAttempts {
		rec.State = resetStateFailed
	}
	if _, swapped, err := r.swapResetRecord(c.ctx, token, raw, rec); err != nil || !swapped {
		if err != nil {
			r.meters.resetOperation(c.ctx, resetOpVerify, rec.Channel, outcomeUnavailable)
			r.fail(c, err)
			return c.result()
		}
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}
	r.meters.resetOperation(c.ctx, resetOpVerify, rec.Channel, outcomeFailed)
	c.unauthorized(mount.CodeLoginFailed)
	return c.result()
}

// The record moves to processing before the mandatory audit row is written, and the password is written only after the attempted row is durable.
func (r *routes) passwordResetComplete(ctx context.Context, in passwordResetCompleteInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	if !r.resetOpen(c) {
		return c.result()
	}
	token := c.cookieValue(r.resetCookie)
	rec, raw, present, err := r.loadResetRecord(c.ctx, token)
	if err != nil {
		r.fail(c, err)
		return c.result()
	}
	if !present {
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}
	if !r.resetChannelReady(c, rec.Channel) {
		return c.result()
	}
	if rec.State != resetStateVerified {
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}
	if strings.TrimSpace(in.New) == "" {
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	account := authn.AccountID(rec.Account)
	eligible, err := r.resetRecordEligible(c.ctx, rec)
	if err != nil {
		r.fail(c, err)
		return c.result()
	}
	if !eligible {
		r.meters.resetOperation(c.ctx, resetOpComplete, rec.Channel, outcomeFailed)
		c.forbidden(mount.CodePasswordResetUnavailable)
		return c.result()
	}

	proc := rec
	proc.State = resetStateProcessing
	procRaw, swapped, err := r.swapResetRecord(c.ctx, token, raw, proc)
	if err != nil {
		r.meters.resetOperation(c.ctx, resetOpComplete, rec.Channel, outcomeUnavailable)
		r.fail(c, err)
		return c.result()
	}
	if !swapped {
		c.unauthorized(mount.CodeFlowInvalid)
		return c.result()
	}

	actionID := event.NewID()
	if !r.resetAudit(c, account, rec.Channel, outcomeAttempted, actionID) {
		r.revertReset(c, token, procRaw, rec)
		r.meters.resetOperation(c.ctx, resetOpComplete, rec.Channel, outcomeUnavailable)
		c.unavailable()
		return c.result()
	}

	setErr := r.passwords.Set(c.ctx, account, in.New, authn.SetPasswordOptions{
		By:                "reset",
		Channel:           rec.Channel,
		MustChange:        false,
		RequireActive:     true,
		RequireExisting:   true,
		RequireIdentifier: rec.IdentifierID,
		AdvanceValidAfter: true,
	})
	if setErr != nil {
		r.revertReset(c, token, procRaw, rec)
		switch {
		case errors.Is(setErr, authn.ErrAccountUnavailable), errors.Is(setErr, authn.ErrAccountNotFound),
			errors.Is(setErr, authn.ErrIdentifierRequired):
			r.resetAudit(c, account, rec.Channel, resetOutcomeDenied, actionID)
			r.meters.resetOperation(c.ctx, resetOpComplete, rec.Channel, outcomeFailed)
			c.forbidden(mount.CodePasswordResetUnavailable)
		case errors.Is(setErr, authn.ErrPasswordPolicy), errors.Is(setErr, authn.ErrInvalidIdentifier),
			errors.Is(setErr, authn.ErrInvalidArgument):
			r.resetAudit(c, account, rec.Channel, resetOutcomeDenied, actionID)
			r.meters.resetOperation(c.ctx, resetOpComplete, rec.Channel, outcomeFailed)
			c.fail(mount.CodeBadRequest)
		default:
			r.meters.resetOperation(c.ctx, resetOpComplete, rec.Channel, outcomeUnavailable)
			log.FromContext(c.ctx).Warn(ModuleName+": a password write could not be made",
				"cause", setErr.Error())
			if errors.Is(setErr, authn.ErrBusy) {
				c.busy()
			} else {
				c.unavailable()
			}
		}
		return c.result()
	}

	r.resetAudit(c, account, rec.Channel, outcomeSuccess, actionID)
	if _, revErr := r.sessions.RevokeAccount(c.ctx, account, "", authn.ReasonPasswordChange); revErr != nil {
		log.FromContext(c.ctx).Error(ModuleName+": the sessions of a reset account could not be revoked",
			"cause", revErr.Error())
	}
	if delErr := r.deleteResetRecord(c.ctx, token); delErr != nil {
		log.FromContext(c.ctx).Warn(ModuleName+": a consumed reset record could not be removed",
			"cause", delErr.Error())
	}
	r.setResetCookie(c, "", -1)
	r.meters.resetOperation(c.ctx, resetOpComplete, rec.Channel, resetCompleted)
	c.empty(http.StatusNoContent)
	return c.result()
}

// A failed revert leaves the record unreadable, the safe direction: the password was not written.
func (r *routes) revertReset(c *call, token string, procRaw []byte, verified resetRecord) {
	if _, swapped, err := r.swapResetRecord(c.ctx, token, procRaw, verified); err != nil || !swapped {
		log.FromContext(c.ctx).Warn(ModuleName+": a reset record could not be returned to verified",
			"cause", fmt.Sprint(err))
	}
}

func (r *routes) resetAudit(c *call, account authn.AccountID, channel, outcome, actionID string) bool {
	if r.audit == nil {
		return false
	}
	id := identity.New(authn.Principal{Account: account}, "", "")
	actx := identity.With(c.ctx, id)
	err := r.audit.Record(actx, audit.Entry{
		Action:  actionPasswordReset,
		Object:  audit.AccountObject(string(account)),
		Outcome: outcome,
		Detail:  map[string]string{"channel": channel, "action_id": actionID},
	})
	if err != nil {
		r.meters.auditFailedFor(c.ctx, true)
		log.FromContext(c.ctx).Error(ModuleName+": a password reset audit row could not be written",
			"outcome", outcome, "cause", err.Error())
		return false
	}
	return true
}

func (r *routes) dispatchResetEmail(ctx context.Context, to, code string, ttl time.Duration, locale string) {
	if r.emailReset == nil {
		r.resetMail.release()
		return
	}
	go func() {
		defer r.resetMail.release()
		// The request's cancellation must not cancel a message it already accepted; its values, the logger among them, are kept.
		sendCtx := context.WithoutCancel(ctx)
		if err := r.emailReset.SendResetCode(sendCtx, to, code, ttl, locale); err != nil {
			log.FromContext(sendCtx).Error(ModuleName+": a password reset email could not be sent",
				"cause", err.Error())
			r.meters.resetOperation(sendCtx, resetOpSend, resetChannelEmail, outcomeFailed)
		}
	}()
}

const (
	resetBudgetOK = iota
	resetBudgetOver
	resetBudgetFailed
)

type resetEmailBudget struct {
	key    string
	limit  int
	bucket time.Duration
}

func resetEmailResendKey(email string) string {
	sum := sha256.Sum256([]byte(budgetalias.Canonical(email)))
	return "authn:email-resend:" + hex.EncodeToString(sum[:])
}

func resetEmailCanonicalSource(source string) string {
	if addr, err := netip.ParseAddr(source); err == nil {
		return addr.String()
	}
	return source
}

func (r *routes) reserveResetEmailBudgets(c *call, now time.Time, email string, account authn.AccountID, source string) ([]string, int) {
	addressSum := sha256.Sum256([]byte(budgetalias.Canonical(email)))
	address := hex.EncodeToString(addressSum[:])
	sourceKey := resetEmailCanonicalSource(source)
	sourceSum := sha256.Sum256([]byte(sourceKey))
	sourceDigest := hex.EncodeToString(sourceSum[:])
	hour := now.UTC().Format("2006010215")
	day := now.UTC().Format("20060102")

	budgets := []resetEmailBudget{
		{
			key:   "authn:reset-email-address:" + address + ":h:" + hour,
			limit: r.settings.emailPerAddressHourly(), bucket: time.Hour,
		},
		{
			key:   "authn:reset-email-address:" + address + ":d:" + day,
			limit: r.settings.emailPerAddressDaily(), bucket: 24 * time.Hour,
		},
	}
	if account != "" {
		budgets = append(budgets,
			resetEmailBudget{
				key:   "authn:reset-email-account:" + string(account) + ":h:" + hour,
				limit: r.settings.emailPerAccountHourly(), bucket: time.Hour,
			},
			resetEmailBudget{
				key:   "authn:reset-email-account:" + string(account) + ":d:" + day,
				limit: r.settings.emailPerAccountDaily(), bucket: 24 * time.Hour,
			},
		)
	}
	if source != "" {
		budgets = append(budgets,
			resetEmailBudget{
				key:   "authn:reset-email-source:" + sourceDigest + ":h:" + hour,
				limit: r.settings.emailPerSourceHourly(), bucket: time.Hour,
			},
			resetEmailBudget{
				key:   "authn:reset-email-source:" + sourceDigest + ":d:" + day,
				limit: r.settings.emailPerSourceDaily(), bucket: 24 * time.Hour,
			},
		)
	}
	if account != "" {
		budgets = append(budgets, resetEmailBudget{
			key:   "authn:reset-email-global:" + hour,
			limit: r.settings.emailGlobalHourly(), bucket: time.Hour,
		})
	}

	taken := make([]string, 0, len(budgets))
	for _, budget := range budgets {
		if budget.limit <= 0 {
			continue
		}
		count, err := r.kv.IncrByFloatWithTTL(c.ctx, budget.key, 1, budget.bucket)
		if err != nil {
			log.FromContext(c.ctx).Warn(ModuleName+": a password reset email budget cannot be counted",
				"cause", err.Error())
			r.refundResetEmailBudgets(c.ctx, taken)
			c.unavailable()
			return nil, resetBudgetFailed
		}
		taken = append(taken, budget.key)
		if count > float64(budget.limit) {
			r.refundResetEmailBudgets(c.ctx, taken)
			return nil, resetBudgetOver
		}
	}
	return taken, resetBudgetOK
}

func (r *routes) refundResetEmailBudgets(ctx context.Context, keys []string) {
	for _, key := range keys {
		raw, present, err := r.kv.Get(ctx, key)
		if err != nil || !present {
			continue
		}
		current, err := strconv.ParseFloat(string(raw), 64)
		if err != nil || current <= 0 {
			continue
		}
		if _, err := r.kv.CompareAndSwap(ctx, key, raw,
			[]byte(strconv.FormatFloat(current-1, 'g', -1, 64))); err != nil {
			log.FromContext(ctx).Warn(ModuleName+": a password reset email budget cannot be given back",
				"cause", err.Error())
		}
	}
}

func (r *routes) releaseResetEmailWindow(ctx context.Context, key string) {
	if err := r.kv.Delete(ctx, key); err != nil {
		log.FromContext(ctx).Warn(ModuleName+": a password reset resend window could not be released",
			"cause", err.Error())
	}
}
