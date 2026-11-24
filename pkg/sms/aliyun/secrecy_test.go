package aliyun

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/sms"
)

// TestAProviderErrorNeverRepeatsTheAccessKeySecret checks that the management seam's
// failures never carry the access-key secret, even when the carrier echoes it back.
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
