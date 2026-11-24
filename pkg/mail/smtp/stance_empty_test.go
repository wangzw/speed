package smtp

import (
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// TestAnEmptySectionStandsDown checks that the empty prototype stands the carrier
// down with a reason instead of failing.
func TestAnEmptySectionStandsDown(t *testing.T) {
	got, err := stance(&configDefaults)
	if err != nil {
		t.Fatalf("the empty section failed instead of standing down: %v", err)
	}
	if got.State != core.StateDisabled {
		t.Errorf("the empty section = %+v, want StateDisabled", got)
	}
	if got.Reason == "" {
		t.Error("the empty section stood down without a reason")
	}
}
