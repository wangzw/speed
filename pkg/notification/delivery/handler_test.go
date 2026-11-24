package delivery

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/mail"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/tenant"
)

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

// The in-app delivery publishes no event of its own.
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

// The email channel is skipped when the mail seam is absent from the
// assembly.
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

// The email channel renders the subject and both bodies from the type's
// own catalog keys and hands them to the mail seam, once.
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

	params := map[string]any{"to": "u1@example.test", "amount": 100}
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
	if !slices.Equal(msg.To, []string{"u1@example.test"}) {
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

	params := map[string]any{"to": "u1@example.test", "amount": 100}
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

// A carrier that answers an uncertain outcome fails the email channel and
// does not starve the others.
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

	params := map[string]any{"to": "u1@example.test", "amount": 100}
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

// One channel's terminal failure does not starve the others and is not
// hidden by their successes.
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

	params := map[string]any{"to": "+15550001", "amount": 100}
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

// A channel whose row is failed is attempted again by the next
// consumption, and its new result settles into the same row.
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

	job := jobOf(t, dispatch("u1", "occ-1", map[string]any{"to": "+15550001", "amount": 100}))
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

// The handler holds a carried destination against the recipient again.
func TestTheHandlerRefusesADestinationTheRecipientDoesNotOwn(t *testing.T) {
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

	_, err := r.p.Handle(ctx, jobOf(t, dispatch("u1", "occ-1", map[string]any{"to": "u2@example.test"})), nil)
	if !errors.Is(err, notification.ErrInvalidRequest) {
		t.Fatalf("the handler ended with %v, want ErrInvalidRequest", err)
	}
	if got := r.mailSender.sendCount(); got != 0 {
		t.Errorf("the carrier was reached %d times for another account's destination, want 0", got)
	}
	if got := r.rowCount("t1"); got != 0 {
		t.Errorf("the ledger holds %d rows, want 0: a refused destination is not a channel attempt", got)
	}

	if _, err := r.p.Handle(ctx, jobOf(t, dispatch("u1", "occ-2", map[string]any{"to": "u1@example.test"})), nil); err != nil {
		t.Fatalf("the dispatch to the recipient's own destination ended with %v", err)
	}
	if got := r.mailSender.sendCount(); got != 1 {
		t.Errorf("the carrier was reached %d times for the recipient's own destination, want 1", got)
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

// TestTheEmailSendWritesOneLineWithoutSecrets holds the external-send log
// rule on the email channel.
func TestTheEmailSendWritesOneLineWithoutSecrets(t *testing.T) {
	const (
		recipient = "u1@example.test"
		body      = "Invoice inv-1 is paid in full."
		param     = "inv-secret-42"
	)
	cases := []struct {
		name    string
		failure error
		level   string
		outcome string
	}{
		{"sent", nil, "level=INFO", logOutcomeSent},
		{"uncertain", fmt.Errorf("the carrier did not answer: %w", mail.ErrUncertain), "level=WARN", logOutcomeUncertain},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			moduleLogs := &logBuffer{}
			r := newRig(t, func(h *host) {
				h.mail = true
				h.logger = bufferLogger{buf: moduleLogs}
				h.specs = map[string]notification.TypeSpec{"test.declarer": {
					Key:             typeName,
					Group:           "billing",
					DefaultChannels: []notification.Channel{notification.ChannelEmail},
					Unsubscribable:  true,
				}}
			})
			r.setChannels(notification.ChannelEmail)
			r.local.set(typeName+".email.subject", "Invoice inv-1 is paid")
			r.local.set(typeName+".email.body_text", body)
			if c.failure != nil {
				r.mailSender.failWith(c.failure)
			}
			ctx := r.tenantCtx("t1")

			params := map[string]any{"to": recipient, "invoice_id": param}
			_, _ = r.p.Handle(ctx, jobOf(t, dispatch("u1", "occ-1", params)), nil)

			lines := moduleLogs.lines("channel=email", "outcome="+c.outcome)
			if len(lines) != 1 {
				t.Fatalf("the email send wrote %d lines with outcome %s, want 1:\n%s",
					len(lines), c.outcome, moduleLogs.String())
			}
			line := lines[0]
			for _, want := range []string{c.level, "type_key=" + typeName, "duration_ms="} {
				if !strings.Contains(line, want) {
					t.Errorf("the line omits %q: %s", want, line)
				}
			}
			if c.failure == nil {
				if !strings.Contains(line, "provider_id=receipt-1") {
					t.Errorf("the success line omits the carrier's identifier: %s", line)
				}
			} else if !strings.Contains(line, "cause=") {
				t.Errorf("the failure line omits the cause: %s", line)
			}
			for _, secret := range []string{recipient, body, param} {
				if strings.Contains(line, secret) {
					t.Errorf("the line carries %q: %s", secret, line)
				}
			}
		})
	}
}

// TestTheSMSSendWritesOneLineWithoutSecrets holds the external-send log
// rule on the sms channel the same way.
func TestTheSMSSendWritesOneLineWithoutSecrets(t *testing.T) {
	const (
		recipient = "+15550001"
		param     = "inv-secret-42"
	)
	cases := []struct {
		name    string
		failure error
		level   string
		outcome string
	}{
		{"sent", nil, "level=INFO", logOutcomeSent},
		{"throttled", fmt.Errorf("the vendor limited the call: %w", sms.ErrThrottled), "level=WARN", logOutcomeThrottled},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			moduleLogs := &logBuffer{}
			r := newRig(t, func(h *host) {
				h.sms = true
				h.logger = bufferLogger{buf: moduleLogs}
				h.specs = map[string]notification.TypeSpec{"test.declarer": {
					Key:             typeName,
					Group:           "billing",
					DefaultChannels: []notification.Channel{notification.ChannelSMS},
					Unsubscribable:  true,
				}}
			})
			r.setChannels(notification.ChannelSMS)
			r.local.set(typeName+".sms.template", "sms-template")
			if c.failure != nil {
				r.smsSender.failWith(c.failure)
			}
			ctx := r.tenantCtx("t1")

			params := map[string]any{"to": recipient, "invoice_id": param}
			_, _ = r.p.Handle(ctx, jobOf(t, dispatch("u1", "occ-1", params)), nil)

			lines := moduleLogs.lines("channel=sms", "outcome="+c.outcome)
			if len(lines) != 1 {
				t.Fatalf("the sms send wrote %d lines with outcome %s, want 1:\n%s",
					len(lines), c.outcome, moduleLogs.String())
			}
			line := lines[0]
			for _, want := range []string{c.level, "type_key=" + typeName, "duration_ms="} {
				if !strings.Contains(line, want) {
					t.Errorf("the line omits %q: %s", want, line)
				}
			}
			if c.failure == nil {
				if !strings.Contains(line, "provider_id=receipt-1") {
					t.Errorf("the success line omits the carrier's identifier: %s", line)
				}
			} else if !strings.Contains(line, "cause=") {
				t.Errorf("the failure line omits the cause: %s", line)
			}
			for _, secret := range []string{recipient, param} {
				if strings.Contains(line, secret) {
					t.Errorf("the line carries %q: %s", secret, line)
				}
			}
		})
	}
}

// TestTheExternalSendLogsToTheModuleLogger holds where an external-send
// line goes.
func TestTheExternalSendLogsToTheModuleLogger(t *testing.T) {
	cases := []struct {
		name    string
		channel notification.Channel
		failure error
		level   string
		outcome string
	}{
		{
			name:    "rejected",
			channel: notification.ChannelEmail,
			failure: fmt.Errorf("the carrier refused the message: %w", mail.ErrRejected),
			level:   "level=WARN",
			outcome: logOutcomeRejected,
		},
		{
			name:    "failed",
			channel: notification.ChannelEmail,
			failure: errors.New("the carrier answered something it does not declare"),
			level:   "level=ERROR",
			outcome: logOutcomeFailed,
		},
		{
			name:    "unroutable",
			channel: notification.ChannelSMS,
			failure: fmt.Errorf("the deployment cannot reach that destination: %w", sms.ErrUnroutable),
			level:   "level=WARN",
			outcome: logOutcomeUnroutable,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			moduleLogs := &logBuffer{}
			r := newRig(t, func(h *host) {
				h.logger = bufferLogger{buf: moduleLogs}
				if c.channel == notification.ChannelEmail {
					h.mail = true
				} else {
					h.sms = true
				}
				h.specs = map[string]notification.TypeSpec{"test.declarer": {
					Key:             typeName,
					Group:           "billing",
					DefaultChannels: []notification.Channel{c.channel},
					Unsubscribable:  true,
				}}
			})
			r.setChannels(c.channel)
			if c.channel == notification.ChannelEmail {
				r.local.set(typeName+".email.subject", "Invoice inv-1 is paid")
				r.local.set(typeName+".email.body_text", "Invoice inv-1 is paid in full.")
				r.mailSender.failWith(c.failure)
			} else {
				r.local.set(typeName+".sms.template", "sms-template")
				r.smsSender.failWith(c.failure)
			}
			contextLogs, ctx := r.capturingLogger(r.tenantCtx("t1"))

			to := "u1@example.test"
			if c.channel == notification.ChannelSMS {
				to = "+15550001"
			}
			params := map[string]any{"to": to, "invoice_id": "inv-1"}
			_, _ = r.p.Handle(ctx, jobOf(t, dispatch("u1", "occ-1", params)), nil)

			lines := moduleLogs.lines("channel="+string(c.channel), "outcome="+c.outcome)
			if len(lines) != 1 {
				t.Fatalf("the module logger holds %d lines with outcome %s, want 1:\n%s",
					len(lines), c.outcome, moduleLogs.String())
			}
			line := lines[0]
			for _, want := range []string{"module=" + ModuleName, c.level, "type_key=" + typeName, "duration_ms="} {
				if !strings.Contains(line, want) {
					t.Errorf("the line omits %q: %s", want, line)
				}
			}
			if got := contextLogs.lines("an external message"); len(got) != 0 {
				t.Errorf("the send line went to the context logger, not the module's: %v", got)
			}
		})
	}
}

// TestTheTenantParameterIsTheNameOnEveryChannel holds the outbound-text
// rule.
func TestTheTenantParameterIsTheNameOnEveryChannel(t *testing.T) {
	const (
		internalID = "ACME-INTERNAL"
		tenantName = "acme-public"
	)
	r := newRig(t, func(h *host) {
		h.mail = true
		h.sms = true
		h.specs = map[string]notification.TypeSpec{"test.declarer": {
			Key:                    typeName,
			Group:                  "billing",
			DefaultChannels:        []notification.Channel{notification.ChannelInApp, notification.ChannelEmail, notification.ChannelSMS},
			Unsubscribable:         true,
			RecipientVisibleParams: []string{"to", "tenant"},
		}}
	})
	ctx := r.tenantCtxAs(internalID, tenantName)
	r.setChannels(notification.ChannelInApp, notification.ChannelEmail, notification.ChannelSMS)
	r.local.set(typeName+".in_app.title", "notice")
	r.local.set(typeName+".in_app.body", "body")
	r.local.set(typeName+".email.subject", "subject")
	r.local.set(typeName+".email.body_text", "text")
	r.local.set(typeName+".sms.template", "sms-template")
	r.accounts.bind("u1", authn.IdentifierEmail, "u1@example.test")

	params := map[string]any{"to": "u1@example.test", "tenant": internalID}
	if _, err := r.p.Handle(ctx, jobOf(t, dispatch("u1", "occ-1", params)), nil); err != nil {
		t.Fatalf("the delivery failed: %v", err)
	}

	msg := r.inbox.last()
	if got := msg.Params["tenant"]; got != tenantName {
		t.Errorf("the in-app payload names the tenant %v, want the name %q", got, tenantName)
	}
	for _, key := range []string{
		typeName + ".in_app.title",
		typeName + ".in_app.body",
		typeName + ".email.subject",
		typeName + ".email.body_text",
	} {
		args, ok := r.local.argsFor(key)
		if !ok {
			t.Fatalf("%q was not rendered", key)
		}
		if got := i18n.StringOf(args["tenant"]); got != tenantName {
			t.Errorf("%s rendered the tenant as %q, want the name %q", key, got, tenantName)
		}
	}
	resolved, ok := r.smsRegistry.lastResolve()
	if !ok {
		t.Fatal("the sms channel resolved no message")
	}
	if got := resolved.Params["tenant"]; got != tenantName {
		t.Errorf("the sms template variable tenant = %q, want the name %q", got, tenantName)
	}

	// The internal identifier must appear in no text a recipient reads.
	sent := r.mailSender.messages()
	if len(sent) != 1 {
		t.Fatalf("the carrier accepted %d messages, want 1", len(sent))
	}
	for name, text := range map[string]string{
		"in-app title":  msg.Title,
		"in-app body":   msg.Body,
		"email subject": sent[0].Subject,
		"email text":    sent[0].Text,
	} {
		if strings.Contains(text, internalID) {
			t.Errorf("the %s carries the internal identifier: %q", name, text)
		}
	}
	for name, value := range resolved.Params {
		if strings.Contains(value, internalID) {
			t.Errorf("the sms variable %q carries the internal identifier: %q", name, value)
		}
	}
}

// TestTheTenantParameterWithoutANameResolverNamesNobody holds the absent
// seam.
func TestTheTenantParameterWithoutANameResolverNamesNobody(t *testing.T) {
	r := newRig(t, func(h *host) {
		h.specs = map[string]notification.TypeSpec{"test.declarer": {
			Key:                    typeName,
			Group:                  "billing",
			DefaultChannels:        []notification.Channel{notification.ChannelInApp},
			Unsubscribable:         true,
			RecipientVisibleParams: []string{"invoice_id", "amount", "tenant"},
		}}
	})
	ctx := r.tenantCtx("t1")
	r.p.tenantNames = nil
	r.setChannels(notification.ChannelInApp)
	r.local.set(typeName+".in_app.title", "notice")
	r.local.set(typeName+".in_app.body", "body")

	params := map[string]any{"tenant": "t1", "invoice_id": "inv-1", "amount": 100}
	if _, err := r.p.Handle(ctx, jobOf(t, dispatch("u1", "occ-1", params)), nil); err != nil {
		t.Fatalf("the delivery failed: %v", err)
	}
	args, ok := r.local.argsFor(typeName + ".in_app.title")
	if !ok {
		t.Fatal("the title was not rendered")
	}
	if got := i18n.StringOf(args["tenant"]); got != "" {
		t.Errorf("the title rendered the tenant as %q, want empty with no resolver", got)
	}
}
