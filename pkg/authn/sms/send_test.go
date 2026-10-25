package sms

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/sms"
)

// TestAnAllowedSendWritesARowAndQueuesAMessage holds the happy path: the allowed
// table admits the number, a challenge row is written with a keyed digest of the
// code, and the message is queued for the carrier with the code the row was
// written for.
func TestAnAllowedSendWritesARowAndQueuesAMessage(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()

	id, err := r.sendLogin()
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id == "" {
		t.Fatal("Send returned an empty challenge identifier")
	}
	if got := len(r.p.pool.queue); got != 1 {
		t.Fatalf("the queue holds %d messages, want 1", got)
	}
	msg := <-r.p.pool.queue
	if msg.To != phone || msg.Template != templateLogin || msg.DedupeID != id {
		t.Errorf("queued message = %+v, want To %s, Template %s, DedupeID %s", msg, phone, templateLogin, id)
	}
	if _, ok := msg.Params["code"]; !ok {
		t.Errorf("the queued message carries no code: %+v", msg.Params)
	}
	if _, ok := msg.Params["ttl_minutes"]; !ok {
		t.Errorf("the queued message carries no ttl_minutes: %+v", msg.Params)
	}
	if n := r.rowCount(); n != 1 {
		t.Fatalf("the table holds %d rows, want 1", n)
	}
	if suppressed, _ := r.boolColumn(id, "suppressed"); suppressed {
		t.Errorf("a delivered challenge is marked suppressed")
	}
	if got := r.counter(sendsName, attribute.String(outcomeAttribute, outcomeSent)); got != 0 {
		t.Errorf("outcome sent recorded %d before the worker ran, want 0", got)
	}
}

// TestASuppressedSendTakesTheSamePathAndQueuesNothing holds the suppression:
// Deliver false writes a row marked suppressed, spends the budget like a real
// one, and enqueues nothing — the only difference between the two, and one the
// caller cannot see in the answer.
func TestASuppressedSendTakesTheSamePathAndQueuesNothing(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()

	id, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin,
		Phone:   phone,
		Deliver: false,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id == "" {
		t.Fatal("a suppressed Send returned an empty challenge identifier")
	}
	if got := len(r.p.pool.queue); got != 0 {
		t.Fatalf("a suppressed send queued %d messages, want none", got)
	}
	if got := r.rowCount(); got != 1 {
		t.Fatalf("the table holds %d rows, want 1", got)
	}
	if suppressed, present := r.boolColumn(id, "suppressed"); !present || !suppressed {
		t.Errorf("the suppressed challenge is not marked suppressed (present %v)", present)
	}
	if got := r.counter(sendsName, attribute.String(outcomeAttribute, outcomeSuppressed)); got != 1 {
		t.Errorf("outcome suppressed = %d, want 1", got)
	}
}

// TestTheEmptyAllowedTableRefusesEveryNumber holds the closed default: an empty
// allowed table admits no number, and the refusal is ErrUnsupportedRegion with
// no row written.
func TestTheEmptyAllowedTableRefusesEveryNumber(t *testing.T) {
	r := newRigSQLite(t)
	_, err := r.sendLogin()
	if !errors.Is(err, authn.ErrUnsupportedRegion) {
		t.Fatalf("Send with an empty allowed table returned %v, want ErrUnsupportedRegion", err)
	}
	if got := r.rowCount(); got != 0 {
		t.Errorf("a refused send wrote %d rows, want 0", got)
	}
	if got := r.counter(sendsName, attribute.String(outcomeAttribute, outcomeUnsupportedRegion)); got != 1 {
		t.Errorf("outcome unsupported-region = %d, want 1", got)
	}
}

// TestANumberOutsideTheAllowedTableIsRefused holds the prefix test: a number
// whose prefix is not in the table is refused, whatever its registration.
func TestANumberOutsideTheAllowedTableIsRefused(t *testing.T) {
	r := newRigSQLite(t)
	r.store.set(allowedPrefixesPath, `["+1"]`)
	_, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin,
		Phone:   phone,
		Deliver: true,
	})
	if !errors.Is(err, authn.ErrUnsupportedRegion) {
		t.Fatalf("Send with a number outside the table returned %v, want ErrUnsupportedRegion", err)
	}
}

// TestTheCallerContractIsEnforced holds the input checks: a purpose outside the
// set, an empty number and a missing account for mfa are defects of the caller
// and are ordinary errors, not sentinels. The locale is deliberately not one of
// them: a language the carrier does not declare is a preference that resolves to
// the default, not a defect to refuse.
func TestTheCallerContractIsEnforced(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	cases := []struct {
		name string
		in   authn.SendCode
	}{
		{"purpose", authn.SendCode{Purpose: "reset", Phone: phone, Deliver: true}},
		// A purpose outside the set with an account set: the account check that
		// follows does not catch this one, so the closed-purpose check is what
		// has to refuse it.
		{"purpose-with-account", authn.SendCode{Purpose: "reset", Phone: phone, Account: acct, Deliver: true}},
		{"phone", authn.SendCode{Purpose: authn.SMSPurposeLogin, Phone: "", Deliver: true}},
		{"account", authn.SendCode{Purpose: authn.SMSPurposeMFA, Phone: phone, Deliver: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := r.p.Send(context.Background(), c.in)
			if !errors.Is(err, errCallerDefect) {
				t.Fatalf("Send returned %v, want a caller defect", err)
			}
			if errors.Is(err, authn.ErrUnsupportedRegion) || errors.Is(err, authn.ErrThrottled) ||
				errors.Is(err, authn.ErrUnavailable) {
				t.Errorf("a caller defect reads as a sentinel: %v", err)
			}
		})
	}
	if got := r.rowCount(); got != 0 {
		t.Errorf("a caller defect wrote %d rows, want 0", got)
	}
}

// TestAResendVoidsThePreviousChallengeOfTheNumber holds the void: a second send
// to the same number and purpose marks the earlier live row consumed, so two
// codes are never valid at once. A row of another purpose is left alone.
func TestAResendVoidsThePreviousChallengeOfTheNumber(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()

	first, err := r.sendLogin()
	if err != nil {
		t.Fatalf("the first Send: %v", err)
	}
	// Move past the resend interval so the second send is not throttled.
	r.clock.advance(2 * time.Minute)
	second, err := r.sendLogin()
	if err != nil {
		t.Fatalf("the second Send: %v", err)
	}
	// A row of another purpose for the same number is not voided.
	r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeMFA, Phone: phone, Account: acct, Deliver: true,
	})

	consumed, _ := r.column(first, "consumed_at")
	if consumed == nil {
		t.Errorf("the first challenge was not voided by the resend")
	}
	secondConsumed, _ := r.column(second, "consumed_at")
	if secondConsumed != nil {
		t.Errorf("the second challenge is already consumed")
	}
}

// TestNoPlaintextCodeReachesTheTable holds the digest: the row carries the code
// only as a keyed digest, and the code text appears in no column of any row.
func TestNoPlaintextCodeReachesTheTable(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	if _, err := r.sendLogin(); err != nil {
		t.Fatalf("Send: %v", err)
	}
	msg := <-r.p.pool.queue
	code := msg.Params["code"]
	if code == "" || len(code) != 6 {
		t.Fatalf("the queued code is %q, want six digits", code)
	}
	if strings.Contains(r.allText(), code) {
		t.Errorf("the plaintext code %q appears in the table", code)
	}
}

// TestARegisteredAndASuppressedNumberTakeTheSameCountingPath holds the
// enumeration defense: the two requests differ only in whether a message is
// queued, and by every other observable — the answer's shape, the row written,
// the budget spent — they are the same.
func TestARegisteredAndASuppressedNumberTakeTheSameCountingPath(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()

	registeredID, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin, Phone: phone, Deliver: true,
	})
	if err != nil {
		t.Fatalf("Send (registered): %v", err)
	}
	r.clock.advance(2 * time.Minute)
	suppressedID, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin, Phone: phone, Deliver: false,
	})
	if err != nil {
		t.Fatalf("Send (suppressed): %v", err)
	}

	if registeredID == "" || suppressedID == "" {
		t.Fatalf("a challenge identifier is empty: registered %q, suppressed %q", registeredID, suppressedID)
	}
	if got := r.rowCount(); got != 2 {
		t.Fatalf("the table holds %d rows, want 2 (one per send, whatever the registration)", got)
	}
	phoneMac := r.database.BlindIndex(phone)
	n, err := r.p.register.countPhoneSince(r.h, phoneMac, r.clock.read().Add(-time.Hour))
	if err != nil {
		t.Fatalf("counting the phone budget: %v", err)
	}
	if n != 2 {
		t.Errorf("the phone budget counts %d sends, want 2: the two paths must agree", n)
	}
	// The one difference the caller may observe is off the answer: the registered
	// send queued a message and the suppressed one did not.
	if got := len(r.p.pool.queue); got != 1 {
		t.Errorf("the queue holds %d messages, want 1 (the registered send only)", got)
	}
}

// TestAQueuedMessageIsDroppedWhenTheQueueIsFull holds the drop: a full queue
// refuses the send before anything is written, with ErrUnavailable and the
// dropped outcome. The same check runs for a suppressed send, so the full queue
// is not an observation that tells the two apart.
func TestAQuedMessageIsDroppedWhenTheQueueIsFull(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	// Fill the queue without starting the pool, so nothing drains it.
	for range cap(r.p.pool.queue) {
		r.p.pool.queue <- sms.Resolved{To: phone}
	}
	_, err := r.sendLogin()
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("Send with a full queue returned %v, want ErrUnavailable", err)
	}
	if got := r.rowCount(); got != 0 {
		t.Errorf("a dropped send wrote %d rows, want 0", got)
	}
	if got := r.counter(sendsName, attribute.String(outcomeAttribute, outcomeDropped)); got != 1 {
		t.Errorf("outcome dropped = %d, want 1", got)
	}
}

// TestTheAllowedPrefixIsAnchoredAndNeverBlank holds the prefix test's edges: a
// blank entry admits nothing rather than everything, a prefix that appears
// mid-number does not admit the number, and a number equal to the prefix is
// admitted.
func TestTheAllowedPrefixIsAnchoredAndNeverBlank(t *testing.T) {
	cases := []struct {
		name    string
		table   string
		phone   string
		allowed bool
	}{
		{"an empty table admits nothing", "[]", phone, false},
		{"a blank entry admits nothing", `[""]`, phone, false},
		{"a blank entry beside a real one is inert", `["","+1"]`, phone, false},
		{"the prefix must begin the number", `["86"]`, phone, false},
		{"a number equal to the prefix is admitted", `["+8613800138000"]`, phone, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prefixes, err := parsePrefixes(c.table)
			if err != nil {
				t.Fatalf("parsing %s: %v", c.table, err)
			}
			if got := prefixAllowed(prefixes, c.phone); got != c.allowed {
				t.Errorf("prefixAllowed(%s, %s) = %v, want %v", c.table, c.phone, got, c.allowed)
			}
		})
	}
}

// TestAnEnrollSendAndAnEnglishSendGoThrough holds two values that no other case
// drives positively: the enroll purpose and the en-US language are both declared
// by the carrier, so a send carrying either is legal.
func TestAnEnrollSendAndAnEnglishSendGoThrough(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()

	enrollID, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeEnroll, Phone: phone, Account: acct, Deliver: true,
	})
	if err != nil {
		t.Fatalf("an enroll send returned %v, want success", err)
	}
	if enrollID == "" {
		t.Error("the enroll send returned no challenge identifier")
	}

	enID, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin, Phone: "+8613800138001", Locale: "en-US", Deliver: true,
	})
	if err != nil {
		t.Fatalf("an en-US login send returned %v, want success", err)
	}
	if enID == "" {
		t.Error("the en-US send returned no challenge identifier")
	}
}

// TestTheQueuedMessageCarriesTheLanguageTheCallerNamed holds that the message
// locale is not lost: an empty input reads as the configured default and a
// language the carrier declares is carried as it is.
func TestTheQueuedMessageCarriesTheLanguageTheCallerNamed(t *testing.T) {
	cases := []struct {
		locale string
		want   string
	}{
		{"", testDefaultLocale},
		{"en-US", "en-US"},
		{"zh-CN", "zh-CN"},
	}
	for _, c := range cases {
		t.Run("locale "+c.locale, func(t *testing.T) {
			r := newRigSQLite(t)
			r.allow()
			if _, err := r.p.Send(context.Background(), authn.SendCode{
				Purpose: authn.SMSPurposeLogin, Phone: phone, Locale: c.locale, Deliver: true,
			}); err != nil {
				t.Fatalf("Send: %v", err)
			}
			msg := <-r.p.pool.queue
			if msg.Locale != c.want {
				t.Errorf("the queued message locale = %q, want %q", msg.Locale, c.want)
			}
		})
	}
}

// TestAnUndeclaredRequestedLocaleResolvesToTheDefault holds that a language the
// carrier does not declare is a preference and not a caller defect: the send goes
// through, in the configured default, rather than being refused.
//
// On the pre-change code a "fr-FR" send is refused as a caller defect — the
// locale is held to the hardcoded pair — so this send returns an error instead of
// reaching the carrier.
func TestAnUndeclaredRequestedLocaleResolvesToTheDefault(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	if _, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin, Phone: phone, Locale: "fr-FR", Deliver: true,
	}); err != nil {
		t.Fatalf("a fr-FR send returned %v, want success (resolving to the default)", err)
	}
	msg := <-r.p.pool.queue
	if msg.Locale != testDefaultLocale {
		t.Errorf("the queued message locale = %q, want the default %q", msg.Locale, testDefaultLocale)
	}
}

// TestTheMessageLanguageIsTheTemplatesOwnNotTheWholeSet holds that the
// languages a message may go out in are those of the template its purpose maps
// to, not the union of every template the registry declares. A registry whose
// login template declares en-US and ja-JP and whose mfa template declares en-US
// and fr-FR starts up — every template covers the configured default — a login
// request in ja-JP goes out in ja-JP, and an mfa request in ja-JP resolves to
// the configured default, because the mfa template does not carry ja-JP.
//
// On the union code this case fails: ja-JP is in the union, taken from the login
// template, so the mfa message would go out in ja-JP — a language its own
// template does not hold. The registry accepts the startup and then refuses
// every mfa message at send time, which is the shape the startup check exists to
// prevent.
func TestTheMessageLanguageIsTheTemplatesOwnNotTheWholeSet(t *testing.T) {
	registry := newRegistry()
	registry.setDeclared([]sms.Template{
		{Name: templateLogin, Params: append([]string(nil), templateParams...), Locales: []string{"en-US", "ja-JP"}},
		{Name: templateMFA, Params: append([]string(nil), templateParams...), Locales: []string{"en-US", "fr-FR"}},
		{Name: templateEnroll, Params: append([]string(nil), templateParams...), Locales: []string{"en-US"}},
	})
	if err := checkSender(registry.Declared(), testDefaultLocale); err != nil {
		t.Fatalf("a registry whose templates declare different languages was refused at startup: %v", err)
	}
	r := newRigOver(t, openSQLite(t), registry, newSender(), testDefaultLocale)
	r.allow()

	// A login send requested in ja-JP goes out in ja-JP: its template declares it.
	if _, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin, Phone: phone, Locale: "ja-JP", Deliver: true,
	}); err != nil {
		t.Fatalf("a ja-JP login send returned %v, want success", err)
	}
	loginMsg := <-r.p.pool.queue
	if loginMsg.Locale != "ja-JP" {
		t.Errorf("the login message locale = %q, want ja-JP", loginMsg.Locale)
	}

	// An mfa send requested in ja-JP goes out in the configured default: the mfa
	// template declares en-US and fr-FR and carries no ja-JP. A distinct number
	// keeps this send off the login send's resend-interval and phone budgets.
	if _, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeMFA, Phone: "+8613800138001", Account: acct, Locale: "ja-JP", Deliver: true,
	}); err != nil {
		t.Fatalf("a ja-JP mfa send returned %v, want success", err)
	}
	mfaMsg := <-r.p.pool.queue
	if mfaMsg.Locale != testDefaultLocale {
		t.Errorf("the mfa message locale = %q, want the configured default %q", mfaMsg.Locale, testDefaultLocale)
	}
}

// TestANormalEnqueueIsNotRecordedAsADrop holds the enqueue discrimination: a
// send whose message reaches the queue records no dropped outcome, so inverting
// the enqueue result is not silently mislabeled.
func TestANormalEnqueueIsNotRecordedAsADrop(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	if _, err := r.sendLogin(); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := r.counter(sendsName, attribute.String(outcomeAttribute, outcomeDropped)); got != 0 {
		t.Errorf("a normal enqueue recorded outcome dropped %d times, want 0", got)
	}
	if got := len(r.p.pool.queue); got != 1 {
		t.Errorf("the queue holds %d messages, want 1", got)
	}
}

// TestATemplateTheRegistryWontSendUnderIsNotSendable holds the registry-refusal
// path: the request path resolves the message and the registry refuses it
// because the template is disabled or its vendor review is not back, so the send
// records the refusal under its own outcome — not sent, not rejected — queues
// nothing, and returns the challenge identifier like a send a carrier would
// later refuse, because the row is already written.
func TestATemplateTheRegistryWontSendUnderIsNotSendable(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.registry.setResolveErr(sms.ErrNotSendable)

	id, err := r.sendLogin()
	if err != nil {
		t.Fatalf("a not-sendable send returned %v, want the challenge identifier", err)
	}
	if id == "" {
		t.Error("a not-sendable send returned an empty challenge identifier")
	}
	if got := len(r.p.pool.queue); got != 0 {
		t.Errorf("a not-sendable send queued %d messages, want none", got)
	}
	if got := r.rowCount(); got != 1 {
		t.Errorf("a not-sendable send wrote %d rows, want 1", got)
	}
	if got := r.counter(sendsName, attribute.String(outcomeAttribute, outcomeNotSendable)); got != 1 {
		t.Errorf("outcome not-sendable = %d, want 1", got)
	}
	if got := r.counter(sendsName, attribute.String(outcomeAttribute, outcomeRejected)); got != 0 {
		t.Errorf("a not-sendable send recorded outcome rejected %d times, want 0", got)
	}
	if got := r.counter(sendsName, attribute.String(outcomeAttribute, outcomeSent)); got != 0 {
		t.Errorf("a not-sendable send recorded outcome sent %d times, want 0", got)
	}
}

// TestADatabaseThatCannotBeReadIsNotRecordedAsABudgetRefusal holds the outcome
// split: a budget check that cannot read its rows answers unavailable and is
// recorded under no outcome, so a dashboard does not read a database outage as a
// policy refusal.
func TestADatabaseThatCannotBeReadIsNotRecordedAsABudgetRefusal(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	if err := r.h.Exec("DROP TABLE " + challengeTable).Error; err != nil {
		t.Fatalf("dropping the challenge table: %v", err)
	}
	_, err := r.sendLogin()
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("a Send over an unreadable table returned %v, want ErrUnavailable", err)
	}
	if got := r.counter(sendsName, attribute.String(outcomeAttribute, outcomeBudgetExhausted)); got != 0 {
		t.Errorf("a database read failure recorded outcome budget-exhausted %d times, want 0", got)
	}
}

// TestTheSuppressedAndDeliveredPathsTakeComparableTime holds the weak half of the
// timing claim: an implementation that made the suppressed path a fast exit — a
// sleep, a skipped write — would show here. It proves nothing on its own; the
// structural sameness above is what does.
func TestTheSuppressedAndDeliveredPathsTakeComparableTime(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	// The two runs together send more than the declared hourly login budget; lift
	// it so the timing is not measuring a refusal.
	r.store.set(globalLoginPath, 1000000)

	measure := func(tag int, deliver bool) time.Duration {
		start := time.Now()
		for i := range 200 {
			// A distinct number each iteration keeps the per-number budgets from
			// accumulating, and the tag keeps the two runs off each other's numbers;
			// the global login budget is well above 200.
			number := fmt.Sprintf("+861380%02d%05d", tag, i)
			if _, err := r.p.Send(context.Background(), authn.SendCode{
				Purpose: authn.SMSPurposeLogin, Phone: number, Deliver: deliver,
			}); err != nil {
				t.Fatalf("Send: %v", err)
			}
			for len(r.p.pool.queue) > 0 {
				<-r.p.pool.queue
			}
		}
		return time.Since(start)
	}

	delivered := measure(1, true)
	suppressed := measure(2, false)
	if delivered == 0 || suppressed == 0 {
		t.Fatal("a measurement was zero")
	}
	// A generous bound: it catches a path that is an order of magnitude slower or
	// faster, not a small difference, which is all a timing check can do.
	ratio := float64(delivered) / float64(suppressed)
	if ratio > 20 || ratio < 1.0/20 {
		t.Errorf("the delivered path took %v and the suppressed path %v, a ratio of %.1f; "+
			"the two paths have to take comparable time", delivered, suppressed, ratio)
	}
}
