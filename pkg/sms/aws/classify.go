package aws

import (
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2/types"

	"github.com/vislake/speed/pkg/sms"
)

// Nothing the vendor said is carried into the error text, because its message can quote the request.
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

func isA[T error](err error) bool {
	var target T
	return errors.As(err, &target)
}

func refusal(sentinel, err error) error {
	return fmt.Errorf("%w: %s answered %s", sentinel, carrierName, exceptionName(err))
}

func exceptionName(err error) string {
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		return coded.ErrorCode()
	}
	return "an error this carrier cannot place"
}
