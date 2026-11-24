package sso

import (
	"testing"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/sso/saml"
)

// TestSPKeyConfiguredReadsTheRealSAMLSection checks that the SAML key configuration is read from the real settings section.
func TestSPKeyConfiguredReadsTheRealSAMLSection(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "the key file is configured beside the switch",
			body: `{"authn":{"sso":{"saml":{"enabled":true,"sp-key-file":"/tmp/sp-key.pem"}}}}`,
			want: true,
		},
		{
			name: "no key file is configured",
			body: `{"authn":{"sso":{"saml":{"enabled":true}}}}`,
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reader := authntest.ConfigReaderAll(t, c.body, saml.Module())
			p := &product{reader: reader}
			if got := p.spKeyConfigured(); got != c.want {
				t.Errorf("spKeyConfigured() = %t, want %t: the key file is read from authn.sso.saml by "+
					"the item name samlKeySettings declares", got, c.want)
			}
		})
	}
}
