package wechatmp

import (
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestDescribeReportsTheRedirect holds the descriptor the discovery endpoint
// publishes for the WeChat mini-program method: a redirect button that speaks
// OAuth 2.0, opens an account, and whose bindings land under the union-id
// provider and the open-id namespace prefix.
func TestDescribeReportsTheRedirect(t *testing.T) {
	got := (&method{}).Describe()
	if got.Name != "wechat.mp" || got.Kind != authn.MethodKindRedirect ||
		got.Protocol != authn.MethodProtocolOAuth2 || got.Label != "WeChat" || !got.OpensAccount {
		t.Errorf("Describe() = %+v, want a redirect OAuth 2.0 WeChat that opens an account", got)
	}
	if len(got.Fields) != 0 {
		t.Errorf("Describe().Fields = %+v, want none for a redirect method", got.Fields)
	}
	if want := []string{"wechat", "wechat-openid:"}; !slices.Equal(got.BindingProviders, want) {
		t.Errorf("Describe().BindingProviders = %+v, want %+v", got.BindingProviders, want)
	}
}
