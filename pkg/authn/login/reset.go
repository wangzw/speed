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
	"github.com/vislake/speed/pkg/authn/internal/identity"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// The anonymous password reset. It is not a login flow: it keeps its own
// `__Host-authn-reset` binding and its own key-value record, writes no login flow,
// creates no session and touches no factor. A record belongs only to the cookie
// that asked for it, and only a target that is eligible is ever sent a message;
// every other target follows the same record, verify and answer path with a
// suppressed record, so an answer never says whether an account can be reset.
const (
	// resetCookieHost is the production cookie name. The __Host- prefix requires
	// Secure, a path of "/" and no Domain, so the insecure form drops the prefix
	// and the Secure attribute together.
	resetCookieHost  = "__Host-authn-reset"
	resetCookiePlain = "authn-reset"
	resetCookiePath  = "/"

	// resetKeyPrefix starts the key of a reset record. The rest is the SHA-256 of
	// the cookie secret, never the secret itself.
	resetKeyPrefix = "authn:reset:"

	resetRecordVersion = 1

	// The states of a reset record. processing is the internal state while the
	// password is being written; there is no done, because a success removes the
	// record.
	resetStateSent       = "sent"
	resetStateVerified   = "verified"
	resetStateProcessing = "processing"
	resetStateFailed     = "failed"

	// The two channels, the closed set the request names and the record keeps.
	resetChannelEmail = "email"
	resetChannelSMS   = "sms"

	// The code is six digits and a record admits this many wrong answers, after
	// which the record is failed and every later answer is still login-failed.
	resetCodeDigits    = 6
	maxResetAttempts   = 5
	maxResetCodeLength = 32

	// resetMailWorkers bounds the goroutines that deliver reset mail, so a burst
	// of requests cannot open an unbounded number of carrier calls. A full pool
	// answers 503 before a record is written, for an eligible and a suppressed
	// target alike.
	resetMailWorkers = 4

	// The mandatory audit action of the reset, and the outcome its pre-write row
	// carries. success and denied exist already, as outcomeSuccess and
	// outcomeDenied are not declared in this package, so the two are named here.
	actionPasswordReset = "authn.password.reset"
	outcomeAttempted    = "attempted"
	resetOutcomeDenied  = "denied"
)

// resetE164 and resetEmail are the shapes a reset identifier is accepted in. The
// account module owns the authoritative normalization; these are the shapes a
// message is paid for.
var (
	resetE164  = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
	resetEmail = regexp.MustCompile(`^[^@\s]+@[^@\s]+$`)
)

// resetRecord is one anonymous password reset, the key-value store's value. It
// stores no address and no phone: only the keyed digest of the normalized
// identifier, the account and identifier row of an eligible target, the challenge
// or a local digest of the code, and the counters and deadlines.
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

// resetMailer bounds the goroutines that deliver reset email. A slot is reserved
// before the record is written, so a full pool answers 503 with nothing written;
// the slot is released when the carrier call returns.
type resetMailer struct {
	slots chan struct{}
}

func newResetMailer(workers int) *resetMailer {
	if workers < 1 {
		workers = 1
	}
	return &resetMailer{slots: make(chan struct{}, workers)}
}

// reserve takes one slot without waiting. A nil mailer, which is a product that
// was not built through newProduct, admits everything.
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

// release gives one slot back.
func (m *resetMailer) release() {
	if m == nil {
		return
	}
	<-m.slots
}

// resetCookieName is the cookie the reset binding travels in.
func (r *routes) resetCookieName() string {
	if r.allowInsecure {
		return resetCookiePlain
	}
	return resetCookieHost
}

// setResetCookie writes the reset cookie. A maxAge of -1 clears it. The cookie is
// SameSite=Strict: the reset is driven from the site's own page, and a cross-site
// request must not carry it.
func (r *routes) setResetCookie(c *call, token string, maxAge int) {
	c.setCookie(&http.Cookie{
		Name:     r.resetCookieName(),
		Value:    token,
		Path:     resetCookiePath,
		MaxAge:   maxAge,
		Secure:   !r.allowInsecure,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// resetKey is the key of a reset record: the prefix and the SHA-256 of the cookie
// secret.
func resetKey(token string) string { return resetKeyPrefix + tokenDigest(token) }

// decodeResetRecord reads a reset record and refuses one of another version or
// with an unknown field.
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

// loadResetRecord reads the record a presented cookie is bound to. A record whose
// binding does not match the cookie, and one that cannot be decoded, are invalid
// flows rather than outages.
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

// storeResetRecord creates a reset record with a fresh lifetime and returns the
// bytes it wrote, which a later compare-and-swap matches.
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

// swapResetRecord advances a reset record with one compare-and-swap. A false
// result, with no error, means another writer moved the record first, which the
// caller answers as an invalid flow. The compare-and-swap does not extend the key's
// lifetime, so a record's deadline is fixed when it is created.
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

// deleteResetRecord removes a reset record; it is idempotent.
func (r *routes) deleteResetRecord(ctx context.Context, token string) error {
	if err := r.kv.Delete(ctx, resetKey(token)); err != nil {
		return fmt.Errorf("%s: removing a reset record: %w", ModuleName, err)
	}
	return nil
}

// normalizeResetIdentifier holds a reset identifier to the shape its channel
// accepts and returns the form the account module normalizes to: an address is
// trimmed and lowercased, a phone is kept as written.
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

// resetIdentifierDigest is the keyed digest of a normalized identifier. The key
// is the cookie secret's digest, so a reader of the store cannot enumerate
// addresses or numbers, and the same-cookie resend test can compare it.
func resetIdentifierDigest(token, channel, normalized string) string {
	sum := sha256.Sum256([]byte(tokenDigest(token) + "\x00" + channel + "\x00" + normalized))
	return hex.EncodeToString(sum[:])
}

// resetCodeDigest is the salted digest of a code, salted with the cookie secret's
// digest. A reader of the store cannot recompute the digest of a guessed code.
func resetCodeDigest(token, code string) string {
	sum := sha256.Sum256([]byte(tokenDigest(token) + "\x00" + code))
	return hex.EncodeToString(sum[:])
}

// resetDigestMatches compares two hexadecimal digests in constant time.
func resetDigestMatches(want, got string) bool {
	w, err1 := hex.DecodeString(want)
	g, err2 := hex.DecodeString(got)
	if err1 != nil || err2 != nil || len(w) != len(g) || len(w) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare(w, g) == 1
}

// resetCode draws a six-digit code from the system's entropy source.
func resetCode() (string, error) {
	bound := new(big.Int).Exp(big.NewInt(10), big.NewInt(resetCodeDigits), nil)
	n, err := rand.Int(rand.Reader, bound)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", resetCodeDigits, n.Int64()), nil
}

// resetDecoy is a local random digest a suppressed record compares against. It is
// never the digest of a code a client could present, so a suppressed record never
// verifies, and no call to the code verifier is made.
func resetDecoy() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// resetEligible decides whether a target may be reset and returns the account and
// identifier row that make it eligible. An identifier that names no account, a
// deleted account, a disabled account, a service account, an account without a
// password, and an identifier whose proof is not the requested channel's are all
// ineligible, and none of them is an error.
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

// resetRecordEligible re-checks, at the completion, that the account a verified
// record names is still enabled, still a user with a password, and still holds the
// proved identifier row under the same proof channel.
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

// resetOpen is the surface check every reset route makes before it acts: the
// switch is open and the mandatory audit record is present. A closed switch is
// 403 password-reset-closed; an absent audit is 503 unavailable, because the reset
// does not publish without one.
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

// resetChannelReady is the channel check: the channel is assembled, a SMS channel
// was not turned off by disabled-methods, and the SMS carrier can carry the reset
// purpose. A channel the deployment took off answers 403 password-reset-closed; a
// channel that is not assembled answers 503 unavailable.
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

// resetChannelAvailable reports whether a channel may be published in the
// discovery document. disabled is the set of method names turned off, read once by
// the discovery handler.
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

// passwordResetCapability is the discovery member: the deployment offers the
// reset when the switch is open, the audit is present and at least one channel is
// available. It is a deployment fact and carries no account dimension.
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

// slicesContains reports whether list holds value.
func slicesContains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// resetDurations is the code lifetime and resend interval of a channel.
func (r *routes) resetDurations(channel string) (codeTTL, resend time.Duration) {
	if channel == resetChannelSMS {
		return r.settings.smsCodeTTL(), r.settings.smsResend()
	}
	return r.settings.emailCodeTTL(), r.settings.emailResend()
}

// resetAnswer writes the answer of the send, current and verify routes. It is one
// function so every path answers one shape.
func resetAnswer(c *call, status int, state string, expiresIn, resendAfter int) {
	c.json(status, passwordResetState{State: state, ExpiresIn: expiresIn, ResendAfter: resendAfter})
}

// resetSeconds is d in whole seconds, rounded up.
func resetSeconds(d time.Duration) int {
	return int((d + time.Second - 1) / time.Second)
}

// passwordResetStart runs POST password-reset. The order is the one the surface
// fixes: the switch and the channel, then the identifier shape, then the human
// proof, then the source-address pre-throttle, then the record and the same-cookie
// resend interval, then eligibility, then the channel's own budgets and send.
//
// A target that is not eligible and a target the budgets suppress follow the same
// path as an eligible one: they get the same record shape, the same 202 and the
// same cookie. The only 429 answers are the same cookie's outstanding resend
// interval and the source-address pre-throttle; every send budget overrun
// suppresses and still answers 202.
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
	if !r.checkHumanProof(c, in.HumanProof) {
		return c.result()
	}
	if !r.admitSends(c) {
		r.meters.resetOperation(c.ctx, resetOpSend, channel, outcomeThrottled)
		return c.result()
	}

	now := r.clock.Now()
	codeTTL, resend := r.resetDurations(channel)
	ticketTTL := r.settings.passwordResetTicketTTL()

	// A live record on the presented cookie decides whether this is a resend of
	// the same flow or a fresh one. A record being completed is refused outright:
	// a start cannot race a password write on the same binding.
	token := ""
	oldToken := ""
	var oldRaw []byte
	var old *resetRecord
	if presented := c.cookieValue(r.resetCookieName()); presented != "" {
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
			// The record's lifetime was fixed when it was created. A code whose
			// ticket could not fit before that deadline is not sent into it; the
			// flow starts again with a fresh binding instead.
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

	// The channel sends, or the suppressed path, produce the code material and the
	// send count the record keeps.
	var (
		suppressed  = !eligible
		challengeID string
		codeDigest  string
		sends       int
	)
	if resendFlow {
		sends = old.Sends
	}
	// The email channel reserves one work-pool slot for an eligible and a
	// suppressed target alike, before the record is written. The slot is released
	// at return unless a real message was handed to the pool.
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
		})
		switch {
		case sendErr == nil:
			challengeID = sentID
		case errors.Is(sendErr, authn.ErrThrottled):
			// Any send budget or resend window overrun suppresses and still answers
			// 202; the target cannot tell it from a real send.
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
			// The pre-throttle is charged for the message this call sent. A call
			// that crosses the limit still sends, exactly as the login engine's does.
			if !r.chargeSends(c, 1) {
				r.meters.resetOperation(c.ctx, resetOpSend, channel, outcomeThrottled)
				return c.result()
			}
		}

	case resetChannelEmail:
		// The work pool is judged for an eligible and a suppressed target alike,
		// before the record is written, so neither learns anything from a full
		// pool. The slot is released at return for a target that sends nothing.
		if !r.resetMail.reserve() {
			r.meters.resetOperation(c.ctx, resetOpSend, channel, outcomeUnavailable)
			c.unavailable()
			return c.result()
		}
		emailSlot = true
		// A send to the address is only meaningful for an eligible target, and it
		// still meets an inbox's resend window and the reset's own budgets. Every
		// such overrun suppresses; none is a 429.
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
			// A decoy sends nothing, so it spends no inbox resend window and no
			// account or global budget. The inbox and source dimensions are still
			// counted, exactly as the SMS capability counts a suppressed row, so the
			// two paths meet the same counters.
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
		// A fresh flow replaces whatever the presented cookie named.
		if old != nil && oldToken != token {
			if err := r.deleteResetRecord(c.ctx, oldToken); err != nil {
				// The old record is unreachable now that the cookie names the new
				// one; a failed removal only leaves it to expire.
				log.FromContext(c.ctx).Warn(ModuleName+": a replaced reset record could not be removed",
					"cause", err.Error())
			}
		}
	}

	// The message is handed to the pool only once the record it belongs to is
	// durable. The deferred function dispatches it on return, off the response
	// path; every early return above leaves emailDeliver false and releases the
	// slot instead.
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

// passwordResetCurrent runs GET password-reset/current. It reports the state and
// the deadlines and never the account, the identifier or the code.
func (r *routes) passwordResetCurrent(ctx context.Context, _ struct{}, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	if !r.resetOpen(c) {
		return c.result()
	}
	token := c.cookieValue(r.resetCookieName())
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
		// processing is the internal state of a password write; failed and any
		// other state are not readable as a live reset.
		c.unauthorized(mount.CodeFlowInvalid)
	}
	return c.result()
}

// passwordResetDelete runs DELETE password-reset/current. It is idempotent, does
// not change with the switch, sends nothing and changes no account.
func (r *routes) passwordResetDelete(ctx context.Context, _ struct{}, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	token := c.cookieValue(r.resetCookieName())
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

// passwordResetVerify runs POST password-reset/verify. A suppressed record
// compares against its local decoy and never calls the code verifier, so any code
// answers the same login-failed as a wrong one on a real record.
func (r *routes) passwordResetVerify(ctx context.Context, in passwordResetVerifyInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	if !r.resetOpen(c) {
		return c.result()
	}
	token := c.cookieValue(r.resetCookieName())
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
		// The decoy digest is random; the comparison is made in the same shape as a
		// real email compare so a suppressed record does no less work.
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

// passwordResetComplete runs POST password-reset/complete. The record moves
// verified -> processing before the mandatory audit row is written, so two
// concurrent completions have one winner; the password write happens only after
// the attempted row is durable.
func (r *routes) passwordResetComplete(ctx context.Context, in passwordResetCompleteInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	if !r.resetOpen(c) {
		return c.result()
	}
	token := c.cookieValue(r.resetCookieName())
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

	// The compare-and-swap is the at-most-once gate: the winner writes the
	// password, every concurrent loser is refused as an invalid flow.
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
		// The mandatory row is the path's leave-behind: without it no password is
		// written, and the record returns to verified so the caller can try again.
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
		case errors.Is(setErr, authn.ErrAccountUnavailable):
			r.resetAudit(c, account, rec.Channel, resetOutcomeDenied, actionID)
			r.meters.resetOperation(c.ctx, resetOpComplete, rec.Channel, outcomeFailed)
			c.forbidden(mount.CodePasswordResetUnavailable)
		case errors.Is(setErr, authn.ErrPasswordPolicy), errors.Is(setErr, authn.ErrInvalidIdentifier),
			errors.Is(setErr, authn.ErrInvalidArgument):
			r.resetAudit(c, account, rec.Channel, resetOutcomeDenied, actionID)
			r.meters.resetOperation(c.ctx, resetOpComplete, rec.Channel, outcomeFailed)
			c.fail(mount.CodeBadRequest)
		default:
			// An outage is not a refusal and writes no terminal row; the attempted
			// row already records that the path was entered. The completion never
			// answers 429: only the send route has the two 429 answers.
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
	// The password is written. Revoking the account's sessions is the half a
	// failure here cannot undo; a failed revocation is an Error and the answer
	// stays 204. The target's bearer impersonation tokens lapse through
	// valid_after; an operator's cookie impersonation session belongs to another
	// account and is not revoked.
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

// revertReset moves a processing record back to verified, best effort. A failed
// revert leaves the record unreadable, which is the safe direction: the password
// was not written, and the caller has to start again.
func (r *routes) revertReset(c *call, token string, procRaw []byte, verified resetRecord) {
	if _, swapped, err := r.swapResetRecord(c.ctx, token, procRaw, verified); err != nil || !swapped {
		log.FromContext(c.ctx).Warn(ModuleName+": a reset record could not be returned to verified",
			"cause", fmt.Sprint(err))
	}
}

// resetAudit writes one row of the mandatory reset action under a context whose
// identity is the account being reset; the derived context reaches this one
// Record call and is never handed to a handler. It reports whether the row was
// written. A failure is counted on the audit-failure instrument, which the reset
// rows mark mandatory.
func (r *routes) resetAudit(c *call, account authn.AccountID, channel, outcome, actionID string) bool {
	if r.audit == nil {
		return false
	}
	id := identity.New(authn.Principal{Account: account}, "", "")
	actx := identity.With(c.ctx, id)
	err := r.audit.Record(actx, audit.Entry{
		Action:  actionPasswordReset,
		Object:  string(account),
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

// dispatchResetEmail hands one reset message to the bounded pool. The response has
// already been written and does not wait for the carrier; a failure is an Error
// and a count, never a change of the answer.
func (r *routes) dispatchResetEmail(ctx context.Context, to, code string, ttl time.Duration, locale string) {
	if r.emailReset == nil {
		r.resetMail.release()
		return
	}
	go func() {
		defer r.resetMail.release()
		// The request's cancellation must not cancel the message it already
		// accepted; its values, the logger among them, are kept.
		sendCtx := context.WithoutCancel(ctx)
		if err := r.emailReset.SendResetCode(sendCtx, to, code, ttl, locale); err != nil {
			log.FromContext(sendCtx).Error(ModuleName+": a password reset email could not be sent",
				"cause", err.Error())
			// The send this request answered was already counted as sent (or
			// suppressed); only the failure the response could not carry is added.
			r.meters.resetOperation(sendCtx, resetOpSend, resetChannelEmail, outcomeFailed)
		}
	}()
}

// The result of reserving the reset email budgets.
const (
	resetBudgetOK = iota
	resetBudgetOver
	resetBudgetFailed
)

// resetEmailBudget is one dimension of the reset email send budget.
type resetEmailBudget struct {
	key    string
	limit  int
	bucket time.Duration
}

// resetEmailResendKey is the inbox resend window, shared with the self-service
// email branch: one inbox carries one code at a time whatever purpose asked for
// it.
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

// reserveResetEmailBudgets counts one attempt against the reset's own email
// budgets. eligible selects the full set; an ineligible target only counts the
// inbox and source dimensions, because it has no account and sends no message.
// An overrun is reported as resetBudgetOver with what the attempt took given back;
// a counter that cannot be written is resetBudgetFailed with the answer already
// set to 503.
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

// refundResetEmailBudgets gives back the counts one attempt took, by a
// compare-and-swap per key rather than a negative increment, so an expired window
// is not recreated.
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

// releaseResetEmailWindow gives back an inbox's resend claim when no message was
// sent.
func (r *routes) releaseResetEmailWindow(ctx context.Context, key string) {
	if err := r.kv.Delete(ctx, key); err != nil {
		log.FromContext(ctx).Warn(ModuleName+": a password reset resend window could not be released",
			"cause", err.Error())
	}
}
