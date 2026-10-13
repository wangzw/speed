package aliyun

import (
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestAConfigurationErrorNeverRepeatsTheAccessKeySecret holds the discipline that
// a module holding a secret configuration value keeps it out of the text of every
// failure on the way to a runnable configuration. The value is registered as a
// literal redaction rule for logs; this is the other half: the failure a startup
// prints must not carry it, whichever item the failure actually names.
func TestAConfigurationErrorNeverRepeatsTheAccessKeySecret(t *testing.T) {
	const secret = "s3cr3t-aliyun-access-key-sentinel-9f8e7d6c"
	for name, edit := range map[string]func(*moduleConfig){
		"a missing login template": func(c *moduleConfig) { delete(c.Templates, "login/zh-CN") },
		"a blank enrollment template": func(c *moduleConfig) {
			c.Templates["enroll/en-US"] = "   "
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := completeConfig()
			cfg.AccessKeySecret = secret
			edit(&cfg)
			_, err := cfg.resolve()
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("resolve = %v, want one wrapping ErrInvalidConfig", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the failure %q repeats the access-key secret", err)
			}
		})
	}
}
