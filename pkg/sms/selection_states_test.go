package sms

import (
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

func smsUnconfigured(*pickConfig) (core.Enablement, error) {
	return core.Enablement{State: core.StateDisabled, Reason: "the section is unset"}, nil
}

// TestExplicitSelectionStandsTheOtherCarrierDown checks that a carrier the selection does not name stands down with a reason.
func TestExplicitSelectionStandsTheOtherCarrierDown(t *testing.T) {
	primary := pickModule("sms.pick.primary", "primary", false, configured)
	fallback := pickModule("sms.pick.fallback", "log", true, configured)
	reg := pickRegistry(t, `{"sms":{"carrier":"primary"}}`, primary, fallback)

	got, err := primary.Prepare(t.Context(), reg)
	if err != nil {
		t.Fatalf("the selected carrier: %v", err)
	}
	if got.State != core.StateEnabled {
		t.Errorf("the selected carrier = %+v, want enabled", got)
	}
	other, err := fallback.Prepare(t.Context(), reg)
	if err != nil {
		t.Fatalf("the unselected carrier: %v", err)
	}
	if other.State != core.StateDisabled || !strings.Contains(other.Reason, "primary") {
		t.Errorf("the unselected carrier = %+v, want disabled naming the selection", other)
	}
}

// TestFallbackStatesAutoWithoutASelection checks that the fallback carrier states StateAuto without a selection.
func TestFallbackStatesAutoWithoutASelection(t *testing.T) {
	primary := pickModule("sms.pick.primary", "primary", false, smsUnconfigured)
	fallback := pickModule("sms.pick.fallback", "log", true, configured)
	reg := pickRegistry(t, `{}`, primary, fallback)

	got, err := primary.Prepare(t.Context(), reg)
	if err != nil {
		t.Fatalf("the unconfigured carrier: %v", err)
	}
	if got.State != core.StateDisabled {
		t.Errorf("the unconfigured carrier = %+v, want disabled", got)
	}
	fallbackStance, err := fallback.Prepare(t.Context(), reg)
	if err != nil {
		t.Fatalf("the fallback carrier: %v", err)
	}
	if fallbackStance.State != core.StateAuto {
		t.Errorf("the fallback with no selection = %+v, want StateAuto", fallbackStance)
	}
}

// TestOffBySelectionReportsTheSweepOff checks that none reports the capability off with a reason naming the selection.
func TestOffBySelectionReportsTheSweepOff(t *testing.T) {
	reg := pickRegistry(t, `{"sms":{"carrier":"none"}}`)
	off, reason, err := OffBySelection(reg)
	if err != nil {
		t.Fatalf("reading the selection: %v", err)
	}
	if !off {
		t.Fatal("sms.carrier=none did not report the capability off")
	}
	if !strings.Contains(reason, "none") {
		t.Errorf("the reason %q does not name the selection", reason)
	}
}

// TestOffBySelectionWithoutAReaderLeavesTheCapabilityOn checks that an assembly with no reader is not reported off.
func TestOffBySelectionWithoutAReaderLeavesTheCapabilityOn(t *testing.T) {
	off, _, err := OffBySelection(core.New())
	if err != nil {
		t.Fatalf("reading a selection with no reader: %v", err)
	}
	if off {
		t.Fatal("an assembly with no configuration reader reported the SMS capability off")
	}
}

// TestSelectionNamesACarrierThatCannotRun checks that a selection naming this carrier makes
// its configuration gap a startup error naming the module, the carrier and its reason.
func TestSelectionNamesACarrierThatCannotRun(t *testing.T) {
	primary := pickModule("sms.pick.primary", "primary", false, smsUnconfigured)
	reg := pickRegistry(t, `{"sms":{"carrier":"primary"}}`, primary)

	_, err := primary.Prepare(t.Context(), reg)
	if err == nil {
		t.Fatal("a named but unrunnable carrier prepared without an error")
	}
	for _, want := range []string{"sms.pick.primary", "primary", "cannot run", "the section is unset"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure %q does not name %q", err.Error(), want)
		}
	}
}
