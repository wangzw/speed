package sms

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/sms"
)

// errCallerDefect is what a Send whose input breaks the caller's contract is
// wrapped in. It is an ordinary error and not one of the sentinels: the caller
// breached its side, which is a defect to fix and not a fact about the request.
var errCallerDefect = errors.New("authn.sms: the caller's input is not well formed")

// errPoolFull is what a Send that finds the queue full is wrapped in.
var errPoolFull = errors.New("the sending queue is full")

// errBudgetReadFailed marks a budget check that could not read its rows. It is
// an outage and not a spent budget, so it is recorded under no outcome rather
// than as budget-exhausted, which a dashboard would read as a policy refusal.
var errBudgetReadFailed = errors.New("a budget could not be read")

// Send checks the allowed table and every budget, writes the challenge row,
// hands the sending to the bounded worker pool and returns at once. A suppressed
// request — a number that is not registered while signup is closed — takes the
// same path and the same lock order as a real one, and differs in two ways the
// answer hides: its row is marked suppressed, so it does not spend the
// deployment-wide budget, and nothing is enqueued.
func (p *product) Send(ctx context.Context, in authn.SendCode) (string, error) {
	if err := checkSendCode(in); err != nil {
		return "", err
	}
	now := p.clock.Now()
	if !prefixAllowed(p.settings.allowedPrefixes(), in.Phone) {
		p.meters.sent(ctx, outcomeUnsupportedRegion)
		return "", authn.ErrUnsupportedRegion
	}
	// The queue is checked before anything is written, for a real send and a
	// suppressed one alike, so that a full queue is not an observation that tells
	// the two apart.
	if p.poolFull() {
		p.meters.sent(ctx, outcomeDropped)
		return "", p.unavailable(errPoolFull)
	}
	// The budgets' limits are read once, before the write transaction opens, so
	// that no settings read happens while an advisory lock is held.
	limits := p.readBudgetLimits(in.Purpose)

	code, err := generateCode(p.settings.codeLength())
	if err != nil {
		return "", p.unavailable(err)
	}
	id := event.NewID()
	ttl := p.settings.codeTTL()
	sourceMAC := p.sourceMAC(in.Source)
	row := challengeRow{
		ID:          id,
		Purpose:     in.Purpose,
		PhoneMAC:    p.database.BlindIndex(in.Phone),
		SourceMAC:   sourceMAC,
		AccountID:   accountOf(in),
		Suppressed:  !in.Deliver,
		CodeMAC:     p.database.BlindIndex(id + "|" + code),
		MaxAttempts: p.settings.codeAttempts(),
		CreatedAt:   now,
		ExpiresAt:   now.Add(ttl),
	}
	// The budget check runs inside the write transaction, after the locks the
	// ordered set takes, and the row is written only while those locks are held.
	// A refusal rolled back the transaction, so it wrote nothing.
	if err = p.register.insert(ctx, row, func(tx *gorm.DB) error {
		return p.checkBudgets(tx, in, sourceMAC, limits, now)
	}); err != nil {
		if isBudgetRefusal(err) {
			// A budget that could not be read is an outage, not a spent budget,
			// and is recorded under no outcome so the two stay apart on the
			// dashboard.
			if !errors.Is(err, errBudgetReadFailed) {
				p.meters.sent(ctx, outcomeBudgetExhausted)
			}
			return "", err
		}
		return "", p.unavailable(err)
	}

	if !in.Deliver {
		p.meters.sent(ctx, outcomeSuppressed)
		return id, nil
	}
	template := purposeTemplate(in.Purpose)
	// The message is resolved against the registry before it is queued: which
	// template a message goes out under is the registry's decision, and the
	// worker is handed the resolved form. A registry that will not build one —
	// the template is disabled, or its vendor review is not back — answers
	// ErrNotSendable, and the send records it under its own outcome. The row is
	// already written, so the answer to the caller is the same shape as a send
	// the carrier later refuses.
	resolved, err := p.registry.Resolve(sms.Message{
		To:       in.Phone,
		Template: template,
		Params:   map[string]string{"code": code, "ttl_minutes": ttlMinutes(ttl)},
		Locale:   resolveLocale(in.Locale, p.defaultLocale, p.localesByTemplate[template]),
		DedupeID: id,
	})
	if err != nil {
		outcome := resolveOutcome(err)
		p.meters.sent(ctx, outcome)
		p.warnNotSent(ctx, outcome, err)
		return id, nil
	}
	if p.enqueue(resolved) {
		// The outcome is recorded by the worker once the carrier answers.
		return id, nil
	}
	// The row is written and counted against every budget; only the sending was
	// dropped, because the queue filled between the check above and here. The
	// user resends.
	p.meters.sent(ctx, outcomeDropped)
	return id, nil
}

// resolveOutcome reads the outcome a registry refusal is recorded under. A
// template the registry will not send under — disabled, or not yet approved — is
// its own outcome, because the remedy is the template's state and not the
// message; a message the registry otherwise refuses has a shape the sending side
// will not take, and reads as rejected, the direction that records nothing as
// sent.
func resolveOutcome(err error) string {
	if errors.Is(err, sms.ErrNotSendable) {
		return outcomeNotSendable
	}
	return outcomeRejected
}

// checkSendCode holds the input to the caller's contract. The purpose is one of
// the closed set, the phone is a normalized number, and the account is set for
// the purposes that count against one. The locale is not held to a set here: it
// is a caller's preference for the message language, and the carrier's set — what
// a message can actually be sent in — is what resolves it, at send time. A breach
// of the checks below is a defect of the caller.
func checkSendCode(in authn.SendCode) error {
	if !validPurpose(in.Purpose) {
		return fmt.Errorf("%w: the purpose %q is not one of %s, %s and %s",
			errCallerDefect, in.Purpose, purposeLogin, purposeMFA, purposeEnroll)
	}
	if in.Phone == "" {
		return fmt.Errorf("%w: the phone number is empty", errCallerDefect)
	}
	if in.Purpose != purposeLogin && in.Account == "" {
		return fmt.Errorf("%w: the purpose %q counts against an account, and none is set",
			errCallerDefect, in.Purpose)
	}
	return nil
}

// accountOf returns the account the row carries. The login purpose has none.
func accountOf(in authn.SendCode) *string {
	if in.Purpose == purposeLogin || in.Account == "" {
		return nil
	}
	account := string(in.Account)
	return &account
}

// prefixAllowed reports whether the number begins with a prefix in the table. An
// empty table admits no number.
func prefixAllowed(prefixes []string, phone string) bool {
	for _, prefix := range prefixes {
		if prefix != "" && strings.HasPrefix(phone, prefix) {
			return true
		}
	}
	return false
}

// sourceMAC returns the keyed digest of a request's client address, or nil when
// the caller gave none. The address is normalised through netip first, so two
// spellings of one address share a source budget; one that does not parse is
// hashed as it stands, because a send whose origin the deployment cannot read
// still counts. The plaintext address is never written to the table and never
// logged.
func (p *product) sourceMAC(source string) *string {
	if source == "" {
		return nil
	}
	normalised := source
	if addr, err := netip.ParseAddr(source); err == nil {
		normalised = addr.String()
	}
	mac := p.database.BlindIndex(normalised)
	return &mac
}

// budgetLimits is every limit the budget check compares against, read once
// before the write transaction opens. Reading them first is what keeps a
// settings read out of the span in which an advisory lock is held.
type budgetLimits struct {
	resendInterval   time.Duration
	perPhoneHourly   int
	perPhoneDaily    int
	perSourceHourly  int
	perSourceDaily   int
	globalHourly     int
	perAccountHourly int
}

// readBudgetLimits reads every setting the budget check needs, including the
// global budget of the purpose the send carries.
func (p *product) readBudgetLimits(purpose string) budgetLimits {
	return budgetLimits{
		resendInterval:   p.settings.resendInterval(),
		perPhoneHourly:   p.settings.perPhoneHourly(),
		perPhoneDaily:    p.settings.perPhoneDaily(),
		perSourceHourly:  p.settings.perSourceHourly(),
		perSourceDaily:   p.settings.perSourceDaily(),
		globalHourly:     p.settings.globalHourly(purpose),
		perAccountHourly: p.settings.perAccountHourly(),
	}
}

// isBudgetRefusal reports whether a send error came from the budget check and
// already carries its answer, rather than from the write itself. Both a throttled
// budget and a count that could not be read arrive already shaped; anything else
// is a failed write.
func isBudgetRefusal(err error) bool {
	var throttled *authn.ThrottledError
	return errors.As(err, &throttled) || errors.Is(err, authn.ErrUnavailable)
}

// checkBudgets applies the budgets in order against the transaction the send
// holds its locks in, and returns the first that is over, or nil. Running on the
// transaction is what makes the cap hard: every count includes what a concurrent
// send of a shared dimension has committed, and the row guarded by the same
// transaction is written only while the ordered locks are held. The number,
// source and account budgets count suppressed rows too, so a window's rows reach
// the limit at the same count whether the numbers are registered or not. The
// global budget is the exception: it counts only unsuppressed rows, so anonymous
// requests for numbers nobody registered cannot exhaust the deployment, while the
// dimensions that stay asymmetric keep that fact unobservable. The source
// dimension runs only when the send carries a source; an empty source is
// fail-open and takes no source lock. Every dimension leaves out refunded rows:
// a message the carrier certainly did not take gives its spend back, while an
// uncertain outcome stays counted.
func (p *product) checkBudgets(tx *gorm.DB, in authn.SendCode, sourceMAC *string, limits budgetLimits, now time.Time) error {
	phoneMAC := p.database.BlindIndex(in.Phone)

	if interval := limits.resendInterval; interval > 0 {
		last, ok, err := p.register.lastCreatedForPhone(tx, phoneMAC)
		if err != nil {
			return p.budgetReadFailed(err)
		}
		if ok {
			if remaining := interval - now.Sub(last); remaining > 0 {
				return &authn.ThrottledError{RetryAfter: remaining}
			}
		}
	}

	hourlySince := now.Add(-time.Hour)
	dailySince := now.Add(-24 * time.Hour)

	if err := p.throttleOver(
		func() (int64, error) { return p.register.countPhoneSince(tx, phoneMAC, hourlySince) },
		func() (time.Time, bool, error) { return p.register.oldestPhoneSince(tx, phoneMAC, hourlySince) },
		limits.perPhoneHourly, time.Hour, now); err != nil {
		return err
	}
	if err := p.throttleOver(
		func() (int64, error) { return p.register.countPhoneSince(tx, phoneMAC, dailySince) },
		func() (time.Time, bool, error) { return p.register.oldestPhoneSince(tx, phoneMAC, dailySince) },
		limits.perPhoneDaily, 24*time.Hour, now); err != nil {
		return err
	}

	// The source dimension is applied only when the caller gave a source. An
	// empty source is fail-open: no source lock is taken and no source budget is
	// counted, so a deployment that cannot resolve the client's address does not
	// put every send under one key.
	if sourceMAC != nil {
		if err := p.throttleOver(
			func() (int64, error) { return p.register.countSourceSince(tx, *sourceMAC, hourlySince) },
			func() (time.Time, bool, error) { return p.register.oldestSourceSince(tx, *sourceMAC, hourlySince) },
			limits.perSourceHourly, time.Hour, now); err != nil {
			return err
		}
		if err := p.throttleOver(
			func() (int64, error) { return p.register.countSourceSince(tx, *sourceMAC, dailySince) },
			func() (time.Time, bool, error) { return p.register.oldestSourceSince(tx, *sourceMAC, dailySince) },
			limits.perSourceDaily, 24*time.Hour, now); err != nil {
			return err
		}
	}

	// The global budget is per purpose: anonymous login traffic cannot exhaust
	// the budget of a second factor or of an enrollment. It counts only rows that
	// are not suppressed, so a request for a number nobody registered does not
	// spend it; the number, account and source dimensions still count suppressed
	// rows, because a different point of refusal for a registered and an
	// unregistered number is a phone-enumeration oracle. A spent global budget
	// answers the same throttled error and Retry-After as the other dimensions.
	if limit := limits.globalHourly; limit > 0 {
		if err := p.throttleOver(
			func() (int64, error) { return p.register.countRealPurposeSince(tx, in.Purpose, hourlySince) },
			func() (time.Time, bool, error) { return p.register.oldestRealPurposeSince(tx, in.Purpose, hourlySince) },
			limit, time.Hour, now); err != nil {
			return err
		}
	}

	// The account budget is only for mfa and enroll: a login by phone has no
	// account the number was derived from, so counting one would tell a
	// registered number from an unregistered one.
	if in.Purpose != purposeLogin {
		account := string(in.Account)
		if err := p.throttleOver(
			func() (int64, error) { return p.register.countAccountSince(tx, account, hourlySince) },
			func() (time.Time, bool, error) { return p.register.oldestAccountSince(tx, account, hourlySince) },
			limits.perAccountHourly, time.Hour, now); err != nil {
			return err
		}
	}
	return nil
}

// throttleOver reads a count for a window and returns a throttled error when the
// count has reached the limit, with a retry after the moment the oldest counting
// row leaves the window. A failed read is an outage and not a pass. Both readers
// run on the send's transaction, the one its budget locks are held in.
func (p *product) throttleOver(
	count func() (int64, error),
	oldest func() (time.Time, bool, error),
	limit int,
	window time.Duration,
	now time.Time,
) error {
	n, err := count()
	if err != nil {
		return p.budgetReadFailed(err)
	}
	if n < int64(limit) {
		return nil
	}
	retry := window
	if t, ok, err := oldest(); err != nil {
		return p.budgetReadFailed(err)
	} else if ok {
		if remaining := t.Add(window).Sub(now); remaining > 0 {
			retry = remaining
		} else {
			retry = time.Second
		}
	}
	return &authn.ThrottledError{RetryAfter: retry}
}

// unavailable wraps a failure behind an unavailable answer. The direction is to
// refuse: a dependency that cannot be read must send nothing and let nothing
// through.
func (p *product) unavailable(err error) error {
	return fmt.Errorf("%w: %s: %w", authn.ErrUnavailable, ModuleName, err)
}

// budgetReadFailed wraps a failure to read a budget's rows, so that it answers
// unavailable like any outage but is told apart from a spent budget.
func (p *product) budgetReadFailed(err error) error {
	return p.unavailable(fmt.Errorf("%w: %w", errBudgetReadFailed, err))
}
