package tencent

import (
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// TestAnEmptySectionStandsDown holds the unconfigured state: a run with no
// configuration gives this carrier nothing, so it closes itself with a reason
// instead of failing the run.
func TestAnEmptySectionStandsDown(t *testing.T) {
	got, err := stance(&moduleConfig{})
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
