package google

import (
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestAConfigurationErrorNeverRepeatsTheClientSecret holds the discipline that a
// module holding a secret configuration value keeps it out of the text of every
// failure on the way to a runnable configuration. The value is registered as a
// literal redaction rule for logs; this is the other half: the failure a startup
// prints must not carry it, whichever item the failure actually names.
func TestAConfigurationErrorNeverRepeatsTheClientSecret(t *testing.T) {
	const secret = "s3cr3t-google-client-sentinel-9f8e7d6c"
	for name, tc := range map[string]struct {
		cfg moduleConfig
		gm  gateMount
	}{
		"a client secret with no public address": {
			cfg: moduleConfig{ClientID: "the-id", ClientSecret: secret},
		},
		"a client secret with no client identifier": {
			cfg: moduleConfig{ClientSecret: secret},
			gm:  gateMount{PublicURL: "https://app.example.com"},
		},
		"a client secret with a public address that is not an origin": {
			cfg: moduleConfig{ClientID: "the-id", ClientSecret: secret},
			gm:  gateMount{PublicURL: "not an address"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tc.cfg.resolve(tc.gm)
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("resolve = %v, want one wrapping ErrInvalidConfig", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the failure %q repeats the client secret", err)
			}
		})
	}
}
