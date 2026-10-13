package wechatweb

import (
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestAConfigurationErrorNeverRepeatsTheApplicationSecret holds the discipline
// that a module holding a secret configuration value keeps it out of the text of
// every failure on the way to a runnable configuration. The value is registered
// as a literal redaction rule for logs; this is the other half: the failure a
// startup prints must not carry it, whichever item the failure actually names.
func TestAConfigurationErrorNeverRepeatsTheApplicationSecret(t *testing.T) {
	const secret = "s3cr3t-wechat-web-sentinel-9f8e7d6c"
	for name, tc := range map[string]struct {
		cfg moduleConfig
		g   gateMount
	}{
		"an application secret with no application identifier": {
			cfg: moduleConfig{AppSecret: secret},
			g:   gateMount{PublicURL: "https://app.example.com"},
		},
		"an application secret with no public address": {
			cfg: moduleConfig{AppID: "the-id", AppSecret: secret},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tc.cfg.resolve(tc.g)
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("resolve = %v, want one wrapping ErrInvalidConfig", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the failure %q repeats the application secret", err)
			}
		})
	}
}
