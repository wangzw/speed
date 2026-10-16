package saml

import (
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestDescribeReportsTheEnterpriseEntry holds the descriptor the discovery
// endpoint publishes for the enterprise SAML method: an enterprise entry that
// speaks SAML, with no label and no fields, and which opens no account of its
// own.
func TestDescribeReportsTheEnterpriseEntry(t *testing.T) {
	got := (&product{}).Describe()
	if got.Name != "saml" || got.Kind != authn.MethodKindEnterprise ||
		got.Protocol != authn.MethodProtocolSAML || got.Label != "" || got.OpensAccount {
		t.Errorf("Describe() = %+v, want an enterprise SAML entry with no label or account opening", got)
	}
	if len(got.Fields) != 0 {
		t.Errorf("Describe().Fields = %+v, want none for an enterprise method", got.Fields)
	}
}
