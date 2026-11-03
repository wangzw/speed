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

// Send checks the allowed table and every budget, writes the challenge row and
// one attempt in the shared ledger, hands the sending to the bounded worker pool
// and returns at once. A send that certainly did not leave — a registry that will
// not build the message, or a queue that filled before the enqueue — refunds the
// row, so a message the user never received does not spend the budget. A
// suppressed request — a number that is not registered while signup is closed —
// takes the same path and the same lock order as a real one, and differs in two
// ways the answer hides: its row is marked suppressed, so it does not spend the
// deployment-wide budget, and nothing is enqueued.
func (p *product) Send(ctx context.Context, in authn.SendCode) (string, error) {
	if err := checkSendCode(in); err != nil {
		return "", err
	}
	// The human-verification gate runs before the allowed-prefix table, the
	// queue and every budget, so a refused proof spends nothing and tells the
	// caller nothing about the number. The proof is checked here and nowhere on
	// the caller's side: it is single-use, and a second check would consume it
	// twice.
	if err := p.checkProof(ctx, in); err != nil {
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
	// The shared ledger records the attempt before the carrier is asked. A
	// suppressed request never reaches the carrier, so its row is marked
	// suppressed and written already refunded: it spends no send budget, no
	// share of the ceiling and no attempt budget, but the number, source and
	// account dimensions still count it, so the point of refusal does not tell a
	// registered number from an unregistered one. An attempt a budget refuses
	// writes no row at all, because the refusal rolls the transaction back.
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
	// The budget check runs inside the write transaction, after the locks the
	// ordered set takes, and the row is written only while those locks are held.
	// A refusal rolled back the transaction, so it wrote nothing.
	if _, err = p.register.insert(ctx, row, &attempt, func(tx *gorm.DB) error {
		return p.checkBudgets(ctx, tx, in, sourceMAC, limits, now)
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
	// the carrier later refuses; nothing reached the carrier, so the row's spend
	// is given back and its code is spent.
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
		// The outcome is recorded by the worker once the carrier answers.
		return id, nil
	}
	// The row is written and counted against every budget; only the sending was
	// dropped, because the queue filled between the check above and here.
	// Nothing reached the carrier, so the row's spend is given back and its code
	// is spent; the user resends without being charged twice.
	p.meters.sent(ctx, outcomeDropped)
	p.refundChallenge(ctx, id)
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

// checkProof holds a send to the deployment's human-verification gate, if it
// has one. A gate that requires nothing, and an assembly with no gate, are
// skipped entirely: the send goes on as it did before. A proof over the field's
// bound is a bad request before the provider is asked. The provider's own
// failures are carried out as they are: a missing, invalid, expired or used
// proof is ErrHumanVerificationRequired, an over-long proof is
// ErrHumanProofTooLong, and a provider or store that cannot answer is
// ErrUnavailable. None of them has spent any budget.
func (p *product) checkProof(ctx context.Context, in authn.SendCode) error {
	if p.human == nil || !p.human.Required(authn.HumanScopePhone) {
		return nil
	}
	if len(in.Proof) > mount.HumanProofMaxBytes {
		return fmt.Errorf("%w: the human-verification proof is over its limit", authn.ErrHumanProofTooLong)
	}
	return p.human.Verify(ctx, authn.HumanProof{Token: in.Proof, RemoteIP: in.Source}, authn.HumanScopePhone)
}

// checkSendCode holds the input to the caller's contract. The purpose is one of
// the closed set, the phone is a normalized number, and the account is set for
// the purposes that count against one. The locale is not held to a set here: it
// is a caller's preference for the message language, and the carrier's set — what
// a message can actually be sent in — is what resolves it, at send time. A breach
// of the checks below is a defect of the caller.
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

// accountOf returns the account the row carries. The login and reset purposes
// have none: neither has an account the number was derived from, so a row for
// either fills no account whatever the caller set.
func accountOf(in authn.SendCode) *string {
	if in.Purpose == purposeLogin || in.Purpose == purposeReset || in.Account == "" {
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
	// ceilingHourly is the shared cross-channel ceiling, the first budget every
	// send is held to.
	ceilingHourly int
}

// readBudgetLimits reads every setting the budget check needs, including the
// global send and attempt budgets of the purpose the send carries.
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

// budgetCheck is one budget a send is held to: the ledger query that reads it,
// the limit it is compared against, the metric dimension it reports under and the
// window it slides over. distinct counts different destinations rather than
// rows, which is the shape the per-source diversity budget needs.
type budgetCheck struct {
	query     budget.Query
	limit     int
	dimension string
	window    time.Duration
	distinct  bool
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
// holds its locks in, and returns the first that is over, or nil. Every count is
// read from the shared ledger through a store bound to that transaction, so the
// cap is hard: a count includes what a concurrent send of a shared dimension has
// committed, and the row guarded by the same transaction is written only while
// the ordered locks are held. The number, source, account and
// distinct-destination budgets count suppressed rows too, so a window's rows
// reach the limit at the same count whether the numbers are registered or not.
// The global budget is the exception: it counts only unsuppressed rows, so
// anonymous requests for numbers nobody registered cannot exhaust the
// deployment, while the dimensions that stay asymmetric keep that fact
// unobservable. The source dimension runs only when the send carries a source;
// an empty source is fail-open and takes no source lock. The send dimensions
// leave out refunded rows: a message the carrier certainly did not take gives its
// spend back, while an uncertain outcome stays counted. The distinct-destination
// dimension is one of those send dimensions: it counts the different numbers a
// source sent to in the window, and a refund removes a destination from it. The
// attempt dimensions are the exception, and count the other way: they count only
// a row a certain failure refunded and never a suppressed one, so a destination
// or carrier that always fails is what spends the attempt budget, while a send
// that goes out does not. The deployment-wide attempt dimension keeps the global
// budget's asymmetry for the same reason.
//
// The shared cross-channel ceiling is consulted before every one of them. It is
// the one count the authentication and notification channels share, read from
// the ledger rather than from this module's challenge table, and an attempt a
// certain failure refunded does not spend it.
//
// The resend interval is checked before the dimensions. It is not a budget
// dimension but its own limit, measured from the newest attempt to the number:
// every attempt counts, refunded or suppressed, because the interval is about
// how often a code was asked for and must not differ between a registered and
// an unregistered number.
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

	// The number's two windows. A suppressed row counts: the number budget is
	// what keeps the point of refusal from telling a registered number from an
	// unregistered one.
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
		// The attempt dimension counts determinate failures and never a
		// suppressed row, which is what keeps a source from cycling destinations
		// that always fail without spending a diversity budget.
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

	// The global budget is per purpose: anonymous login traffic cannot exhaust
	// the budget of a second factor or of an enrollment. It counts only rows that
	// are not suppressed, so a request for a number nobody registered does not
	// spend it; the number, account and source dimensions still count suppressed
	// rows, because a different point of refusal for a registered and an
	// unregistered number is a phone-enumeration oracle. A spent global budget
	// answers the same throttled error and Retry-After as the other dimensions.
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

	// The deployment-wide attempt budget of the purpose counts the unsuppressed
	// rows a certain failure refunded, so a send that went out does not spend it.
	// It shares the global budget's asymmetry: a suppressed row reached no
	// carrier, so anonymous requests for numbers nobody registered cannot
	// exhaust it. The source and account dimensions still count suppressed rows,
	// so refusal does not tell a registered number from an unregistered one.
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

	// The account budget is only for mfa and enroll: a login by phone and an
	// anonymous reset have no account the number was derived from, so counting
	// one would tell a registered number from an unregistered one.
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

// throttleCeiling applies the cross-channel ceiling, the first budget every send
// is held to. It counts the ledger's non-refunded attempts over both channels;
// the ledger's oldest such attempt is when the window frees a slot, and the
// refusal carries that moment as its retry. A ledger that cannot be read is an
// outage and not a pass.
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

// throttleOver reads one budget from the ledger and returns a throttled error
// when the count has reached the limit, with a retry after the moment the oldest
// counting row leaves the window. A failed read is an outage and not a pass. The
// ledger is bound to the send's transaction, the one its budget locks are held
// in, so the count and the row the send is about to write are one decision. The
// count is reported to the utilization gauge before it is compared, so the share
// in use is visible whether or not the send is refused.
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
