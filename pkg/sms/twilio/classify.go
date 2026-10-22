package twilio

import (
	"errors"
	"fmt"
	"net/http"

	twclient "github.com/twilio/twilio-go/client"

	"github.com/vislake/speed/pkg/sms"
)

// classify turns a failed vendor call into one of the sending sentinels, for the
// sending seam and the template-management seam alike.
//
// The vendor reports an HTTP status: 429 is a limit, another 4xx is a refusal of
// the request as built, and a 5xx is a failure whose outcome is unknown. A
// failure that carries no vendor status at all — a timeout, a network error, an
// ended context — is uncertain for the same reason: none of the vendor's calls
// is idempotent and whether one landed is unknown. Only the status and the
// vendor's numeric code are carried into the text; the vendor's message text is
// left out, because it can quote the request and the request carries the
// credentials and the message content.
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

// vendorStatus reads the HTTP status and the vendor's numeric code out of a
// vendor error. The SDK reports two error shapes: the legacy
// client.TwilioRestError the account and content APIs answer with, and the
// newer client.RestErrorV1. A value that is neither carries no status.
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

// alreadyGone reports whether a vendor error says the resource was not there.
// Retire treats it as success: the template is removed either way.
func alreadyGone(err error) bool {
	status, _, ok := vendorStatus(err)
	return ok && status == http.StatusNotFound
}
