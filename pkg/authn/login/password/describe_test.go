package password

import (
	"reflect"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestDescribeReportsTheFirstStepForm holds the password descriptor: a form asking for identifier and password that opens no account.
func TestDescribeReportsTheFirstStepForm(t *testing.T) {
	got := (&method{}).Describe()
	if got.Name != "password" || got.Kind != authn.MethodKindForm || got.Protocol != "" ||
		got.Label != "" || got.OpensAccount {
		t.Errorf("Describe() = %+v, want a form named password with no protocol, label or account opening", got)
	}
	want := []authn.Field{
		{Name: fieldIdentifier, Kind: authn.FieldText, Required: true},
		{Name: fieldPassword, Kind: authn.FieldPassword, Required: true, Secret: true},
	}
	if !reflect.DeepEqual(got.Fields, want) {
		t.Errorf("Describe().Fields = %+v, want %+v", got.Fields, want)
	}
}
