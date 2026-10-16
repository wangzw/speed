package google

import (
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestDescribeReportsTheRedirect holds the descriptor the discovery endpoint
// publishes for the Google method: a redirect button that speaks OIDC and opens
// an account.
func TestDescribeReportsTheRedirect(t *testing.T) {
	got := (&product{}).Describe()
	if got.Name != "google" || got.Kind != authn.MethodKindRedirect ||
		got.Protocol != authn.MethodProtocolOIDC || got.Label != "Google" || !got.OpensAccount {
		t.Errorf("Describe() = %+v, want a redirect OIDC Google that opens an account", got)
	}
	if len(got.Fields) != 0 {
		t.Errorf("Describe().Fields = %+v, want none for a redirect method", got.Fields)
	}
}
