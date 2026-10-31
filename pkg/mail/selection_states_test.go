package mail

import (
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// TestOffBySelectionReportsTheSweepOff holds the dependent's reading of the
// selection: none switches the capability off and the reason a dependent states
// when it stands itself down names the selection.
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

// TestOffBySelectionWithoutAReaderLeavesTheCapabilityOn holds the other side:
// an assembly with no configuration reader has no selection to read, so it is
// not reported off.
func TestOffBySelectionWithoutAReaderLeavesTheCapabilityOn(t *testing.T) {
	off, _, err := OffBySelection(core.New())
	if err != nil {
		t.Fatalf("reading a selection with no reader: %v", err)
	}
	if off {
		t.Fatal("an assembly with no configuration reader reported the mail capability off")
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
