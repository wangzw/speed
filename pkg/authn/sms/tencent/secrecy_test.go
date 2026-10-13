package tencent

import (
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestAConfigurationErrorNeverRepeatsTheCredentialValues holds the discipline that
// a module holding a secret configuration value keeps it out of the text of every
// failure on the way to a runnable configuration. The credential's identifier and
// secret are both registered as literal redaction rules for logs; this is the
// other half: the failure a startup prints must not carry either of them,
// whichever item the failure actually names.
func TestAConfigurationErrorNeverRepeatsTheCredentialValues(t *testing.T) {
	const (
		secretID  = "s3cr3t-tencent-id-sentinel-9f8e7d6c"
		secretKey = "s3cr3t-tencent-key-sentinel-1a2b3c4d"
	)
	for name, edit := range map[string]func(*moduleConfig){
		"a missing mfa template": func(c *moduleConfig) { c.Templates["mfa/zh-CN"] = "" },
		"a blank login template": func(c *moduleConfig) { c.Templates["login/en-US"] = "   " },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := moduleConfig{
				SecretID:  secretID,
				SecretKey: secretKey,
				SDKAppID:  "1400006666",
				Region:    "ap-guangzhou",
				SignName:  "Speed",
				Templates: validTemplates(),
			}
			edit(&cfg)
			_, err := cfg.resolve()
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("resolve = %v, want one wrapping ErrInvalidConfig", err)
			}
			for _, value := range []string{secretID, secretKey} {
				if strings.Contains(err.Error(), value) {
					t.Errorf("the failure %q repeats the credential value %q", err, value)
				}
			}
		})
	}
}
