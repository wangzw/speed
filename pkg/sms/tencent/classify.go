package tencent

import (
	"errors"
	"fmt"
	"strings"

	tcerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
	tc "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms/v20210111"

	"github.com/vislake/speed/pkg/sms"
)

const (
	throttlePrefix = "LimitExceeded."
	rejectCode     = tc.FAILEDOPERATION_TEMPLATEPARAMSETNOTMATCHAPPROVEDTEMPLATE
	okCode         = "Ok"
)

func classify(err error) error {
	var sdk *tcerr.TencentCloudSDKError
	if errors.As(err, &sdk) {
		return sentinelForCode(sdk.GetCode())
	}
	return sms.Unsettled(ModuleName)
}

func sentinelForCode(code string) error {
	switch {
	case code == rejectCode:
		return fmt.Errorf("%w: %s answered %s", sms.ErrRejected, ModuleName, code)
	case strings.HasPrefix(code, throttlePrefix):
		return fmt.Errorf("%w: %s answered %s", sms.ErrThrottled, ModuleName, code)
	default:
		return fmt.Errorf("%w: %s answered %s", sms.ErrUncertain, ModuleName, code)
	}
}

func classifyStatus(resp *tc.SendSmsResponse) (sms.Receipt, error) {
	if resp == nil || resp.Response == nil || len(resp.Response.SendStatusSet) == 0 ||
		resp.Response.SendStatusSet[0] == nil {
		return sms.Receipt{}, sms.NoStatus(ModuleName)
	}
	status := resp.Response.SendStatusSet[0]
	if code := status.Code; code == nil || *code != okCode {
		if code == nil {
			return sms.Receipt{}, fmt.Errorf("%w: %s answered with no code", sms.ErrUncertain, ModuleName)
		}
		return sms.Receipt{}, sentinelForCode(*code)
	}
	receipt := sms.Receipt{}
	switch {
	case status.SerialNo != nil:
		receipt.ProviderID = *status.SerialNo
	case resp.Response.RequestId != nil:
		receipt.ProviderID = *resp.Response.RequestId
	}
	return receipt, nil
}
