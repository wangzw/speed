package wechatweb

import (
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestDescribeReportsTheRedirect holds the web descriptor: a redirect button speaking OAuth 2.0, bound as union-id and open-id prefix.
func TestDescribeReportsTheRedirect(t *testing.T) {
	got := (&method{}).Describe()
	if got.Name != "wechat.web" || got.Kind != authn.MethodKindRedirect ||
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
