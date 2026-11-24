package twilio

import (
	"errors"
	"fmt"
	"net/http"

	twclient "github.com/twilio/twilio-go/client"

	"github.com/vislake/speed/pkg/sms"
)

// classify turns 429 into ErrThrottled, another 4xx into ErrRejected and anything else into ErrUncertain; only the status and the vendor's code reach the text, never its message.
func classify(err error) error {
	status, code, ok := vendorStatus(err)
	if !ok {
		return sms.Unsettled(carrierName)
	}
	switch {
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s answered HTTP %d (code %d)", sms.ErrThrottled, carrierName, status, code)
	case status >= 400 && status < 500:
		return fmt.Errorf("%w: %s answered HTTP %d (code %d)", sms.ErrRejected, carrierName, status, code)
	default:
		return fmt.Errorf("%w: %s answered HTTP %d (code %d)", sms.ErrUncertain, carrierName, status, code)
	}
}

func vendorStatus(err error) (status, code int, ok bool) {
	var rest *twclient.TwilioRestError
	if errors.As(err, &rest) {
		return rest.Status, rest.Code, true
	}
	var v1 *twclient.RestErrorV1
	if errors.As(err, &v1) {
		return v1.HttpStatusCode, v1.Code, true
	}
	return 0, 0, false
}

func alreadyGone(err error) bool {
	status, _, ok := vendorStatus(err)
	return ok && status == http.StatusNotFound
}
