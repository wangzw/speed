package github

import (
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestDescribeReportsTheRedirect holds the descriptor the discovery endpoint
// publishes for the GitHub method: a redirect button that speaks OAuth 2.0,
// opens an account, and whose bindings land under the provider value "github".
func TestDescribeReportsTheRedirect(t *testing.T) {
	got := (&method{}).Describe()
	if got.Name != "github" || got.Kind != authn.MethodKindRedirect ||
		got.Protocol != authn.MethodProtocolOAuth2 || got.Label != "GitHub" || !got.OpensAccount {
		t.Errorf("Describe() = %+v, want a redirect OAuth 2.0 GitHub that opens an account", got)
	}
	if len(got.Fields) != 0 {
		t.Errorf("Describe().Fields = %+v, want none for a redirect method", got.Fields)
	}
	if want := []string{"github"}; !slices.Equal(got.BindingProviders, want) {
		t.Errorf("Describe().BindingProviders = %+v, want %+v", got.BindingProviders, want)
	}
}
