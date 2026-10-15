package delivery

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/mail"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/tenant"
)

// setChannels installs the effective channel set a case wants for the declared
// type.
func (r *rig) setChannels(channels ...notification.Channel) {
	r.t.Helper()
	r.prefs.mu.Lock()
	defer r.prefs.mu.Unlock()
	r.prefs.channels[typeName] = channels
}

// The in-app channel renders the message from the type's own catalog keys,
// writes it to the inbox and records one succeeded row.
func TestTheInAppChannelRendersAndWritesTheMessage(t *testing.T) {
	r := newRig(t, nil)
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelInApp)
	r.local.set(typeName+".in_app.title", "Invoice paid")
	r.local.set(typeName+".in_app.body", "Invoice inv-1 is paid")

	if _, err := r.p.Handle(ctx, jobOf(t, dispatch("u1", "occ-1", validParams())), nil); err != nil {
		t.Fatalf("the delivery failed: %v", err)
	}
	if got := r.inbox.writeCount(); got != 1 {
		t.Fatalf("the inbox was written %d times, want 1", got)
	}
	msg := r.inbox.last()
	if msg.Title != "Invoice paid" || msg.Body != "Invoice inv-1 is paid" {
		t.Errorf("the message is %q/%q, want the rendered text", msg.Title, msg.Body)
	}
	if !r.local.asked(typeName + ".in_app.title") {
		t.Error("the title was not rendered from the type's catalog key")
	}
	rows := r.rows("t1")
	if len(rows) != 1 || rows[0].channel != "in_app" || rows[0].status != statusSucceeded {
		t.Fatalf("the ledger holds %+v, want one succeeded in_app row", rows)
	}
}

// The in-app delivery publishes no event of its own: the row is the inbox's to
// announce, and the module that wrote it publishes its created event after it
// committed. The bus is present in this assembly, so a delivery that published
// one of its own would be seen here — and a subscriber would push twice.
func TestTheInAppDeliveryPublishesNoEventOfItsOwn(t *testing.T) {
	r := newRig(t, nil)
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelInApp)

	if _, err := r.p.Handle(ctx, jobOf(t, dispatch("u1", "occ-1", validParams())), nil); err != nil {
		t.Fatalf("the delivery failed: %v", err)
	}
	if got := r.inbox.writeCount(); got != 1 {
		t.Fatalf("the inbox was written %d times, want 1", got)
	}
	if events := r.bus.events(); len(events) != 0 {
		t.Errorf("this module published %d events over a present bus, want none: the row's created "+
			"event belongs to the module that wrote the row, and two publishers would push twice", len(events))
	}
}

// A repeated consumption of one job does not deliver the channel twice: the
// ledger row the first consumption landed makes the second skip it.
func TestRepeatedConsumptionDoesNotDeliverTwice(t *testing.T) {
	r := newRig(t, nil)
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelInApp)
	job := jobOf(t, dispatch("u1", "occ-1", validParams()))

	if _, err := r.p.Handle(ctx, job, nil); err != nil {
		t.Fatalf("the first delivery failed: %v", err)
	}
	if _, err := r.p.Handle(ctx, job, nil); err != nil {
		t.Fatalf("the repeated delivery failed: %v", err)
	}
	if got := r.inbox.writeCount(); got != 1 {
		t.Errorf("the inbox was written %d times, want 1", got)
	}
	if got := r.rowCount("t1"); got != 1 {
		t.Errorf("the ledger holds %d rows, want 1", got)
	}
}

// The email channel is skipped when the mail seam is absent from the assembly:
// the channel is unavailable exactly as an absent seam is, and that is not a
// failure.
func TestTheEmailChannelIsSkipped(t *testing.T) {
	r := newRig(t, nil)
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelEmail)

	if _, err := r.p.Handle(ctx, jobOf(t, dispatch("u1", "occ-1", validParams())), nil); err != nil {
		t.Fatalf("a skipped email channel failed the delivery: %v", err)
	}
	rows := r.rows("t1")
	if len(rows) != 1 || rows[0].channel != "email" || rows[0].status != statusSkipped {
		t.Fatalf("the ledger holds %+v, want one skipped email row", rows)
	}
}

// The email channel renders the subject and both bodies from the type's own
// catalog keys and hands them to the mail seam, once. The message is the
// module's own to shape: the sender is left to the carrier's configured default,
// the destination travels in the dispatch's parameters, the dedupe key is the
// channel's own, and no attachment is carried — so nothing in it is a reader a
// second send would find spent.
func TestTheEmailChannelRendersAndSendsTheMessage(t *testing.T) {
	r := newRig(t, func(h *host) {
		h.mail = true
		h.specs = map[string]notification.TypeSpec{"test.declarer": {
			Key:             typeName,
			Group:           "billing",
			DefaultChannels: []notification.Channel{notification.ChannelEmail},
			Unsubscribable:  true,
		}}
	})
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelEmail)
	r.local.set(typeName+".email.subject", "Invoice inv-1 is paid")
	r.local.set(typeName+".email.body_text", "Invoice inv-1 is paid in full.")
	r.local.set(typeName+".email.body_html", "<p>Invoice inv-1 is paid in full.</p>")

	params := map[string]any{"to": "alice@acme.test", "amount": 100}
	if _, err := r.p.Handle(ctx, jobOf(t, dispatch("u1", "occ-1", params)), nil); err != nil {
		t.Fatalf("the delivery failed: %v", err)
	}
	sent := r.mailSender.messages()
	if len(sent) != 1 {
		t.Fatalf("the carrier accepted %d messages, want 1", len(sent))
	}
	msg := sent[0]
	if msg.From != "" {
		t.Errorf("Message.From is %q, want it empty: the carrier's own configured default sender is the sender",
			msg.From)
	}
	if !slices.Equal(msg.To, []string{"alice@acme.test"}) {
		t.Errorf("Message.To is %v, want the dispatch's destination", msg.To)
	}
	if msg.Subject != "Invoice inv-1 is paid" {
		t.Errorf("Message.Subject is %q, want the rendered subject", msg.Subject)
	}
	if msg.Text != "Invoice inv-1 is paid in full." {
		t.Errorf("Message.Text is %q, want the rendered plain-text body", msg.Text)
	}
	if msg.HTML != "<p>Invoice inv-1 is paid in full.</p>" {
		t.Errorf("Message.HTML is %q, want the rendered HTML body", msg.HTML)
	}
	if len(msg.Attachments) != 0 {
		t.Errorf("the message carries %d attachments, want none", len(msg.Attachments))
	}
	if !r.local.asked(typeName + ".email.subject") {
		t.Error("the subject was not rendered from the type's catalog key")
	}
	rows := r.rows("t1")
	if len(rows) != 1 || rows[0].channel != "email" || rows[0].status != statusSucceeded {
		t.Fatalf("the ledger holds %+v, want one succeeded email row", rows)
	}
	if msg.DedupeID != rows[0].key {
		t.Errorf("Message.DedupeID is %q, want the channel's idempotency key %q", msg.DedupeID, rows[0].key)
	}
	if rows[0].receipt != "receipt-1" {
		t.Errorf("the ledger carries the receipt %q, want the carrier's own", rows[0].receipt)
	}
}

// A catalog that carries no HTML body leaves the field empty rather than putting
// the key itself into markup a client would render.
func TestTheEmailChannelOmitsAnAbsentHTMLBody(t *testing.T) {
	r := newRig(t, func(h *host) {
		h.mail = true
		h.specs = map[string]notification.TypeSpec{"test.declarer": {
			Key:             typeName,
			Group:           "billing",
			DefaultChannels: []notification.Channel{notification.ChannelEmail},
			Unsubscribable:  true,
		}}
	})
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelEmail)
	r.local.set(typeName+".email.subject", "Invoice inv-1 is paid")
	r.local.set(typeName+".email.body_text", "Invoice inv-1 is paid in full.")

	params := map[string]any{"to": "alice@acme.test", "amount": 100}
	if _, err := r.p.Handle(ctx, jobOf(t, dispatch("u1", "occ-1", params)), nil); err != nil {
		t.Fatalf("the delivery failed: %v", err)
	}
	sent := r.mailSender.messages()
	if len(sent) != 1 {
		t.Fatalf("the carrier accepted %d messages, want 1", len(sent))
	}
	if sent[0].HTML != "" {
		t.Errorf("Message.HTML is %q, want it empty: the key is not the body", sent[0].HTML)
	}
	if sent[0].Text == "" {
		t.Error("the message carries no body at all")
	}
}

// A carrier that answers an uncertain outcome fails the email channel and does
// not starve the others: the carrier's own text is what the ledger holds, and the
// joined error is what makes the job retry. This module retries no send itself.
func TestAnEmailFailureDoesNotStarveTheOthers(t *testing.T) {
	r := newRig(t, func(h *host) {
		h.mail = true
		h.specs = map[string]notification.TypeSpec{"test.declarer": {
			Key:             typeName,
			Group:           "billing",
			DefaultChannels: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
			Unsubscribable:  true,
		}}
	})
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelInApp, notification.ChannelEmail)
	r.mailSender.failWith(fmt.Errorf("the carrier did not answer: %w", mail.ErrUncertain))

	params := map[string]any{"to": "alice@acme.test", "amount": 100}
	_, err := r.p.Handle(ctx, jobOf(t, dispatch("u1", "occ-1", params)), nil)
	if err == nil {
		t.Fatal("a failed channel did not fail the delivery")
	}
	if !errors.Is(err, mail.ErrUncertain) {
		t.Errorf("the joined error is %v, want the mail failure", err)
	}
	if got := r.inbox.writeCount(); got != 1 {
		t.Errorf("the in-app channel was written %d times, want 1 despite the email failure", got)
	}
	if got := r.mailSender.sendCount(); got != 1 {
		t.Errorf("the carrier was reached %d times, want 1: this module does not resend on its own", got)
	}
	rows := r.rows("t1")
	byChannel := map[string]ledgerRow{}
	for _, row := range rows {
		byChannel[row.channel] = row
	}
	if len(rows) != 2 {
		t.Fatalf("the ledger holds %+v, want two rows", rows)
	}
	if byChannel["email"].status != statusFailed {
		t.Errorf("the email row is %q, want failed", byChannel["email"].status)
	}
	if byChannel["email"].err == "" {
		t.Error("the failed email row carries no reason")
	}
	if byChannel["in_app"].status != statusSucceeded {
		t.Errorf("the in_app row is %q, want succeeded", byChannel["in_app"].status)
	}
}

// A dispatch that names no destination fails the email channel with this
// module's own reason, and the carrier is never reached.
func TestAnEmailDispatchWithoutADestinationFailsTheChannel(t *testing.T) {
	r := newRig(t, func(h *host) {
		h.mail = true
		h.specs = map[string]notification.TypeSpec{"test.declarer": {
			Key:             typeName,
			Group:           "billing",
			DefaultChannels: []notification.Channel{notification.ChannelEmail},
			Unsubscribable:  true,
		}}
	})
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelEmail)

	_, err := r.p.Handle(ctx, jobOf(t, dispatch("u1", "occ-1", validParams())), nil)
	if err == nil {
		t.Fatal("a channel with no destination did not fail the delivery")
	}
	if !errors.Is(err, notification.ErrInvalidRequest) {
		t.Errorf("the joined error is %v, want notification.ErrInvalidRequest", err)
	}
	if got := r.mailSender.sendCount(); got != 0 {
		t.Errorf("the carrier was reached %d times, want 0", got)
	}
	rows := r.rows("t1")
	if len(rows) != 1 || rows[0].status != statusFailed || rows[0].err == "" {
		t.Fatalf("the ledger holds %+v, want one failed email row carrying a reason", rows)
	}
}

// The sms channel is skipped when its seams are absent from the assembly.
func TestTheSmsChannelIsSkippedWithoutItsSeams(t *testing.T) {
	r := newRig(t, nil)
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelSMS)

	if _, err := r.p.Handle(ctx, jobOf(t, dispatch("u1", "occ-1", validParams())), nil); err != nil {
		t.Fatalf("a skipped sms channel failed the delivery: %v", err)
	}
	rows := r.rows("t1")
	if len(rows) != 1 || rows[0].channel != "sms" || rows[0].status != statusSkipped {
		t.Fatalf("the ledger holds %+v, want one skipped sms row", rows)
	}
}

// One channel's terminal failure does not starve the others and is not hidden by
// their successes: the delivery walks every channel, the other channel is still
// delivered, and the joined error is what the job retries on.
func TestOneChannelFailureDoesNotStarveTheOthers(t *testing.T) {
	r := newRig(t, func(h *host) {
		h.sms = true
		h.specs = map[string]notification.TypeSpec{"test.declarer": {
			Key:             typeName,
			Group:           "billing",
			DefaultChannels: []notification.Channel{notification.ChannelInApp, notification.ChannelSMS},
			Unsubscribable:  true,
		}}
	})
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelInApp, notification.ChannelSMS)
	r.local.set(typeName+".sms.template", "sms-template")
	r.smsRegistry.mu.Lock()
	r.smsRegistry.resolveErr = sms.ErrRejected
	r.smsRegistry.mu.Unlock()

	params := map[string]any{"to": "+15550000", "amount": 100}
	_, err := r.p.Handle(ctx, jobOf(t, dispatch("u1", "occ-1", params)), nil)
	if err == nil {
		t.Fatal("a failed channel did not fail the delivery")
	}
	if !errors.Is(err, sms.ErrRejected) {
		t.Errorf("the joined error is %v, want the sms failure", err)
	}
	if got := r.inbox.writeCount(); got != 1 {
		t.Errorf("the in-app channel was written %d times, want 1 despite the sms failure", got)
	}
	rows := r.rows("t1")
	if len(rows) != 2 {
		t.Fatalf("the ledger holds %d rows, want 2", len(rows))
	}
	byChannel := map[string]ledgerRow{}
	for _, row := range rows {
		byChannel[row.channel] = row
	}
	if byChannel["in_app"].status != statusSucceeded {
		t.Errorf("the in_app row is %q, want succeeded", byChannel["in_app"].status)
	}
	if byChannel["sms"].status != statusFailed {
		t.Errorf("the sms row is %q, want failed", byChannel["sms"].status)
	}
	if byChannel["sms"].err == "" {
		t.Error("the failed sms row carries no reason")
	}
}

// A channel whose row is failed is attempted again by the next consumption, and
// its new result settles into the same row: the retry makes up what did not
// succeed, and the ledger keeps one row per channel.
func TestAFailedChannelIsRetriedIntoSameRow(t *testing.T) {
	r := newRig(t, func(h *host) {
		h.sms = true
		h.specs = map[string]notification.TypeSpec{"test.declarer": {
			Key:             typeName,
			Group:           "billing",
			DefaultChannels: []notification.Channel{notification.ChannelSMS},
			Unsubscribable:  true,
		}}
	})
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelSMS)
	r.local.set(typeName+".sms.template", "sms-template")
	r.smsRegistry.mu.Lock()
	r.smsRegistry.resolveErr = sms.ErrRejected
	r.smsRegistry.mu.Unlock()

	job := jobOf(t, dispatch("u1", "occ-1", map[string]any{"to": "+15550000", "amount": 100}))
	if _, err := r.p.Handle(ctx, job, nil); err == nil {
		t.Fatal("the failed channel did not fail the delivery")
	}
	rows := r.rows("t1")
	if len(rows) != 1 || rows[0].status != statusFailed {
		t.Fatalf("the ledger holds %+v, want one failed row", rows)
	}

	r.smsRegistry.mu.Lock()
	r.smsRegistry.resolveErr = nil
	r.smsRegistry.mu.Unlock()
	if _, err := r.p.Handle(ctx, job, nil); err != nil {
		t.Fatalf("the retry failed: %v", err)
	}
	rows = r.rows("t1")
	if len(rows) != 1 {
		t.Fatalf("the retry left %d rows, want the one row updated", len(rows))
	}
	if rows[0].status != statusSucceeded {
		t.Errorf("the retried row is %q, want succeeded", rows[0].status)
	}
}

// Each channel derives its own idempotency key: one logical event on two
// channels lands two rows with two different keys.
func TestTheDedupeKeyDiffersPerChannel(t *testing.T) {
	r := newRig(t, nil)
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelInApp, notification.ChannelEmail)

	if _, err := r.p.Handle(ctx, jobOf(t, dispatch("u1", "occ-1", validParams())), nil); err != nil {
		t.Fatalf("the delivery failed: %v", err)
	}
	rows := r.rows("t1")
	if len(rows) != 2 {
		t.Fatalf("the ledger holds %d rows, want 2", len(rows))
	}
	if rows[0].key == rows[1].key {
		t.Errorf("the two channels share the idempotency key %q", rows[0].key)
	}
}

// A job whose payload names an undeclared type is refused the way a dispatch is:
// the handler runs the same validation again.
func TestTheHandlerValidatesThePayloadAgain(t *testing.T) {
	r := newRig(t, nil)
	ctx := r.tenantCtx("t1")
	bad := dispatch("u1", "occ-1", validParams())
	bad.TypeKey = "billing.no_such_type"
	if _, err := r.p.Handle(ctx, jobOf(t, bad), nil); !errors.Is(err, notification.ErrUnknownType) {
		t.Fatalf("the handler ended with %v, want ErrUnknownType", err)
	}
	if got := r.inbox.writeCount(); got != 0 {
		t.Errorf("a refused payload wrote %d messages, want 0", got)
	}
}

// A handler context that carries no tenant is refused before any storage is
// touched.
func TestTheHandlerRefusesAContextWithoutATenant(t *testing.T) {
	r := newRig(t, nil)
	_, err := r.p.Handle(context.Background(), jobOf(t, dispatch("u1", "occ-1", validParams())), nil)
	if !errors.Is(err, tenant.ErrNoTenantInContext) {
		t.Fatalf("the handler ended with %v, want tenant.ErrNoTenantInContext", err)
	}
}
