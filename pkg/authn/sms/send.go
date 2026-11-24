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
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/sms/budget"
)

var errCallerDefect = errors.New("authn.sms: the caller's input is not well formed")

var errPoolFull = errors.New("the sending queue is full")

var errBudgetReadFailed = errors.New("a budget could not be read")

// Send checks the gate and every budget, writes the challenge row and one attempt in the shared ledger, and queues the send. A send that certainly did not leave refunds the row, and a suppressed request takes the same path without spending the deployment-wide budget or enqueuing.
func (p *product) Send(ctx context.Context, in authn.SendCode) (string, error) {
	if err := checkSendCode(in); err != nil {
		return "", err
	}
	// The gate runs before the allowed table, the queue and every budget, so a refused proof spends nothing; the proof is single-use and is checked only here.
	if err := p.checkProof(ctx, in); err != nil {
		return "", err
	}
	now := p.clock.Now()
	if !prefixAllowed(p.settings.allowedPrefixes(), in.Phone) {
		p.meters.sent(ctx, outcomeUnsupportedRegion)
		return "", authn.ErrUnsupportedRegion
	}
	// The queue is checked before anything is written for a real and a suppressed send alike, so a full queue cannot tell the two apart.
	if p.poolFull() {
		p.meters.sent(ctx, outcomeDropped)
		return "", p.unavailable(errPoolFull)
	}
	// Budget limits are read before the write transaction opens, so no settings read happens while an advisory lock is held.
	limits := p.readBudgetLimits(in.Purpose)

	code, err := generateCode(p.settings.codeLength())
	if err != nil {
		return "", p.unavailable(err)
	}
	id := event.NewID()
	ttl := p.settings.codeTTL()
	sourceMAC := p.sourceMAC(in.Source)
	phoneMAC := p.database.BlindIndex(in.Phone)
	row := challengeRow{
		ID:          id,
		Purpose:     in.Purpose,
		PhoneMAC:    phoneMAC,
		SourceMAC:   sourceMAC,
		AccountID:   accountOf(in),
		Suppressed:  !in.Deliver,
		CodeMAC:     p.database.BlindIndex(id + "|" + code),
		MaxAttempts: p.settings.codeAttempts(),
		CreatedAt:   now,
		ExpiresAt:   now.Add(ttl),
	}
	// A suppressed row is written already refunded and counts only the number, source and account dimensions, so the point of refusal does not tell a registered number from an unregistered one.
	attempt := budget.Attempt{
		Channel:        budget.ChannelAuthn,
		Purpose:        in.Purpose,
		SourceMAC:      sourceMAC,
		DestinationMAC: phoneMAC,
		AccountID:      accountOf(in),
		AttemptedAt:    now,
		Outcome:        budget.OutcomePending,
		Suppressed:     !in.Deliver,
	}
	if !in.Deliver {
		refunded := now
		attempt.RefundedAt = &refunded
	}
	if _, err = p.register.insert(ctx, row, &attempt, func(tx *gorm.DB) error {
		return p.checkBudgets(ctx, tx, in, sourceMAC, limits, now)
	}); err != nil {
		if isBudgetRefusal(err) {
			// A budget that could not be read is an outage, recorded under no outcome.
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
	// The registry resolves the message before it is queued; a refusal records ErrNotSendable, refunds the row and spends its code.
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
		p.refundChallenge(ctx, id)
		return id, nil
	}
	if p.enqueue(resolved) {
		return id, nil
	}
	// A row written before a full queue is refunded and its code spent, because nothing reached the carrier.
	p.meters.sent(ctx, outcomeDropped)
	p.refundChallenge(ctx, id)
	return id, nil
}

func resolveOutcome(err error) string {
	if errors.Is(err, sms.ErrNotSendable) {
		return outcomeNotSendable
	}
	return outcomeRejected
}

func (p *product) checkProof(ctx context.Context, in authn.SendCode) error {
	if p.human == nil || !p.human.Required(authn.HumanScopePhone) {
		return nil
	}
	if len(in.Proof) > mount.HumanProofMaxBytes {
		return fmt.Errorf("%w: the human-verification proof is over its limit", authn.ErrHumanProofTooLong)
	}
	return p.human.Verify(ctx, authn.HumanProof{Token: in.Proof, RemoteIP: in.Source}, authn.HumanScopePhone)
}

func checkSendCode(in authn.SendCode) error {
	if !validPurpose(in.Purpose) {
		return fmt.Errorf("%w: the purpose %q is not one of %s, %s, %s and %s",
			errCallerDefect, in.Purpose, purposeLogin, purposeMFA, purposeEnroll, purposeReset)
	}
	if in.Phone == "" {
		return fmt.Errorf("%w: the phone number is empty", errCallerDefect)
	}
	if in.Purpose != purposeLogin && in.Purpose != purposeReset && in.Account == "" {
		return fmt.Errorf("%w: the purpose %q counts against an account, and none is set",
			errCallerDefect, in.Purpose)
	}
	return nil
}

func accountOf(in authn.SendCode) *string {
	if in.Purpose == purposeLogin || in.Purpose == purposeReset || in.Account == "" {
		return nil
	}
	account := string(in.Account)
	return &account
}

func prefixAllowed(prefixes []string, phone string) bool {
	for _, prefix := range prefixes {
		if prefix != "" && strings.HasPrefix(phone, prefix) {
			return true
		}
	}
	return false
}

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

type budgetLimits struct {
	resendInterval          time.Duration
	perPhoneHourly          int
	perPhoneDaily           int
	perSourceHourly         int
	perSourceDaily          int
	perSourceDistinctHourly int
	perSourceDistinctDaily  int
	perSourceAttemptHourly  int
	globalHourly            int
	globalAttemptHourly     int
	perAccountHourly        int
	ceilingHourly           int
}

func (p *product) readBudgetLimits(purpose string) budgetLimits {
	return budgetLimits{
		resendInterval:          p.settings.resendInterval(),
		perPhoneHourly:          p.settings.perPhoneHourly(),
		perPhoneDaily:           p.settings.perPhoneDaily(),
		perSourceHourly:         p.settings.perSourceHourly(),
		perSourceDaily:          p.settings.perSourceDaily(),
		perSourceDistinctHourly: p.settings.perSourceDistinctHourly(),
		perSourceDistinctDaily:  p.settings.perSourceDistinctDaily(),
		perSourceAttemptHourly:  p.settings.perSourceAttemptHourly(),
		globalHourly:            p.settings.globalHourly(purpose),
		globalAttemptHourly:     p.settings.globalAttemptHourly(purpose),
		perAccountHourly:        p.settings.perAccountHourly(),
		ceilingHourly:           p.settings.ceilingHourly(),
	}
}

type budgetCheck struct {
	query     budget.Query
	limit     int
	dimension string
	window    time.Duration
	distinct  bool
}

func isBudgetRefusal(err error) bool {
	var throttled *authn.ThrottledError
	return errors.As(err, &throttled) || errors.Is(err, authn.ErrUnavailable)
}

func (p *product) checkBudgets(ctx context.Context, tx *gorm.DB, in authn.SendCode, sourceMAC *string, limits budgetLimits, now time.Time) error {
	phoneMAC := p.database.BlindIndex(in.Phone)
	ledger := budget.New(tx)

	hourlySince := now.Add(-time.Hour)
	dailySince := now.Add(-24 * time.Hour)

	if limit := limits.ceilingHourly; limit > 0 {
		if err := p.throttleCeiling(ctx, ledger, limit, hourlySince, now); err != nil {
			return err
		}
	}

	if interval := limits.resendInterval; interval > 0 {
		last, ok, err := ledger.Latest(ctx, budget.Query{
			Channel:           budget.ChannelAuthn,
			Dimension:         budget.DimensionDestination,
			Value:             phoneMAC,
			Since:             now.Add(-interval),
			IncludeRefunded:   true,
			IncludeSuppressed: true,
		})
		if err != nil {
			return p.budgetReadFailed(err)
		}
		if ok {
			if remaining := interval - now.Sub(last); remaining > 0 {
				return &authn.ThrottledError{RetryAfter: remaining}
			}
		}
	}

	phone := budget.Query{
		Channel:           budget.ChannelAuthn,
		Dimension:         budget.DimensionDestination,
		Value:             phoneMAC,
		Since:             hourlySince,
		IncludeSuppressed: true,
	}
	phoneDaily := phone
	phoneDaily.Since = dailySince

	checks := []budgetCheck{
		{query: phone, limit: limits.perPhoneHourly, dimension: dimensionPhone, window: time.Hour},
		{query: phoneDaily, limit: limits.perPhoneDaily, dimension: dimensionPhone, window: 24 * time.Hour},
	}

	if sourceMAC != nil {
		source := budget.Query{
			Channel:           budget.ChannelAuthn,
			Dimension:         budget.DimensionSource,
			Value:             *sourceMAC,
			Since:             hourlySince,
			IncludeSuppressed: true,
		}
		sourceDaily := source
		sourceDaily.Since = dailySince
		sourceAttempts := budget.Query{
			Channel:      budget.ChannelAuthn,
			Dimension:    budget.DimensionSource,
			Value:        *sourceMAC,
			Since:        hourlySince,
			OnlyRefunded: true,
		}
		checks = append(checks,
			budgetCheck{query: source, limit: limits.perSourceHourly, dimension: dimensionSource, window: time.Hour},
			budgetCheck{query: sourceDaily, limit: limits.perSourceDaily, dimension: dimensionSource, window: 24 * time.Hour},
			budgetCheck{query: source, limit: limits.perSourceDistinctHourly, dimension: dimensionSourceDistinct, window: time.Hour, distinct: true},
			budgetCheck{query: sourceDaily, limit: limits.perSourceDistinctDaily, dimension: dimensionSourceDistinct, window: 24 * time.Hour, distinct: true},
			budgetCheck{query: sourceAttempts, limit: limits.perSourceAttemptHourly, dimension: dimensionSourceAttempt, window: time.Hour},
		)
	}

	// The global budget is per purpose and counts only unsuppressed rows; the number, account and source dimensions count suppressed rows so refusal is not an enumeration oracle.
	if limit := limits.globalHourly; limit > 0 {
		checks = append(checks, budgetCheck{
			query: budget.Query{
				Channel: budget.ChannelAuthn,
				Purpose: in.Purpose,
				Since:   hourlySince,
			},
			limit:     limit,
			dimension: dimensionGlobal,
			window:    time.Hour,
		})
	}

	if limit := limits.globalAttemptHourly; limit > 0 {
		checks = append(checks, budgetCheck{
			query: budget.Query{
				Channel:      budget.ChannelAuthn,
				Purpose:      in.Purpose,
				Since:        hourlySince,
				OnlyRefunded: true,
			},
			limit:     limit,
			dimension: dimensionGlobalAttempt,
			window:    time.Hour,
		})
	}

	// The account budget covers only mfa and enroll: a login or reset has no account the number was derived from.
	if in.Purpose == purposeMFA || in.Purpose == purposeEnroll {
		checks = append(checks, budgetCheck{
			query: budget.Query{
				Channel:           budget.ChannelAuthn,
				Dimension:         budget.DimensionAccount,
				Value:             string(in.Account),
				Since:             hourlySince,
				IncludeSuppressed: true,
			},
			limit:     limits.perAccountHourly,
			dimension: dimensionAccount,
			window:    time.Hour,
		})
	}

	for _, check := range checks {
		if err := p.throttleOver(ctx, ledger, in.Purpose, check, now); err != nil {
			return err
		}
	}
	return nil
}

func (p *product) throttleCeiling(ctx context.Context, ledger budget.Store, limit int, since, now time.Time) error {
	n, err := ledger.CountCeiling(ctx, since)
	if err != nil {
		return p.budgetReadFailed(err)
	}
	p.meters.budget(ctx, purposeShared, dimensionCeiling, n, limit)
	if n < int64(limit) {
		return nil
	}
	retry := time.Hour
	oldest, ok, err := ledger.Oldest(ctx, budget.Query{Since: since})
	if err != nil {
		return p.budgetReadFailed(err)
	}
	if ok {
		if remaining := oldest.Add(time.Hour).Sub(now); remaining > 0 {
			retry = remaining
		} else {
			retry = time.Second
		}
	}
	return &authn.ThrottledError{RetryAfter: retry}
}

func (p *product) throttleOver(ctx context.Context, ledger budget.Store, purpose string, check budgetCheck, now time.Time) error {
	var n int64
	var err error
	if check.distinct {
		n, err = ledger.CountDistinctDestinations(ctx, check.query)
	} else {
		n, err = ledger.Count(ctx, check.query)
	}
	if err != nil {
		return p.budgetReadFailed(err)
	}
	p.meters.budget(ctx, purpose, check.dimension, n, check.limit)
	if n < int64(check.limit) {
		return nil
	}
	retry := check.window
	if oldest, ok, err := ledger.Oldest(ctx, check.query); err != nil {
		return p.budgetReadFailed(err)
	} else if ok {
		if remaining := oldest.Add(check.window).Sub(now); remaining > 0 {
			retry = remaining
		} else {
			retry = time.Second
		}
	}
	return &authn.ThrottledError{RetryAfter: retry}
}

func (p *product) unavailable(err error) error {
	return fmt.Errorf("%w: %s: %w", authn.ErrUnavailable, ModuleName, err)
}

func (p *product) budgetReadFailed(err error) error {
	return p.unavailable(fmt.Errorf("%w: %w", errBudgetReadFailed, err))
}
