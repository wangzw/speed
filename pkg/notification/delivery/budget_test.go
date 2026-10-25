package delivery

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/settings"
)

// The full paths of the delivery-budget settings, as an operator writes them.
// The cases state them literally rather than through the package's own path
// variables, so a case run against a tree that does not declare them fails on the
// settings store's refusal rather than on a missing identifier.
const (
	perRecipientHourlySetting = "notification.delivery.per-recipient-hourly"
	perRecipientDailySetting  = "notification.delivery.per-recipient-daily"
	globalHourlySetting       = "notification.delivery.global-hourly"
)

// declaredPerRecipientHourly is the declared default of the per-recipient hourly
// budget. A case that states no limit exercises the default, which is what a
// deployment with no settings row runs on.
const declaredPerRecipientHourly = 20

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

// The per-recipient hourly budget refuses the dispatch past the limit, tells the
// caller when to retry, and enqueues nothing for the refused one. The limit here
// is the declared default, so the case also holds that the module carries one.
func TestThePerRecipientHourlyBudgetRefusesPastTheLimit(t *testing.T) {
	r := newRig(t, func(h *host) { h.mail = true })
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelInApp, notification.ChannelEmail)

	for i := 0; i < declaredPerRecipientHourly; i++ {
		if _, err := r.p.Dispatch(ctx, dispatch("u1", fmt.Sprintf("occ-%d", i), validParams())); err != nil {
			t.Fatalf("dispatch %d inside the budget ended with %v", i+1, err)
		}
	}
	_, err := r.p.Dispatch(ctx, dispatch("u1", "one-too-many", validParams()))
	throttled := throttledBy(t, err)
	if got := throttled.RetryAfter; got <= 0 || got > time.Hour {
		t.Errorf("RetryAfter is %v, want a positive delay no longer than the hourly window", got)
	}
	if got := len(r.queue.enqueued()); got != declaredPerRecipientHourly {
		t.Errorf("the queue holds %d jobs, want %d: a refused dispatch must not enqueue",
			got, declaredPerRecipientHourly)
	}
}

// The per-recipient daily budget refuses the dispatch past its limit on its own:
// the hourly and deployment-wide windows are raised so that only the daily one
// can refuse, and the retry time it reports is bounded by the daily bucket rather
// than the hourly one. The refused dispatch spends and enqueues nothing, so the
// queue holds the one accepted job.
func TestThePerRecipientDailyBudgetRefusesPastTheLimit(t *testing.T) {
	r := newRig(t, func(h *host) { h.mail = true })
	ctx := r.tenantCtx("t1")
	r.setBudget(t, perRecipientHourlySetting, 1000)
	r.setBudget(t, perRecipientDailySetting, 1)
	r.setBudget(t, globalHourlySetting, 10000000)
	r.setChannels(notification.ChannelInApp, notification.ChannelEmail)

	if _, err := r.p.Dispatch(ctx, dispatch("u1", "occ-1", validParams())); err != nil {
		t.Fatalf("the first dispatch inside the daily budget ended with %v", err)
	}
	_, err := r.p.Dispatch(ctx, dispatch("u1", "occ-2", validParams()))
	throttled := throttledBy(t, err)
	if got := throttled.RetryAfter; got <= 0 || got > 24*time.Hour {
		t.Errorf("RetryAfter is %v, want a positive delay no longer than the daily window", got)
	}
	if got := len(r.queue.enqueued()); got != 1 {
		t.Errorf("the queue holds %d jobs, want 1: a refused dispatch must not enqueue", got)
	}
}

// The deployment-wide budget refuses a dispatch once it is spent, across
// different recipients, and reports a retry time.
func TestTheGlobalHourlyBudgetRefusesOnceSpent(t *testing.T) {
	r := newRig(t, func(h *host) { h.mail = true })
	ctx := r.tenantCtx("t1")
	r.setBudget(t, perRecipientHourlySetting, 1000)
	r.setBudget(t, perRecipientDailySetting, 10000)
	r.setBudget(t, globalHourlySetting, 2)
	r.setChannels(notification.ChannelInApp, notification.ChannelEmail)

	for _, user := range []string{"u1", "u2"} {
		if _, err := r.p.Dispatch(ctx, dispatch(user, "occ-1", validParams())); err != nil {
			t.Fatalf("the dispatch to %s inside the global budget ended with %v", user, err)
		}
	}
	_, err := r.p.Dispatch(ctx, dispatch("u3", "occ-1", validParams()))
	throttled := throttledBy(t, err)
	if got := throttled.RetryAfter; got <= 0 || got > time.Hour {
		t.Errorf("RetryAfter is %v, want a positive delay no longer than the hourly window", got)
	}
	if got := len(r.queue.enqueued()); got != 2 {
		t.Errorf("the queue holds %d jobs, want 2: the dispatch past the global budget must not enqueue", got)
	}
}

// One recipient's spent window does not bound another recipient.
func TestAnotherRecipientsWindowDoesNotBoundADifferentRecipient(t *testing.T) {
	r := newRig(t, func(h *host) { h.mail = true })
	ctx := r.tenantCtx("t1")
	r.setBudget(t, perRecipientHourlySetting, 1)
	r.setBudget(t, perRecipientDailySetting, 10)
	r.setBudget(t, globalHourlySetting, 1000)
	r.setChannels(notification.ChannelInApp, notification.ChannelEmail)

	if _, err := r.p.Dispatch(ctx, dispatch("u1", "occ-1", validParams())); err != nil {
		t.Fatalf("the first dispatch to u1 ended with %v", err)
	}
	if _, err := r.p.Dispatch(ctx, dispatch("u1", "occ-2", validParams())); err == nil {
		t.Fatal("the second dispatch to u1 was let through past its window")
	}
	if _, err := r.p.Dispatch(ctx, dispatch("u2", "occ-1", validParams())); err != nil {
		t.Fatalf("a dispatch to u2 was refused by u1's spent window: %v", err)
	}
}

// The limits are read from the store on every dispatch rather than fixed when
// the module is built: raising one reaches the next dispatch.
func TestTheBudgetLimitsAreReadFromTheStore(t *testing.T) {
	r := newRig(t, func(h *host) { h.mail = true })
	ctx := r.tenantCtx("t1")
	r.setBudget(t, perRecipientHourlySetting, 1)
	r.setBudget(t, perRecipientDailySetting, 1000)
	r.setBudget(t, globalHourlySetting, 10000)
	r.setChannels(notification.ChannelInApp, notification.ChannelEmail)

	if _, err := r.p.Dispatch(ctx, dispatch("u1", "occ-1", validParams())); err != nil {
		t.Fatalf("the first dispatch ended with %v", err)
	}
	if _, err := r.p.Dispatch(ctx, dispatch("u1", "occ-2", validParams())); err == nil {
		t.Fatal("a stored limit of 1 let a second dispatch through")
	}

	// The counter now holds two: the accepted dispatch and the refused one, which
	// spends by design. A limit of three read afresh lets the next dispatch
	// through; a limit still cached at one would refuse it.
	r.setBudget(t, perRecipientHourlySetting, 3)
	if _, err := r.p.Dispatch(ctx, dispatch("u1", "occ-3", validParams())); err != nil {
		t.Fatalf("the dispatch after the limit was raised ended with %v", err)
	}
}

// A type whose effective channels are in_app alone goes nowhere outside the
// process, so it spends none of the delivery budgets even when they are spent.
func TestAnInAppOnlyDispatchDoesNotSpendTheDeliveryBudget(t *testing.T) {
	r := newRig(t, func(h *host) { h.mail = true })
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelInApp)
	r.setBudget(t, perRecipientHourlySetting, 1)
	r.setBudget(t, perRecipientDailySetting, 1)
	r.setBudget(t, globalHourlySetting, 1)

	for i := 0; i < 3; i++ {
		if _, err := r.p.Dispatch(ctx, dispatch("u1", fmt.Sprintf("occ-%d", i), validParams())); err != nil {
			t.Fatalf("an in-app only dispatch %d spent a delivery budget: %v", i+1, err)
		}
	}
}

// A dispatch to an external channel whose seam this assembly did not deliver is
// skipped rather than sent, so it spends none of the delivery budgets.
func TestADispatchToAnAbsentExternalSeamDoesNotSpendTheDeliveryBudget(t *testing.T) {
	r := newRig(t, nil)
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelInApp, notification.ChannelEmail)
	r.setBudget(t, perRecipientHourlySetting, 1)
	r.setBudget(t, perRecipientDailySetting, 1)
	r.setBudget(t, globalHourlySetting, 1)

	for i := 0; i < 3; i++ {
		if _, err := r.p.Dispatch(ctx, dispatch("u1", fmt.Sprintf("occ-%d", i), validParams())); err != nil {
			t.Fatalf("dispatch %d to an absent external seam spent a delivery budget: %v", i+1, err)
		}
	}
}

// failingCounters is a kv.KV whose counter increment fails, so a case can hold
// that a budget which cannot be counted refuses the dispatch instead of letting
// it through. Every other operation is the real store's.
type failingCounters struct {
	kv.KV
	err error
}

func (f failingCounters) IncrByFloatWithTTL(context.Context, string, float64, time.Duration) (float64, error) {
	return 0, f.err
}

// A budget that cannot be counted is an outage, not a pass: the dispatch ends
// with an error that reads as authn.ErrUnavailable and carries the store's own
// cause, and nothing is enqueued.
func TestADispatchWhoseBudgetCannotBeCountedIsRefused(t *testing.T) {
	r := newRig(t, func(h *host) { h.mail = true })
	ctx := r.tenantCtx("t1")
	r.setChannels(notification.ChannelInApp, notification.ChannelEmail)

	storeDown := errors.New("the counter store is down")
	r.p.counters = failingCounters{KV: r.p.counters, err: storeDown}

	_, err := r.p.Dispatch(ctx, dispatch("u1", "occ-1", validParams()))
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("Dispatch ended with %v, want authn.ErrUnavailable", err)
	}
	if !errors.Is(err, storeDown) {
		t.Errorf("the error does not carry the counter store's cause: %v", err)
	}
	if got := len(r.queue.enqueued()); got != 0 {
		t.Errorf("a dispatch whose budget could not be counted enqueued %d jobs, want 0", got)
	}
}
