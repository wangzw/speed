package aws

import (
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2/types"

	"github.com/vislake/speed/pkg/sms"
)

// classify turns a failed SendTextMessage call into one of the sending
// sentinels.
//
// The mapping is by exception type, the vendor's own vocabulary: a throttling
// exception is ErrThrottled, a validation exception is ErrRejected, and the four
// exceptions that say this deployment lacks the permission, the resource, the
// state or the quota are ErrUnroutable. Everything else — another exception type,
// a timeout, a network error, an ended context — is ErrUncertain, because the send
// interface is not idempotent and a call that failed without a place in the table
// may still have delivered the message. It never resends.
//
// Nothing the vendor said is carried into the error text: only the exception's own
// name is. Its message can quote the request, and the request carries the body and
// the parameter values.
func classify(err error) error {
	switch {
	case isA[*types.ThrottlingException](err):
		return refusal(sms.ErrThrottled, err)
	case isA[*types.ValidationException](err):
		return refusal(sms.ErrRejected, err)
	case isA[*types.AccessDeniedException](err),
		isA[*types.ResourceNotFoundException](err),
		isA[*types.ConflictException](err),
		isA[*types.ServiceQuotaExceededException](err):
		return refusal(sms.ErrUnroutable, err)
	default:
		return sms.Unsettled(carrierName)
	}
}

// isA reports whether err names a vendor exception of type T.
func isA[T error](err error) bool {
	var target T
	return errors.As(err, &target)
}

// refusal is the error a classified failure becomes: the sentinel, the carrier,
// and the vendor's exception name. The exception's message text is left out on
// purpose.
func refusal(sentinel, err error) error {
	return fmt.Errorf("%w: %s answered %s", sentinel, carrierName, exceptionName(err))
}

// exceptionName reads the vendor's own name for a modeled exception.
func exceptionName(err error) string {
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		return coded.ErrorCode()
	}
	return "an error this carrier cannot place"
}
