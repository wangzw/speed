package selfservice

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

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/budgetalias"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

const (
	identifierKindUsername = "username"
	identifierKindEmail    = "email"
)

const (
	identifierStateAdded   = "added"
	identifierStatePending = "pending"
)

const (
	emailCodeDigits  = 6
	maxEmailAttempts = 5

	emailKeyPrefix = "authn:email:"

	// emailAttemptKeyPrefix gives each code its own atomic count: a count kept inside the record would lose concurrent wrong codes.
	emailAttemptKeyPrefix = "authn:email-attempts:"

	emailResendKeyPrefix = "authn:email-resend:"

	emailAddressBudgetPrefix = "authn:email-address:"
	emailAccountBudgetPrefix = "authn:email-account:"
	emailSourceBudgetPrefix  = "authn:email-source:"
	emailGlobalBudgetPrefix  = "authn:email-global:"
)

const (
	emailHourBucket = time.Hour
	emailDayBucket  = 24 * time.Hour
	emailHourLayout = "2006010215"
	emailDayLayout  = "20060102"
)

var emailAddress = regexp.MustCompile(`^[^@\s]+@[^@\s]+$`)

type emailRecord struct {
	Account    string `json:"account"`
	Email      string `json:"email"`
	CodeDigest string `json:"code_digest"`
	ExpiresAt  int64  `json:"expires_at"`
}

type emailResendMark struct {
	SentAt int64 `json:"sent_at"`
}

func emailKey(session authn.SessionID) string {
	sum := sha256.Sum256([]byte(session))
	return emailKeyPrefix + hex.EncodeToString(sum[:])
}

func emailAttemptKey(codeDigest string) string {
	return emailAttemptKeyPrefix + codeDigest
}

func emailResendKey(email string) string {
	sum := sha256.Sum256([]byte(budgetalias.Canonical(email)))
	return emailResendKeyPrefix + hex.EncodeToString(sum[:])
}

func emailAddressHourlyKey(email string, now time.Time) string {
	sum := sha256.Sum256([]byte(budgetalias.Canonical(email)))
	return emailAddressBudgetPrefix + hex.EncodeToString(sum[:]) + ":h:" + now.UTC().Format(emailHourLayout)
}

func emailAddressDailyKey(email string, now time.Time) string {
	sum := sha256.Sum256([]byte(budgetalias.Canonical(email)))
	return emailAddressBudgetPrefix + hex.EncodeToString(sum[:]) + ":d:" + now.UTC().Format(emailDayLayout)
}

func emailAccountHourlyKey(account authn.AccountID, now time.Time) string {
	return emailAccountBudgetPrefix + string(account) + ":h:" + now.UTC().Format(emailHourLayout)
}

func emailAccountDailyKey(account authn.AccountID, now time.Time) string {
	return emailAccountBudgetPrefix + string(account) + ":d:" + now.UTC().Format(emailDayLayout)
}

func canonicalSource(source string) string {
	if addr, err := netip.ParseAddr(source); err == nil {
		return addr.String()
	}
	return source
}

func emailSourceHourlyKey(source string, now time.Time) string {
	sum := sha256.Sum256([]byte(canonicalSource(source)))
	return emailSourceBudgetPrefix + hex.EncodeToString(sum[:]) + ":h:" + now.UTC().Format(emailHourLayout)
}

func emailSourceDailyKey(source string, now time.Time) string {
	sum := sha256.Sum256([]byte(canonicalSource(source)))
	return emailSourceBudgetPrefix + hex.EncodeToString(sum[:]) + ":d:" + now.UTC().Format(emailDayLayout)
}

func emailGlobalHourlyKey(now time.Time) string {
	return emailGlobalBudgetPrefix + now.UTC().Format(emailHourLayout)
}

func emailDigest(session authn.SessionID, code string) string {
	sum := sha256.Sum256([]byte(string(session) + "\x00" + code))
	return hex.EncodeToString(sum[:])
}

func emailCodeMatches(want, got string) bool {
	w, err1 := hex.DecodeString(want)
	g, err2 := hex.DecodeString(got)
	if err1 != nil || err2 != nil || len(w) != len(g) {
		return false
	}
	return subtle.ConstantTimeCompare(w, g) == 1
}

func emailCode() (string, error) {
	bound := new(big.Int).Exp(big.NewInt(10), big.NewInt(emailCodeDigits), nil)
	n, err := rand.Int(rand.Reader, bound)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", emailCodeDigits, n.Int64()), nil
}

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

func (r *routes) addIdentifier(ctx context.Context, in addIdentifierInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	who, ok := r.identity(c)
	if !ok {
		return c.result()
	}
	switch in.Kind {
	case identifierKindUsername:
		if !r.freshIdentity(c, "") {
			return c.result()
		}
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
		r.addEmail(c, who, in.Value, in.HumanProof)
	default:
		c.fail(mount.CodeBadRequest)
	}
	return c.result()
}

func (r *routes) addEmail(c *call, who caller, raw, proof string) {
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

	if !r.checkHumanProof(c, proof) {
		return
	}

	held, err := r.emailTakenByAnother(c.ctx, email, who.account)
	if err != nil {
		logWarn(c.ctx, "whether an email address is already taken cannot be read", err)
		c.unavailable()
		return
	}
	if held {
		c.fail(mount.CodeEmailTaken)
		return
	}

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

	reserved, ok := r.reserveEmailBudgets(c, now, email, who.account, c.clientIP)
	if !ok {
		r.releaseResend(c.ctx, resendKey)
		return
	}

	code, err := emailCode()
	if err != nil {
		logWarn(c.ctx, "an email verification code could not be drawn", err)
		r.releaseResend(c.ctx, resendKey)
		r.refundEmailBudgets(c.ctx, reserved)
		c.unavailable()
		return
	}
	if err = r.deps.EmailSender.SendCode(c.ctx, email, code, ttl, smsLocale(c.ctx)); err != nil {
		r.releaseResend(c.ctx, resendKey)
		if r.emailCertifiesNotSent(err) {
			r.refundEmailBudgets(c.ctx, reserved)
		}
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
	r.answerPending(c, ttl, resend)
}

func (r *routes) answerPending(c *call, ttl, resend time.Duration) {
	c.json(http.StatusOK, addIdentifierResponse{
		State: identifierStatePending, ExpiresIn: int(seconds(ttl)), ResendAfter: int(seconds(resend)),
	})
}

func (r *routes) emailTakenByAnother(ctx context.Context, email string, who authn.AccountID) (bool, error) {
	owner, err := r.deps.Accounts.Lookup(ctx, authn.IdentifierEmail, email)
	switch {
	case err == nil:
		return owner != who, nil
	case errors.Is(err, authn.ErrAccountNotFound), errors.Is(err, authn.ErrInvalidIdentifier):
	default:
		return false, err
	}
	reserved, ok, err := r.deps.Signup.PendingAccountByContact(ctx, identifierKindEmail, email)
	if errors.Is(err, authn.ErrInvalidIdentifier) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return ok && authn.AccountID(reserved) != who, nil
}

type emailBudget struct {
	name   string
	key    string
	limit  int
	bucket time.Duration
}

type notSentCertifier interface {
	CertifiesNotSent(err error) bool
}

func (r *routes) emailCertifiesNotSent(err error) bool {
	if errors.Is(err, authn.ErrEmailUncertain) {
		return false
	}
	certifier, ok := r.deps.EmailSender.(notSentCertifier)
	return ok && certifier.CertifiesNotSent(err)
}

func (r *routes) reserveEmailBudgets(c *call, now time.Time, email string, account authn.AccountID, source string) ([]string, bool) {
	budgets := []emailBudget{
		{
			"per-address hourly", emailAddressHourlyKey(email, now),
			r.budgets.get(c.ctx, keyEmailPerAddressHourly,
				defaultEmailPerAddressHourly, minEmailPerAddressHourly, maxEmailPerAddressHourly),
			emailHourBucket,
		},
		{
			"per-address daily", emailAddressDailyKey(email, now),
			r.budgets.get(c.ctx, keyEmailPerAddressDaily,
				defaultEmailPerAddressDaily, minEmailPerAddressDaily, maxEmailPerAddressDaily),
			emailDayBucket,
		},
		{
			"per-account hourly", emailAccountHourlyKey(account, now),
			r.budgets.get(c.ctx, keyEmailPerAccountHourly,
				defaultEmailPerAccountHourly, minEmailPerAccountHourly, maxEmailPerAccountHourly),
			emailHourBucket,
		},
		{
			"per-account daily", emailAccountDailyKey(account, now),
			r.budgets.get(c.ctx, keyEmailPerAccountDaily,
				defaultEmailPerAccountDaily, minEmailPerAccountDaily, maxEmailPerAccountDaily),
			emailDayBucket,
		},
	}
	if source != "" {
		budgets = append(budgets,
			emailBudget{
				"per-source hourly", emailSourceHourlyKey(source, now),
				r.budgets.get(c.ctx, keyEmailPerSourceHourly,
					defaultEmailPerSourceHourly, minEmailPerSourceHourly, maxEmailPerSourceHourly),
				emailHourBucket,
			},
			emailBudget{
				"per-source daily", emailSourceDailyKey(source, now),
				r.budgets.get(c.ctx, keyEmailPerSourceDaily,
					defaultEmailPerSourceDaily, minEmailPerSourceDaily, maxEmailPerSourceDaily),
				emailDayBucket,
			},
		)
	}
	budgets = append(budgets, emailBudget{
		"global hourly", emailGlobalHourlyKey(now),
		r.budgets.get(c.ctx, keyEmailGlobalHourly,
			defaultEmailGlobalHourly, minEmailGlobalHourly, maxEmailGlobalHourly),
		emailHourBucket,
	})

	taken := make([]string, 0, len(budgets))
	for _, budget := range budgets {
		if budget.limit <= 0 {
			continue
		}
		count, err := r.deps.KV.IncrByFloatWithTTL(c.ctx, budget.key, 1, budget.bucket)
		if err != nil {
			logWarn(c.ctx, "the "+budget.name+" budget of an email verification code cannot be counted, so the "+
				"address is not sent to", err)
			r.refundEmailBudgets(c.ctx, taken)
			c.unavailable()
			return nil, false
		}
		taken = append(taken, budget.key)
		if count > float64(budget.limit) {
			r.refundEmailBudgets(c.ctx, taken)
			c.throttled(untilNextEmailBucket(now, budget.bucket))
			return nil, false
		}
	}
	return taken, true
}

// refundEmailBudgetAttempts bounds re-reads when another writer changed the count between the read and the swap.
const refundEmailBudgetAttempts = 3

func (r *routes) refundEmailBudgets(ctx context.Context, keys []string) {
	for _, key := range keys {
		r.refundEmailBudget(ctx, key)
	}
}

func (r *routes) refundEmailBudget(ctx context.Context, key string) {
	for attempt := 0; attempt < refundEmailBudgetAttempts; attempt++ {
		raw, present, err := r.deps.KV.Get(ctx, key)
		if err != nil {
			logWarn(ctx, "an email budget count cannot be read back, so it stays spent", err)
			return
		}
		if !present {
			return
		}
		current, err := strconv.ParseFloat(string(raw), 64)
		if err != nil {
			logWarn(ctx, "an email budget count is not a number, so it stays spent", err)
			return
		}
		if current <= 0 {
			return
		}
		swapped, err := r.deps.KV.CompareAndSwap(ctx, key, raw,
			[]byte(strconv.FormatFloat(current-1, 'g', -1, 64)))
		if err != nil {
			logWarn(ctx, "an email budget count cannot be given back, so it stays spent", err)
			return
		}
		if swapped {
			return
		}
	}
	logWarn(ctx, "an email budget count changed under every refund attempt, so it stays spent", nil)
}

func untilNextEmailBucket(now time.Time, bucket time.Duration) time.Duration {
	wait := now.Truncate(bucket).Add(bucket).Sub(now)
	if wait < time.Second {
		return time.Second
	}
	return wait
}

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

func (r *routes) releaseResend(ctx context.Context, resendKey string) {
	if err := r.deps.KV.Delete(ctx, resendKey); err != nil {
		logWarn(ctx, "a resend claim of an email address could not be released", err)
	}
}

func (r *routes) verifyIdentifier(ctx context.Context, in verifyIdentifierInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	who, ok := r.identity(c)
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
		c.unauthorized(mount.CodeLoginFailed)
		return c.result()
	}
	attemptKey := emailAttemptKey(rec.CodeDigest)
	if !emailCodeMatches(rec.CodeDigest, emailDigest(who.session, in.Code)) {
		r.countEmailAttempt(c.ctx, key, attemptKey, rec.ExpiresAt)
		c.unauthorized(mount.CodeLoginFailed)
		return c.result()
	}

	needsFresh, err := r.contactProofNeedsFreshness(c.ctx, who.account, authn.IdentifierEmail, rec.Email)
	if err != nil {
		r.fail(c, err)
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

	bindErr := r.deps.Admin.AddIdentifier(c.ctx, who.account, authn.IdentifierEmail, rec.Email,
		authn.Verification{Via: authn.VerifiedViaEmailCode})
	if err := r.deps.KV.Delete(c.ctx, key); err != nil {
		logWarn(c.ctx, "a consumed email verification could not be deleted", err)
	}
	if err := r.deps.KV.Delete(c.ctx, attemptKey); err != nil {
		logWarn(c.ctx, "the attempt count of a consumed email verification could not be deleted", err)
	}
	if bindErr != nil {
		if errors.Is(bindErr, authn.ErrContactTaken) {
			c.fail(mount.CodeEmailTaken)
			return c.result()
		}
		r.fail(c, bindErr)
		return c.result()
	}
	c.json(http.StatusOK, addIdentifierResponse{State: identifierStateAdded})
	return c.result()
}

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
