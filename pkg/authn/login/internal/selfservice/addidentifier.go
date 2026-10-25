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
	// rest of the key is the SHA-256 of the address's budget form, so the window
	// is held against the inbox and shared across sessions and accounts: a second
	// session cannot turn the route into a flood of messages to one address, and
	// two writings of one inbox meet the same window.
	emailResendKeyPrefix = "authn:email-resend:"

	// The prefixes of the email verification code budgets: per inbox per hour and
	// per day, per account per hour and per UTC day, per request source per hour
	// and per UTC day, and one for the whole deployment per hour. They are keys of
	// their own rather than fields of the pending record, so each is advanced by
	// the store's atomic increment and no read-modify-write can lose a count.
	emailAddressBudgetPrefix = "authn:email-address:"
	emailAccountBudgetPrefix = "authn:email-account:"
	emailSourceBudgetPrefix  = "authn:email-source:"
	emailGlobalBudgetPrefix  = "authn:email-global:"
)

// The widths and key layouts of the email budgets. A bucket is the fixed window
// a counter lives in; its key carries the window it was started in, so a new
// window is a new key and the count starts at zero without a reset step.
const (
	emailHourBucket = time.Hour
	emailDayBucket  = 24 * time.Hour
	emailHourLayout = "2006010215"
	emailDayLayout  = "20060102"
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
// budget form, shared across the sessions and accounts that name it. The budget
// form is what makes two writings of one inbox meet the same window; the address
// the code is sent to is the normalized one, untouched by this folding.
func emailResendKey(email string) string {
	sum := sha256.Sum256([]byte(budgetalias.Canonical(email)))
	return emailResendKeyPrefix + hex.EncodeToString(sum[:])
}

// emailAddressHourlyKey is an address's hourly budget key for the window now
// falls in. The address's budget form is digested, so the store carries no
// address in the clear, exactly as the resend window does, and its alias writings
// share the count.
func emailAddressHourlyKey(email string, now time.Time) string {
	sum := sha256.Sum256([]byte(budgetalias.Canonical(email)))
	return emailAddressBudgetPrefix + hex.EncodeToString(sum[:]) + ":h:" + now.UTC().Format(emailHourLayout)
}

// emailAddressDailyKey is an address's daily budget key for the fixed UTC day now
// falls in.
func emailAddressDailyKey(email string, now time.Time) string {
	sum := sha256.Sum256([]byte(budgetalias.Canonical(email)))
	return emailAddressBudgetPrefix + hex.EncodeToString(sum[:]) + ":d:" + now.UTC().Format(emailDayLayout)
}

// emailAccountHourlyKey is an account's hourly budget key.
func emailAccountHourlyKey(account authn.AccountID, now time.Time) string {
	return emailAccountBudgetPrefix + string(account) + ":h:" + now.UTC().Format(emailHourLayout)
}

// emailAccountDailyKey is an account's daily budget key for the fixed UTC day.
func emailAccountDailyKey(account authn.AccountID, now time.Time) string {
	return emailAccountBudgetPrefix + string(account) + ":d:" + now.UTC().Format(emailDayLayout)
}

// canonicalSource is the address a source budget is counted against. The address
// is normalized through netip first, so two spellings of one address share a
// budget; one that does not parse is digested as it stands, because a request
// whose origin the deployment resolves in an unexpected shape still counts.
func canonicalSource(source string) string {
	if addr, err := netip.ParseAddr(source); err == nil {
		return addr.String()
	}
	return source
}

// emailSourceHourlyKey is a request source's hourly budget key. The normalized
// address is digested, so the store carries no source address in the clear.
func emailSourceHourlyKey(source string, now time.Time) string {
	sum := sha256.Sum256([]byte(canonicalSource(source)))
	return emailSourceBudgetPrefix + hex.EncodeToString(sum[:]) + ":h:" + now.UTC().Format(emailHourLayout)
}

// emailSourceDailyKey is a request source's daily budget key for the fixed UTC
// day.
func emailSourceDailyKey(source string, now time.Time) string {
	sum := sha256.Sum256([]byte(canonicalSource(source)))
	return emailSourceBudgetPrefix + hex.EncodeToString(sum[:]) + ":d:" + now.UTC().Format(emailDayLayout)
}

// emailGlobalHourlyKey is the deployment-wide hourly budget key.
func emailGlobalHourlyKey(now time.Time) string {
	return emailGlobalBudgetPrefix + now.UTC().Format(emailHourLayout)
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
		r.addEmail(c, who, in.Value, in.HumanProof)
	default:
		c.fail(mount.CodeBadRequest)
	}
	return c.result()
}

// addEmail starts the email branch of POST /identifiers: it sends a code to the
// address and keeps what is needed to check it. A deployment with no email
// channel has no way to prove an address, so it answers that the route is
// unavailable.
//
// An address another account holds as a proved identifier, or has reserved with
// an unproved contact row, is answered 409 email-taken and gets no code. The
// check runs before the resend window and the budgets, so a taken address costs
// nothing and a client learns at once that it cannot use it. A registration
// already refuses such an address when it inserts its reservation; this answer
// is the fallback for the race in which the address was taken after the code was
// requested, and for a row written before reservations existed. The write of the
// identifier at POST /identifiers/verify is the authoritative refusal, and it
// answers email-taken as well.
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
	// The gate is checked after the address is known to be free and before the
	// resend window, so a refused proof spends no window and no budget, and a
	// taken address is answered as taken without asking for one.
	if !r.checkHumanProof(c, proof) {
		return
	}

	// Another code within the interval is a 429, so a client cannot turn the
	// route into a flood of messages to one address. The window is held against
	// the address rather than the session: the claim is one atomic step, so a
	// second session — or a second account — naming the same address still meets
	// it. A store that cannot be claimed is a 503 before the code is drawn,
	// because a code that cannot be recorded is a message paid for and sent to no
	// use. The window is claimed after the address is known to be free, so a
	// taken address never spends it and every free address meets the same 429 on
	// a repeat.
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

	// The budgets bound what one inbox, one account, one request source and the
	// whole deployment may be sent. They are counted after the resend window is
	// claimed and before a code is drawn, so a request that never sends spends
	// none of them, and a refusal gives back every count it took. A counter that
	// cannot be advanced releases the claim and answers 503, because these budgets
	// are the only bound on messages to an address and a skipped count would leave
	// none.
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
		// No code was recorded, so the address is not held to the interval: the
		// claim is given back and the caller may ask again at once. The budget is
		// another matter: a failure that certifies nothing was sent gives back what
		// the attempt reserved, while an uncertain one keeps it, because the
		// message may have gone out.
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

// answerPending writes the answer of the email branch: a code was sent. It is
// one function so that every send answers with the same shape.
func (r *routes) answerPending(c *call, ttl, resend time.Duration) {
	c.json(http.StatusOK, addIdentifierResponse{
		State: identifierStatePending, ExpiresIn: int(seconds(ttl)), ResendAfter: int(seconds(resend)),
	})
}

// emailTakenByAnother reports whether an account other than who already holds
// email: a proved identifier, or an unproved reservation a self-registration
// wrote. Either is a contact the asking account cannot use, so the caller
// answers email-taken.
//
// An address neither table carries, and one this account already holds, are both
// sendable: the first is the ordinary case, and the second is a re-add of the
// account's own proof. An address the account module cannot normalize is treated
// as free here and refused by the write step if it is submitted. Every other
// failure is an outage and is handed back for the caller to answer 503: a read
// that cannot be made must not be read as "nobody holds it".
func (r *routes) emailTakenByAnother(ctx context.Context, email string, who authn.AccountID) (bool, error) {
	owner, err := r.deps.Accounts.Lookup(ctx, authn.IdentifierEmail, email)
	switch {
	case err == nil:
		// An address this account holds is a re-add of its own proof, and no
		// reservation of another account can outweigh that.
		return owner != who, nil
	case errors.Is(err, authn.ErrAccountNotFound), errors.Is(err, authn.ErrInvalidIdentifier):
	default:
		return false, err
	}
	reserved, ok, err := r.deps.Signup.PendingAccountByContact(ctx, identifierKindEmail, email)
	if errors.Is(err, authn.ErrInvalidIdentifier) {
		// The route's shape check is looser than the account module's
		// normalization, so a value this module refuses can reach here. It cannot
		// be reserved either: the write step refuses it as a bad request.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return ok && authn.AccountID(reserved) != who, nil
}

// emailBudget is one dimension of the email send budget: where its count lives,
// how many sends it admits and the window the count falls in.
type emailBudget struct {
	name   string
	key    string
	limit  int
	bucket time.Duration
}

// notSentCertifier is a channel that can certify a failed send left no message
// behind. The bundled channel publishes it; a sender that does not is treated as
// uncertain, so its failures keep the budget the attempt reserved.
type notSentCertifier interface {
	CertifiesNotSent(err error) bool
}

// emailCertifiesNotSent reports whether a failed send certifies that no message
// left. An error wrapping ErrEmailUncertain is never certified; beyond that, only
// a channel that publishes the classification is believed. Every other sender,
// and every error the channel did not certify, counts as uncertain.
func (r *routes) emailCertifiesNotSent(err error) bool {
	if errors.Is(err, authn.ErrEmailUncertain) {
		return false
	}
	certifier, ok := r.deps.EmailSender.(notSentCertifier)
	return ok && certifier.CertifiesNotSent(err)
}

// reserveEmailBudgets counts one send against the email budgets and reports
// whether every one of them admits it, returning the keys it took.
//
// The order is the narrowest bound first: hour and day per inbox, hour and day
// per account, hour and day per request source, hour for the deployment. Each is
// one atomic increment on a key of its own window. A count past its limit is the
// 429 the caller answers with the time left of that window, and a counter that
// cannot be advanced is a 503 and no send: the failure document's direction for a
// dependency that cannot answer, and the only bound there is on messages to an
// address.
//
// Either way the request sends nothing, so every key it took is given back
// before the answer. The source dimensions run only when the request carries an
// address; an empty source is fail-open, because folding every origin-less send
// into one key would make the budget fail-closed on a deployment that cannot
// resolve an address.
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

// refundEmailBudgetAttempts bounds how often a refund re-reads a count another
// writer changed between its read and its swap. Each attempt that loses saw a
// spendable count, so the retry is a real attempt and not a spin.
const refundEmailBudgetAttempts = 3

// refundEmailBudgets gives back one count on each key the request took. Each is a
// read followed by a compare-and-swap, never a negative increment: a window that
// has expired by the time the send falls over has no key left to give back, and
// an increment would recreate it and mint a send in the current window. A swap
// that loses a race is judged again against what the other writer left.
func (r *routes) refundEmailBudgets(ctx context.Context, keys []string) {
	for _, key := range keys {
		r.refundEmailBudget(ctx, key)
	}
}

// refundEmailBudget gives one count back on one key.
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

// untilNextEmailBucket is how long the window a count fell into has left: the
// delay a 429 states, never below one second so a client does not retry inside
// the same window.
func untilNextEmailBucket(now time.Time, bucket time.Duration) time.Duration {
	wait := now.Truncate(bucket).Add(bucket).Sub(now)
	if wait < time.Second {
		return time.Second
	}
	return wait
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
// parse is refused under the same login-failed code as a wrong code. The
// address another account holds never receives a code, so it has no record;
// answering a distinct code there would let an authenticated caller tell a held
// address from a free one. A code that does not match counts an attempt, and
// the record is spent once the attempts are used up, so a six-digit code cannot
// be guessed over its lifetime.
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
		c.unauthorized(mount.CodeLoginFailed)
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
		// The address was taken between the code going out and its answer. The
		// reservation row is written in the same transaction as the identifier,
		// so an address another account proved or reserved answers ErrContactTaken
		// and is the caller's to replace. Proving the account's own unproved row
		// succeeds in place, so ErrAccountExists is only a row written around the
		// reservation table, and stays the conflict it was before reservations
		// existed.
		if errors.Is(bindErr, authn.ErrContactTaken) {
			c.fail(mount.CodeEmailTaken)
			return c.result()
		}
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
