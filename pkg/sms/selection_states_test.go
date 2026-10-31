package sms

import (
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// The selection states below mirror the ones mail's selection cases hold. The
// two capabilities share one mechanism, so a state covered on one side and not
// the other is an untested leg of this package's copy of it.

// smsUnconfigured is the stance of a carrier whose section is wholly absent.
func smsUnconfigured(*pickConfig) (core.Enablement, error) {
	return core.Enablement{State: core.StateDisabled, Reason: "the section is unset"}, nil
}

// TestExplicitSelectionStandsTheOtherCarrierDown holds the named-carrier state:
// a carrier the selection does not name stands down with a reason, without its
// own configuration being judged.
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

// TestFallbackStatesAutoWithoutASelection holds the automatic state: with no
// selection the fallback carrier states StateAuto, so core leaves it running
// when no other carrier is enabled and stands it down when one is.
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

// TestOffBySelectionReportsTheSweepOff holds the dependent's reading of the
// selection: none switches the capability off and the reason a dependent states
// when it stands itself down names the selection.
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

// TestOffBySelectionWithoutAReaderLeavesTheCapabilityOn holds the other side:
// an assembly with no configuration reader has no selection to read, so it is
// not reported off.
func TestOffBySelectionWithoutAReaderLeavesTheCapabilityOn(t *testing.T) {
	off, _, err := OffBySelection(core.New())
	if err != nil {
		t.Fatalf("reading a selection with no reader: %v", err)
	}
	if off {
		t.Fatal("an assembly with no configuration reader reported the SMS capability off")
	}
}

// TestSelectionNamesACarrierThatCannotRun holds the named-but-unrunnable state:
// a selection that names this carrier makes its configuration gap a startup
// error, not a silent stand-down. The message names the module, the carrier and
// the carrier's own reason.
//
// The error does not wrap ErrInvalidConfig today, while the unknown-name path
// does. The test holds the observable text only so it does not pin that
// classification either way.
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
