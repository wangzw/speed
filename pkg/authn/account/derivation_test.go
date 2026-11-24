package account

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/password"
)

// A derivation that finds the module's concurrent capacity full is ErrBusy, not a wrong password.
func TestADerivationThatFindsTheWidthFullIsBusy(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.user("crowded", "correct horse battery")
		h.p.gov = password.NewGovernor(1, h.p.cfg.memoryBudget)
		h.p.wait = 10 * time.Millisecond
		release, err := h.p.gov.Hold(ctx, 0, 0)
		if err != nil {
			t.Fatalf("taking the one place: %v", err)
		}
		defer release()
		if _, _, err := h.p.Verify(ctx, "crowded", "correct horse battery"); !errors.Is(err, authn.ErrBusy) {
			t.Errorf("a saturated governor gave %v, want ErrBusy", err)
		}
	})
}
