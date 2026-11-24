package account

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// Every time the module writes goes through internal/clock in UTC, so another zone does not break SQLite's string comparison of times.
func TestTimeComparisonsHoldWithANonUTCCLock(t *testing.T) {
	t.Setenv("TZ", "America/New_York")
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("loading the zone: %v", err)
	}
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.p.now = func() time.Time { return h.clock.read().In(loc) }

		id := h.user("zoned", "correct horse battery")
		base := h.clock.read()
		if err := h.p.SetStatus(ctx, id, authn.StatusDisabled, authn.ReasonAdmin); err != nil {
			t.Fatalf("disabling: %v", err)
		}
		var row accountRow
		if err := h.handle.Where("id = ?", id.String()).Take(&row).Error; err != nil {
			t.Fatalf("reading back: %v", err)
		}
		want := base.Truncate(time.Second).Add(time.Second)
		if !utc(row.ValidAfter).Equal(want) {
			t.Errorf("valid_after is %v, want the UTC %v regardless of the clock's zone", row.ValidAfter, want)
		}

		const key = "zoned-key"
		for range defaults.ThrottleFreeAttempts + 1 {
			if _, err := h.p.Fail(ctx, authn.ScopeIdentifier, key); err != nil {
				t.Fatalf("failing: %v", err)
			}
		}
		if wait, err := h.p.Check(ctx, authn.ScopeIdentifier, key); err != nil || wait <= 0 || wait > 2*time.Second {
			t.Errorf("Check gave %v and %v, want a positive wait under two seconds", wait, err)
		}
		h.clock.advance(3 * time.Second)
		if wait, err := h.p.Check(ctx, authn.ScopeIdentifier, key); err != nil || wait != 0 {
			t.Errorf("Check after the lock gave %v and %v, want no wait", wait, err)
		}

		service := h.service("zoned-robot")
		expiry := h.clock.read().Add(time.Hour)
		_, secret, err := h.p.CreateServiceSecret(ctx, service, "ci", &expiry)
		if err != nil {
			t.Fatalf("making a secret: %v", err)
		}
		if _, _, _, err := h.p.VerifyServiceSecret(ctx, service.String(), secret); err != nil {
			t.Errorf("a live secret gave %v, want it accepted", err)
		}
		h.clock.advance(2 * time.Hour)
		if _, _, _, err := h.p.VerifyServiceSecret(ctx, service.String(), secret); !errors.Is(err, authn.ErrInvalidCredentials) {
			t.Errorf("an expired secret gave %v, want ErrInvalidCredentials", err)
		}
	})
}
