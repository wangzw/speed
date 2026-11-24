package sms

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/sms/budget"
)

func (r *rig) ledgerCount(q budget.Query) int64 {
	r.t.Helper()
	n, err := r.ledger.Count(context.Background(), q)
	if err != nil {
		r.t.Fatalf("counting the ledger: %v", err)
	}
	return n
}

func (r *rig) ledgerCeiling(since time.Time) int64 {
	r.t.Helper()
	n, err := r.ledger.CountCeiling(context.Background(), since)
	if err != nil {
		r.t.Fatalf("counting the ledger's ceiling: %v", err)
	}
	return n
}

func (r *rig) recordNotification(purpose, destination, tenant string) budget.ID {
	r.t.Helper()
	id, err := r.ledger.Record(context.Background(), budget.Attempt{
		Channel:        budget.ChannelNotification,
		Purpose:        purpose,
		DestinationMAC: destination,
		TenantID:       tenant,
		AttemptedAt:    r.clock.read(),
		Outcome:        budget.OutcomeSent,
	})
	if err != nil {
		r.t.Fatalf("recording a notification attempt in the ledger: %v", err)
	}
	return id
}

// TestEverySendWritesOneLedgerAttemptAndItsRefund checks that every send writes one ledger attempt and its refund.
func TestEverySendWritesOneLedgerAttemptAndItsRefund(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)

	phoneMAC := r.database.BlindIndex(phone)
	since := base.Add(-time.Minute)
	attempts := budget.Query{
		Channel:         budget.ChannelAuthn,
		Purpose:         authn.SMSPurposeLogin,
		Dimension:       budget.DimensionDestination,
		Value:           phoneMAC,
		Since:           since,
		IncludeRefunded: true,
	}
	sends := attempts
	sends.IncludeRefunded = false

	if got := r.ledgerCount(attempts); got != 0 {
		t.Fatalf("the ledger holds %d attempts before the first send, want 0", got)
	}

	id, err := r.sendLogin()
	if err != nil {
		t.Fatalf("the first Send: %v", err)
	}
	if got := r.ledgerCount(attempts); got != 1 {
		t.Errorf("the ledger holds %d authentication attempts after one send, want 1", got)
	}
	if got := r.ledgerCount(sends); got != 1 {
		t.Errorf("the ledger holds %d non-refunded sends after one send, want 1", got)
	}
	if v, ok := r.column(id, "budget_event_id"); !ok || v == nil {
		t.Errorf("the challenge row names no ledger attempt (column present %v, value %v)", ok, v)
	}

	r.deliverQueued(t, sms.ErrRejected)
	if got := r.ledgerCount(sends); got != 0 {
		t.Errorf("the ledger holds %d non-refunded sends after a certain failure, want 0", got)
	}
	if got := r.ledgerCount(attempts); got != 1 {
		t.Errorf("the ledger holds %d attempts after a certain failure, want the refunded attempt kept", got)
	}
	if got := r.ledgerCeiling(since); got != 0 {
		t.Errorf("the ledger's ceiling counts %d after a certain failure, want 0", got)
	}
}

// TestTheSharedCeilingRefusesCombinedChannels checks that the shared ceiling refuses combined channels.
func TestTheSharedCeilingRefusesCombinedChannels(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(ceilingHourlyPath, 2)

	r.recordNotification("alert", "notification-destination", "tenant-1")

	if _, err := r.sendLogin(); err != nil {
		t.Fatalf("the send below the ceiling returned %v, want success", err)
	}
	if got := r.ledgerCount(budget.Query{
		Channel: budget.ChannelAuthn, Since: base.Add(-time.Minute), IncludeRefunded: true,
	}); got != 1 {
		t.Fatalf("the ledger holds %d authentication attempts after one send, want 1", got)
	}
	r.clock.advance(31 * time.Second)

	_, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin, Phone: "+8613800138001", Deliver: true,
	})
	if !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("the send that reaches the shared ceiling returned %v, want ErrThrottled", err)
	}
	if got := r.rowCount(); got != 1 {
		t.Errorf("the challenge table holds %d rows after the refused send, want 1", got)
	}
}

// TestTheAuthnSendDimensionsStillRefuseAtTheirLimits checks that the authentication send dimensions still refuse at their limits.
func TestTheAuthnSendDimensionsStillRefuseAtTheirLimits(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(perPhoneHourlyPath, 1)

	if _, err := r.sendLogin(); err != nil {
		t.Fatalf("the first Send: %v", err)
	}
	r.clock.advance(31 * time.Second)
	if _, err := r.sendLogin(); !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("the send after the number's limit returned %v, want ErrThrottled", err)
	}
}

// TestARefundFreesTheSendBudgetAndStaysInTheAttemptBudget checks that a refund frees the send budget and stays in the attempt budget.
func TestARefundFreesTheSendBudgetAndStaysInTheAttemptBudget(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(perPhoneHourlyPath, 1)
	r.store.set(perSourceAttemptHourlyPath, 1)

	const source = "203.0.113.60"
	if _, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin, Phone: phone, Source: source, Deliver: true,
	}); err != nil {
		t.Fatalf("the first Send: %v", err)
	}
	r.deliverQueued(t, sms.ErrRejected)
	r.clock.advance(31 * time.Second)

	if _, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin, Phone: phone, Source: "203.0.113.61", Deliver: true,
	}); err != nil {
		t.Fatalf("a send after the refund returned %v, want success: the send budget was not given back", err)
	}
	r.clock.advance(31 * time.Second)

	if _, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin, Phone: "+8613800138001", Source: source, Deliver: true,
	}); !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("a send after a refunded one returned %v, want ErrThrottled (the attempt budget)", err)
	}

	sourceMAC := r.database.BlindIndex(source)
	q := budget.Query{
		Channel: budget.ChannelAuthn, Dimension: budget.DimensionSource,
		Value: sourceMAC, Since: base.Add(-time.Minute),
	}
	if got := r.ledgerCount(q); got != 0 {
		t.Errorf("the ledger's send count for the source = %d, want 0 (the refunded attempt is free)", got)
	}
	q.IncludeRefunded = true
	if got := r.ledgerCount(q); got != 1 {
		t.Errorf("the ledger's attempt count for the source = %d, want 1 (the refunded attempt stays)", got)
	}
}

// TestASuppressedAttemptSpendsNoShareOfTheCeiling checks that a suppressed attempt spends no share of the ceiling.
func TestASuppressedAttemptSpendsNoShareOfTheCeiling(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(ceilingHourlyPath, 1)

	if _, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin, Phone: phone, Deliver: false,
	}); err != nil {
		t.Fatalf("the suppressed send returned %v, want success", err)
	}
	if got := r.ledgerCeiling(base.Add(-time.Minute)); got != 0 {
		t.Errorf("the ceiling counts %d after a suppressed attempt, want 0", got)
	}
	r.clock.advance(31 * time.Second)
	if _, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin, Phone: "+8613800138001", Deliver: true,
	}); err != nil {
		t.Fatalf("a real send after a suppressed one returned %v, want success: the suppressed attempt spent the ceiling", err)
	}
}

// TestTheBudgetUtilizationGaugeCoversTheCeilingAndEachPurpose checks that the utilization gauge covers the ceiling and each purpose.
func TestTheBudgetUtilizationGaugeCoversTheCeilingAndEachPurpose(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(ceilingHourlyPath, 10)
	r.store.set(perPhoneHourlyPath, 10)

	if _, err := r.sendLogin(); err != nil {
		t.Fatalf("the Send: %v", err)
	}

	var rm metricdata.ResourceMetrics
	if err := r.metrics.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collecting the metrics: %v", err)
	}
	points := map[string]float64{}
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != utilizationName {
				continue
			}
			gauge, ok := m.Data.(metricdata.Gauge[float64])
			if !ok {
				t.Fatalf("%s is not a float64 gauge", utilizationName)
			}
			found = true
			for _, dp := range gauge.DataPoints {
				purpose, _ := dp.Attributes.Value(purposeAttribute)
				dimension, _ := dp.Attributes.Value(dimensionAttribute)
				channel, _ := dp.Attributes.Value(channelAttribute)
				if channel.AsString() != string(budget.ChannelAuthn) {
					t.Errorf("a utilization point carries channel=%q, want %q", channel.AsString(), budget.ChannelAuthn)
				}
				points[purpose.AsString()+"|"+dimension.AsString()] = dp.Value
			}
		}
	}
	if !found {
		t.Fatalf("no instrument is named the literal %s", utilizationName)
	}
	for _, want := range []string{
		purposeShared + "|" + dimensionCeiling,
		authn.SMSPurposeLogin + "|" + dimensionGlobal,
		authn.SMSPurposeLogin + "|" + dimensionPhone,
	} {
		if _, ok := points[want]; !ok {
			t.Errorf("the utilization gauge has no point %q; points: %v", want, points)
		}
	}
}
