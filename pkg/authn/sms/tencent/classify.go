package tencent

import (
	"errors"
	"fmt"
	"strings"

	tcerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
	tc "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms/v20210111"

	"github.com/vislake/speed/pkg/authn"
)

// The two facts about the carrier's answer that decide which sentinel an error
// becomes. Every other code, a timeout and a network error all fall to
// ErrSMSUncertain: the direction that costs at most one extra budget entry and
// never resends a message the carrier may already have taken.
const (
	// throttlePrefix begins the codes the carrier uses for its rate limits, one
	// per number, application and region. The set is assumed to be all
	// rate-limit codes; the assumption is held by this module's tests.
	throttlePrefix = "LimitExceeded."
	// rejectCode is the code the carrier answers when the template parameters do
	// not match the approved template. It is the one code taken as a refusal of
	// the message as built.
	rejectCode = tc.FAILEDOPERATION_TEMPLATEPARAMSETNOTMATCHAPPROVEDTEMPLATE
	// okCode is the per-number status of a message the carrier accepted.
	okCode = "Ok"
)

// classify turns a failed SendSms call into one of the SMS sentinels.
//
// A carrier error carries a code; the two codes the table names become
// ErrSMSThrottled and ErrSMSRejected, and every other code becomes
// ErrSMSUncertain. A failure that is not a carrier error at all — a timeout, a
// network error, an ended context — also becomes ErrSMSUncertain, because the
// carrier's send call is not idempotent and whether the message went out is
// unknown.
func classify(err error) error {
	var sdk *tcerr.TencentCloudSDKError
	if errors.As(err, &sdk) {
		return sentinelForCode(sdk.GetCode())
	}
	return fmt.Errorf("%w: %s: the call failed before the carrier answered", authn.ErrSMSUncertain, ModuleName)
}

// sentinelForCode maps one carrier error code to its sentinel. The code is the
// only detail carried into the sentinel: the carrier's message text is left out,
// so nothing it might quote — a parameter, a number — reaches a log line.
func sentinelForCode(code string) error {
	switch {
	case code == rejectCode:
		return fmt.Errorf("%w: %s answered %s", authn.ErrSMSRejected, ModuleName, code)
	case strings.HasPrefix(code, throttlePrefix):
		return fmt.Errorf("%w: %s answered %s", authn.ErrSMSThrottled, ModuleName, code)
	default:
		return fmt.Errorf("%w: %s answered %s", authn.ErrSMSUncertain, ModuleName, code)
	}
}

// classifyStatus reads a successful SendSms response: one number is sent per
// call, so its first per-number status is the outcome. A status the carrier did
// not mark Ok is sorted into a sentinel by its code; an Ok status yields the
// receipt, whose provider identifier is the send serial number and, when the
// carrier gave none, the request identifier.
func classifyStatus(resp *tc.SendSmsResponse) (authn.SendReceipt, error) {
	if resp == nil || resp.Response == nil || len(resp.Response.SendStatusSet) == 0 ||
		resp.Response.SendStatusSet[0] == nil {
		return authn.SendReceipt{}, fmt.Errorf("%w: %s answered with no send status",
			authn.ErrSMSUncertain, ModuleName)
	}
	status := resp.Response.SendStatusSet[0]
	if code := status.Code; code == nil || *code != okCode {
		if code == nil {
			return authn.SendReceipt{}, fmt.Errorf("%w: %s answered with no code",
				authn.ErrSMSUncertain, ModuleName)
		}
		return authn.SendReceipt{}, sentinelForCode(*code)
	}
	receipt := authn.SendReceipt{}
	switch {
	case status.SerialNo != nil:
		receipt.ProviderID = *status.SerialNo
	case resp.Response.RequestId != nil:
		receipt.ProviderID = *resp.Response.RequestId
	}
	return receipt, nil
}
