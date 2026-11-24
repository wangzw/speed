package registry

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/sms"
)

// TestALegacyRowIsAdoptedByTheFirstCarrier checks that a row with no carrier is rewritten
// to the mounted carrier's name and the ownership marker is written beside it.
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

	if err := r.s.adopt(context.Background()); err != nil {
		t.Errorf("a second adoption by the same carrier returned %v", err)
	}
}

// TestADifferentCarrierRefusesToTakeOverAdoptedRows checks that a process mounting another carrier after adoption fails its startup.
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

// TestADatabaseWithNoLegacyRowGetsNoOwnershipMarker checks that adoption writes no marker when every row already names a carrier.
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
