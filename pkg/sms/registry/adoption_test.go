package registry

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/sms"
)

// TestALegacyRowIsAdoptedByTheFirstCarrier covers the adoption: a row with no
// carrier is rewritten to the mounted carrier's name and the ownership marker is
// written beside it, so the row the load reads is the row the old implementation
// wrote.
func TestALegacyRowIsAdoptedByTheFirstCarrier(t *testing.T) {
	r := newRig(t, openDialect(t, sqliteDialect), newProvider(), settingsOf(t, nil, 0))
	r.plantLegacy("otp", []string{"code"}, map[string]string{"en": "c"},
		map[string]sms.Approval{"en": {State: sms.ApprovalApproved}}, true, base)

	if err := r.s.adopt(context.Background()); err != nil {
		t.Fatalf("adopting the legacy row: %v", err)
	}
	if got := r.carrierOf("otp"); got != "fake" {
		t.Errorf("the adopted row names the carrier %q, want fake", got)
	}
	owner, ok := r.ownerOf()
	if !ok || owner != "fake" {
		t.Errorf("the ownership marker is %q (present=%v), want fake", owner, ok)
	}

	r.load()
	if _, err := r.s.Resolve(sms.Message{Template: "otp", Locale: "en", Params: map[string]string{"code": "x"}}); err != nil {
		t.Errorf("resolving an adopted template returned %v", err)
	}

	// The same carrier starting again finds the marker and the adopted row in
	// place, and adopting changes nothing.
	if err := r.s.adopt(context.Background()); err != nil {
		t.Errorf("a second adoption by the same carrier returned %v", err)
	}
}

// TestADifferentCarrierRefusesToTakeOverAdoptedRows covers the marker: once one
// carrier has adopted the legacy rows, a process mounting another carrier fails
// its startup instead of silently taking them over.
func TestADifferentCarrierRefusesToTakeOverAdoptedRows(t *testing.T) {
	r := newRig(t, openDialect(t, sqliteDialect), newProvider(), settingsOf(t, nil, 0))
	r.plantLegacy("otp", []string{"code"}, map[string]string{"en": "c"},
		map[string]sms.Approval{"en": {State: sms.ApprovalApproved}}, true, base)
	if err := r.s.adopt(context.Background()); err != nil {
		t.Fatalf("adopting the legacy row: %v", err)
	}

	other := newRigOn(t, r.h, newNamedProvider("other"), settingsOf(t, nil, 0))
	err := other.s.adopt(context.Background())
	if !errors.Is(err, sms.ErrInvalidConfig) {
		t.Fatalf("a different carrier adopting the rows returned %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), `"fake"`) || !strings.Contains(err.Error(), `"other"`) {
		t.Errorf("the failure does not name both carriers: %v", err)
	}
	other.load()
	if got := other.s.Declared(); len(got) != 0 {
		t.Errorf("the other carrier loaded %+v, want none of the adopted rows", got)
	}
}

// TestADatabaseWithNoLegacyRowGetsNoOwnershipMarker covers the other side of the
// marker: adoption has no ownership question to settle when every row already
// names a carrier, so it writes nothing and two carriers may share the database.
func TestADatabaseWithNoLegacyRowGetsNoOwnershipMarker(t *testing.T) {
	r := newRig(t, openDialect(t, sqliteDialect), newProvider(), settingsOf(t, nil, 0))
	r.plant("otp", []string{"code"}, map[string]string{"en": "c"},
		map[string]sms.Approval{"en": {State: sms.ApprovalApproved}}, true, base)

	if err := r.s.adopt(context.Background()); err != nil {
		t.Fatalf("adopting over a database with no legacy row: %v", err)
	}
	if owner, ok := r.ownerOf(); ok {
		t.Errorf("the adoption wrote the ownership marker %q for a database with no legacy row", owner)
	}
	if got := r.carrierOf("otp"); got != "fake" {
		t.Errorf("the row names the carrier %q, want it untouched", got)
	}
}
