package oidc

import (
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestDescribeReportsTheEnterpriseEntry holds the descriptor the discovery
// endpoint publishes for the enterprise OIDC method: an enterprise entry that
// speaks OIDC, with no label and no fields, opens no account of its own, and
// whose bindings land under the "oidc:" namespace prefix.
func TestDescribeReportsTheEnterpriseEntry(t *testing.T) {
	got := (&product{}).Describe()
	if got.Name != "oidc" || got.Kind != authn.MethodKindEnterprise ||
		got.Protocol != authn.MethodProtocolOIDC || got.Label != "" || got.OpensAccount {
		t.Errorf("Describe() = %+v, want an enterprise OIDC entry with no label or account opening", got)
	}
	if len(got.Fields) != 0 {
		t.Errorf("Describe().Fields = %+v, want none for an enterprise method", got.Fields)
	}
	if want := []string{"oidc:"}; !slices.Equal(got.BindingProviders, want) {
		t.Errorf("Describe().BindingProviders = %+v, want %+v", got.BindingProviders, want)
	}
}
