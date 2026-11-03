package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/sms/budget"
)

// The full paths of the delivery-budget settings, as an operator writes them.
// The cases state them literally rather than through the package's own path
// variables, so a case run against a tree that does not declare them fails on the
// settings store's refusal rather than on a missing identifier.
const (
	perRecipientHourlySetting   = "notification.delivery.per-recipient-hourly"
	perRecipientDailySetting    = "notification.delivery.per-recipient-daily"
	globalHourlySetting         = "notification.delivery.global-hourly"
	tenantDistinctHourlySetting = "notification.delivery.tenant-distinct-hourly"
	attemptHourlySetting        = "notification.delivery.attempt-hourly"
)

// The new budgets declare the defaults the abuse redesign plans: twenty
// different recipients an hour and three hundred attempts an hour per tenant.
// The restored email budgets declare the values the module used before the sms
// path moved to the shared ledger.
func TestTheNewBudgetsDeclareThePlannedDefaults(t *testing.T) {
	if got := defaultSettings.TenantDistinctHourly; got != 20 {
		t.Errorf("the tenant distinct default is %d, want 20", got)
	}
	if got := defaultSettings.AttemptHourly; got != 300 {
		t.Errorf("the attempt default is %d, want 300", got)
	}
	if got := defaultSettings.GlobalHourly; got != 10000 {
		t.Errorf("the deployment-wide email default is %d, want 10000", got)
	}
	if got := defaultSettings.PerRecipientHourly; got != 20 {
		t.Errorf("the per-recipient hourly default is %d, want 20", got)
	}
	if got := defaultSettings.PerRecipientDaily; got != 100 {
		t.Errorf("the per-recipient daily default is %d, want 100", got)
	}
}

// TestTheCeilingFallbackIsTheLedgerDefault holds this reader's fallback to the
// ledger module's one statement of the ceiling's default, so a literal here
// cannot drift from the declared value.
func TestTheCeilingFallbackIsTheLedgerDefault(t *testing.T) {
	r := newSettingReader(nil, nil)
	if got := r.limits().ceilingHourly; got != budget.DefaultCeilingHourly {
		t.Errorf("the ceiling fallback is %d, want budget.DefaultCeilingHourly = %d",
			got, budget.DefaultCeilingHourly)
	}
}

// setBudget writes one delivery-budget limit on the global layer.
func (r *rig) setBudget(t *testing.T, key string, value int) {
	t.Helper()
	if err := r.settings.Set(t.Context(), settings.Write{
		Layer: settings.Global(),
		Key:   key,
		Value: value,
	}); err != nil {
		t.Fatalf("writing the budget %s: %v", key, err)
	}
}

// liftOtherBudgets raises every budget a case is not exercising, so only the one
// under test can refuse.
func (r *rig) liftOtherBudgets(t *testing.T, keep string) {
	t.Helper()
	all := map[string]int{
		perRecipientHourlySetting:   1000,
		perRecipientDailySetting:    10000,
		globalHourlySetting:         10000000,
		tenantDistinctHourlySetting: 10000,
		attemptHourlySetting:        100000,
	}
	for key, value := range all {
		if key != keep {
			r.setBudget(t, key, value)
		}
	}
}

// smsSpec is the declared type the sms budget cases use: sms is its default
// channel, and it admits the destination parameter the sms channel reads.
func smsSpec() notification.TypeSpec {
	return notification.TypeSpec{
		Key:             typeName,
		Group:           "billing",
		DefaultChannels: []notification.Channel{notification.ChannelSMS},
		Unsubscribable:  true,
	}
}

// newSMSRig assembles a run that can send sms: both seams are present, the type
// declares sms, and the catalog names a template so a send succeeds unless the
// case makes it fail.
func newSMSRig(t *testing.T, configure func(*host)) *rig {
	t.Helper()
	if configure == nil {
		configure = func(*host) {}
	}
	r := newRig(t, func(h *host) {
		h.sms = true
		h.specs = map[string]notification.TypeSpec{"test.declarer": smsSpec()}
		configure(h)
	})
	r.local.set(typeName+".sms.template", "sms-template")
	r.setChannels(notification.ChannelSMS)
	return r
}

// dispatchSMS builds one sms dispatch to a destination.
func dispatchSMS(userID, occurrence, to string) notification.Dispatch {
	return dispatch(userID, occurrence, map[string]any{"to": to, "invoice_id": "inv-1"})
}

// handleLastJob consumes the most recently enqueued delivery job.
func (r *rig) handleLastJob(t *testing.T, ctx context.Context) error {
	t.Helper()
	tasks := r.queue.enqueued()
	if len(tasks) == 0 {
		t.Fatal("no job was enqueued")
	}
	task := tasks[len(tasks)-1]
	_, err := r.p.Handle(ctx, &jobs.Job{ID: "job", Type: JobType, Payload: task.Payload}, nil)
	return err
}

// throttledBy asserts that err is the platform's retryable rejection and returns
// it, so a case can hold the retry time it carries.
func throttledBy(t *testing.T, err error) *authn.ThrottledError {
	t.Helper()
	var throttled *authn.ThrottledError
	if !errors.As(err, &throttled) {
		t.Fatalf("the dispatch ended with %v, want a throttled rejection", err)
	}
	return throttled
}

// The per-recipient hourly budget still refuses a dispatch past the limit, tells
// the caller when to retry, and enqueues nothing for the refused one.
func TestThePerRecipientHourlyBudgetRefusesPastTheLimit(t *testing.T) {
	r := newSMSRig(t, nil)
	ctx := r.tenantCtx("t1")
	r.setBudget(t, perRecipientHourlySetting, 2)
	r.liftOtherBudgets(t, perRecipientHourlySetting)

	for i := 0; i < 2; i++ {
		if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", fmt.Sprintf("occ-%d", i), "+15550001")); err != nil {
			t.Fatalf("dispatch %d inside the budget ended with %v", i+1, err)
		}
	}
	_, err := r.p.Dispatch(ctx, dispatchSMS("u1", "one-too-many", "+15550001"))
	throttled := throttledBy(t, err)
	if got := throttled.RetryAfter; got <= 0 || got > time.Hour {
		t.Errorf("RetryAfter is %v, want a positive delay no longer than the hourly window", got)
	}
	if got := len(r.queue.enqueued()); got != 2 {
		t.Errorf("the queue holds %d jobs, want 2: a refused dispatch must not enqueue", got)
	}
	if got := len(r.budgetRows("t1")); got != 2 {
		t.Errorf("the ledger holds %d attempts, want 2: a refused dispatch writes no row", got)
	}
}

// The per-recipient daily budget refuses the dispatch past its limit on its own:
// the hourly window is raised so that only the daily one can refuse, and the
// retry time it reports is bounded by the daily window rather than the hourly
// one.
func TestThePerRecipientDailyBudgetRefusesPastTheLimit(t *testing.T) {
	r := newSMSRig(t, nil)
	ctx := r.tenantCtx("t1")
	r.setBudget(t, perRecipientDailySetting, 1)
	r.liftOtherBudgets(t, perRecipientDailySetting)
	r.setBudget(t, perRecipientHourlySetting, 1000)

	if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-1", "+15550001")); err != nil {
		t.Fatalf("the first dispatch inside the daily budget ended with %v", err)
	}
	_, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-2", "+15550001"))
	throttled := throttledBy(t, err)
	if got := throttled.RetryAfter; got <= 0 || got > 24*time.Hour {
		t.Errorf("RetryAfter is %v, want a positive delay no longer than the daily window", got)
	}
	if got := len(r.queue.enqueued()); got != 1 {
		t.Errorf("the queue holds %d jobs, want 1: a refused dispatch must not enqueue", got)
	}
}

// The tenant distinct-destination budget refuses a producer that fans out to new
// recipients, and does not count a repeated destination as a new one.
func TestTheTenantDistinctHourlyBudgetRefusesPastTheLimit(t *testing.T) {
	r := newSMSRig(t, nil)
	ctx := r.tenantCtx("t1")
	r.accounts.bind("u2", authn.IdentifierPhone, "+15550002")
	r.accounts.bind("u3", authn.IdentifierPhone, "+15550003")
	r.setBudget(t, tenantDistinctHourlySetting, 2)
	r.liftOtherBudgets(t, tenantDistinctHourlySetting)

	if got := r.p.settings.limits().tenantDistinctHourly; got != 2 {
		t.Fatalf("the tenant distinct limit reads %d, want 2", got)
	}
	for _, c := range []struct{ user, to string }{{"u1", "+15550001"}, {"u2", "+15550002"}} {
		if _, err := r.p.Dispatch(ctx, dispatchSMS(c.user, "occ-1", c.to)); err != nil {
			t.Fatalf("the dispatch to %s inside the distinct budget ended with %v", c.user, err)
		}
	}
	// A repeat destination does not add a distinct one.
	if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-2", "+15550001")); err != nil {
		t.Fatalf("a repeat destination was refused by the distinct budget: %v", err)
	}
	_, err := r.p.Dispatch(ctx, dispatchSMS("u3", "occ-1", "+15550003"))
	throttled := throttledBy(t, err)
	if got := throttled.RetryAfter; got <= 0 || got > time.Hour {
		t.Errorf("RetryAfter is %v, want a positive delay no longer than the hourly window", got)
	}
	if got := len(r.queue.enqueued()); got != 3 {
		t.Errorf("the queue holds %d jobs, want 3: the third distinct destination must not enqueue", got)
	}
}

// The tenant attempt budget refuses once the tenant has made its hourly number
// of attempts, whether or not the carrier accepted them.
func TestTheTenantAttemptHourlyBudgetRefusesPastTheLimit(t *testing.T) {
	r := newSMSRig(t, nil)
	ctx := r.tenantCtx("t1")
	r.setBudget(t, attemptHourlySetting, 2)
	r.liftOtherBudgets(t, attemptHourlySetting)

	if got := r.p.settings.limits().attemptHourly; got != 2 {
		t.Fatalf("the attempt limit reads %d, want 2", got)
	}
	for i := 0; i < 2; i++ {
		if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", fmt.Sprintf("occ-%d", i), "+15550001")); err != nil {
			t.Fatalf("attempt %d inside the budget ended with %v", i+1, err)
		}
	}
	_, err := r.p.Dispatch(ctx, dispatchSMS("u1", "one-too-many", "+15550001"))
	throttledBy(t, err)
	if got := len(r.queue.enqueued()); got != 2 {
		t.Errorf("the queue holds %d jobs, want 2: the dispatch past the attempt budget must not enqueue", got)
	}
}

// A definitive carrier failure refunds the attempt: the send budget frees, so the
// next dispatch to the same recipient passes, while a refused dispatch cannot
// hand a fresh attempt budget back. The refund is what the ledger stores, and the
// notification row still records the failed delivery.
func TestARefundFreesTheSendBudgetButNotTheAttemptBudget(t *testing.T) {
	t.Run("send budget freed", func(t *testing.T) {
		r := newSMSRig(t, nil)
		ctx := r.tenantCtx("t1")
		r.setBudget(t, perRecipientHourlySetting, 1)
		r.liftOtherBudgets(t, perRecipientHourlySetting)
		r.setBudget(t, perRecipientDailySetting, 10000)

		if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-1", "+15550001")); err != nil {
			t.Fatalf("the first dispatch ended with %v", err)
		}
		r.smsSender.failWith(fmt.Errorf("the carrier refused the message: %w", sms.ErrRejected))
		if err := r.handleLastJob(t, ctx); err == nil {
			t.Fatal("the rejected send did not fail the job")
		}
		rows := r.budgetRows("t1")
		if len(rows) != 1 || !rows[0].refunded {
			t.Fatalf("the ledger holds %+v, want one refunded attempt", rows)
		}

		r.smsSender.failWith(nil)
		if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-2", "+15550001")); err != nil {
			t.Fatalf("the refund did not free the send budget: %v", err)
		}
	})

	t.Run("attempt budget kept", func(t *testing.T) {
		r := newSMSRig(t, nil)
		ctx := r.tenantCtx("t1")
		r.setBudget(t, attemptHourlySetting, 1)
		r.liftOtherBudgets(t, attemptHourlySetting)
		r.setBudget(t, perRecipientHourlySetting, 1000)

		if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-1", "+15550001")); err != nil {
			t.Fatalf("the first dispatch ended with %v", err)
		}
		r.smsSender.failWith(fmt.Errorf("the carrier refused the message: %w", sms.ErrRejected))
		if err := r.handleLastJob(t, ctx); err == nil {
			t.Fatal("the rejected send did not fail the job")
		}
		if rows := r.budgetRows("t1"); len(rows) != 1 || !rows[0].refunded {
			t.Fatalf("the ledger holds %+v, want one refunded attempt", rows)
		}

		r.smsSender.failWith(nil)
		_, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-2", "+15550001"))
		throttledBy(t, err)
	})
}

// ceilingSetting is the cross-channel ceiling's full path, as an operator
// writes it. The ledger module declares it under the shared sms namespace, and
// this module reads that one declaration.
const ceilingSetting = "sms.ceiling-hourly"

// The cross-channel ceiling refuses a dispatch at its limit even when the rows
// filling it were written by both channels. It is consulted before the carrier,
// so a refused dispatch enqueues nothing, reaches no sender and writes no row.
//
// Skipping the ceiling check lets the dispatch through, which is the failure
// this case reports.
func TestTheSharedCeilingRefusesBeforeTheCarrier(t *testing.T) {
	r := newSMSRig(t, nil)
	ctx := r.tenantCtx("t1")
	r.setBudget(t, ceilingSetting, 2)
	r.liftOtherBudgets(t, "")

	// One attempt from each sending channel reaches the ceiling together. The
	// notification row belongs to another tenant, so this case's own row count
	// cannot be what refuses the dispatch: only the shared count can.
	r.seedLedger(t, budget.Attempt{
		Channel: budget.ChannelAuthn, Purpose: "login", DestinationMAC: "authn-destination",
		Outcome: budget.OutcomeSent,
	})
	r.seedLedger(t, budget.Attempt{
		Channel: budget.ChannelNotification, Purpose: "alert", DestinationMAC: "notification-destination",
		TenantID: "another-tenant", Outcome: budget.OutcomeSent,
	})

	_, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-1", "+15550001"))
	throttled := throttledBy(t, err)
	if got := throttled.RetryAfter; got <= 0 || got > time.Hour {
		t.Errorf("RetryAfter is %v, want a positive delay no longer than the hourly window", got)
	}
	if got := len(r.queue.enqueued()); got != 0 {
		t.Errorf("the queue holds %d jobs, want 0: a dispatch refused by the ceiling must not enqueue", got)
	}
	if got := r.smsSender.sendCount(); got != 0 {
		t.Errorf("the carrier was asked %d times, want 0: the ceiling is checked before the send", got)
	}
	if got := len(r.budgetRows("t1")); got != 0 {
		t.Errorf("the ledger holds %d rows for the tenant, want 0: a refused dispatch writes no row", got)
	}
}

// A refunded attempt frees the cross-channel ceiling: the ceiling counts the
// ledger the same way the send budgets do, so a certain failure gives its share
// back.
//
// Skipping the ceiling check makes the dispatch at the ceiling pass where this
// case requires a refusal.
func TestARefundedAttemptFreesTheSharedCeiling(t *testing.T) {
	r := newSMSRig(t, nil)
	ctx := r.tenantCtx("t1")
	r.setBudget(t, ceilingSetting, 1)
	r.liftOtherBudgets(t, "")

	charged := r.seedLedger(t, budget.Attempt{
		Channel: budget.ChannelNotification, Purpose: "alert", DestinationMAC: "notification-destination",
		TenantID: "another-tenant", Outcome: budget.OutcomePending,
	})
	if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-1", "+15550001")); err == nil {
		t.Fatal("a dispatch at the shared ceiling was admitted")
	}

	r.refundLedger(t, charged)
	if got := r.ceilingCount(t); got != 0 {
		t.Fatalf("the ceiling counts %d after a refund, want 0", got)
	}
	if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-2", "+15550001")); err != nil {
		t.Fatalf("the refund did not free the shared ceiling: %v", err)
	}
	if got := r.ceilingCount(t); got != 1 {
		t.Errorf("the ceiling counts %d after the admitted dispatch, want 1", got)
	}
}

// The ceiling's accounting matches the ledger's predicates on the real send
// path: a successful send stays in both the send and the attempt counts, and a
// definitive provider failure frees the ceiling and the send budget while the
// attempt count keeps the refunded row.
func TestTheCeilingFollowsTheLedgerPredicates(t *testing.T) {
	r := newSMSRig(t, nil)
	ctx := r.tenantCtx("t1")
	r.setBudget(t, ceilingSetting, 10)
	r.liftOtherBudgets(t, "")

	recipient := recipientHash("t1", "u1")
	attempts := budget.Query{
		Channel: budget.ChannelNotification, Dimension: budget.DimensionDestination,
		Value: recipient, Since: time.Now().UTC().Add(-time.Hour), IncludeRefunded: true,
	}

	if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-1", "+15550001")); err != nil {
		t.Fatalf("the first dispatch ended with %v", err)
	}
	if err := r.handleLastJob(t, ctx); err != nil {
		t.Fatalf("the successful send failed: %v", err)
	}
	if got := r.ceilingCount(t); got != 1 {
		t.Errorf("the ceiling counts %d after a successful send, want 1", got)
	}
	if got, err := budget.New(r.db.DB()).Count(context.Background(), attempts); err != nil || got != 1 {
		t.Errorf("the attempt count = %d (err %v) after a successful send, want 1", got, err)
	}

	r.smsSender.failWith(fmt.Errorf("the carrier refused the message: %w", sms.ErrRejected))
	if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-2", "+15550001")); err != nil {
		t.Fatalf("the second dispatch ended with %v", err)
	}
	if err := r.handleLastJob(t, ctx); err == nil {
		t.Fatal("the rejected send did not fail the job")
	}
	if got := r.ceilingCount(t); got != 1 {
		t.Errorf("the ceiling counts %d after a definitive failure, want 1: the refunded row must free its share", got)
	}
	if got, err := budget.New(r.db.DB()).Count(context.Background(), attempts); err != nil || got != 2 {
		t.Errorf("the attempt count = %d (err %v) after a definitive failure, want 2: the refunded row stays an attempt", got, err)
	}
}

// A dispatch that makes no sms attempt spends no share of the ceiling: it writes
// no ledger row, so the shared count does not move and a later sms dispatch is
// still admitted.
func TestADispatchWithoutAnSMSAttemptSpendsNoCeiling(t *testing.T) {
	r := newRig(t, func(h *host) {
		h.sms = true
		h.specs = map[string]notification.TypeSpec{"test.declarer": smsSpec()}
	})
	ctx := r.tenantCtx("t1")
	r.setBudget(t, ceilingSetting, 1)
	r.liftOtherBudgets(t, "")

	r.setChannels(notification.ChannelInApp)
	if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-1", "+15550001")); err != nil {
		t.Fatalf("the in-app dispatch ended with %v", err)
	}
	if got := r.ceilingCount(t); got != 0 {
		t.Errorf("the ceiling counts %d after an in-app dispatch, want 0", got)
	}

	r.setChannels(notification.ChannelSMS)
	if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-2", "+15550001")); err != nil {
		t.Fatalf("the sms dispatch after an in-app one ended with %v: the in-app dispatch spent the ceiling", err)
	}
}

// The lock the ceiling is charged under is the one the authentication path
// takes, so the two channels serialise on one lock. The value is stated as the
// literal the authentication path writes.
func TestTheCeilingLockNameIsTheSharedKey(t *testing.T) {
	if ceilingLockKey != "sms.budget:lock:ceiling" {
		t.Errorf("the ceiling lock key = %q, want %q", ceilingLockKey, "sms.budget:lock:ceiling")
	}
}

// An uncertain outcome is not a definitive failure: the message may have gone
// out, so its send budget stays spent.
func TestAnUncertainOutcomeDoesNotRefundTheAttempt(t *testing.T) {
	r := newSMSRig(t, nil)
	ctx := r.tenantCtx("t1")
	r.setBudget(t, perRecipientHourlySetting, 1)
	r.liftOtherBudgets(t, perRecipientHourlySetting)

	if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-1", "+15550001")); err != nil {
		t.Fatalf("the first dispatch ended with %v", err)
	}
	r.smsSender.failWith(fmt.Errorf("the carrier answered ambiguously: %w", sms.ErrUncertain))
	if err := r.handleLastJob(t, ctx); err == nil {
		t.Fatal("the uncertain send did not fail the job")
	}
	if rows := r.budgetRows("t1"); len(rows) != 1 || rows[0].refunded {
		t.Fatalf("the ledger holds %+v, want one attempt that is not refunded", rows)
	}
	if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-2", "+15550001")); err == nil {
		t.Fatal("the send budget was freed by an uncertain outcome")
	}
}

// Every sms attempt writes one row on the shared ledger with the notification
// channel, the type as its purpose and the tenant it belongs to.
func TestEverySMSAttemptWritesOneLedgerRow(t *testing.T) {
	r := newSMSRig(t, nil)
	ctx := r.tenantCtx("t1")

	if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-1", "+15550001")); err != nil {
		t.Fatalf("the dispatch ended with %v", err)
	}
	rows := r.budgetRows("t1")
	if len(rows) != 1 {
		t.Fatalf("the ledger holds %d rows, want 1", len(rows))
	}
	row := rows[0]
	if row.channel != string(budget.ChannelNotification) {
		t.Errorf("the row's channel is %q, want %q", row.channel, budget.ChannelNotification)
	}
	if row.purpose != typeName {
		t.Errorf("the row's purpose is %q, want %q", row.purpose, typeName)
	}
	if row.tenant != "t1" {
		t.Errorf("the row's tenant is %q, want t1", row.tenant)
	}
	if row.destination == "" {
		t.Error("the row carries no recipient digest")
	}
	if row.outcome != string(budget.OutcomePending) {
		t.Errorf("the row's outcome is %q, want %q", row.outcome, budget.OutcomePending)
	}
	if row.refunded {
		t.Error("a fresh attempt is already refunded")
	}
	if since := time.Since(row.attemptedAt); since < 0 || since > time.Minute {
		t.Errorf("the attempt was stamped %v ago, want it near now", since)
	}
}

// A spent counter left in the key-value store by the old budget is not
// consulted by the sms path: the ledger is authoritative for sms, so a dispatch
// the old key would have refused passes.
func TestTheOldKVCountersAreNotConsulted(t *testing.T) {
	r := newSMSRig(t, nil)
	ctx := r.tenantCtx("t1")
	r.setBudget(t, perRecipientHourlySetting, 2)
	r.liftOtherBudgets(t, perRecipientHourlySetting)

	subject := oldRecipientSubject("t1", "u1")
	key := ModuleName + ":budget:recipient-hourly:" + subject + ":" + time.Now().UTC().Format("2006010215")
	if err := r.kv.Set(ctx, key, []byte("999"), time.Hour); err != nil {
		t.Fatalf("seeding the old counter: %v", err)
	}
	if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-1", "+15550001")); err != nil {
		t.Fatalf("a dispatch was refused by an old key-value counter: %v", err)
	}
}

// oldRecipientSubject is the digest the key-value budget folded a recipient into.
func oldRecipientSubject(tenantID, userID string) string {
	sum := sha256.Sum256([]byte(tenantID + "\x00" + userID))
	return hex.EncodeToString(sum[:])
}

// A dispatch that resolves to no sms channel writes no ledger row, so it spends
// none of the sms budgets. The email budget is lifted here: it is a separate
// path, and only the sms ledger is under test.
func TestAnInAppOrEmailOnlyDispatchDoesNotSpendAnSMSBudget(t *testing.T) {
	r := newRig(t, func(h *host) { h.mail = true })
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelInApp, notification.ChannelEmail)
	r.liftOtherBudgets(t, "")

	for i := 0; i < 3; i++ {
		if _, err := r.p.Dispatch(ctx, dispatch("u1", fmt.Sprintf("occ-%d", i), validParams())); err != nil {
			t.Fatalf("an in-app and email dispatch %d spent an sms budget: %v", i+1, err)
		}
	}
	if rows := r.budgetRows("t1"); len(rows) != 0 {
		t.Errorf("the ledger holds %+v, want no sms attempt", rows)
	}
}

// A dispatch to an sms channel whose seam this assembly did not deliver is
// skipped rather than sent, so it spends none of the sms budgets.
func TestADispatchToAnAbsentSMSSeamDoesNotSpendAnSMSBudget(t *testing.T) {
	r := newRig(t, func(h *host) {
		h.specs = map[string]notification.TypeSpec{"test.declarer": smsSpec()}
	})
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelSMS)
	r.setBudget(t, perRecipientHourlySetting, 1)
	r.liftOtherBudgets(t, perRecipientHourlySetting)

	for i := 0; i < 3; i++ {
		if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", fmt.Sprintf("occ-%d", i), "+15550001")); err != nil {
			t.Fatalf("dispatch %d to an absent sms seam spent an sms budget: %v", i+1, err)
		}
	}
	if rows := r.budgetRows("t1"); len(rows) != 0 {
		t.Errorf("the ledger holds %+v, want no sms attempt", rows)
	}
}

// failingBudgetStore is a ledger whose count fails, so a case can hold that a
// budget which cannot be counted refuses the dispatch instead of letting it
// through. Every other operation is the real store's.
type failingBudgetStore struct {
	budget.Store
	err error
}

func (f failingBudgetStore) Count(context.Context, budget.Query) (int64, error) {
	return 0, f.err
}

// A budget that cannot be counted is an outage, not a pass: the dispatch ends
// with an error that reads as authn.ErrUnavailable and carries the store's own
// cause, and nothing is enqueued.
func TestADispatchWhoseLedgerCannotBeCountedIsRefused(t *testing.T) {
	r := newSMSRig(t, nil)
	ctx := r.tenantCtx("t1")

	storeDown := errors.New("the ledger is down")
	r.p.newLedger = func(h *gorm.DB) budget.Store {
		return failingBudgetStore{Store: budget.New(h), err: storeDown}
	}

	_, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-1", "+15550001"))
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("Dispatch ended with %v, want authn.ErrUnavailable", err)
	}
	if !errors.Is(err, storeDown) {
		t.Errorf("the error does not carry the ledger's cause: %v", err)
	}
	if got := len(r.queue.enqueued()); got != 0 {
		t.Errorf("a dispatch whose budget could not be counted enqueued %d jobs, want 0", got)
	}
}

// The limits are read from the store on every dispatch rather than fixed when
// the module is built: raising one reaches the next dispatch.
func TestTheBudgetLimitsAreReadFromTheStore(t *testing.T) {
	r := newSMSRig(t, nil)
	ctx := r.tenantCtx("t1")
	r.setBudget(t, perRecipientHourlySetting, 1)
	r.liftOtherBudgets(t, perRecipientHourlySetting)

	if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-1", "+15550001")); err != nil {
		t.Fatalf("the first dispatch ended with %v", err)
	}
	if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-2", "+15550001")); err == nil {
		t.Fatal("a stored limit of 1 let a second dispatch through")
	}

	r.setBudget(t, perRecipientHourlySetting, 3)
	if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-3", "+15550001")); err != nil {
		t.Fatalf("the dispatch after the limit was raised ended with %v", err)
	}
}

// emailCounter reads one email budget counter from the key-value store and
// reports whether it is present.
func (r *rig) emailCounter(t *testing.T, key string) (float64, bool) {
	t.Helper()
	raw, present, err := r.kv.Get(r.t.Context(), key)
	if err != nil {
		t.Fatalf("reading the email counter %q: %v", key, err)
	}
	if !present {
		return 0, false
	}
	value, parseErr := strconv.ParseFloat(string(raw), 64)
	if parseErr != nil {
		t.Fatalf("parsing the email counter %q = %q: %v", key, raw, parseErr)
	}
	return value, true
}

// emailRecipientHourlyKey is the fixed-window key one recipient's hourly email
// count lands on. It is the layout the module used before the sms path moved to
// the shared ledger. The daily and deployment keys follow the same shape.
func emailRecipientHourlyKey(tenantID, userID string, now time.Time) string {
	return ModuleName + ":budget:recipient-hourly:" + recipientHash(tenantID, userID) + ":" + now.Format(hourLayout)
}

func emailRecipientDailyKey(tenantID, userID string, now time.Time) string {
	return ModuleName + ":budget:recipient-daily:" + recipientHash(tenantID, userID) + ":" + now.Format(dayLayout)
}

func emailGlobalHourlyKey(now time.Time) string {
	return ModuleName + ":budget:global-hourly:" + now.Format(hourLayout)
}

// emailCounterAmong answers the first of the candidate keys that is present,
// with its value. A case lists the bucket before and after the dispatch so that
// a dispatch crossing a bucket boundary does not make the case flaky.
func (r *rig) emailCounterAmong(t *testing.T, keys ...string) (float64, bool) {
	t.Helper()
	for _, key := range keys {
		if value, present := r.emailCounter(t, key); present {
			return value, true
		}
	}
	return 0, false
}

// The email per-recipient hourly budget refuses an email delivery past the
// limit and enqueues nothing for the refused one. Disabling the restored email
// charge lets the refused dispatch through, which is the failure this case
// reports.
func TestTheEmailPerRecipientHourlyBudgetRefusesPastTheLimit(t *testing.T) {
	r := newRig(t, func(h *host) { h.mail = true })
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelEmail)
	r.setBudget(t, perRecipientHourlySetting, 2)
	r.liftOtherBudgets(t, perRecipientHourlySetting)

	for i := 0; i < 2; i++ {
		if _, err := r.p.Dispatch(ctx, dispatch("u1", fmt.Sprintf("occ-%d", i), validParams())); err != nil {
			t.Fatalf("email dispatch %d inside the budget ended with %v", i+1, err)
		}
	}
	_, err := r.p.Dispatch(ctx, dispatch("u1", "one-too-many", validParams()))
	throttled := throttledBy(t, err)
	if got := throttled.RetryAfter; got <= 0 || got > time.Hour {
		t.Errorf("RetryAfter is %v, want a positive delay no longer than the hourly window", got)
	}
	if got := len(r.queue.enqueued()); got != 2 {
		t.Errorf("the queue holds %d jobs, want 2: a refused email dispatch must not enqueue", got)
	}
}

// The email deployment-wide budget refuses an email delivery once the whole
// deployment has reached it, even when every recipient is still inside its own
// window.
func TestTheEmailGlobalHourlyBudgetRefusesPastTheLimit(t *testing.T) {
	r := newRig(t, func(h *host) { h.mail = true })
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelEmail)
	r.liftOtherBudgets(t, globalHourlySetting)
	r.setBudget(t, globalHourlySetting, 1)

	if _, err := r.p.Dispatch(ctx, dispatch("u1", "occ-1", validParams())); err != nil {
		t.Fatalf("the first email dispatch inside the deployment budget ended with %v", err)
	}
	_, err := r.p.Dispatch(ctx, dispatch("u2", "occ-2", validParams()))
	throttledBy(t, err)
	if got := len(r.queue.enqueued()); got != 1 {
		t.Errorf("the queue holds %d jobs, want 1: the deployment-wide refusal must not enqueue", got)
	}
}

// An email delivery charges the key-value counters the module used before the
// sms path moved to the ledger: the recipient's hourly and daily windows and the
// deployment window each hold one for the delivery. A test that stops charging
// email finds no counter and fails here.
func TestAnEmailDeliveryChargesTheKeyValueCounters(t *testing.T) {
	r := newRig(t, func(h *host) { h.mail = true })
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelEmail)
	r.liftOtherBudgets(t, "")

	before := time.Now().UTC()
	if _, err := r.p.Dispatch(ctx, dispatch("u1", "occ-1", validParams())); err != nil {
		t.Fatalf("the email dispatch ended with %v", err)
	}
	after := time.Now().UTC()
	checks := []struct {
		name string
		keys []string
	}{
		{"recipient hourly", []string{
			emailRecipientHourlyKey("t1", "u1", before), emailRecipientHourlyKey("t1", "u1", after),
		}},
		{"recipient daily", []string{
			emailRecipientDailyKey("t1", "u1", before), emailRecipientDailyKey("t1", "u1", after),
		}},
		{"deployment", []string{emailGlobalHourlyKey(before), emailGlobalHourlyKey(after)}},
	}
	for _, check := range checks {
		value, present := r.emailCounterAmong(t, check.keys...)
		if !present {
			t.Errorf("the %s email counter is absent: the delivery charged nothing", check.name)
			continue
		}
		if value != 1 {
			t.Errorf("the %s email counter is %g, want 1", check.name, value)
		}
	}
}

// Email and sms do not share the sms ledger: an email delivery charges its own
// key-value counters and writes no ledger row, while an sms dispatch writes one.
func TestAnEmailDeliveryWritesNoSMSLedgerRow(t *testing.T) {
	r := newRig(t, func(h *host) { h.mail = true; h.sms = true })
	ctx := r.tenantCtx("t1")
	r.liftOtherBudgets(t, "")

	r.setChannels(notification.ChannelEmail)
	before := time.Now().UTC()
	if _, err := r.p.Dispatch(ctx, dispatch("u1", "occ-email", validParams())); err != nil {
		t.Fatalf("the email dispatch ended with %v", err)
	}
	after := time.Now().UTC()
	if _, present := r.emailCounterAmong(t,
		emailRecipientHourlyKey("t1", "u1", before), emailRecipientHourlyKey("t1", "u1", after),
	); !present {
		t.Error("the email dispatch charged no key-value counter, so the ledger assertion would prove nothing")
	}
	if rows := r.budgetRows("t1"); len(rows) != 0 {
		t.Errorf("the ledger holds %+v after an email delivery, want no sms row", rows)
	}
	if got := r.ceilingCount(t); got != 0 {
		t.Errorf("the shared ceiling counts %d after an email delivery, want 0", got)
	}

	r.setChannels(notification.ChannelSMS)
	if _, err := r.p.Dispatch(ctx, dispatch("u1", "occ-sms", validParams())); err != nil {
		t.Fatalf("the sms dispatch ended with %v", err)
	}
	if rows := r.budgetRows("t1"); len(rows) != 1 {
		t.Errorf("the ledger holds %d rows after an sms dispatch, want 1", len(rows))
	}
}

// The module reports the same utilization gauge the authentication capability
// uses, with the notification channel and the window's dimension on each point.
func TestTheBudgetReportsItsUtilization(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	r := newSMSRig(t, func(h *host) { h.meter = provider })
	ctx := r.tenantCtx("t1")

	if _, err := r.p.Dispatch(ctx, dispatchSMS("u1", "occ-1", "+15550001")); err != nil {
		t.Fatalf("the dispatch ended with %v", err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collecting the metrics: %v", err)
	}
	dimensions := map[string]bool{}
	found := false
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != utilizationName {
				continue
			}
			gauge, ok := m.Data.(metricdata.Gauge[float64])
			if !ok {
				t.Fatalf("the utilization instrument carries %T, want a float gauge", m.Data)
			}
			for _, dp := range gauge.DataPoints {
				found = true
				if got, ok := dp.Attributes.Value(utilizationChannelAttribute); !ok || got.AsString() != string(budget.ChannelNotification) {
					t.Errorf("channel attribute = %v (present %v), want %q", got, ok, budget.ChannelNotification)
				}
				purpose, _ := dp.Attributes.Value(utilizationPurposeAttribute)
				switch purpose.AsString() {
				case typeName:
				case purposeShared:
					if dimension, ok := dp.Attributes.Value(utilizationDimensionAttribute); !ok || dimension.AsString() != dimensionCeiling {
						t.Errorf("the shared point carries dimension %v (present %v), want %q", dimension, ok, dimensionCeiling)
					}
				default:
					t.Errorf("purpose attribute = %q, want %q or %q", purpose.AsString(), typeName, purposeShared)
				}
				if dimension, ok := dp.Attributes.Value(utilizationDimensionAttribute); ok {
					dimensions[dimension.AsString()] = true
				}
			}
		}
	}
	if !found {
		t.Fatalf("the reader holds no %s gauge", utilizationName)
	}
	for _, want := range []string{
		dimensionCeiling, dimensionRecipientHourly, dimensionRecipientDaily,
		dimensionTenantDistinctHourly, dimensionAttemptHourly,
	} {
		if !dimensions[want] {
			t.Errorf("the gauge carries no %q point: %v", want, dimensions)
		}
	}
}
