package webauthn

import (
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestDescribeReportsTheCeremony checks that Describe reports the passkey ceremony that opens no account.
func TestDescribeReportsTheCeremony(t *testing.T) {
	got := (&product{}).Describe()
	if got.Name != "passkey" || got.Kind != authn.MethodKindWebAuthn ||
		got.Protocol != "" || got.Label != "" || got.OpensAccount {
		t.Errorf("Describe() = %+v, want a webauthn ceremony named passkey with no protocol, label or account opening", got)
	}
	if len(got.Fields) != 0 {
		t.Errorf("Describe().Fields = %+v, want none for a ceremony method", got.Fields)
	}
}
