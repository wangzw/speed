package preferences

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
)

// TestEffectiveWithoutARowTakesTheDefaultChannels holds the core of the
// effective-set computation.
func TestEffectiveWithoutARowTakesTheDefaultChannels(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		spec := aSpec("billing.invoice_paid", true, notification.ChannelInApp, notification.ChannelEmail)
		h := e.on(t, harness{declare: []core.Module{declaringModule("billing", spec)}})
		h.drive(t, func(_ context.Context, reg *core.Registry) error {
			ctx := tenantCtx(t, reg, "acme")
			got, err := preferencesOf(t, reg).Effective(ctx, "user-1", "billing.invoice_paid")
			if err != nil {
				t.Fatalf("Effective: %v", err)
			}
			if want := []notification.Channel{notification.ChannelInApp, notification.ChannelEmail}; !slices.Equal(got, want) {
				t.Errorf("Effective = %v, want %v", got, want)
			}
			return nil
		})
	})
}

// TestSetReplacesTheChannelSetAndEffectiveReportsIt holds that a write replaces
// the set wholesale and that the read then reports it.
func TestSetReplacesTheChannelSetAndEffectiveReportsIt(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		spec := aSpec("billing.invoice_paid", true, notification.ChannelInApp, notification.ChannelEmail)
		h := e.on(t, harness{declare: []core.Module{declaringModule("billing", spec)}})
		h.drive(t, func(_ context.Context, reg *core.Registry) error {
			ctx := tenantCtx(t, reg, "acme")
			prefs := preferencesOf(t, reg)

			written, err := prefs.Set(ctx, "user-1", "billing.invoice_paid", []notification.Channel{notification.ChannelSMS})
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			if want := []notification.Channel{notification.ChannelSMS}; !slices.Equal(written, want) {
				t.Errorf("Set returned %v, want %v", written, want)
			}
			got, err := prefs.Effective(ctx, "user-1", "billing.invoice_paid")
			if err != nil {
				t.Fatalf("Effective: %v", err)
			}
			if want := []notification.Channel{notification.ChannelSMS}; !slices.Equal(got, want) {
				t.Errorf("Effective = %v, want %v", got, want)
			}
			written, err = prefs.Set(ctx, "user-1", "billing.invoice_paid", []notification.Channel{notification.ChannelEmail})
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			if want := []notification.Channel{notification.ChannelEmail}; !slices.Equal(written, want) {
				t.Errorf("second Set returned %v, want %v", written, want)
			}
			if n := rowCount(t, reg); n != 1 {
				t.Errorf("the table holds %d rows, want 1: a write replaces the row", n)
			}
			return nil
		})
	})
}

// TestTheEffectiveSetIsDeduplicatedAndOrderedByTheClosedSet holds that the
// effective set is deduplicated and ordered by the closed channel set.
func TestTheEffectiveSetIsDeduplicatedAndOrderedByTheClosedSet(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		spec := aSpec("billing.invoice_paid", true, notification.ChannelInApp)
		h := e.on(t, harness{declare: []core.Module{declaringModule("billing", spec)}})
		h.drive(t, func(_ context.Context, reg *core.Registry) error {
			ctx := tenantCtx(t, reg, "acme")
			prefs := preferencesOf(t, reg)
			written, err := prefs.Set(ctx, "user-1", "billing.invoice_paid", []notification.Channel{
				notification.ChannelSMS, notification.ChannelInApp, notification.ChannelSMS, notification.ChannelEmail,
			})
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			want := []notification.Channel{notification.ChannelInApp, notification.ChannelEmail, notification.ChannelSMS}
			if !slices.Equal(written, want) {
				t.Errorf("Set returned %v, want %v deduplicated and ordered", written, want)
			}
			return nil
		})
	})
}

// TestANotUnsubscribableTypeTakesAllDefaultsAndRefusesAWrite holds that an
// unsubscribable type takes all taxonomy defaults and never stores a row.
func TestANotUnsubscribableTypeTakesAllDefaultsAndRefusesAWrite(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		spec := aSpec("authn.code.sent", false, notification.ChannelInApp)
		h := e.on(t, harness{declare: []core.Module{declaringModule("authn", spec)}})
		h.drive(t, func(_ context.Context, reg *core.Registry) error {
			ctx := tenantCtx(t, reg, "acme")
			prefs := preferencesOf(t, reg)
			got, err := prefs.Effective(ctx, "user-1", "authn.code.sent")
			if err != nil {
				t.Fatalf("Effective: %v", err)
			}
			if want := []notification.Channel{notification.ChannelInApp}; !slices.Equal(got, want) {
				t.Errorf("Effective of a not-unsubscribable type = %v, want %v", got, want)
			}
			if _, err := prefs.Set(ctx, "user-1", "authn.code.sent", []notification.Channel{notification.ChannelEmail}); !errors.Is(err, notification.ErrNotUnsubscribable) {
				t.Errorf("Set on a not-unsubscribable type ended with %v, want ErrNotUnsubscribable", err)
			}
			return nil
		})
	})
}

// TestEffectiveAndSetRefuseAnUnknownTypeAndAMissingTenant holds the two
// refusals both seam methods share.
func TestEffectiveAndSetRefuseAnUnknownTypeAndAMissingTenant(t *testing.T) {
	spec := aSpec("billing.invoice_paid", true)
	h := engines[0].on(t, harness{declare: []core.Module{declaringModule("billing", spec)}})
	h.drive(t, func(_ context.Context, reg *core.Registry) error {
		ctx := tenantCtx(t, reg, "acme")
		prefs := preferencesOf(t, reg)

		if _, err := prefs.Effective(ctx, "user-1", "nope.nope"); !errors.Is(err, notification.ErrUnknownType) {
			t.Errorf("Effective of an unknown type ended with %v, want ErrUnknownType", err)
		}
		if _, err := prefs.Set(ctx, "user-1", "nope.nope", nil); !errors.Is(err, notification.ErrUnknownType) {
			t.Errorf("Set of an unknown type ended with %v, want ErrUnknownType", err)
		}

		noTenant := context.Background()
		if _, err := prefs.Effective(noTenant, "user-1", "billing.invoice_paid"); !errors.Is(err, tenant.ErrNoTenantInContext) {
			t.Errorf("Effective with no tenant ended with %v, want a tenant error", err)
		}
		if _, err := prefs.Set(noTenant, "user-1", "billing.invoice_paid", nil); !errors.Is(err, tenant.ErrNoTenantInContext) {
			t.Errorf("Set with no tenant ended with %v, want a tenant error", err)
		}
		return nil
	})
}

// TestAPreferenceOfOneTenantIsInvisibleToAnother holds that the matrix is
// tenant-scoped: a row written under one tenant does not reach another.
func TestAPreferenceOfOneTenantIsInvisibleToAnother(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		spec := aSpec("billing.invoice_paid", true, notification.ChannelInApp, notification.ChannelEmail)
		h := e.on(t, harness{declare: []core.Module{declaringModule("billing", spec)}})
		h.drive(t, func(_ context.Context, reg *core.Registry) error {
			acme := tenantCtx(t, reg, "acme")
			globex := tenantCtx(t, reg, "globex")
			prefs := preferencesOf(t, reg)
			if _, err := prefs.Set(acme, "user-1", "billing.invoice_paid", []notification.Channel{notification.ChannelSMS}); err != nil {
				t.Fatalf("Set: %v", err)
			}
			got, err := prefs.Effective(globex, "user-1", "billing.invoice_paid")
			if err != nil {
				t.Fatalf("Effective: %v", err)
			}
			if want := []notification.Channel{notification.ChannelInApp, notification.ChannelEmail}; !slices.Equal(got, want) {
				t.Errorf("another tenant's Effective = %v, want the defaults %v", got, want)
			}
			return nil
		})
	})
}
