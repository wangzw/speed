package saml

import (
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestDescribeReportsTheEnterpriseEntry checks that Describe reports the enterprise entry.
func TestDescribeReportsTheEnterpriseEntry(t *testing.T) {
	got := (&product{}).Describe()
	if got.Name != "saml" || got.Kind != authn.MethodKindEnterprise ||
		got.Protocol != authn.MethodProtocolSAML || got.Label != "" || got.OpensAccount {
		t.Errorf("Describe() = %+v, want an enterprise SAML entry with no label or account opening", got)
	}
	if len(got.Fields) != 0 {
		t.Errorf("Describe().Fields = %+v, want none for an enterprise method", got.Fields)
	}
	if want := []string{"saml:"}; !slices.Equal(got.BindingProviders, want) {
		t.Errorf("Describe().BindingProviders = %+v, want %+v", got.BindingProviders, want)
	}
}
