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
	"github.com/vislake/speed/pkg/sms/budget"
)

// TestAnAllowedSendWritesARowAndQueuesAMessage checks that an allowed send writes a row and queues a message.
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

// TestASuppressedSendTakesTheSamePathAndQueuesNothing checks that a suppressed send takes the same path and queues nothing.
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

// TestTheEmptyAllowedTableRefusesEveryNumber checks that an empty allowed table refuses every number.
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

// TestANumberOutsideTheAllowedTableIsRefused checks that a number outside the allowed table is refused.
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

// TestTheCallerContractIsEnforced checks that the caller contract is enforced.
func TestTheCallerContractIsEnforced(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	cases := []struct {
		name string
		in   authn.SendCode
	}{
		{"purpose", authn.SendCode{Purpose: "signup", Phone: phone, Deliver: true}},
		{"purpose-with-account", authn.SendCode{Purpose: "signup", Phone: phone, Account: acct, Deliver: true}},
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

// TestAResendVoidsThePreviousChallengeOfTheNumber checks that a resend voids the number's previous challenge.
func TestAResendVoidsThePreviousChallengeOfTheNumber(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()

	first, err := r.sendLogin()
	if err != nil {
		t.Fatalf("the first Send: %v", err)
	}
	r.clock.advance(2 * time.Minute)
	second, err := r.sendLogin()
	if err != nil {
		t.Fatalf("the second Send: %v", err)
	}
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

// TestNoPlaintextCodeReachesTheTable checks that no plaintext code reaches the table.
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

// TestARegisteredAndASuppressedNumberTakeTheSameCountingPath checks that a registered and a suppressed number take the same counting path.
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
	if n := r.ledgerCount(budget.Query{
		Channel:           budget.ChannelAuthn,
		Dimension:         budget.DimensionDestination,
		Value:             phoneMac,
		Since:             r.clock.read().Add(-time.Hour),
		IncludeSuppressed: true,
	}); n != 2 {
		t.Errorf("the phone budget counts %d sends, want 2: the two paths must agree", n)
	}
	if got := len(r.p.pool.queue); got != 1 {
		t.Errorf("the queue holds %d messages, want 1 (the registered send only)", got)
	}
}

// TestAQuedMessageIsDroppedWhenTheQueueIsFull checks that a queued message is dropped when the queue is full.
func TestAQuedMessageIsDroppedWhenTheQueueIsFull(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
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

// TestTheAllowedPrefixIsAnchoredAndNeverBlank checks that the allowed prefix is anchored and never blank.
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

// TestAnEnrollSendAndAnEnglishSendGoThrough checks that an enroll send and an English send go through.
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

// TestTheQueuedMessageCarriesTheLanguageTheCallerNamed checks that the queued message carries the language the caller named.
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

// TestAnUndeclaredRequestedLocaleResolvesToTheDefault checks that an undeclared requested locale resolves to the default.
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

// TestTheMessageLanguageIsTheTemplatesOwnNotTheWholeSet checks that the message language comes from its template, not the whole set.
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

	if _, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin, Phone: phone, Locale: "ja-JP", Deliver: true,
	}); err != nil {
		t.Fatalf("a ja-JP login send returned %v, want success", err)
	}
	loginMsg := <-r.p.pool.queue
	if loginMsg.Locale != "ja-JP" {
		t.Errorf("the login message locale = %q, want ja-JP", loginMsg.Locale)
	}

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

// TestANormalEnqueueIsNotRecordedAsADrop checks that a normal enqueue is not recorded as a drop.
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

// TestATemplateTheRegistryWontSendUnderIsNotSendable checks that a template the registry will not send under is not sendable.
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

// TestADatabaseThatCannotBeReadIsNotRecordedAsABudgetRefusal checks that an unreadable database is not recorded as a budget refusal.
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

// TestTheSuppressedAndDeliveredPathsTakeComparableTime checks that the suppressed and delivered paths take comparable time.
func TestTheSuppressedAndDeliveredPathsTakeComparableTime(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.store.set(globalLoginPath, 1000000)

	measure := func(tag int, deliver bool) time.Duration {
		start := time.Now()
		for i := range 200 {
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
	ratio := float64(delivered) / float64(suppressed)
	if ratio > 20 || ratio < 1.0/20 {
		t.Errorf("the delivered path took %v and the suppressed path %v, a ratio of %.1f; "+
			"the two paths have to take comparable time", delivered, suppressed, ratio)
	}
}
