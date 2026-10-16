package sms

import (
	"reflect"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestDescribeReportsTheFirstStepForm holds the descriptor the discovery
// endpoint publishes for the SMS method: a form whose first step asks for a
// phone number, and which may open an account.
func TestDescribeReportsTheFirstStepForm(t *testing.T) {
	got := (&method{}).Describe()
	if got.Name != "sms" || got.Kind != authn.MethodKindForm || got.Protocol != "" ||
		got.Label != "" || !got.OpensAccount {
		t.Errorf("Describe() = %+v, want an account-opening form named sms with no protocol or label", got)
	}
	want := []authn.Field{{Name: fieldPhone, Kind: authn.FieldPhone, Required: true}}
	if !reflect.DeepEqual(got.Fields, want) {
		t.Errorf("Describe().Fields = %+v, want %+v", got.Fields, want)
	}
}
