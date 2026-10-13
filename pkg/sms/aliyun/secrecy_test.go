package aliyun

import (
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/sms"
)

// TestAConfigurationErrorNeverRepeatsTheAccessKeySecret holds the discipline that
// a module holding a secret configuration value keeps it out of the text of every
// failure on the way to a runnable configuration. The value is registered as a
// literal redaction rule for logs; this is the other half: the failure a startup
// prints must not carry it, whichever item the failure actually names.
func TestAConfigurationErrorNeverRepeatsTheAccessKeySecret(t *testing.T) {
	const secret = "s3cr3t-aliyun-access-key-sentinel-9f8e7d6c"
	for name, edit := range map[string]func(*moduleConfig){
		"a template with no parameter": func(c *moduleConfig) {
			c.Templates["authn.code.login"] = sms.TemplateSpec{Codes: map[string]string{"zh-CN": "SMS_X"}}
		},
		"a template with a blank code": func(c *moduleConfig) {
			c.Templates["authn.code.enroll"] = sms.TemplateSpec{
				Params: []string{"code", "ttl_minutes"},
				Codes:  map[string]string{"zh-CN": "SMS_X", "en-US": "   "},
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := completeConfig()
			cfg.AccessKeySecret = secret
			edit(&cfg)
			_, err := cfg.resolve()
			if !errors.Is(err, sms.ErrInvalidConfig) {
				t.Fatalf("resolve = %v, want one wrapping ErrInvalidConfig", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the failure %q repeats the access-key secret", err)
			}
		})
	}
}
