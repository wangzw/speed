package sms

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
// same path and the same budgets and differs only in that nothing is enqueued,
// so its answer is the same shape as a real one.
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
	if err := p.checkBudgets(ctx, in, now); err != nil {
		// A budget that could not be read is an outage, not a spent budget, and
		// is recorded under no outcome so the two stay apart on the dashboard.
		if !errors.Is(err, errBudgetReadFailed) {
			p.meters.sent(ctx, outcomeBudgetExhausted)
		}
		return "", err
	}

	code, err := generateCode(p.settings.codeLength())
	if err != nil {
		return "", p.unavailable(err)
	}
	id := event.NewID()
	ttl := p.settings.codeTTL()
	row := challengeRow{
		ID:          id,
		Purpose:     in.Purpose,
		PhoneMAC:    p.database.BlindIndex(in.Phone),
		AccountID:   accountOf(in),
		Suppressed:  !in.Deliver,
		CodeMAC:     p.database.BlindIndex(id + "|" + code),
		MaxAttempts: p.settings.codeAttempts(),
		CreatedAt:   now,
		ExpiresAt:   now.Add(ttl),
	}
	if err := p.register.insert(ctx, row); err != nil {
		return "", p.unavailable(err)
	}

	if !in.Deliver {
		p.meters.sent(ctx, outcomeSuppressed)
		return id, nil
	}
	msg := sms.Message{
		To:       in.Phone,
		Template: purposeTemplate(in.Purpose),
		Params:   map[string]string{"code": code, "ttl_minutes": ttlMinutes(ttl)},
		Locale:   localeOf(in.Locale),
		DedupeID: id,
	}
	if p.enqueue(msg) {
		// The outcome is recorded by the worker once the carrier answers.
		return id, nil
	}
	// The row is written and counted against every budget; only the sending was
	// dropped, because the queue filled between the check above and here. The
	// user resends.
	p.meters.sent(ctx, outcomeDropped)
	return id, nil
}

// checkSendCode holds the input to the caller's contract. The purpose is one of
// the closed set, the phone is a normalized number, the locale is empty or one
// of the two languages, and the account is set for the purposes that count
// against one. A breach is a defect of the caller.
func checkSendCode(in authn.SendCode) error {
	if !validPurpose(in.Purpose) {
		return fmt.Errorf("%w: the purpose %q is not one of %s, %s and %s",
			errCallerDefect, in.Purpose, purposeLogin, purposeMFA, purposeEnroll)
	}
	if in.Phone == "" {
		return fmt.Errorf("%w: the phone number is empty", errCallerDefect)
	}
	if !validLocale(in.Locale) {
		return fmt.Errorf("%w: the locale %q is not one of %s and %s",
			errCallerDefect, in.Locale, localeChinese, localeEnglish)
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

// checkBudgets applies the budgets in order and returns the first that is over,
// or nil. Every budget is a count over the challenge rows, so a suppressed row
// pays a real one: a window's rows reach the limit at the same count whether the
// numbers are registered or not.
func (p *product) checkBudgets(ctx context.Context, in authn.SendCode, now time.Time) error {
	phoneMAC := p.database.BlindIndex(in.Phone)

	if interval := p.settings.resendInterval(); interval > 0 {
		last, ok, err := p.register.lastCreatedForPhone(ctx, phoneMAC)
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

	if err := p.throttleOver(ctx,
		func() (int64, error) { return p.register.countPhoneSince(ctx, phoneMAC, hourlySince) },
		func() (time.Time, bool, error) { return p.register.oldestPhoneSince(ctx, phoneMAC, hourlySince) },
		p.settings.perPhoneHourly(), time.Hour, now); err != nil {
		return err
	}
	if err := p.throttleOver(ctx,
		func() (int64, error) { return p.register.countPhoneSince(ctx, phoneMAC, dailySince) },
		func() (time.Time, bool, error) { return p.register.oldestPhoneSince(ctx, phoneMAC, dailySince) },
		p.settings.perPhoneDaily(), 24*time.Hour, now); err != nil {
		return err
	}

	// The global budget is per purpose: anonymous login traffic cannot exhaust
	// the budget of a second factor or of an enrollment. It answers unavailable,
	// not throttled.
	if limit := p.settings.globalHourly(in.Purpose); limit > 0 {
		n, err := p.register.countPurposeSince(ctx, in.Purpose, hourlySince)
		if err != nil {
			return p.budgetReadFailed(err)
		}
		if n >= int64(limit) {
			return p.unavailable(fmt.Errorf("the hourly budget of the %s purpose is spent", in.Purpose))
		}
	}

	// The account budget is only for mfa and enroll: a login by phone has no
	// account the number was derived from, so counting one would tell a
	// registered number from an unregistered one.
	if in.Purpose != purposeLogin {
		account := string(in.Account)
		if err := p.throttleOver(ctx,
			func() (int64, error) { return p.register.countAccountSince(ctx, account, hourlySince) },
			func() (time.Time, bool, error) { return p.register.oldestAccountSince(ctx, account, hourlySince) },
			p.settings.perAccountHourly(), time.Hour, now); err != nil {
			return err
		}
	}
	return nil
}

// throttleOver reads a count for a window and returns a throttled error when the
// count has reached the limit, with a retry after the moment the oldest counting
// row leaves the window. A failed read is an outage and not a pass.
func (p *product) throttleOver(
	ctx context.Context,
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
