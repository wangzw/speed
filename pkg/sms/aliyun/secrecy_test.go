package aliyun

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/sms"
)

// TestAProviderErrorNeverRepeatsTheAccessKeySecret holds that a module holding a
// secret configuration value keeps it out of the text of every failure its
// management seam returns, even when the carrier echoes the value back in its own
// answer. The value is registered as a literal redaction rule for logs; this is
// the other half: what the seam returns must not carry it either, so the module
// reports the carrier's status code and never its message text.
func TestAProviderErrorNeverRepeatsTheAccessKeySecret(t *testing.T) {
	echoed := `{"Code":"isv.SMS_TEMPLATE_ILLEGAL","Message":"the key ` + testAccessKeySecret + ` was rejected"}`

	calls := map[string]func(*sender) error{
		"offer": func(s *sender) error {
			_, err := s.Offer(t.Context(), oneSubmission())
			return err
		},
		"approval": func(s *sender) error {
			_, err := s.Approval(t.Context(), "SMS_1")
			return err
		},
		"retire": func(s *sender) error {
			return s.Retire(t.Context(), "SMS_1")
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(jsonReply(echoed))
			defer server.Close()

			s := senderOver(t, server.Listener.Addr().String(), "http")
			err := call(s)
			if !errors.Is(err, sms.ErrUncertain) {
				t.Fatalf("%s answered %v, want the uncertain sentinel", name, err)
			}
			if strings.Contains(err.Error(), testAccessKeySecret) {
				t.Errorf("the failure %q repeats the access-key secret", err)
			}
		})
	}
}
