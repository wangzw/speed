package mail

import (
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// TestOffBySelectionReportsTheSweepOff checks that the none selection reports the
// capability off with a reason naming the selection.
func TestOffBySelectionReportsTheSweepOff(t *testing.T) {
	reg := pickRegistry(t, `{"mail":{"carrier":"none"}}`)
	off, reason, err := OffBySelection(reg)
	if err != nil {
		t.Fatalf("reading the selection: %v", err)
	}
	if !off {
		t.Fatal("mail.carrier=none did not report the capability off")
	}
	if !strings.Contains(reason, "none") {
		t.Errorf("the reason %q does not name the selection", reason)
	}
}

// TestOffBySelectionWithoutAReaderLeavesTheCapabilityOn checks that an assembly
// with no configuration reader is not reported off.
func TestOffBySelectionWithoutAReaderLeavesTheCapabilityOn(t *testing.T) {
	off, _, err := OffBySelection(core.New())
	if err != nil {
		t.Fatalf("reading a selection with no reader: %v", err)
	}
	if off {
		t.Fatal("an assembly with no configuration reader reported the mail capability off")
	}
}

// TestSelectionNamesACarrierThatCannotRun checks that a selection naming an
// unrunnable carrier is a startup error naming the module, carrier and reason.
func TestSelectionNamesACarrierThatCannotRun(t *testing.T) {
	primary := pickModule("mail.pick.primary", "primary", false, unconfigured)
	reg := pickRegistry(t, `{"mail":{"carrier":"primary"}}`, primary)

	_, err := primary.Prepare(t.Context(), reg)
	if err == nil {
		t.Fatal("a named but unrunnable carrier prepared without an error")
	}
	for _, want := range []string{"mail.pick.primary", "primary", "cannot run", "the section is unset"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure %q does not name %q", err.Error(), want)
		}
	}
}
